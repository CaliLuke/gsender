package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	gen "github.com/Sienci-Labs/gsender/machine-core/gen/machine"
	obs "github.com/Sienci-Labs/gsender/machine-core/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type sessionRecord struct {
	session           *gen.Session
	loadedFile        *gen.LoadedFile
	controllerState   map[string]interface{}
	controllerSetting map[string]interface{}
	senderStatus      map[string]interface{}
	feederStatus      map[string]interface{}
	liveSenderStatus  bool
	liveFeederStatus  bool
	homingState       *gen.HomingState
	alarmState        interface{}
	runtimeFlags      *gen.RuntimeFlags
	lastError         *string
	progressPercent   float64
	clients           map[string]struct{}
	events            []*gen.SessionEvent
	nextSequence      int64
}

type runtimeFlagState struct {
	canStart  bool
	canPause  bool
	canResume bool
	canStop   bool
	canUnload bool
}

func annotateMachineSpan(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

func addMachineSpanEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.AddEvent(name, trace.WithAttributes(attrs...))
	}
}

type MemoryMachineService struct {
	mu        sync.RWMutex
	sessions  map[string]*sessionRecord
	closed    map[string][]*gen.SessionEvent
	devices   []*gen.Device
	discovery deviceDiscovery
	detector  controllerDetector
}

var defaultKnownDevices = []*gen.Device{
	{
		ID:           "sim://loopback",
		Kind:         "serial",
		Path:         ptrString("sim://loopback"),
		Manufacturer: ptrString("Sienci Labs"),
		DisplayName:  "Simulated Machine Port",
		InUse:        false,
		Capabilities: []string{"simulate", "open_session"},
	},
	{
		ID:             "tcp://sim.local:23",
		Kind:           "network",
		NetworkAddress: ptrString("sim.local:23"),
		Manufacturer:   ptrString("Sienci Labs"),
		DisplayName:    "Simulated Machine Port",
		InUse:          false,
		Capabilities:   []string{"simulate", "open_session"},
	},
}

func NewMemoryMachineService() *MemoryMachineService {
	return &MemoryMachineService{
		sessions:  make(map[string]*sessionRecord),
		closed:    make(map[string][]*gen.SessionEvent),
		devices:   cloneDevices(defaultKnownDevices),
		discovery: hostDeviceDiscovery{},
		detector:  hostControllerDetector{},
	}
}

func nowRFC3339() *string {
	t := time.Now().UTC().Format(time.RFC3339)
	return &t
}

func machineError(code, message string) error {
	return &gen.MachineError{
		Code:      code,
		Message:   message,
		Retryable: false,
	}
}

func defaultRuntimeFlags() *gen.RuntimeFlags {
	return &gen.RuntimeFlags{
		CanStartJob:      false,
		CanPauseJob:      false,
		CanResumeJob:     false,
		CanStopJob:       false,
		CanUnloadFile:    false,
		CanFlashFirmware: true,
	}
}

func ptrString(value string) *string {
	return &value
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneSession(session *gen.Session) *gen.Session {
	if session == nil {
		return nil
	}
	copied := *session
	copied.CreatedAt = cloneString(session.CreatedAt)
	copied.UpdatedAt = cloneString(session.UpdatedAt)
	return &copied
}

func cloneRuntimeFlags(flags *gen.RuntimeFlags) *gen.RuntimeFlags {
	if flags == nil {
		return nil
	}
	return &gen.RuntimeFlags{
		CanStartJob:      flags.CanStartJob,
		CanPauseJob:      flags.CanPauseJob,
		CanResumeJob:     flags.CanResumeJob,
		CanStopJob:       flags.CanStopJob,
		CanUnloadFile:    flags.CanUnloadFile,
		CanFlashFirmware: flags.CanFlashFirmware,
	}
}

func cloneState(values map[string]interface{}) map[string]interface{} {
	if values == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneHomingState(state *gen.HomingState) *gen.HomingState {
	if state == nil {
		return nil
	}
	copied := *state
	copied.LastHomingAt = cloneString(state.LastHomingAt)
	return &copied
}

func cloneBool(value interface{}) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	case float64:
		return typed != 0, true
	case string:
		switch strings.TrimSpace(strings.ToLower(typed)) {
		case "1", "true", "on", "yes":
			return true, true
		case "0", "false", "off", "no":
			return false, true
		}
	}
	return false, false
}

func cloneStringValue(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case fmt.Stringer:
		return typed.String(), true
	}
	return "", false
}

func cloneFloat64(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed, true
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func cloneLoadedFile(file *gen.LoadedFile) *gen.LoadedFile {
	if file == nil {
		return nil
	}
	copied := *file
	copied.LoadedAt = cloneString(file.LoadedAt)
	copied.LineCount = cloneInt64(file.LineCount)
	copied.Checksum = cloneString(file.Checksum)
	return &copied
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneDevices(devices []*gen.Device) []*gen.Device {
	cloned := make([]*gen.Device, 0, len(devices))
	for _, device := range devices {
		if device == nil {
			continue
		}
		copied := *device
		copied.Path = cloneString(device.Path)
		copied.NetworkAddress = cloneString(device.NetworkAddress)
		copied.Manufacturer = cloneString(device.Manufacturer)
		copied.VendorID = cloneString(device.VendorID)
		copied.ProductID = cloneString(device.ProductID)
		copied.SerialNumber = cloneString(device.SerialNumber)
		copied.DisplayName = device.DisplayName
		copied.Capabilities = append([]string(nil), device.Capabilities...)
		cloned = append(cloned, &copied)
	}
	return cloned
}

func cloneSessionEvent(event *gen.SessionEvent) *gen.SessionEvent {
	if event == nil {
		return nil
	}
	copied := *event
	return &copied
}

func normalizeControllerType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "grbl":
		return "grbl"
	case "grblhal", "grbl_hal", "grbl-hal":
		return "grblhal"
	case "fluidnc":
		return "fluidnc"
	default:
		return "unknown"
	}
}

func inferConnectionState(controllerType string) string {
	if controllerType == "unknown" {
		return "probing_firmware"
	}
	return "connected"
}

func deviceIsSimulator(device *gen.Device) bool {
	if device == nil {
		return false
	}
	for _, capability := range device.Capabilities {
		if capability == "simulate" {
			return true
		}
	}
	return strings.HasPrefix(device.ID, "sim://")
}

func buildControllerSettings(device *gen.Device, controllerType string, payload *gen.OpenSessionPayload) map[string]interface{} {
	settings := map[string]interface{}{
		"simulator": deviceIsSimulator(device),
	}
	if payload != nil {
		if payload.BaudRate != nil {
			settings["baud_rate"] = *payload.BaudRate
		}
		if payload.Rtscts != nil {
			settings["rtscts"] = *payload.Rtscts
		}
	}
	if controllerType != "unknown" {
		settings["controller_type"] = controllerType
	}
	return settings
}

func buildControllerState(device *gen.Device, controllerType, connectionState string) map[string]interface{} {
	state := map[string]interface{}{
		"status":           "idle",
		"connection_state": connectionState,
	}
	if device != nil {
		state["device_kind"] = device.Kind
		if device.Path != nil {
			state["path"] = *device.Path
		}
		if device.NetworkAddress != nil {
			state["network_address"] = *device.NetworkAddress
		}
	}
	if controllerType != "unknown" {
		state["controller_type"] = controllerType
	}
	return state
}

func deriveControllerStatus(connectionState, workflowState string, hasAlarm bool) string {
	if hasAlarm {
		return "alarm"
	}
	switch connectionState {
	case "connected":
		switch workflowState {
		case "running":
			return "run"
		case "paused", "hold":
			return "hold"
		case "stopping":
			return "stop"
		default:
			return "idle"
		}
	case "probing_firmware":
		return "probing"
	case "reconnecting":
		return "reconnecting"
	case "disconnected":
		return "offline"
	case "closing":
		return "closing"
	case "flashing":
		return "flashing"
	case "errored":
		return "error"
	default:
		return connectionState
	}
}

func buildSenderStatus(connectionState string) map[string]interface{} {
	return map[string]interface{}{
		"connection_state": connectionState,
		"active":           connectionState == "connected",
		"hold":             false,
		"holdReason":       nil,
	}
}

func buildFeederStatus() map[string]interface{} {
	return map[string]interface{}{
		"queue_state": "idle",
		"queue":       0,
		"hold":        false,
		"holdReason":  nil,
		"buffered":    false,
		"pending":     0,
		"changed":     false,
	}
}

func setStateDefault(status map[string]interface{}, key string, value interface{}) {
	if status == nil {
		return
	}
	if _, exists := status[key]; exists {
		return
	}
	status[key] = value
}

func updateSenderExecutionState(status map[string]interface{}, workflowState string) map[string]interface{} {
	if status == nil {
		status = map[string]interface{}{}
	}
	status["workflow_state"] = workflowState
	status["active"] = workflowState == "running" || workflowState == "paused"
	return status
}

func updateFeederExecutionState(status map[string]interface{}, workflowState string, hasFile bool) map[string]interface{} {
	if status == nil {
		status = map[string]interface{}{}
	}

	switch workflowState {
	case "running":
		status["queue_state"] = "running"
		status["buffered"] = true
		status["pending"] = 1
	case "paused":
		status["queue_state"] = "paused"
		status["buffered"] = true
		status["pending"] = 1
	default:
		if hasFile {
			status["queue_state"] = "ready"
			status["buffered"] = false
			status["pending"] = 1
		} else {
			status["queue_state"] = "idle"
			status["buffered"] = false
			status["pending"] = 0
		}
	}

	return status
}

func (record *sessionRecord) appendEvent(eventType string, payload interface{}) {
	if record == nil || record.session == nil {
		return
	}
	record.nextSequence++
	record.events = append(record.events, &gen.SessionEvent{
		SessionID:  record.session.ID,
		Sequence:   record.nextSequence,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Type:       eventType,
		Payload:    normalizeEventPayload(payload),
	})
}

func normalizeEventPayload(payload interface{}) interface{} {
	switch typed := payload.(type) {
	case nil:
		return nil
	case map[string]interface{}:
		normalized := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			normalized[key] = normalizeEventPayload(value)
		}
		return normalized
	case []interface{}:
		normalized := make([]interface{}, len(typed))
		for index, value := range typed {
			normalized[index] = normalizeEventPayload(value)
		}
		return normalized
	case *gen.HomingState:
		return buildHomingStatePayload(typed)
	}

	value := reflect.ValueOf(payload)
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		return normalizeEventPayload(value.Elem().Interface())
	}
	return payload
}

func (record *sessionRecord) appendControllerStateChanged() {
	payload := cloneState(record.controllerState)
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if record != nil && record.session != nil {
		payload["controller_type"] = record.session.ControllerType
	}
	record.appendEvent("controller_state_changed", payload)
}

func (record *sessionRecord) appendControllerSettingsChanged() {
	payload := cloneState(record.controllerSetting)
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if record != nil && record.session != nil {
		payload["controller_type"] = record.session.ControllerType
	}
	record.appendEvent("controller_settings_changed", payload)
}

func (record *sessionRecord) appendWorkflowStateChanged() {
	record.appendEvent("workflow_state_changed", map[string]interface{}{
		"workflow_state": record.session.WorkflowState,
	})
}

func (record *sessionRecord) appendSenderStatusChanged() {
	record.appendEvent("sender_status_changed", cloneState(record.senderStatus))
}

func (record *sessionRecord) appendFeederStatusChanged() {
	record.appendEvent("feeder_status_changed", cloneState(record.feederStatus))
}

func buildHomingStatePayload(state *gen.HomingState) map[string]interface{} {
	if state == nil {
		return nil
	}
	payload := map[string]interface{}{
		"homing_required": state.HomingRequired,
		"has_homed":       state.HasHomed,
	}
	if state.LastHomingAt != nil && *state.LastHomingAt != "" {
		payload["last_homing_at"] = *state.LastHomingAt
	}
	return payload
}

func (record *sessionRecord) appendHomingStateChanged() {
	record.appendEvent("homing_state_changed", buildHomingStatePayload(record.homingState))
}

func (record *sessionRecord) appendJobProgress(progress float64) {
	if record != nil {
		record.progressPercent = progress
	}
	payload := map[string]interface{}{
		"progress": progress,
	}
	if progress >= 100 {
		payload["terminal"] = true
	}
	if record != nil && record.loadedFile != nil && record.loadedFile.LineCount != nil {
		payload["line_count"] = *record.loadedFile.LineCount
	}
	record.appendEvent("job_progress", payload)
}

func (record *sessionRecord) syncDerivedHomingState() {
	if record == nil {
		return
	}

	if record.homingState == nil {
		record.homingState = &gen.HomingState{}
	}

	homingEnabled := false
	if value, exists := record.controllerSetting["homing_enabled"]; exists {
		if parsed, ok := cloneBool(value); ok {
			homingEnabled = parsed
		}
	}
	if settings, ok := record.controllerSetting["firmware_config"].(map[string]interface{}); ok {
		if value, exists := settings["homing_enabled"]; exists {
			if parsed, ok := cloneBool(value); ok {
				homingEnabled = parsed
			}
		} else if value, exists := settings["$22"]; exists {
			if parsed, ok := cloneBool(value); ok {
				homingEnabled = parsed
			}
		}
	}

	record.homingState.HomingRequired = homingEnabled && !record.homingState.HasHomed
}

func (record *sessionRecord) syncDerivedSenderStatus() {
	if record == nil || record.session == nil {
		return
	}

	status := cloneState(record.senderStatus)
	if status == nil {
		status = map[string]interface{}{}
	}

	status["connection_state"] = record.session.ConnectionState
	status["workflowState"] = record.session.WorkflowState
	status["active"] = record.session.WorkflowState == "running" || record.session.WorkflowState == "paused"
	status["hold"] = record.session.WorkflowState == "paused"
	if record.session.WorkflowState == "paused" {
		status["holdReason"] = "pause"
	} else if _, exists := status["holdReason"]; !exists {
		status["holdReason"] = nil
	}
	setStateDefault(status, "startTime", nil)
	setStateDefault(status, "finishTime", nil)
	setStateDefault(status, "elapsedTime", 0)
	setStateDefault(status, "timePaused", 0)
	setStateDefault(status, "timeRunning", 0)
	setStateDefault(status, "remainingTime", 0)
	setStateDefault(status, "toolChanges", 0)
	setStateDefault(status, "estimatedTime", 0)
	setStateDefault(status, "bufferSize", 0)
	setStateDefault(status, "dataLength", 0)
	setStateDefault(status, "ovF", 100)
	setStateDefault(status, "sp", 0)
	setStateDefault(status, "context", map[string]interface{}{})
	setStateDefault(status, "isRotaryFile", false)

	if record.loadedFile == nil {
		status["name"] = ""
		status["size"] = 0
		status["total"] = 0
		status["sent"] = 0
		status["received"] = 0
		status["currentLineRunning"] = 0
		record.senderStatus = status
		return
	}

	totalLines := int64(0)
	if record.loadedFile.LineCount != nil {
		totalLines = *record.loadedFile.LineCount
	}
	currentLine := int64(float64(totalLines) * (record.progressPercent / 100.0))
	if currentLine > totalLines {
		currentLine = totalLines
	}
	if record.liveSenderStatus {
		setStateDefault(status, "name", record.loadedFile.Name)
		setStateDefault(status, "size", record.loadedFile.SizeBytes)
		setStateDefault(status, "total", totalLines)
		setStateDefault(status, "sent", currentLine)
		setStateDefault(status, "received", currentLine)
		setStateDefault(status, "currentLineRunning", currentLine)
	} else {
		status["name"] = record.loadedFile.Name
		status["size"] = record.loadedFile.SizeBytes
		status["total"] = totalLines
		status["sent"] = currentLine
		status["received"] = currentLine
		status["currentLineRunning"] = currentLine
	}
	record.senderStatus = status
}

func (record *sessionRecord) syncDerivedFeederStatus() {
	if record == nil || record.session == nil {
		return
	}

	status := cloneState(record.feederStatus)
	if status == nil {
		status = map[string]interface{}{}
	}

	status["hold"] = record.session.WorkflowState == "paused"
	if record.session.WorkflowState == "paused" {
		status["holdReason"] = "pause"
	} else if _, exists := status["holdReason"]; !exists {
		status["holdReason"] = nil
	}
	status["changed"] = true

	totalLines := int64(0)
	if record.loadedFile != nil && record.loadedFile.LineCount != nil {
		totalLines = *record.loadedFile.LineCount
	}
	currentLine := int64(float64(totalLines) * (record.progressPercent / 100.0))
	if currentLine > totalLines {
		currentLine = totalLines
	}
	remaining := totalLines - currentLine
	if remaining < 0 {
		remaining = 0
	}
	if record.liveFeederStatus {
		setStateDefault(status, "queue", remaining)
	} else {
		status["queue"] = remaining
	}

	record.feederStatus = status
}

func (record *sessionRecord) syncDerivedControllerState() {
	if record == nil || record.session == nil {
		return
	}
	state := cloneState(record.controllerState)
	if state == nil {
		state = map[string]interface{}{}
	}
	state["connection_state"] = record.session.ConnectionState
	state["workflow_state"] = record.session.WorkflowState
	state["status"] = deriveControllerStatus(record.session.ConnectionState, record.session.WorkflowState, record.alarmState != nil)
	state["connected_clients"] = len(record.clients)
	state["has_loaded_file"] = record.loadedFile != nil
	state["has_alarm"] = record.alarmState != nil
	if record.lastError != nil {
		state["last_error"] = *record.lastError
	} else {
		delete(state, "last_error")
	}
	record.controllerState = state
}

func (record *sessionRecord) syncDerivedRuntimeStatus() {
	record.syncDerivedSenderStatus()
	record.syncDerivedFeederStatus()
}

func decodeRuntimeStateMap(raw string) (map[string]interface{}, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	decoded := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func metadataString(metadata map[string]string, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata[key]
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

func metadataBool(metadata map[string]string, key string) (bool, bool) {
	raw, ok := metadataString(metadata, key)
	if !ok {
		return false, false
	}
	return cloneBool(raw)
}

func metadataJSONMap(metadata map[string]string, key string) (map[string]interface{}, bool) {
	raw, ok := metadataString(metadata, key)
	if !ok {
		return nil, false
	}
	return decodeRuntimeStateMap(raw)
}

func runtimeProgressFromSenderStatus(status map[string]interface{}) (float64, bool) {
	if status == nil {
		return 0, false
	}
	total, ok := cloneFloat64(status["total"])
	if !ok || total <= 0 {
		return 0, false
	}
	for _, key := range []string{"currentLineRunning", "received", "sent"} {
		if current, ok := cloneFloat64(status[key]); ok {
			progress := (current / total) * 100.0
			if progress < 0 {
				progress = 0
			}
			if progress > 100 {
				progress = 100
			}
			return progress, true
		}
	}
	return 0, false
}

func workflowStateFromControllerState(state map[string]interface{}) string {
	if state == nil {
		return ""
	}
	rawStatus, _ := cloneStringValue(state["status"])
	rawStatus = strings.ToLower(strings.TrimSpace(rawStatus))
	activeState := ""
	if statusPayload, ok := state["status"].(map[string]interface{}); ok {
		activeState, _ = cloneStringValue(statusPayload["activeState"])
	}
	if activeState == "" {
		activeState, _ = cloneStringValue(state["active_state"])
	}
	switch strings.ToLower(strings.TrimSpace(activeState)) {
	case "run", "running":
		return "running"
	case "hold", "paused":
		return "paused"
	case "alarm":
		return "paused"
	case "home", "idle", "sleep", "jog", "check", "door", "":
	default:
	}
	switch rawStatus {
	case "run", "running":
		return "running"
	case "hold", "paused":
		return "paused"
	case "idle", "sleep", "jog":
		return "idle"
	}
	return ""
}

func (record *sessionRecord) applyRuntimeTelemetryMetadata(metadata map[string]string) {
	if record == nil || record.session == nil || len(metadata) == 0 {
		return
	}

	controllerStateChanged := false
	controllerSettingsChanged := false
	senderStatusChanged := false
	feederStatusChanged := false
	homingStateChanged := false
	workflowChanged := false
	alarmChanged := false

	if controllerType, ok := metadataString(metadata, "controller_type"); ok {
		normalized := normalizeControllerType(controllerType)
		if normalized != "" && record.session.ControllerType != normalized {
			record.session.ControllerType = normalized
			controllerStateChanged = true
			controllerSettingsChanged = true
		}
	}

	if settings, ok := metadataJSONMap(metadata, "controller_settings"); ok {
		record.controllerSetting = settings
		controllerSettingsChanged = true
	}

	if controllerState, ok := metadataJSONMap(metadata, "controller_state"); ok {
		record.controllerState = controllerState
		controllerStateChanged = true
		if workflowState := workflowStateFromControllerState(controllerState); workflowState != "" && workflowState != record.session.WorkflowState {
			record.session.WorkflowState = workflowState
			workflowChanged = true
		}
		if statusPayload, ok := controllerState["status"].(map[string]interface{}); ok {
			if activeState, ok := cloneStringValue(statusPayload["activeState"]); ok {
				record.setControllerActiveState(activeState)
				controllerStateChanged = true
				if strings.EqualFold(activeState, "alarm") {
					record.alarmState = map[string]interface{}{
						"code":    statusPayload["subState"],
						"message": "alarm",
					}
					alarmChanged = true
				}
			}
			if subState, ok := statusPayload["subState"]; ok {
				if record.controllerState == nil {
					record.controllerState = map[string]interface{}{}
				}
				record.controllerState["sub_state"] = subState
				controllerStateChanged = true
			}
		}
	}

	if senderStatus, ok := metadataJSONMap(metadata, "sender_status"); ok {
		record.senderStatus = senderStatus
		record.liveSenderStatus = true
		senderStatusChanged = true
		if progress, ok := runtimeProgressFromSenderStatus(senderStatus); ok {
			record.progressPercent = progress
		}
	}

	if feederStatus, ok := metadataJSONMap(metadata, "feeder_status"); ok {
		record.feederStatus = feederStatus
		record.liveFeederStatus = true
		feederStatusChanged = true
	}

	if workflowState, ok := metadataString(metadata, "workflow_state"); ok {
		normalized := strings.ToLower(strings.TrimSpace(workflowState))
		if normalized != "" && normalized != record.session.WorkflowState {
			record.session.WorkflowState = normalized
			workflowChanged = true
		}
	}

	if hasHomed, ok := metadataBool(metadata, "homing_has_homed"); ok {
		record.homingState = cloneHomingState(record.homingState)
		if record.homingState == nil {
			record.homingState = &gen.HomingState{}
		}
		if record.homingState.HasHomed != hasHomed {
			record.homingState.HasHomed = hasHomed
			if hasHomed {
				record.homingState.LastHomingAt = nowRFC3339()
			}
			homingStateChanged = true
		}
	}

	if lastErrorMessage, ok := metadataString(metadata, "last_error"); ok {
		record.lastError = ptrString(lastErrorMessage)
		alarmChanged = true
	}

	if errorCode, ok := metadataString(metadata, "error_code"); ok {
		errorMessage, _ := metadataString(metadata, "error_message")
		if errorMessage == "" {
			errorMessage = errorCode
		}
		record.lastError = ptrString(fmt.Sprintf("%s: %s", errorCode, errorMessage))
		isAlarm, _ := metadataBool(metadata, "error_is_alarm")
		if isAlarm {
			record.alarmState = map[string]interface{}{
				"code":    errorCode,
				"message": errorMessage,
			}
		}
		alarmChanged = true
	}

	if hasAlarm, ok := metadataBool(metadata, "has_alarm"); ok {
		if !hasAlarm {
			record.alarmState = nil
			if _, hasLastError := metadata["last_error"]; !hasLastError && metadata["error_code"] == "" {
				record.lastError = nil
			}
		}
		alarmChanged = true
	}

	record.syncDerivedHomingState()
	record.syncDerivedRuntimeStatus()
	record.syncDerivedControllerState()

	if controllerSettingsChanged {
		record.appendControllerSettingsChanged()
	}
	if workflowChanged {
		record.appendWorkflowStateChanged()
	}
	if controllerStateChanged || workflowChanged || alarmChanged {
		record.appendControllerStateChanged()
	}
	if senderStatusChanged || workflowChanged {
		record.appendSenderStatusChanged()
	}
	if feederStatusChanged || workflowChanged {
		record.appendFeederStatusChanged()
	}
	if homingStateChanged || controllerSettingsChanged {
		record.appendHomingStateChanged()
	}
}

func (record *sessionRecord) applyRuntimeFlags(flags runtimeFlagState) {
	if record == nil || record.runtimeFlags == nil {
		return
	}
	record.runtimeFlags.CanStartJob = flags.canStart
	record.runtimeFlags.CanPauseJob = flags.canPause
	record.runtimeFlags.CanResumeJob = flags.canResume
	record.runtimeFlags.CanStopJob = flags.canStop
	record.runtimeFlags.CanUnloadFile = flags.canUnload
}

func (record *sessionRecord) applyWorkflowState(workflowState string, flags runtimeFlagState, progress *float64) {
	if record == nil || record.session == nil {
		return
	}
	record.session.WorkflowState = workflowState
	record.applyRuntimeFlags(flags)
	record.session.UpdatedAt = nowRFC3339()
	if progress != nil {
		record.progressPercent = *progress
	}
	record.senderStatus = updateSenderExecutionState(record.senderStatus, record.session.WorkflowState)
	record.feederStatus = updateFeederExecutionState(record.feederStatus, record.session.WorkflowState, record.loadedFile != nil)
	record.syncDerivedRuntimeStatus()
	record.syncDerivedControllerState()
}

func (record *sessionRecord) appendWorkflowRuntimeEvents() {
	record.appendWorkflowStateChanged()
	record.appendSenderStatusChanged()
	record.appendFeederStatusChanged()
}

func (record *sessionRecord) applyConnectionState(connectionState string) {
	if record == nil || record.session == nil {
		return
	}
	record.session.ConnectionState = connectionState
	record.session.UpdatedAt = nowRFC3339()
	record.syncDerivedControllerState()
	record.appendEvent("connection_state_changed", map[string]interface{}{
		"connection_state": connectionState,
	})
	record.appendControllerStateChanged()
}

func (record *sessionRecord) isFlashing() bool {
	return record != nil && record.session != nil && record.session.ConnectionState == "flashing"
}

func rejectIfFlashing(record *sessionRecord) error {
	if record != nil && record.isFlashing() {
		return machineError("flash_not_allowed", "session is flashing")
	}
	return nil
}

func (record *sessionRecord) clearAlarmState() {
	record.lastError = nil
	record.alarmState = nil
	record.syncDerivedControllerState()
}

func (record *sessionRecord) setControllerActiveState(activeState string) {
	if record == nil {
		return
	}
	if record.controllerState == nil {
		record.controllerState = map[string]interface{}{}
	}
	if activeState == "" {
		delete(record.controllerState, "active_state")
		return
	}
	record.controllerState["active_state"] = activeState
}

func (record *sessionRecord) setLastError(code, message string) {
	if message == "" {
		record.lastError = nil
		record.alarmState = nil
		record.syncDerivedControllerState()
		return
	}

	formatted := fmt.Sprintf("%s: %s", code, message)
	record.lastError = ptrString(formatted)
	record.alarmState = map[string]interface{}{
		"code":    code,
		"message": message,
	}
	record.syncDerivedControllerState()
	record.appendEvent("error_raised", map[string]interface{}{
		"code":    code,
		"message": message,
	})
	record.appendEvent("alarm_raised", map[string]interface{}{
		"code":    code,
		"message": message,
	})
}

func cloneEvents(events []*gen.SessionEvent) []*gen.SessionEvent {
	cloned := make([]*gen.SessionEvent, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, cloneSessionEvent(event))
	}
	return cloned
}

func (s *MemoryMachineService) findDevice(deviceID string) *gen.Device {
	for _, device := range s.devices {
		if device != nil && device.ID == deviceID {
			return device
		}
	}
	return nil
}

func (s *MemoryMachineService) buildSessionSnapshot(record *sessionRecord) *gen.SessionSnapshot {
	snapshot := &gen.SessionSnapshot{
		Session:            cloneSession(record.session),
		ControllerState:    cloneState(record.controllerState),
		ControllerSettings: cloneState(record.controllerSetting),
		SenderStatus:       cloneState(record.senderStatus),
		FeederStatus:       cloneState(record.feederStatus),
		HomingState:        cloneHomingState(record.homingState),
		RuntimeFlags:       cloneRuntimeFlags(record.runtimeFlags),
		AlarmState:         record.alarmState,
		LastError:          cloneString(record.lastError),
	}
	snapshot.LoadedFile = cloneLoadedFile(record.loadedFile)
	return snapshot
}

func (s *MemoryMachineService) deviceExists(deviceID string) bool {
	for _, device := range s.devices {
		if device.ID == deviceID {
			return true
		}
	}
	return false
}

func (s *MemoryMachineService) copyDevicesLocked() []*gen.Device {
	devices := cloneDevices(s.devices)
	inUseDeviceIDs := make(map[string]struct{}, len(s.sessions))
	for _, record := range s.sessions {
		if record == nil || record.session == nil || record.session.DeviceID == "" {
			continue
		}
		inUseDeviceIDs[record.session.DeviceID] = struct{}{}
	}
	for _, device := range devices {
		_, inUse := inUseDeviceIDs[device.ID]
		device.InUse = inUse
	}
	return devices
}

func (s *MemoryMachineService) refreshDevicesLocked() error {
	if s.discovery == nil {
		return nil
	}
	devices, err := s.discovery.ListDevices()
	if err != nil {
		return err
	}
	s.devices = cloneDevices(devices)
	return nil
}

func (s *MemoryMachineService) getSessionRecord(sessionID string) (*sessionRecord, error) {
	record, ok := s.sessions[sessionID]
	if !ok || record == nil || record.session == nil {
		return nil, machineError("session_not_found", "session not found")
	}
	return record, nil
}

func (s *MemoryMachineService) getSessionRecordByDeviceID(deviceID string) (*sessionRecord, bool) {
	for _, record := range s.sessions {
		if record == nil || record.session == nil {
			continue
		}
		if record.session.DeviceID == deviceID {
			return record, true
		}
	}
	return nil, false
}

func (s *MemoryMachineService) ListDevices(context.Context) ([]*gen.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshDevicesLocked(); err != nil {
		return nil, machineError("transport_error", err.Error())
	}
	return s.copyDevicesLocked(), nil
}

func (s *MemoryMachineService) OpenSession(ctx context.Context, payload *gen.OpenSessionPayload) (*gen.OpenSessionResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		clientID := ""
		if payload.ClientID != nil {
			clientID = *payload.ClientID
		}
		annotateMachineSpan(ctx,
			attribute.String("machine.device_id", payload.DeviceID),
			attribute.String("machine.client_id", clientID),
		)
	}
	if payload == nil || payload.DeviceID == "" {
		return nil, machineError("device_not_found", "device_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshDevicesLocked(); err != nil {
		return nil, machineError("transport_error", err.Error())
	}
	if !s.deviceExists(payload.DeviceID) {
		return nil, machineError("device_not_found", fmt.Sprintf("unknown device_id %q", payload.DeviceID))
	}
	if _, exists := s.getSessionRecordByDeviceID(payload.DeviceID); exists {
		return nil, machineError("device_busy", fmt.Sprintf("device_id %q is already attached to an open session", payload.DeviceID))
	}

	device := s.findDevice(payload.DeviceID)
	detection := &controllerDetection{
		controllerType:    "unknown",
		connectionState:   "probing_firmware",
		workflowState:     "idle",
		controllerState:   buildControllerState(device, "unknown", "probing_firmware"),
		controllerSetting: buildControllerSettings(device, "unknown", payload),
		senderStatus:      buildSenderStatus("probing_firmware"),
		feederStatus:      buildFeederStatus(),
		homingState: &gen.HomingState{
			HomingRequired: false,
			HasHomed:       false,
		},
	}
	if s.detector != nil {
		resolvedDetection, err := s.detector.Detect(ctx, device, payload)
		if err != nil {
			return nil, machineError("firmware_detection_failed", err.Error())
		}
		if resolvedDetection != nil {
			detection = resolvedDetection
		}
	}

	sessionID := "session-" + payload.DeviceID + "-" + time.Now().UTC().Format("150405.000000000")
	createdAt := nowRFC3339()
	session := &gen.Session{
		ID:              sessionID,
		DeviceID:        payload.DeviceID,
		ControllerType:  detection.controllerType,
		ConnectionState: detection.connectionState,
		WorkflowState:   detection.workflowState,
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt,
	}
	record := &sessionRecord{
		session:           session,
		controllerState:   detection.controllerState,
		controllerSetting: detection.controllerSetting,
		senderStatus:      detection.senderStatus,
		feederStatus:      detection.feederStatus,
		homingState:       detection.homingState,
		alarmState:        detection.alarmState,
		lastError:         detection.lastError,
		runtimeFlags:      defaultRuntimeFlags(),
		clients:           map[string]struct{}{},
	}
	if payload.ClientID != nil && *payload.ClientID != "" {
		record.clients[*payload.ClientID] = struct{}{}
	}
	record.syncDerivedHomingState()
	record.syncDerivedRuntimeStatus()
	record.syncDerivedControllerState()
	record.appendEvent("session_opened", map[string]interface{}{
		"device_id":        payload.DeviceID,
		"connection_state": detection.connectionState,
	})
	addMachineSpanEvent(ctx, "machine.session_opened",
		attribute.String("machine.session_id", sessionID),
		attribute.String("machine.controller_type", detection.controllerType),
		attribute.String("machine.connection_state", detection.connectionState),
	)
	record.appendEvent("connection_state_changed", map[string]interface{}{
		"connection_state": detection.connectionState,
	})
	record.appendControllerStateChanged()
	record.appendControllerSettingsChanged()
	record.appendSenderStatusChanged()
	record.appendFeederStatusChanged()
	record.appendHomingStateChanged()
	if detection.controllerType != "unknown" {
		record.appendEvent("controller_detected", map[string]interface{}{
			"controller_type": detection.controllerType,
		})
	}
	s.sessions[sessionID] = record
	return &gen.OpenSessionResult{
		Session:  cloneSession(session),
		Snapshot: s.buildSessionSnapshot(record),
	}, nil
}

func (s *MemoryMachineService) CloseSession(ctx context.Context, payload *gen.SessionIDPayload) (*gen.BooleanResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx, attribute.String("machine.session_id", payload.SessionID))
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[payload.SessionID]
	if !ok || record == nil || record.session == nil {
		return &gen.BooleanResult{OK: false}, nil
	}
	record.session.ConnectionState = "closing"
	record.session.UpdatedAt = nowRFC3339()
	record.liveSenderStatus = false
	record.liveFeederStatus = false
	record.senderStatus = buildSenderStatus(record.session.ConnectionState)
	record.syncDerivedControllerState()
	record.appendEvent("connection_state_changed", map[string]interface{}{
		"connection_state": record.session.ConnectionState,
	})
	record.appendControllerStateChanged()
	record.appendSenderStatusChanged()
	record.appendEvent("session_closed", map[string]interface{}{
		"device_id": record.session.DeviceID,
	})
	addMachineSpanEvent(ctx, "machine.session_closed",
		attribute.String("machine.device_id", record.session.DeviceID),
	)
	s.closed[payload.SessionID] = cloneEvents(record.events)
	delete(s.sessions, payload.SessionID)
	return &gen.BooleanResult{OK: true}, nil
}

func (s *MemoryMachineService) GetSession(_ context.Context, payload *gen.SessionIDPayload) (*gen.Session, error) {
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	return cloneSession(record.session), nil
}

func (s *MemoryMachineService) GetSnapshot(_ context.Context, payload *gen.SessionIDPayload) (*gen.SessionSnapshot, error) {
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	return s.buildSessionSnapshot(record), nil
}

func (s *MemoryMachineService) AttachClient(ctx context.Context, payload *gen.AttachClientPayload) (*gen.SessionSnapshot, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx,
			attribute.String("machine.session_id", payload.SessionID),
			attribute.String("machine.client_id", payload.ClientID),
		)
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	if payload.ClientID == "" {
		return nil, machineError("command_rejected", "client_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	return s.attachClientLocked(record, payload.ClientID), nil
}

func (s *MemoryMachineService) ResolveSession(ctx context.Context, payload *gen.ResolveSessionPayload) (*gen.SessionSnapshot, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx,
			attribute.String("machine.device_id", payload.DeviceID),
			attribute.String("machine.client_id", payload.ClientID),
		)
	}
	if payload == nil || payload.DeviceID == "" {
		return nil, machineError("device_not_found", "device_id is required")
	}
	if payload.ClientID == "" {
		return nil, machineError("command_rejected", "client_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.getSessionRecordByDeviceID(payload.DeviceID)
	if !exists {
		return nil, machineError("session_not_found", "no active session for device")
	}
	return s.attachClientLocked(record, payload.ClientID), nil
}

func (s *MemoryMachineService) DetachClient(ctx context.Context, payload *gen.DetachClientPayload) (*gen.BooleanResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx,
			attribute.String("machine.session_id", payload.SessionID),
			attribute.String("machine.client_id", payload.ClientID),
		)
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	if payload.ClientID == "" {
		return nil, machineError("command_rejected", "client_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return &gen.BooleanResult{OK: false}, nil
	}
	if _, exists := record.clients[payload.ClientID]; !exists {
		return &gen.BooleanResult{OK: false}, nil
	}
	delete(record.clients, payload.ClientID)
	record.session.UpdatedAt = nowRFC3339()
	record.appendEvent("client_detached", map[string]interface{}{
		"client_id": payload.ClientID,
	})
	if len(record.clients) == 0 {
		record.session.ConnectionState = "disconnected"
		if !record.liveSenderStatus {
			record.senderStatus = buildSenderStatus(record.session.ConnectionState)
		}
		record.syncDerivedRuntimeStatus()
		record.syncDerivedControllerState()
		record.appendEvent("connection_state_changed", map[string]interface{}{
			"connection_state": record.session.ConnectionState,
		})
		record.appendControllerStateChanged()
		record.appendSenderStatusChanged()
		record.feederStatus = updateFeederExecutionState(record.feederStatus, record.session.WorkflowState, record.loadedFile != nil)
		record.appendFeederStatusChanged()
	}
	return &gen.BooleanResult{OK: true}, nil
}

func (s *MemoryMachineService) attachClientLocked(record *sessionRecord, clientID string) *gen.SessionSnapshot {
	if _, exists := record.clients[clientID]; exists {
		return s.buildSessionSnapshot(record)
	}
	record.clients[clientID] = struct{}{}
	record.session.ConnectionState = "connected"
	record.session.UpdatedAt = nowRFC3339()
	if !record.liveSenderStatus {
		record.senderStatus = buildSenderStatus(record.session.ConnectionState)
	}
	record.feederStatus = updateFeederExecutionState(record.feederStatus, record.session.WorkflowState, record.loadedFile != nil)
	record.syncDerivedRuntimeStatus()
	record.syncDerivedControllerState()
	record.appendEvent("client_attached", map[string]interface{}{
		"client_id": clientID,
	})
	record.appendEvent("connection_state_changed", map[string]interface{}{
		"connection_state": record.session.ConnectionState,
	})
	record.appendControllerStateChanged()
	record.appendSenderStatusChanged()
	record.appendFeederStatusChanged()
	return s.buildSessionSnapshot(record)
}

func (s *MemoryMachineService) SendCommand(ctx context.Context, payload *gen.SendCommandPayload) (*gen.AcceptedResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		commandType := ""
		if payload.Command != nil {
			commandType = payload.Command.Type
		}
		annotateMachineSpan(ctx,
			attribute.String("machine.session_id", payload.SessionID),
			attribute.String("machine.command_type", commandType),
		)
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	if payload.Command == nil || payload.Command.Type == "" {
		return nil, machineError("command_rejected", "command is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	if err := rejectIfFlashing(record); err != nil {
		return nil, err
	}
	record.session.UpdatedAt = nowRFC3339()
	commandType := payload.Command.Type

	switch commandType {
	case "status_report":
		if len(payload.Command.Metadata) != 0 {
			addMachineSpanEvent(ctx, "machine.runtime_telemetry",
				attribute.Int("machine.metadata_keys", len(payload.Command.Metadata)),
			)
			record.applyRuntimeTelemetryMetadata(payload.Command.Metadata)
			break
		}
		record.appendControllerStateChanged()
		record.appendSenderStatusChanged()
		record.appendFeederStatusChanged()
		record.appendHomingStateChanged()
	case "unlock":
		record.clearAlarmState()
		record.setControllerActiveState("Idle")
		record.appendControllerStateChanged()
	case "reset":
		record.clearAlarmState()
		progress := 0.0
		record.applyWorkflowState("idle", runtimeFlagState{
			canStart:  record.loadedFile != nil,
			canPause:  false,
			canResume: false,
			canStop:   false,
			canUnload: record.loadedFile != nil,
		}, &progress)
		record.setControllerActiveState("Idle")
		record.syncDerivedControllerState()
		record.appendControllerStateChanged()
		record.appendWorkflowRuntimeEvents()
	case "sleep":
		record.applyWorkflowState("idle", runtimeFlagState{
			canStart:  record.runtimeFlags.CanStartJob,
			canPause:  false,
			canResume: false,
			canStop:   false,
			canUnload: record.runtimeFlags.CanUnloadFile,
		}, nil)
		record.setControllerActiveState("Sleep")
		record.syncDerivedControllerState()
		record.appendControllerStateChanged()
		record.appendSenderStatusChanged()
	case "home":
		record.homingState = cloneHomingState(record.homingState)
		if record.homingState == nil {
			record.homingState = &gen.HomingState{}
		}
		record.homingState.HasHomed = true
		record.homingState.HomingRequired = false
		record.homingState.LastHomingAt = nowRFC3339()
		record.setControllerActiveState("Home")
		record.syncDerivedControllerState()
		record.appendHomingStateChanged()
		record.appendControllerStateChanged()
	case "feed_hold":
		if record.session.WorkflowState != "running" {
			record.setLastError("command_rejected", "machine not running")
			return nil, machineError("command_rejected", "machine not running")
		}
		record.clearAlarmState()
		record.applyWorkflowState("paused", runtimeFlagState{
			canStart:  false,
			canPause:  false,
			canResume: true,
			canStop:   record.loadedFile != nil,
			canUnload: record.runtimeFlags.CanUnloadFile,
		}, nil)
		record.setControllerActiveState("Hold")
		record.syncDerivedControllerState()
		record.appendControllerStateChanged()
		record.appendWorkflowRuntimeEvents()
	case "cycle_start":
		if record.session.WorkflowState == "paused" || (record.session.WorkflowState == "idle" && record.loadedFile != nil) {
			record.clearAlarmState()
			record.applyWorkflowState("running", runtimeFlagState{
				canStart:  false,
				canPause:  true,
				canResume: false,
				canStop:   true,
				canUnload: record.runtimeFlags.CanUnloadFile,
			}, nil)
			record.setControllerActiveState("Run")
			record.syncDerivedControllerState()
			record.appendControllerStateChanged()
			record.appendWorkflowRuntimeEvents()
			break
		}
		record.setLastError("command_rejected", "machine not paused")
		return nil, machineError("command_rejected", "machine not paused")
	case "set_feed_override":
		if payload.Command.FeedOverride != nil {
			record.senderStatus["ovF"] = *payload.Command.FeedOverride
		}
		record.appendSenderStatusChanged()
	case "set_spindle_override":
		if payload.Command.SpindleOverride != nil {
			record.senderStatus["ovS"] = *payload.Command.SpindleOverride
		}
		record.appendSenderStatusChanged()
	case "set_rapid_override":
		if payload.Command.RapidOverride != nil {
			record.senderStatus["ovR"] = *payload.Command.RapidOverride
		}
		record.appendSenderStatusChanged()
	case "raw_gcode_line":
		if payload.Command.RawLine != nil {
			record.senderStatus["lastLine"] = *payload.Command.RawLine
		}
		record.appendSenderStatusChanged()
	case "jog":
		record.setControllerActiveState("Jog")
		if payload.Command.AxisMove != nil {
			record.controllerState["last_jog"] = map[string]interface{}{
				"x":         payload.Command.AxisMove.X,
				"y":         payload.Command.AxisMove.Y,
				"z":         payload.Command.AxisMove.Z,
				"a":         payload.Command.AxisMove.A,
				"feed_rate": payload.Command.AxisMove.FeedRate,
			}
		}
		record.syncDerivedControllerState()
		record.appendControllerStateChanged()
	default:
		record.appendControllerStateChanged()
	}
	return &gen.AcceptedResult{Accepted: true}, nil
}

func (s *MemoryMachineService) LoadFile(ctx context.Context, payload *gen.LoadFilePayload) (*gen.SessionSnapshot, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx,
			attribute.String("machine.session_id", payload.SessionID),
			attribute.String("machine.file_name", payload.Name),
			attribute.Int("machine.file_bytes", len(payload.Content)),
		)
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	if err := rejectIfFlashing(record); err != nil {
		return nil, err
	}
	loadedAt := nowRFC3339()
	loaded := &gen.LoadedFile{
		Name:      payload.Name,
		SizeBytes: int64(len(payload.Content)),
		LoadedAt:  loadedAt,
	}
	lineCount := int64(0)
	if payload.Content != "" {
		lineCount = int64(strings.Count(payload.Content, "\n") + 1)
	}
	loaded.LineCount = &lineCount
	checksum := "mem://sim"
	loaded.Checksum = &checksum
	record.loadedFile = loaded
	record.liveSenderStatus = false
	record.liveFeederStatus = false
	progress := 0.0
	record.applyWorkflowState("idle", runtimeFlagState{
		canStart:  true,
		canPause:  false,
		canResume: false,
		canStop:   false,
		canUnload: true,
	}, &progress)
	record.appendEvent("file_loaded", map[string]interface{}{
		"name":       loaded.Name,
		"size_bytes": loaded.SizeBytes,
		"line_count": lineCount,
	})
	addMachineSpanEvent(ctx, "machine.file_loaded",
		attribute.String("machine.file_name", loaded.Name),
		attribute.Int64("machine.line_count", lineCount),
	)
	record.appendWorkflowRuntimeEvents()
	return s.buildSessionSnapshot(record), nil
}

func (s *MemoryMachineService) UnloadFile(ctx context.Context, payload *gen.SessionIDPayload) (*gen.BooleanResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx, attribute.String("machine.session_id", payload.SessionID))
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return &gen.BooleanResult{OK: false}, nil
	}
	if err := rejectIfFlashing(record); err != nil {
		return nil, err
	}
	record.loadedFile = nil
	record.liveSenderStatus = false
	record.liveFeederStatus = false
	progress := 0.0
	record.applyWorkflowState("idle", runtimeFlagState{
		canStart:  false,
		canPause:  false,
		canResume: false,
		canStop:   false,
		canUnload: false,
	}, &progress)
	record.appendEvent("file_unloaded", map[string]interface{}{})
	addMachineSpanEvent(ctx, "machine.file_unloaded")
	record.appendWorkflowRuntimeEvents()
	return &gen.BooleanResult{OK: true}, nil
}

func (s *MemoryMachineService) StartJob(ctx context.Context, payload *gen.SessionIDPayload) (*gen.AcceptedResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx, attribute.String("machine.session_id", payload.SessionID))
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	if err := rejectIfFlashing(record); err != nil {
		return nil, err
	}
	if !record.runtimeFlags.CanStartJob {
		record.setLastError("job_not_running", "no file loaded")
		return nil, machineError("job_not_running", "no file loaded")
	}
	record.setLastError("", "")
	record.progressPercent = 0
	progress := 0.0
	record.applyWorkflowState("running", runtimeFlagState{
		canStart:  false,
		canPause:  true,
		canResume: false,
		canStop:   true,
		canUnload: record.runtimeFlags.CanUnloadFile,
	}, &progress)
	record.appendEvent("job_started", map[string]interface{}{
		"workflow_state": record.session.WorkflowState,
		"outcome":        "started",
	})
	addMachineSpanEvent(ctx, "machine.job_started")
	record.appendJobProgress(0)
	record.appendWorkflowRuntimeEvents()
	return &gen.AcceptedResult{Accepted: true}, nil
}

func (s *MemoryMachineService) PauseJob(ctx context.Context, payload *gen.SessionIDPayload) (*gen.AcceptedResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx, attribute.String("machine.session_id", payload.SessionID))
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	if err := rejectIfFlashing(record); err != nil {
		return nil, err
	}
	if record.session.WorkflowState != "running" {
		record.setLastError("command_rejected", "machine not running")
		return nil, machineError("command_rejected", "machine not running")
	}
	record.setLastError("", "")
	record.applyWorkflowState("paused", runtimeFlagState{
		canStart:  false,
		canPause:  false,
		canResume: true,
		canStop:   record.loadedFile != nil,
		canUnload: record.runtimeFlags.CanUnloadFile,
	}, nil)
	record.appendEvent("job_paused", map[string]interface{}{
		"workflow_state": record.session.WorkflowState,
		"outcome":        "paused",
	})
	addMachineSpanEvent(ctx, "machine.job_paused")
	record.appendWorkflowRuntimeEvents()
	return &gen.AcceptedResult{Accepted: true}, nil
}

func (s *MemoryMachineService) ResumeJob(ctx context.Context, payload *gen.SessionIDPayload) (*gen.AcceptedResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx, attribute.String("machine.session_id", payload.SessionID))
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	if err := rejectIfFlashing(record); err != nil {
		return nil, err
	}
	if record.session.WorkflowState != "paused" {
		record.setLastError("command_rejected", "machine not paused")
		return nil, machineError("command_rejected", "machine not paused")
	}
	record.setLastError("", "")
	record.applyWorkflowState("running", runtimeFlagState{
		canStart:  false,
		canPause:  true,
		canResume: false,
		canStop:   record.loadedFile != nil,
		canUnload: record.runtimeFlags.CanUnloadFile,
	}, nil)
	record.appendEvent("job_resumed", map[string]interface{}{
		"workflow_state": record.session.WorkflowState,
		"outcome":        "resumed",
	})
	addMachineSpanEvent(ctx, "machine.job_resumed")
	record.appendWorkflowRuntimeEvents()
	return &gen.AcceptedResult{Accepted: true}, nil
}

func (s *MemoryMachineService) StopJob(ctx context.Context, payload *gen.StopJobPayload) (*gen.AcceptedResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		markCompleted := false
		if payload.Force != nil {
			markCompleted = !*payload.Force
		}
		annotateMachineSpan(ctx,
			attribute.String("machine.session_id", payload.SessionID),
			attribute.Bool("machine.mark_completed", markCompleted),
		)
	}
	if payload == nil || payload.SessionID == "" {
		return nil, machineError("session_not_found", "session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err != nil {
		return nil, err
	}
	if err := rejectIfFlashing(record); err != nil {
		return nil, err
	}
	record.applyWorkflowState("idle", runtimeFlagState{
		canStart:  record.loadedFile != nil,
		canPause:  false,
		canResume: false,
		canStop:   false,
		canUnload: record.runtimeFlags.CanUnloadFile,
	}, nil)
	outcome := "completed"
	force := payload.Force != nil && *payload.Force
	if force {
		outcome = "cancelled"
	}
	record.appendEvent("job_stopped", map[string]interface{}{
		"workflow_state": record.session.WorkflowState,
		"force":          force,
		"outcome":        outcome,
	})
	addMachineSpanEvent(ctx, "machine.job_stopped",
		attribute.String("machine.outcome", outcome),
	)
	record.appendJobProgress(100)
	record.appendWorkflowRuntimeEvents()
	return &gen.AcceptedResult{Accepted: true}, nil
}

func (s *MemoryMachineService) FlashFirmware(ctx context.Context, payload *gen.FlashFirmwarePayload) (*gen.AcceptedResult, error) {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx,
			attribute.String("machine.device_id", payload.DeviceID),
			attribute.String("machine.image", payload.Image),
		)
	}
	if payload == nil || payload.DeviceID == "" {
		return nil, machineError("device_not_found", "device_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if record, exists := s.getSessionRecordByDeviceID(payload.DeviceID); exists {
		if record.isFlashing() {
			return nil, machineError("flash_not_allowed", "session is already flashing")
		}
		if record.session.WorkflowState == "running" || record.session.WorkflowState == "paused" {
			return nil, machineError("flash_not_allowed", "cannot flash while job is active")
		}
		previousConnectionState := record.session.ConnectionState
		record.runtimeFlags.CanFlashFirmware = false
		record.applyConnectionState("flashing")
		record.appendEvent("flash_started", map[string]interface{}{
			"device_id":       payload.DeviceID,
			"image":           payload.Image,
			"controller_type": payload.ControllerType,
		})
		record.appendEvent("flash_progress", map[string]interface{}{
			"device_id": payload.DeviceID,
			"progress":  100.0,
		})
		record.appendEvent("flash_completed", map[string]interface{}{
			"device_id": payload.DeviceID,
			"image":     payload.Image,
		})
		addMachineSpanEvent(ctx, "machine.flash_completed",
			attribute.String("machine.session_id", record.session.ID),
		)
		record.runtimeFlags.CanFlashFirmware = true
		record.applyConnectionState(previousConnectionState)
	}
	return &gen.AcceptedResult{Accepted: true}, nil
}

func (s *MemoryMachineService) SubscribeEvents(ctx context.Context, payload *gen.AttachClientPayload, stream gen.SubscribeEventsServerStream) error {
	obs.AnnotateSpanFromRequest(ctx, payload)
	if payload != nil {
		annotateMachineSpan(ctx,
			attribute.String("machine.session_id", payload.SessionID),
			attribute.String("machine.client_id", payload.ClientID),
		)
	}
	if payload == nil || payload.SessionID == "" {
		return machineError("session_not_found", "session_id is required")
	}
	s.mu.RLock()
	record, err := s.getSessionRecord(payload.SessionID)
	if err == nil {
		events := cloneEvents(record.events)
		s.mu.RUnlock()
		for _, event := range events {
			if event == nil {
				continue
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		}
		return nil
	}
	events, ok := s.closed[payload.SessionID]
	s.mu.RUnlock()
	if !ok {
		return err
	}
	events = cloneEvents(events)
	for _, event := range events {
		if event == nil {
			continue
		}
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return nil
}

var _ gen.Service = (*MemoryMachineService)(nil)
