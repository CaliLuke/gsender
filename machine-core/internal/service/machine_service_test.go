package service

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	gen "github.com/Sienci-Labs/gsender/machine-core/gen/machine"
)

type staticDeviceDiscovery struct {
	devices []*gen.Device
	err     error
}

type staticControllerDetector struct {
	detection *controllerDetection
	err       error
}

func (d staticDeviceDiscovery) ListDevices() ([]*gen.Device, error) {
	if d.err != nil {
		return nil, d.err
	}
	return cloneDevices(d.devices), nil
}

func (d staticControllerDetector) Detect(context.Context, *gen.Device, *gen.OpenSessionPayload) (*controllerDetection, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.detection, nil
}

func requireMachineErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected machine error %s, got nil", code)
	}
	machineErr, ok := err.(*gen.MachineError)
	if !ok {
		t.Fatalf("expected *gen.MachineError, got %T", err)
	}
	if machineErr.Code != code {
		t.Fatalf("expected machine error %s, got %s", code, machineErr.Code)
	}
}

type captureSubscribeEventsStream struct {
	events []*gen.SessionEvent
}

func (s *captureSubscribeEventsStream) Send(event *gen.SessionEvent) error {
	s.events = append(s.events, cloneSessionEvent(event))
	return nil
}

func (s *captureSubscribeEventsStream) SendWithContext(_ context.Context, event *gen.SessionEvent) error {
	return s.Send(event)
}

func (s *captureSubscribeEventsStream) Close() error {
	return nil
}

func TestOpenAttachCloseLifecycle(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}

	devices, err := service.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices returned error: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 simulated devices, got %d", len(devices))
	}

	sessionID := devices[0].ID
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: sessionID,
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if openResult == nil || openResult.Session == nil {
		t.Fatal("OpenSession should return a session")
	}

	session := openResult.Session
	if session.ControllerType != "grblhal" {
		t.Fatalf("expected simulated serial device to resolve grblhal, got %s", session.ControllerType)
	}
	if session.ConnectionState != "connected" {
		t.Fatalf("expected connected session state, got %s", session.ConnectionState)
	}
	if got := openResult.Snapshot.RuntimeFlags.CanStartJob; got {
		t.Fatalf("expected CanStartJob=false on empty session, got %t", got)
	}
	if openResult.Snapshot.HomingState == nil {
		t.Fatal("expected homing state in snapshot")
	}
	if openResult.Snapshot.HomingState.HomingRequired {
		t.Fatal("expected simulator without live homing config to default homing_required=false")
	}
	controllerSettings, ok := openResult.Snapshot.ControllerSettings.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller settings map, got %T", openResult.Snapshot.ControllerSettings)
	}
	if got := controllerSettings["controller_type"]; got != "grblhal" {
		t.Fatalf("expected controller settings to expose grblhal, got %#v", got)
	}
	controllerState, ok := openResult.Snapshot.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", openResult.Snapshot.ControllerState)
	}
	if got := controllerState["device_kind"]; got != "serial" {
		t.Fatalf("expected controller state to expose serial device kind, got %#v", got)
	}
	if got := controllerState["firmware_banner"]; got != "grblHAL simulator" {
		t.Fatalf("expected controller state to expose firmware banner, got %#v", got)
	}
	if got := controllerState["firmware_family"]; got != "grblHAL" {
		t.Fatalf("expected controller state to expose firmware family, got %#v", got)
	}
	if got := controllerState["status"]; got != "idle" {
		t.Fatalf("expected controller state status to expose idle session, got %#v", got)
	}
	if got := controllerState["workflow_state"]; got != "idle" {
		t.Fatalf("expected controller state workflow_state to expose idle session, got %#v", got)
	}
	if got := controllerState["connected_clients"]; got != 0 {
		t.Fatalf("expected controller state to expose no connected clients, got %#v", got)
	}
	if got := controllerState["has_loaded_file"]; got != false {
		t.Fatalf("expected controller state to expose no loaded file, got %#v", got)
	}
	if got := controllerState["has_alarm"]; got != false {
		t.Fatalf("expected controller state to expose no alarm, got %#v", got)
	}

	if got := controllerSettings["firmware_family"]; got != "grblHAL" {
		t.Fatalf("expected controller settings to expose firmware family, got %#v", got)
	}
	if got := controllerSettings["simulator"]; got != true {
		t.Fatalf("expected simulated device to expose simulator=true, got %#v", got)
	}

	snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if snapshot.Session.ID != session.ID {
		t.Fatalf("snapshot session mismatch: got %s want %s", snapshot.Session.ID, session.ID)
	}

	attached, err := service.AttachClient(context.Background(), &gen.AttachClientPayload{
		SessionID: session.ID,
		ClientID:  "desktop-ui",
	})
	if err != nil {
		t.Fatalf("AttachClient returned error: %v", err)
	}
	if attached == nil || attached.Session == nil {
		t.Fatal("AttachClient should return a snapshot")
	}

	closeResult, err := service.CloseSession(context.Background(), &gen.SessionIDPayload{
		SessionID: session.ID,
	})
	if err != nil {
		t.Fatalf("CloseSession returned error: %v", err)
	}
	if !closeResult.OK {
		t.Fatal("expected CloseSession OK=true")
	}

	devicesAfterClose, err := service.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices after close returned error: %v", err)
	}
	if devicesAfterClose[0].InUse {
		t.Fatal("expected device to report in_use=false after close")
	}

	_, err = service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.ID,
	})
	if err == nil {
		t.Fatal("expected GetSnapshot after close to error")
	}

	stream := &captureSubscribeEventsStream{}
	if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
		SessionID: session.ID,
		ClientID:  "desktop-ui",
	}, stream); err != nil {
		t.Fatalf("SubscribeEvents after close returned error: %v", err)
	}
	if got := len(stream.events); got == 0 {
		t.Fatal("expected closed session replay events after close")
	}
	lastEvent := stream.events[len(stream.events)-1]
	if lastEvent.Type != "session_closed" {
		t.Fatalf("expected last replay event to be session_closed, got %s", lastEvent.Type)
	}
}

func TestControllerStateTracksWorkflowErrorsAndDisconnects(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}

	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: func() *string {
			id := "renderer"
			return &id
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if _, err := service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StartJob returned error: %v", err)
	}

	snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	controllerState, ok := snapshot.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", snapshot.ControllerState)
	}
	if got := controllerState["status"]; got != "run" {
		t.Fatalf("expected running controller status, got %#v", got)
	}
	if got := controllerState["workflow_state"]; got != "running" {
		t.Fatalf("expected running workflow_state, got %#v", got)
	}
	if got := controllerState["connected_clients"]; got != 1 {
		t.Fatalf("expected one connected client, got %#v", got)
	}
	if got := controllerState["has_loaded_file"]; got != true {
		t.Fatalf("expected loaded file flag, got %#v", got)
	}
	if got := controllerState["has_alarm"]; got != false {
		t.Fatalf("expected no alarm while job is running, got %#v", got)
	}

	if _, err := service.PauseJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("PauseJob returned error: %v", err)
	}
	paused, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot after pause returned error: %v", err)
	}
	controllerState, ok = paused.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected paused controller state map, got %T", paused.ControllerState)
	}
	if got := controllerState["status"]; got != "hold" {
		t.Fatalf("expected paused controller status hold, got %#v", got)
	}

	if _, err := service.PauseJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err == nil {
		t.Fatal("expected second PauseJob to return command_rejected")
	}
	errored, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot after rejected command returned error: %v", err)
	}
	controllerState, ok = errored.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected errored controller state map, got %T", errored.ControllerState)
	}
	if got := controllerState["status"]; got != "alarm" {
		t.Fatalf("expected alarm controller status after rejected command, got %#v", got)
	}
	if got := controllerState["has_alarm"]; got != true {
		t.Fatalf("expected alarm flag after rejected command, got %#v", got)
	}
	lastError, ok := controllerState["last_error"].(string)
	if !ok || !strings.Contains(lastError, "command_rejected") {
		t.Fatalf("expected last_error in controller state, got %#v", controllerState["last_error"])
	}

	if _, err := service.DetachClient(context.Background(), &gen.DetachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "renderer",
	}); err != nil {
		t.Fatalf("DetachClient returned error: %v", err)
	}
	disconnected, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot after detach returned error: %v", err)
	}
	controllerState, ok = disconnected.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected disconnected controller state map, got %T", disconnected.ControllerState)
	}
	if got := controllerState["status"]; got != "alarm" {
		t.Fatalf("expected alarm status to persist until cleared, got %#v", got)
	}
	if got := controllerState["connection_state"]; got != "disconnected" {
		t.Fatalf("expected disconnected connection_state, got %#v", got)
	}
	if got := controllerState["connected_clients"]; got != 0 {
		t.Fatalf("expected no connected clients after detach, got %#v", got)
	}
}

func TestBuildControllerSettingsMarksRealDeviceAsNonSimulator(t *testing.T) {
	settings := buildControllerSettings(&gen.Device{
		ID:           "/dev/ttyUSB99",
		Kind:         "serial",
		Path:         ptrString("/dev/ttyUSB99"),
		DisplayName:  "/dev/ttyUSB99",
		Capabilities: []string{"open_session"},
	}, "grbl", &gen.OpenSessionPayload{})

	if got := settings["simulator"]; got != false {
		t.Fatalf("expected real device simulator=false, got %#v", got)
	}
}

func TestLoadAndUnloadFileUpdatesFlags(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	_, err = service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: session.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1\n",
	})
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}

	snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if snapshot.LoadedFile == nil {
		t.Fatal("expected loaded file in snapshot")
	}
	if snapshot.LoadedFile.Name != "part.nc" {
		t.Fatalf("expected file part.nc, got %s", snapshot.LoadedFile.Name)
	}
	if !snapshot.RuntimeFlags.CanStartJob {
		t.Fatal("expected CanStartJob true after load")
	}

	unload, err := service.UnloadFile(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("UnloadFile returned error: %v", err)
	}
	if !unload.OK {
		t.Fatal("expected UnloadFile OK")
	}
}

func TestWorkflowMethodsRejectInvalidStateTransitions(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	_, err = service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	requireMachineErrorCode(t, err, "job_not_running")
	snapshot, snapshotErr := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if snapshotErr != nil {
		t.Fatalf("GetSnapshot returned error: %v", snapshotErr)
	}
	if snapshot.LastError == nil || !strings.Contains(*snapshot.LastError, "job_not_running") {
		t.Fatalf("expected last_error to record job_not_running, got %#v", snapshot.LastError)
	}

	_, err = service.PauseJob(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	requireMachineErrorCode(t, err, "command_rejected")

	_, err = service.ResumeJob(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	requireMachineErrorCode(t, err, "command_rejected")
}

func TestSessionRPCsRejectMissingOrInvalidPayloads(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}

	_, err := service.AttachClient(context.Background(), &gen.AttachClientPayload{
		SessionID: "missing",
		ClientID:  "ui",
	})
	requireMachineErrorCode(t, err, "session_not_found")

	_, err = service.AttachClient(context.Background(), &gen.AttachClientPayload{
		SessionID: "sim://loopback",
	})
	requireMachineErrorCode(t, err, "command_rejected")

	_, err = service.GetSession(context.Background(), &gen.SessionIDPayload{
		SessionID: "missing",
	})
	requireMachineErrorCode(t, err, "session_not_found")

	_, err = service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: "missing",
	})
	requireMachineErrorCode(t, err, "session_not_found")
}

func TestOpenSessionRejectsUnknownDevice(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	if _, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "usb://definitely-not-real",
	}); err == nil {
		t.Fatal("expected OpenSession to reject unknown device")
	}
}

func TestListDevicesReturnsTransportErrorOnDiscoveryFailure(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{
		err: errors.New("serial device disconnected"),
	}

	_, err := service.ListDevices(context.Background())
	requireMachineErrorCode(t, err, "transport_error")
}

func TestOpenSessionReturnsFirmwareDetectionFailedOnProbeTimeout(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	service.detector = staticControllerDetector{
		err: context.DeadlineExceeded,
	}

	_, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	requireMachineErrorCode(t, err, "firmware_detection_failed")
}

func TestOpenSessionReturnsFirmwareDetectionFailedOnProbeShortWrite(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	service.detector = staticControllerDetector{
		err: io.ErrShortWrite,
	}

	_, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	requireMachineErrorCode(t, err, "firmware_detection_failed")
}

func TestRepeatedResolveSessionAndMultiClientAttachRemainStable(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}

	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	for i := 0; i < 50; i++ {
		clientID := "ui-" + strconv.Itoa(i)

		snapshot, err := service.ResolveSession(context.Background(), &gen.ResolveSessionPayload{
			DeviceID: "sim://loopback",
			ClientID: clientID,
		})
		if err != nil {
			t.Fatalf("ResolveSession iteration %d returned error: %v", i, err)
		}
		if snapshot == nil || snapshot.Session == nil || snapshot.Session.ID != openResult.Session.ID {
			t.Fatalf("ResolveSession iteration %d returned wrong session", i)
		}

		stream := &captureSubscribeEventsStream{}
		if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
			SessionID: openResult.Session.ID,
			ClientID:  clientID,
		}, stream); err != nil {
			t.Fatalf("SubscribeEvents iteration %d returned error: %v", i, err)
		}
		if len(stream.events) == 0 {
			t.Fatalf("SubscribeEvents iteration %d returned no events", i)
		}

		if _, err := service.DetachClient(context.Background(), &gen.DetachClientPayload{
			SessionID: openResult.Session.ID,
			ClientID:  clientID,
		}); err != nil {
			t.Fatalf("DetachClient iteration %d returned error: %v", i, err)
		}
	}

	finalSnapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	controllerState, ok := finalSnapshot.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", finalSnapshot.ControllerState)
	}
	if got := controllerState["connected_clients"]; got != 0 {
		t.Fatalf("expected no connected clients after repeated attach/detach, got %#v", got)
	}
}

func TestOpenSessionRejectsBusyDevice(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}

	_, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("first OpenSession returned error: %v", err)
	}

	_, err = service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	requireMachineErrorCode(t, err, "device_busy")
}

func TestListDevicesMarksOpenSessionDeviceInUse(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}

	devices, err := service.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices returned error: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devices))
	}
	for _, device := range devices {
		if device.InUse {
			t.Fatalf("expected device %s to be free before open", device.ID)
		}
	}

	_, err = service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: devices[1].ID,
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	devicesAfterOpen, err := service.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices after open returned error: %v", err)
	}
	if !devicesAfterOpen[1].InUse {
		t.Fatalf("expected device %s to be marked in use", devicesAfterOpen[1].ID)
	}
	if devicesAfterOpen[0].InUse {
		t.Fatalf("expected unopened device %s to remain free", devicesAfterOpen[0].ID)
	}
}

func TestCloseSessionIsIdempotent(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if session == nil || session.Session == nil {
		t.Fatal("expected OpenSession to return session")
	}

	sessionID := session.Session.ID

	once, err := service.CloseSession(context.Background(), &gen.SessionIDPayload{
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("CloseSession first call returned error: %v", err)
	}
	if once == nil || !once.OK {
		t.Fatal("expected first CloseSession to return OK=true")
	}

	again, err := service.CloseSession(context.Background(), &gen.SessionIDPayload{
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("CloseSession second call returned error: %v", err)
	}
	if again == nil || again.OK {
		t.Fatal("expected second CloseSession to return OK=false")
	}
}

func TestOpenSessionUsesDefaultFirmwareFallbackForUnknownDeviceFlavor(t *testing.T) {
	customDevices := append(cloneDevices(defaultKnownDevices), []*gen.Device{{
		ID:           "sim://mystery",
		Kind:         "serial",
		Path:         func() *string { v := "sim://mystery"; return &v }(),
		DisplayName:  "Mystery Simulator",
		Capabilities: []string{"simulate", "open_session"},
	}}...)
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: customDevices}

	defaultFirmware := "grbl"
	result, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID:        "sim://mystery",
		DefaultFirmware: &defaultFirmware,
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if result.Session.ControllerType != "grbl" {
		t.Fatalf("expected default firmware fallback to resolve grbl, got %s", result.Session.ControllerType)
	}
	if result.Session.ConnectionState != "connected" {
		t.Fatalf("expected connected state with default firmware fallback, got %s", result.Session.ConnectionState)
	}
}

func TestOpenSessionUsesDetectorResultWhenAvailable(t *testing.T) {
	customDevices := []*gen.Device{{
		ID:           "/dev/ttyUSB99",
		Kind:         "serial",
		Path:         ptrString("/dev/ttyUSB99"),
		DisplayName:  "/dev/ttyUSB99",
		Capabilities: []string{"open_session"},
	}}
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: customDevices}
	service.detector = staticControllerDetector{
		detection: &controllerDetection{
			controllerType:  "fluidnc",
			connectionState: "connected",
			controllerState: map[string]interface{}{
				"controller_type":  "fluidnc",
				"detection_source": "serial_probe",
			},
			controllerSetting: map[string]interface{}{
				"controller_type":  "fluidnc",
				"detection_source": "serial_probe",
			},
			senderStatus: map[string]interface{}{
				"connection_state": "connected",
				"active":           true,
			},
		},
	}

	result, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "/dev/ttyUSB99",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if result.Session.ControllerType != "fluidnc" {
		t.Fatalf("expected detector to resolve fluidnc, got %s", result.Session.ControllerType)
	}
	if result.Session.ConnectionState != "connected" {
		t.Fatalf("expected detector to resolve connected state, got %s", result.Session.ConnectionState)
	}
	controllerState, ok := result.Snapshot.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", result.Snapshot.ControllerState)
	}
	if got := controllerState["detection_source"]; got != "serial_probe" {
		t.Fatalf("expected serial_probe detection source, got %#v", got)
	}
}

func TestOpenSessionReturnsFirmwareDetectionFailedWhenDetectorErrors(t *testing.T) {
	customDevices := []*gen.Device{{
		ID:           "/dev/ttyUSB99",
		Kind:         "serial",
		Path:         ptrString("/dev/ttyUSB99"),
		DisplayName:  "/dev/ttyUSB99",
		Capabilities: []string{"open_session"},
	}}
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: customDevices}
	service.detector = staticControllerDetector{
		err: context.DeadlineExceeded,
	}

	_, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "/dev/ttyUSB99",
	})
	requireMachineErrorCode(t, err, "firmware_detection_failed")
}

func TestBuildSerialProbeResultExtractsControllerBannerAndStatus(t *testing.T) {
	probeResult := buildSerialProbeResult("<Hold:0|MPos:5.000,2.000,0.000|FS:0,0|H:1>\nGrblHAL 1.1f ['$' or '$HELP' for help]\n$20=1\n$21=0\n$22=1\n$130=830.000\n$131=780.000\nok\n")
	if probeResult.controllerType != "grblhal" {
		t.Fatalf("expected grblhal controller type, got %s", probeResult.controllerType)
	}
	if probeResult.banner != "GrblHAL 1.1f ['$' or '$HELP' for help]" {
		t.Fatalf("unexpected firmware banner: %#v", probeResult.banner)
	}
	if probeResult.firmwareFamily != "GrblHAL" {
		t.Fatalf("unexpected firmware family: %#v", probeResult.firmwareFamily)
	}
	if probeResult.firmwareVersion != "1.1f" {
		t.Fatalf("unexpected firmware version: %#v", probeResult.firmwareVersion)
	}
	if got := probeResult.firmwareConfig["$20"]; got != int64(1) {
		t.Fatalf("unexpected raw firmware config $20: %#v", got)
	}
	if got := probeResult.firmwareConfig["homing_enabled"]; got != true {
		t.Fatalf("unexpected derived homing_enabled: %#v", got)
	}
	if got := probeResult.firmwareConfig["soft_limits"]; got != true {
		t.Fatalf("unexpected derived soft_limits: %#v", got)
	}
	if got := probeResult.firmwareConfig["hard_limits"]; got != false {
		t.Fatalf("unexpected derived hard_limits: %#v", got)
	}
	if got := probeResult.firmwareConfig["max_travel_x"]; got != 830.0 {
		t.Fatalf("unexpected derived max_travel_x: %#v", got)
	}
	if probeResult.activeState != "Hold" {
		t.Fatalf("unexpected activeState: %#v", probeResult.activeState)
	}
	if probeResult.subState != "0" {
		t.Fatalf("unexpected subState: %#v", probeResult.subState)
	}
	if probeResult.hasHomed == nil || !*probeResult.hasHomed {
		t.Fatalf("expected hasHomed=true from status probe, got %#v", probeResult.hasHomed)
	}
}

func TestExtractFirmwareConfigReturnsNilWhenNoSettingsPresent(t *testing.T) {
	config := extractFirmwareConfig("Grbl 1.1h ['$' for help]\nok\n")
	if config != nil {
		t.Fatalf("expected nil firmware config without $$ lines, got %#v", config)
	}
}

func TestApplyFirmwareConfigSummaryPromotesStableTopLevelSettings(t *testing.T) {
	settings := map[string]interface{}{}
	applyFirmwareConfigSummary(settings, map[string]interface{}{
		"soft_limits":    true,
		"hard_limits":    false,
		"homing_enabled": true,
		"max_travel_x":   830.0,
	})

	if got := settings["soft_limits"]; got != true {
		t.Fatalf("expected soft_limits summary, got %#v", got)
	}
	if got := settings["hard_limits"]; got != false {
		t.Fatalf("expected hard_limits summary, got %#v", got)
	}
	if got := settings["homing_enabled"]; got != true {
		t.Fatalf("expected homing_enabled summary, got %#v", got)
	}
	if got := settings["max_travel_x"]; got != 830.0 {
		t.Fatalf("expected max_travel_x summary, got %#v", got)
	}
}

func TestOpenSessionIncludesLiveFirmwareConfigInControllerSettings(t *testing.T) {
	customDevices := []*gen.Device{{
		ID:           "/dev/ttyUSB99",
		Kind:         "serial",
		Path:         ptrString("/dev/ttyUSB99"),
		DisplayName:  "/dev/ttyUSB99",
		Capabilities: []string{"open_session"},
	}}
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: customDevices}
	service.detector = staticControllerDetector{
		detection: &controllerDetection{
			controllerType:  "grbl",
			connectionState: "connected",
			controllerState: map[string]interface{}{
				"connection_state": "connected",
			},
			controllerSetting: map[string]interface{}{
				"controller_type": "grbl",
				"settings_source": "serial_probe",
				"soft_limits":     true,
				"homing_enabled":  true,
				"firmware_config": map[string]interface{}{
					"$20":            int64(1),
					"$22":            int64(1),
					"soft_limits":    true,
					"homing_enabled": true,
				},
			},
			senderStatus: map[string]interface{}{
				"connection_state": "connected",
				"active":           true,
			},
		},
	}

	result, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "/dev/ttyUSB99",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	settings, ok := result.Snapshot.ControllerSettings.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller settings map, got %T", result.Snapshot.ControllerSettings)
	}
	if got := settings["settings_source"]; got != "serial_probe" {
		t.Fatalf("expected settings_source serial_probe, got %#v", got)
	}
	if got := settings["soft_limits"]; got != true {
		t.Fatalf("expected top-level soft_limits from live config, got %#v", got)
	}
	if got := settings["homing_enabled"]; got != true {
		t.Fatalf("expected top-level homing_enabled from live config, got %#v", got)
	}
	firmwareConfig, ok := settings["firmware_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected firmware_config map, got %T", settings["firmware_config"])
	}
	if got := firmwareConfig["homing_enabled"]; got != true {
		t.Fatalf("expected derived homing_enabled, got %#v", got)
	}
	if result.Snapshot.HomingState == nil {
		t.Fatal("expected homing state to be derived from live firmware config")
	}
	if !result.Snapshot.HomingState.HomingRequired {
		t.Fatal("expected homing_required=true from firmware config")
	}

	stream := &captureSubscribeEventsStream{}
	if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
		SessionID: result.Session.ID,
		ClientID:  "renderer",
	}, stream); err != nil {
		t.Fatalf("SubscribeEvents returned error: %v", err)
	}
	foundHomingState := false
	for _, event := range stream.events {
		if event.Type != "homing_state_changed" {
			continue
		}
		foundHomingState = true
		payload, ok := event.Payload.(map[string]interface{})
		if !ok {
			t.Fatalf("expected homing_state_changed payload map, got %T", event.Payload)
		}
		if got := payload["homing_required"]; got != true {
			t.Fatalf("expected homing_required=true, got %#v", got)
		}
		if got := payload["has_homed"]; got != false {
			t.Fatalf("unexpected homing state payload: %#v", payload)
		}
	}
	if !foundHomingState {
		t.Fatal("expected replay to include homing_state_changed event")
	}
}

func TestOpenSessionUsesProbeRuntimeStateForWorkflowAlarmAndHoming(t *testing.T) {
	customDevices := []*gen.Device{{
		ID:           "/dev/ttyUSB99",
		Kind:         "serial",
		Path:         ptrString("/dev/ttyUSB99"),
		DisplayName:  "/dev/ttyUSB99",
		Capabilities: []string{"open_session"},
	}}
	hasHomed := false
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: customDevices}
	service.detector = staticControllerDetector{
		detection: &controllerDetection{
			controllerType:  "grblhal",
			connectionState: "connected",
			workflowState:   "paused",
			controllerState: map[string]interface{}{
				"connection_state": "connected",
				"active_state":     "Alarm",
				"alarm_code":       "11",
			},
			controllerSetting: map[string]interface{}{
				"controller_type": "grblhal",
				"settings_source": "serial_probe",
				"homing_enabled":  true,
			},
			senderStatus: map[string]interface{}{
				"connection_state": "connected",
				"active":           true,
				"hold":             true,
				"holdReason":       "controller_hold",
			},
			feederStatus: map[string]interface{}{
				"queue_state": "paused",
				"hold":        true,
				"holdReason":  "controller_hold",
				"queue":       int64(0),
			},
			homingState: &gen.HomingState{
				HomingRequired: true,
				HasHomed:       hasHomed,
			},
			alarmState: map[string]interface{}{
				"code":    "11",
				"message": "controller alarm 11",
			},
			lastError: ptrString("controller_alarm: controller alarm 11"),
		},
	}

	result, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "/dev/ttyUSB99",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if result.Session.WorkflowState != "paused" {
		t.Fatalf("expected workflow_state paused from probe runtime state, got %s", result.Session.WorkflowState)
	}
	if result.Snapshot.HomingState == nil || !result.Snapshot.HomingState.HomingRequired {
		t.Fatalf("expected homing_required from probe runtime state, got %#v", result.Snapshot.HomingState)
	}
	if result.Snapshot.AlarmState == nil {
		t.Fatal("expected alarm_state from probe runtime state")
	}
	if result.Snapshot.LastError == nil || !strings.Contains(*result.Snapshot.LastError, "controller_alarm") {
		t.Fatalf("expected last_error from probe runtime state, got %#v", result.Snapshot.LastError)
	}
	senderStatus, ok := result.Snapshot.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", result.Snapshot.SenderStatus)
	}
	if got := senderStatus["hold"]; got != true {
		t.Fatalf("expected sender hold=true from probe runtime state, got %#v", got)
	}
	controllerState, ok := result.Snapshot.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", result.Snapshot.ControllerState)
	}
	if got := controllerState["has_alarm"]; got != true {
		t.Fatalf("expected controller has_alarm=true from probe runtime state, got %#v", got)
	}
	if got := controllerState["status"]; got != "alarm" {
		t.Fatalf("expected controller status alarm from probe runtime state, got %#v", got)
	}
}

func TestOpenSessionRemainsInProbingStateWithoutControllerSignal(t *testing.T) {
	customDevices := append(cloneDevices(defaultKnownDevices), []*gen.Device{{
		ID:           "sim://unknown",
		Kind:         "serial",
		Path:         func() *string { v := "sim://unknown"; return &v }(),
		DisplayName:  "Unknown Simulator",
		Capabilities: []string{"simulate", "open_session"},
	}}...)
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: customDevices}

	result, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://unknown",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if result.Session.ControllerType != "unknown" {
		t.Fatalf("expected unknown controller type, got %s", result.Session.ControllerType)
	}
	if result.Session.ConnectionState != "probing_firmware" {
		t.Fatalf("expected probing_firmware state, got %s", result.Session.ConnectionState)
	}
	senderStatus, ok := result.Snapshot.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", result.Snapshot.SenderStatus)
	}
	if got := senderStatus["active"]; got != false {
		t.Fatalf("expected inactive sender during probing, got %#v", got)
	}
	feederStatus, ok := result.Snapshot.FeederStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected feeder status map, got %T", result.Snapshot.FeederStatus)
	}
	if got := feederStatus["queue_state"]; got != "idle" {
		t.Fatalf("expected idle feeder during probing, got %#v", got)
	}
}

func TestSubscribeEventsReplaysBootstrapAndAttachEventsInOrder(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: func() *string {
			id := "launcher"
			return &id
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	_, err = service.AttachClient(context.Background(), &gen.AttachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "renderer",
	})
	if err != nil {
		t.Fatalf("AttachClient returned error: %v", err)
	}

	_, err = service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	})
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}

	if _, err = service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StartJob returned error: %v", err)
	}

	if _, err = service.PauseJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("PauseJob returned error: %v", err)
	}

	if _, err = service.ResumeJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("ResumeJob returned error: %v", err)
	}

	if _, err = service.StopJob(context.Background(), &gen.StopJobPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StopJob returned error: %v", err)
	}

	if _, err = service.UnloadFile(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("UnloadFile returned error: %v", err)
	}

	stream := &captureSubscribeEventsStream{}
	if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "renderer",
	}, stream); err != nil {
		t.Fatalf("SubscribeEvents returned error: %v", err)
	}

	if got := len(stream.events); got != 39 {
		t.Fatalf("expected 39 replayed events, got %d", got)
	}
	expectedTypes := []string{
		"session_opened",
		"connection_state_changed",
		"controller_state_changed",
		"controller_settings_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"homing_state_changed",
		"controller_detected",
		"client_attached",
		"connection_state_changed",
		"controller_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"file_loaded",
		"workflow_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"job_started",
		"job_progress",
		"workflow_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"job_paused",
		"workflow_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"job_resumed",
		"workflow_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"job_stopped",
		"job_progress",
		"workflow_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"file_unloaded",
		"workflow_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
	}
	for index, event := range stream.events {
		if event.Type != expectedTypes[index] {
			t.Fatalf("event %d type mismatch: got %s want %s", index, event.Type, expectedTypes[index])
		}
		if event.Sequence != int64(index+1) {
			t.Fatalf("event %d sequence mismatch: got %d want %d", index, event.Sequence, index+1)
		}
		if event.SessionID != openResult.Session.ID {
			t.Fatalf("event %d session mismatch: got %s want %s", index, event.SessionID, openResult.Session.ID)
		}
	}

	controllerSettingsPayload, ok := stream.events[3].Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller_settings_changed payload map, got %T", stream.events[3].Payload)
	}
	if got := controllerSettingsPayload["controller_type"]; got != "grblhal" {
		t.Fatalf("expected controller_settings_changed payload controller_type=grblhal, got %#v", got)
	}

	controllerStatePayload, ok := stream.events[2].Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller_state_changed payload map, got %T", stream.events[2].Payload)
	}
	if got := controllerStatePayload["controller_type"]; got != "grblhal" {
		t.Fatalf("expected controller_state_changed payload controller_type=grblhal, got %#v", got)
	}
}

func TestJobProgressEventsReplayDeterministically(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if _, err := service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StartJob returned error: %v", err)
	}
	if _, err := service.StopJob(context.Background(), &gen.StopJobPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StopJob returned error: %v", err)
	}

	stream := &captureSubscribeEventsStream{}
	if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "renderer",
	}, stream); err != nil {
		t.Fatalf("SubscribeEvents returned error: %v", err)
	}

	progressValues := make([]float64, 0, 2)
	for _, event := range stream.events {
		if event.Type != "job_progress" {
			continue
		}
		payload, ok := event.Payload.(map[string]interface{})
		if !ok {
			t.Fatalf("expected job_progress payload map, got %T", event.Payload)
		}
		progress, ok := payload["progress"].(float64)
		if !ok {
			t.Fatalf("expected numeric progress payload, got %#v", payload["progress"])
		}
		progressValues = append(progressValues, progress)
	}

	if len(progressValues) != 2 {
		t.Fatalf("expected 2 job_progress events, got %d", len(progressValues))
	}
	if progressValues[0] != 0 {
		t.Fatalf("expected first job_progress=0, got %#v", progressValues[0])
	}
	if progressValues[1] != 100 {
		t.Fatalf("expected second job_progress=100, got %#v", progressValues[1])
	}
}

func TestJobStopReplayIncludesTerminalOutcomeDetails(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		force           bool
		expectedOutcome string
	}{
		{name: "completed", force: false, expectedOutcome: "completed"},
		{name: "cancelled", force: true, expectedOutcome: "cancelled"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewMemoryMachineService()
			service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
			openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
				DeviceID: "sim://loopback",
			})
			if err != nil {
				t.Fatalf("OpenSession returned error: %v", err)
			}

			if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
				SessionID: openResult.Session.ID,
				Name:      "part.nc",
				Content:   "G0 X0 Y0\nG1 X1 Y1",
			}); err != nil {
				t.Fatalf("LoadFile returned error: %v", err)
			}
			if _, err := service.StartJob(context.Background(), &gen.SessionIDPayload{
				SessionID: openResult.Session.ID,
			}); err != nil {
				t.Fatalf("StartJob returned error: %v", err)
			}
			force := testCase.force
			if _, err := service.StopJob(context.Background(), &gen.StopJobPayload{
				SessionID: openResult.Session.ID,
				Force:     &force,
			}); err != nil {
				t.Fatalf("StopJob returned error: %v", err)
			}

			stream := &captureSubscribeEventsStream{}
			if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
				SessionID: openResult.Session.ID,
				ClientID:  "renderer",
			}, stream); err != nil {
				t.Fatalf("SubscribeEvents returned error: %v", err)
			}

			var stoppedPayload map[string]interface{}
			var progressPayload map[string]interface{}
			for _, event := range stream.events {
				switch event.Type {
				case "job_stopped":
					payload, ok := event.Payload.(map[string]interface{})
					if !ok {
						t.Fatalf("expected job_stopped payload map, got %T", event.Payload)
					}
					stoppedPayload = payload
				case "job_progress":
					payload, ok := event.Payload.(map[string]interface{})
					if !ok {
						t.Fatalf("expected job_progress payload map, got %T", event.Payload)
					}
					if progress, _ := payload["progress"].(float64); progress == 100 {
						progressPayload = payload
					}
				}
			}

			if stoppedPayload == nil {
				t.Fatal("expected replay to include job_stopped payload")
			}
			if got := stoppedPayload["outcome"]; got != testCase.expectedOutcome {
				t.Fatalf("expected job_stopped outcome %s, got %#v", testCase.expectedOutcome, got)
			}
			if got := stoppedPayload["force"]; got != testCase.force {
				t.Fatalf("expected job_stopped force=%t, got %#v", testCase.force, got)
			}
			if progressPayload == nil {
				t.Fatal("expected replay to include terminal job_progress payload")
			}
			if got := progressPayload["terminal"]; got != true {
				t.Fatalf("expected terminal job_progress flag, got %#v", got)
			}
		})
	}
}

func TestAttachClientIsIdempotentAndSnapshotIsReplayedFromCopy(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: func() *string {
			id := "launcher"
			return &id
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	record, err := service.getSessionRecord(session.Session.ID)
	if err != nil {
		t.Fatalf("expected in-memory session record: %v", err)
	}
	if got := len(record.clients); got != 1 {
		t.Fatalf("expected one bootstrap client from OpenSession, got %d", got)
	}

	attachPayload := &gen.AttachClientPayload{
		SessionID: session.Session.ID,
		ClientID:  "renderer",
	}
	first, err := service.AttachClient(context.Background(), attachPayload)
	if err != nil {
		t.Fatalf("AttachClient first call returned error: %v", err)
	}
	second, err := service.AttachClient(context.Background(), attachPayload)
	if err != nil {
		t.Fatalf("AttachClient second call returned error: %v", err)
	}
	if first == nil || first.Session == nil {
		t.Fatal("first attach should return snapshot")
	}
	if second == nil || second.Session == nil {
		t.Fatal("second attach should return snapshot")
	}
	if first.Session.ID != second.Session.ID {
		t.Fatalf("expected same session on replay, got %s and %s", first.Session.ID, second.Session.ID)
	}

	if got := len(record.clients); got != 2 {
		t.Fatalf("expected 2 clients total after initial launch + renderer attach, got %d", got)
	}

	if _, ok := record.clients[attachPayload.ClientID]; !ok {
		t.Fatalf("expected attach client %s to remain tracked", attachPayload.ClientID)
	}

	if got := len(record.events); got != 13 {
		t.Fatalf("expected duplicate attach to avoid duplicate events, got %d events", got)
	}

	_, err = service.LoadFile(context.Background(), &gen.LoadFilePayload{
		// Missing session_id verifies required-field guardrails.
	})
	if err == nil {
		t.Fatal("expected LoadFile with invalid payload to return error")
	}

	loaded, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: session.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	})
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if loaded == nil || loaded.LoadedFile == nil || loaded.LoadedFile.Name != "part.nc" {
		t.Fatal("expected loaded snapshot after file upload")
	}

	first.RuntimeFlags.CanStartJob = false
	replayed, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if replayed.RuntimeFlags == nil || !replayed.RuntimeFlags.CanStartJob {
		t.Fatal("expected replayed snapshot to be isolated copy with original state preserved")
	}
}

func TestFeederStatusTracksFileAndWorkflowLifecycle(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	snapshot, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	})
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	feederStatus, ok := snapshot.FeederStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected feeder status map, got %T", snapshot.FeederStatus)
	}
	if got := feederStatus["queue_state"]; got != "ready" {
		t.Fatalf("expected ready feeder after load, got %#v", got)
	}
	if got := feederStatus["queue"]; got != int64(2) {
		t.Fatalf("expected queued line count after load, got %#v", got)
	}

	senderStatus, ok := snapshot.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", snapshot.SenderStatus)
	}
	if got := senderStatus["name"]; got != "part.nc" {
		t.Fatalf("expected sender name after load, got %#v", got)
	}
	if got := senderStatus["total"]; got != int64(2) {
		t.Fatalf("expected sender total line count after load, got %#v", got)
	}
	if got := senderStatus["sent"]; got != int64(0) {
		t.Fatalf("expected sender sent count 0 after load, got %#v", got)
	}

	if _, err := service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StartJob returned error: %v", err)
	}
	started, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	feederStatus, ok = started.FeederStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected feeder status map, got %T", started.FeederStatus)
	}
	if got := feederStatus["queue_state"]; got != "running" {
		t.Fatalf("expected running feeder after start, got %#v", got)
	}
	if got := feederStatus["queue"]; got != int64(2) {
		t.Fatalf("expected queued line count to remain deterministic at start, got %#v", got)
	}
	senderStatus, ok = started.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", started.SenderStatus)
	}
	if got := senderStatus["hold"]; got != false {
		t.Fatalf("expected sender hold=false while running, got %#v", got)
	}
	if got := senderStatus["workflowState"]; got != "running" {
		t.Fatalf("expected sender workflowState running, got %#v", got)
	}

	if _, err := service.PauseJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("PauseJob returned error: %v", err)
	}
	paused, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	feederStatus, ok = paused.FeederStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected feeder status map, got %T", paused.FeederStatus)
	}
	if got := feederStatus["queue_state"]; got != "paused" {
		t.Fatalf("expected paused feeder after pause, got %#v", got)
	}
	if got := feederStatus["hold"]; got != true {
		t.Fatalf("expected feeder hold=true while paused, got %#v", got)
	}
	senderStatus, ok = paused.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", paused.SenderStatus)
	}
	if got := senderStatus["hold"]; got != true {
		t.Fatalf("expected sender hold=true while paused, got %#v", got)
	}
	if got := senderStatus["holdReason"]; got != "pause" {
		t.Fatalf("expected sender holdReason pause, got %#v", got)
	}
}

func TestRejectedCommandsEmitErrorAndAlarmEvents(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	_, err = service.PauseJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	})
	requireMachineErrorCode(t, err, "command_rejected")

	stream := &captureSubscribeEventsStream{}
	if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "renderer",
	}, stream); err != nil {
		t.Fatalf("SubscribeEvents returned error: %v", err)
	}

	foundError := false
	foundAlarm := false
	for _, event := range stream.events {
		if event.Type == "error_raised" {
			foundError = true
		}
		if event.Type == "alarm_raised" {
			foundAlarm = true
		}
	}
	if !foundError {
		t.Fatal("expected replay to include error_raised event")
	}
	if !foundAlarm {
		t.Fatal("expected replay to include alarm_raised event")
	}
}

func TestResolveSessionReplaysLoadedFileStateAfterReconnect(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: func() *string {
			id := "launcher"
			return &id
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if _, err := service.DetachClient(context.Background(), &gen.DetachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "launcher",
	}); err != nil {
		t.Fatalf("DetachClient returned error: %v", err)
	}

	reconnected, err := service.ResolveSession(context.Background(), &gen.ResolveSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: "renderer",
	})
	if err != nil {
		t.Fatalf("ResolveSession returned error: %v", err)
	}
	if reconnected.LoadedFile == nil || reconnected.LoadedFile.Name != "part.nc" {
		t.Fatalf("expected loaded file to replay after reconnect, got %#v", reconnected.LoadedFile)
	}
	if reconnected.RuntimeFlags == nil || !reconnected.RuntimeFlags.CanStartJob {
		t.Fatal("expected reconnect snapshot to preserve startable loaded-file state")
	}
}

func TestResolveSessionReplaysPausedStateAfterReconnect(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: func() *string {
			id := "launcher"
			return &id
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if _, err := service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StartJob returned error: %v", err)
	}
	if _, err := service.PauseJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("PauseJob returned error: %v", err)
	}
	if _, err := service.DetachClient(context.Background(), &gen.DetachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "launcher",
	}); err != nil {
		t.Fatalf("DetachClient returned error: %v", err)
	}

	reconnected, err := service.ResolveSession(context.Background(), &gen.ResolveSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: "renderer",
	})
	if err != nil {
		t.Fatalf("ResolveSession returned error: %v", err)
	}
	if reconnected.Session.WorkflowState != "paused" {
		t.Fatalf("expected paused workflow after reconnect, got %s", reconnected.Session.WorkflowState)
	}
	if reconnected.RuntimeFlags == nil || !reconnected.RuntimeFlags.CanResumeJob {
		t.Fatal("expected reconnect snapshot to preserve resume capability")
	}
	controllerState, ok := reconnected.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", reconnected.ControllerState)
	}
	if got := controllerState["status"]; got != "hold" {
		t.Fatalf("expected paused controller status after reconnect, got %#v", got)
	}
}

func TestResolveSessionReplaysAlarmStateAfterReconnect(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: func() *string {
			id := "launcher"
			return &id
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.PauseJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err == nil {
		t.Fatal("expected rejected PauseJob to raise alarm state")
	}
	if _, err := service.DetachClient(context.Background(), &gen.DetachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "launcher",
	}); err != nil {
		t.Fatalf("DetachClient returned error: %v", err)
	}

	reconnected, err := service.ResolveSession(context.Background(), &gen.ResolveSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: "renderer",
	})
	if err != nil {
		t.Fatalf("ResolveSession returned error: %v", err)
	}
	if reconnected.AlarmState == nil {
		t.Fatal("expected alarm state to replay after reconnect")
	}
	if reconnected.LastError == nil || !strings.Contains(*reconnected.LastError, "command_rejected") {
		t.Fatalf("expected last_error to replay after reconnect, got %#v", reconnected.LastError)
	}
	controllerState, ok := reconnected.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", reconnected.ControllerState)
	}
	if got := controllerState["has_alarm"]; got != true {
		t.Fatalf("expected has_alarm=true after reconnect, got %#v", got)
	}
}

func TestResolveSessionReplaysStoppedStateAfterReconnect(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: func() *string {
			id := "launcher"
			return &id
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if _, err := service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StartJob returned error: %v", err)
	}
	if _, err := service.StopJob(context.Background(), &gen.StopJobPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StopJob returned error: %v", err)
	}
	if _, err := service.DetachClient(context.Background(), &gen.DetachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "launcher",
	}); err != nil {
		t.Fatalf("DetachClient returned error: %v", err)
	}

	reconnected, err := service.ResolveSession(context.Background(), &gen.ResolveSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: "renderer",
	})
	if err != nil {
		t.Fatalf("ResolveSession returned error: %v", err)
	}
	if reconnected.Session.WorkflowState != "idle" {
		t.Fatalf("expected idle workflow after stop/reconnect, got %s", reconnected.Session.WorkflowState)
	}
	if reconnected.RuntimeFlags == nil || !reconnected.RuntimeFlags.CanStartJob {
		t.Fatal("expected reconnect snapshot to preserve loaded-but-stopped start capability")
	}
	if reconnected.RuntimeFlags.CanStopJob {
		t.Fatal("expected reconnect snapshot to preserve stopped can_stop_job=false")
	}
}

func TestDetachClientIsIdempotentAndDisconnectsLastClient(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	_, err = service.AttachClient(context.Background(), &gen.AttachClientPayload{
		SessionID: session.Session.ID,
		ClientID:  "renderer",
	})
	if err != nil {
		t.Fatalf("AttachClient returned error: %v", err)
	}

	first, err := service.DetachClient(context.Background(), &gen.DetachClientPayload{
		SessionID: session.Session.ID,
		ClientID:  "renderer",
	})
	if err != nil {
		t.Fatalf("DetachClient first call returned error: %v", err)
	}
	if first == nil || !first.OK {
		t.Fatal("expected first DetachClient to return OK=true")
	}

	second, err := service.DetachClient(context.Background(), &gen.DetachClientPayload{
		SessionID: session.Session.ID,
		ClientID:  "renderer",
	})
	if err != nil {
		t.Fatalf("DetachClient second call returned error: %v", err)
	}
	if second == nil || second.OK {
		t.Fatal("expected second DetachClient to return OK=false")
	}

	snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if snapshot.Session.ConnectionState != "disconnected" {
		t.Fatalf("expected disconnected session after last client detaches, got %s", snapshot.Session.ConnectionState)
	}

	senderStatus, ok := snapshot.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", snapshot.SenderStatus)
	}
	if got := senderStatus["active"]; got != false {
		t.Fatalf("expected inactive sender after detach, got %#v", got)
	}

	stream := &captureSubscribeEventsStream{}
	if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
		SessionID: session.Session.ID,
		ClientID:  "renderer",
	}, stream); err != nil {
		t.Fatalf("SubscribeEvents returned error: %v", err)
	}

	expectedTypes := []string{
		"session_opened",
		"connection_state_changed",
		"controller_state_changed",
		"controller_settings_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"homing_state_changed",
		"controller_detected",
		"client_attached",
		"connection_state_changed",
		"controller_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
		"client_detached",
		"connection_state_changed",
		"controller_state_changed",
		"sender_status_changed",
		"feeder_status_changed",
	}
	if got := len(stream.events); got != len(expectedTypes) {
		t.Fatalf("expected %d replayed events, got %d", len(expectedTypes), got)
	}
	for index, event := range stream.events {
		if event.Type != expectedTypes[index] {
			t.Fatalf("event %d type mismatch: got %s want %s", index, event.Type, expectedTypes[index])
		}
	}
}

func TestSubscribeEventsReplaysSessionClosedAfterClose(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: func() *string {
			id := "launcher"
			return &id
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.CloseSession(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("CloseSession returned error: %v", err)
	}

	stream := &captureSubscribeEventsStream{}
	if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "renderer",
	}, stream); err != nil {
		t.Fatalf("SubscribeEvents returned error: %v", err)
	}

	expectedTail := []string{
		"connection_state_changed",
		"controller_state_changed",
		"sender_status_changed",
		"session_closed",
	}
	if got := len(stream.events); got < len(expectedTail) {
		t.Fatalf("expected at least %d close events, got %d", len(expectedTail), got)
	}
	tail := stream.events[len(stream.events)-len(expectedTail):]
	for index, event := range tail {
		if event.Type != expectedTail[index] {
			t.Fatalf("close replay event %d mismatch: got %s want %s", index, event.Type, expectedTail[index])
		}
	}
}

func TestFlashFirmwareReplaysFlashLifecycleEventsForActiveSession(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	result, err := service.FlashFirmware(context.Background(), &gen.FlashFirmwarePayload{
		DeviceID:       "sim://loopback",
		Image:          "firmware.hex",
		ControllerType: ptrString("grblhal"),
	})
	if err != nil {
		t.Fatalf("FlashFirmware returned error: %v", err)
	}
	if result == nil || !result.Accepted {
		t.Fatal("expected FlashFirmware accepted result")
	}

	stream := &captureSubscribeEventsStream{}
	if err := service.SubscribeEvents(context.Background(), &gen.AttachClientPayload{
		SessionID: openResult.Session.ID,
		ClientID:  "renderer",
	}, stream); err != nil {
		t.Fatalf("SubscribeEvents returned error: %v", err)
	}

	expectedTail := []string{
		"connection_state_changed",
		"controller_state_changed",
		"flash_started",
		"flash_progress",
		"flash_completed",
		"connection_state_changed",
		"controller_state_changed",
	}
	if got := len(stream.events); got < len(expectedTail) {
		t.Fatalf("expected at least %d flash events, got %d", len(expectedTail), got)
	}
	tail := stream.events[len(stream.events)-len(expectedTail):]
	for index, event := range tail {
		if event.Type != expectedTail[index] {
			t.Fatalf("flash replay event %d mismatch: got %s want %s", index, event.Type, expectedTail[index])
		}
	}

	progressPayload, ok := tail[3].Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("expected flash_progress payload map, got %T", tail[3].Payload)
	}
	if got := progressPayload["progress"]; got != 100.0 {
		t.Fatalf("expected flash_progress payload to reach 100, got %#v", got)
	}

	firstConnectionPayload, ok := tail[0].Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("expected initial connection_state_changed payload map, got %T", tail[0].Payload)
	}
	if got := firstConnectionPayload["connection_state"]; got != "flashing" {
		t.Fatalf("expected flashing connection state during flash, got %#v", got)
	}

	finalConnectionPayload, ok := tail[5].Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("expected final connection_state_changed payload map, got %T", tail[5].Payload)
	}
	if got := finalConnectionPayload["connection_state"]; got != "connected" {
		t.Fatalf("expected connected connection state after flash, got %#v", got)
	}
}

func TestFlashFirmwareWithoutActiveSessionStillAccepts(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}

	result, err := service.FlashFirmware(context.Background(), &gen.FlashFirmwarePayload{
		DeviceID: "sim://loopback",
		Image:    "firmware.hex",
	})
	if err != nil {
		t.Fatalf("FlashFirmware returned error: %v", err)
	}
	if result == nil || !result.Accepted {
		t.Fatal("expected FlashFirmware accepted result without active session")
	}
}

func TestFlashFirmwareRejectsActiveJobSession(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if _, err := service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err != nil {
		t.Fatalf("StartJob returned error: %v", err)
	}

	if _, err := service.FlashFirmware(context.Background(), &gen.FlashFirmwarePayload{
		DeviceID: "sim://loopback",
		Image:    "firmware.hex",
	}); err == nil {
		t.Fatal("expected FlashFirmware to reject active job session")
	} else {
		requireMachineErrorCode(t, err, "flash_not_allowed")
	}
}

func TestRuntimeWritesRejectWhileSessionIsFlashing(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	record, err := service.getSessionRecord(openResult.Session.ID)
	if err != nil {
		t.Fatalf("getSessionRecord returned error: %v", err)
	}
	record.session.ConnectionState = "flashing"

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: openResult.Session.ID,
		Command: &gen.MachineCommand{
			Type: "status_report",
		},
	}); err == nil {
		t.Fatal("expected SendCommand to reject while flashing")
	} else {
		requireMachineErrorCode(t, err, "flash_not_allowed")
	}

	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: openResult.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0",
	}); err == nil {
		t.Fatal("expected LoadFile to reject while flashing")
	} else {
		requireMachineErrorCode(t, err, "flash_not_allowed")
	}

	if _, err := service.StartJob(context.Background(), &gen.SessionIDPayload{
		SessionID: openResult.Session.ID,
	}); err == nil {
		t.Fatal("expected StartJob to reject while flashing")
	} else {
		requireMachineErrorCode(t, err, "flash_not_allowed")
	}
}

func TestResolveSessionAttachesClientByDeviceID(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	openResult, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	snapshot, err := service.ResolveSession(context.Background(), &gen.ResolveSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: "renderer",
	})
	if err != nil {
		t.Fatalf("ResolveSession returned error: %v", err)
	}
	if snapshot == nil || snapshot.Session == nil {
		t.Fatal("expected ResolveSession to return snapshot")
	}
	if snapshot.Session.ID != openResult.Session.ID {
		t.Fatalf("expected ResolveSession to return existing session %s, got %s", openResult.Session.ID, snapshot.Session.ID)
	}
	if snapshot.Session.ConnectionState != "connected" {
		t.Fatalf("expected ResolveSession to reconnect session, got %s", snapshot.Session.ConnectionState)
	}

	record, err := service.getSessionRecord(openResult.Session.ID)
	if err != nil {
		t.Fatalf("expected in-memory session record: %v", err)
	}
	if _, ok := record.clients["renderer"]; !ok {
		t.Fatal("expected ResolveSession to register renderer client")
	}
}

func TestResolveSessionRejectsMissingDeviceSession(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}

	_, err := service.ResolveSession(context.Background(), &gen.ResolveSessionPayload{
		DeviceID: "sim://loopback",
		ClientID: "renderer",
	})
	requireMachineErrorCode(t, err, "session_not_found")
}

func TestSendCommandRequiresCommandPayload(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
	}); err == nil {
		t.Fatal("expected SendCommand to fail when command is missing")
	}
}

func TestSendCommandAppliesStructuredWorkflowCommands(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: session.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type: "cycle_start",
		},
	}); err != nil {
		t.Fatalf("cycle_start command returned error: %v", err)
	}

	running, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if running.Session.WorkflowState != "running" {
		t.Fatalf("expected workflow running after cycle_start, got %s", running.Session.WorkflowState)
	}

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type: "feed_hold",
		},
	}); err != nil {
		t.Fatalf("feed_hold command returned error: %v", err)
	}

	paused, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if paused.Session.WorkflowState != "paused" {
		t.Fatalf("expected workflow paused after feed_hold, got %s", paused.Session.WorkflowState)
	}

	senderStatus, ok := paused.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", paused.SenderStatus)
	}
	if got := senderStatus["hold"]; got != true {
		t.Fatalf("expected sender hold=true after feed_hold, got %#v", got)
	}
}

func TestSendCommandAppliesOverridesAndRawLineMetadata(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	feedOverride := 125
	spindleOverride := 90
	rapidOverride := 50
	rawLine := "G1 X10"
	for _, command := range []*gen.MachineCommand{
		{Type: "set_feed_override", FeedOverride: &feedOverride},
		{Type: "set_spindle_override", SpindleOverride: &spindleOverride},
		{Type: "set_rapid_override", RapidOverride: &rapidOverride},
		{Type: "raw_gcode_line", RawLine: &rawLine},
	} {
		if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
			SessionID: session.Session.ID,
			Command:   command,
		}); err != nil {
			t.Fatalf("SendCommand(%s) returned error: %v", command.Type, err)
		}
	}

	snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	senderStatus, ok := snapshot.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", snapshot.SenderStatus)
	}
	if got := senderStatus["ovF"]; got != 125 {
		t.Fatalf("expected ovF override, got %#v", got)
	}
	if got := senderStatus["ovS"]; got != 90 {
		t.Fatalf("expected ovS override, got %#v", got)
	}
	if got := senderStatus["ovR"]; got != 50 {
		t.Fatalf("expected ovR override, got %#v", got)
	}
	if got := senderStatus["lastLine"]; got != "G1 X10" {
		t.Fatalf("expected lastLine metadata, got %#v", got)
	}
}

func TestSendCommandUnlockAndResetClearAlarmState(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type: "feed_hold",
		},
	}); err == nil {
		t.Fatal("expected feed_hold without running job to be rejected")
	}
	errored, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if errored.AlarmState == nil {
		t.Fatal("expected alarm state after rejected command")
	}

	for _, commandType := range []string{"unlock", "reset"} {
		if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
			SessionID: session.Session.ID,
			Command: &gen.MachineCommand{
				Type: commandType,
			},
		}); err != nil {
			t.Fatalf("%s command returned error: %v", commandType, err)
		}
		snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
			SessionID: session.Session.ID,
		})
		if err != nil {
			t.Fatalf("GetSnapshot returned error: %v", err)
		}
		if snapshot.AlarmState != nil {
			t.Fatalf("expected alarm state cleared after %s, got %#v", commandType, snapshot.AlarmState)
		}
		if snapshot.LastError != nil {
			t.Fatalf("expected last_error cleared after %s, got %#v", commandType, snapshot.LastError)
		}
	}
}

func TestSendCommandStatusHomeSleepAndJogUpdateSnapshotState(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type: "home",
		},
	}); err != nil {
		t.Fatalf("home command returned error: %v", err)
	}

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type: "sleep",
		},
	}); err != nil {
		t.Fatalf("sleep command returned error: %v", err)
	}

	x := 1.5
	y := -2.0
	feedRate := 500.0
	axisMove := &gen.AxisMove{
		X:        &x,
		Y:        &y,
		FeedRate: &feedRate,
	}
	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type:     "jog",
			AxisMove: axisMove,
		},
	}); err != nil {
		t.Fatalf("jog command returned error: %v", err)
	}

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type: "status_report",
		},
	}); err != nil {
		t.Fatalf("status_report command returned error: %v", err)
	}

	snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if snapshot.HomingState == nil || !snapshot.HomingState.HasHomed {
		t.Fatalf("expected homing state to reflect home command, got %#v", snapshot.HomingState)
	}

	controllerState, ok := snapshot.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", snapshot.ControllerState)
	}
	if got := controllerState["active_state"]; got != "Jog" {
		t.Fatalf("expected active_state Jog after jog command, got %#v", got)
	}
	lastJog, ok := controllerState["last_jog"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected last_jog map, got %T", controllerState["last_jog"])
	}
	gotX, ok := lastJog["x"].(*float64)
	if !ok || gotX == nil || *gotX != 1.5 {
		t.Fatalf("expected last_jog.x=1.5, got %#v", lastJog["x"])
	}
	gotY, ok := lastJog["y"].(*float64)
	if !ok || gotY == nil || *gotY != -2.0 {
		t.Fatalf("expected last_jog.y=-2.0, got %#v", lastJog["y"])
	}
	gotFeedRate, ok := lastJog["feed_rate"].(*float64)
	if !ok || gotFeedRate == nil || *gotFeedRate != 500 {
		t.Fatalf("expected last_jog.feed_rate=500, got %#v", lastJog["feed_rate"])
	}
}

func TestSendCommandStatusReportIngestsLiveRuntimeTelemetry(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}
	if _, err := service.LoadFile(context.Background(), &gen.LoadFilePayload{
		SessionID: session.Session.ID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1\nG1 X2 Y2\nG1 X3 Y3",
	}); err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type: "status_report",
			Metadata: map[string]string{
				"controller_type":    "grblHAL",
				"controller_settings": `{"homing_enabled":true,"$22":1}`,
				"controller_state":   `{"status":{"activeState":"Run","subState":2},"mpos":{"x":12.5}}`,
				"sender_status":      `{"total":120,"sent":30,"received":30,"currentLineRunning":30,"startTime":"2026-03-11T10:00:00Z","bufferSize":128}`,
				"feeder_status":      `{"queue":90,"pending":4,"buffered":true}`,
				"workflow_state":     "running",
				"homing_has_homed":   "true",
				"error_code":         "2",
				"error_message":      "Door open",
				"error_is_alarm":     "true",
				"has_alarm":          "true",
			},
		},
	}); err != nil {
		t.Fatalf("status_report command returned error: %v", err)
	}

	snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if snapshot.Session.ControllerType != "grblhal" {
		t.Fatalf("expected live telemetry to normalize controller type to grblhal, got %s", snapshot.Session.ControllerType)
	}
	if snapshot.Session.WorkflowState != "running" {
		t.Fatalf("expected workflow running from telemetry, got %s", snapshot.Session.WorkflowState)
	}
	if snapshot.HomingState == nil || !snapshot.HomingState.HasHomed {
		t.Fatalf("expected homing state to reflect live telemetry, got %#v", snapshot.HomingState)
	}
	if snapshot.HomingState.HomingRequired {
		t.Fatalf("expected homing_required=false after has_homed telemetry, got %#v", snapshot.HomingState)
	}
	if snapshot.AlarmState == nil {
		t.Fatal("expected alarm state from live telemetry")
	}
	if snapshot.LastError == nil || !strings.Contains(*snapshot.LastError, "Door open") {
		t.Fatalf("expected last_error from live telemetry, got %#v", snapshot.LastError)
	}

	controllerState, ok := snapshot.ControllerState.(map[string]interface{})
	if !ok {
		t.Fatalf("expected controller state map, got %T", snapshot.ControllerState)
	}
	if got := controllerState["active_state"]; got != "Run" {
		t.Fatalf("expected active_state Run, got %#v", got)
	}
	if got := controllerState["sub_state"]; got != float64(2) {
		t.Fatalf("expected sub_state 2, got %#v", got)
	}
	if got := controllerState["has_alarm"]; got != true {
		t.Fatalf("expected has_alarm=true from live telemetry, got %#v", got)
	}

	senderStatus, ok := snapshot.SenderStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected sender status map, got %T", snapshot.SenderStatus)
	}
	if got := senderStatus["sent"]; got != float64(30) {
		t.Fatalf("expected live sender sent=30, got %#v", got)
	}
	if got := senderStatus["received"]; got != float64(30) {
		t.Fatalf("expected live sender received=30, got %#v", got)
	}
	if got := senderStatus["bufferSize"]; got != float64(128) {
		t.Fatalf("expected live sender bufferSize=128, got %#v", got)
	}
	if got := senderStatus["workflowState"]; got != "running" {
		t.Fatalf("expected sender workflowState running, got %#v", got)
	}

	feederStatus, ok := snapshot.FeederStatus.(map[string]interface{})
	if !ok {
		t.Fatalf("expected feeder status map, got %T", snapshot.FeederStatus)
	}
	if got := feederStatus["queue"]; got != float64(90) {
		t.Fatalf("expected live feeder queue=90, got %#v", got)
	}
	if got := feederStatus["pending"]; got != float64(4) {
		t.Fatalf("expected live feeder pending=4, got %#v", got)
	}

	record, err := service.getSessionRecord(session.Session.ID)
	if err != nil {
		t.Fatalf("getSessionRecord returned error: %v", err)
	}
	if record.progressPercent != 25 {
		t.Fatalf("expected progressPercent derived from live sender telemetry, got %v", record.progressPercent)
	}
}

func TestSendCommandRejectsCycleStartWithoutLoadedFile(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{devices: defaultKnownDevices}
	session, err := service.OpenSession(context.Background(), &gen.OpenSessionPayload{
		DeviceID: "sim://loopback",
	})
	if err != nil {
		t.Fatalf("OpenSession returned error: %v", err)
	}

	if _, err := service.SendCommand(context.Background(), &gen.SendCommandPayload{
		SessionID: session.Session.ID,
		Command: &gen.MachineCommand{
			Type: "cycle_start",
		},
	}); err == nil {
		t.Fatal("expected cycle_start without loaded file to be rejected")
	} else {
		requireMachineErrorCode(t, err, "command_rejected")
	}

	snapshot, err := service.GetSnapshot(context.Background(), &gen.SessionIDPayload{
		SessionID: session.Session.ID,
	})
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}
	if snapshot.Session.WorkflowState != "idle" {
		t.Fatalf("expected workflow to remain idle after rejected cycle_start, got %s", snapshot.Session.WorkflowState)
	}
	if snapshot.AlarmState == nil {
		t.Fatal("expected rejected cycle_start to surface alarm state")
	}
}

func TestListDevicesRefreshesFromDiscoveryProvider(t *testing.T) {
	service := NewMemoryMachineService()
	service.discovery = staticDeviceDiscovery{
		devices: []*gen.Device{
			{
				ID:           "/dev/ttyUSB0",
				Kind:         "serial",
				Path:         ptrString("/dev/ttyUSB0"),
				Manufacturer: ptrString("Sienci"),
				VendorID:     ptrString("0483"),
				ProductID:    ptrString("5740"),
				DisplayName:  "/dev/ttyUSB0",
				InUse:        false,
				Capabilities: []string{"open_session"},
			},
			{
				ID:             "tcp://192.168.0.10:23",
				Kind:           "network",
				NetworkAddress: ptrString("192.168.0.10:23"),
				DisplayName:    "192.168.0.10:23",
				InUse:          false,
				Capabilities:   []string{"open_session"},
			},
		},
	}

	devices, err := service.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices returned error: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 discovered devices, got %d", len(devices))
	}
	if devices[0].ID != "/dev/ttyUSB0" {
		t.Fatalf("expected first device to come from discovery provider, got %s", devices[0].ID)
	}
	if devices[1].Kind != "network" {
		t.Fatalf("expected network device kind, got %s", devices[1].Kind)
	}
}

func TestParseNetworkDeviceEnv(t *testing.T) {
	addresses := parseNetworkDeviceEnv("10.0.0.2:23, sim.local:2300, ,192.168.1.2")
	if len(addresses) != 3 {
		t.Fatalf("expected 3 parsed addresses, got %d", len(addresses))
	}
	if addresses[0] != "10.0.0.2:23" || addresses[1] != "sim.local:2300" || addresses[2] != "192.168.1.2" {
		t.Fatalf("unexpected parsed addresses: %#v", addresses)
	}
}
