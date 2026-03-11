package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"

	gen "github.com/Sienci-Labs/gsender/machine-core/gen/machine"
)

const (
	defaultProbeBaudRate = 115200
	defaultProbeTimeout  = 1500 * time.Millisecond
	probeReadTimeout     = 250 * time.Millisecond
)

type controllerDetection struct {
	controllerType    string
	connectionState   string
	workflowState     string
	controllerState   map[string]interface{}
	controllerSetting map[string]interface{}
	senderStatus      map[string]interface{}
	feederStatus      map[string]interface{}
	homingState       *gen.HomingState
	alarmState        interface{}
	lastError         *string
}

type serialProbeResult struct {
	controllerType  string
	banner          string
	firmwareFamily  string
	firmwareVersion string
	firmwareConfig  map[string]interface{}
	activeState     string
	subState        string
	hasHomed        *bool
	alarmCode       string
}

type controllerDetector interface {
	Detect(context.Context, *gen.Device, *gen.OpenSessionPayload) (*controllerDetection, error)
}

type hostControllerDetector struct{}

func (d hostControllerDetector) Detect(ctx context.Context, device *gen.Device, payload *gen.OpenSessionPayload) (*controllerDetection, error) {
	controllerType, detectionSource, probeResult, err := detectControllerType(ctx, device, payload)
	if err != nil {
		return nil, err
	}
	connectionState := inferConnectionState(controllerType)
	workflowState := inferWorkflowStateFromProbe(probeResult)
	settings := buildControllerSettings(device, controllerType, payload)
	if detectionSource != "" {
		settings["detection_source"] = detectionSource
	}
	if probeResult != nil && probeResult.banner != "" {
		settings["firmware_banner"] = probeResult.banner
	}
	if probeResult != nil && probeResult.firmwareFamily != "" {
		settings["firmware_family"] = probeResult.firmwareFamily
	}
	if probeResult != nil && probeResult.firmwareVersion != "" {
		settings["firmware_version"] = probeResult.firmwareVersion
	}
	if probeResult != nil && len(probeResult.firmwareConfig) > 0 {
		settings["settings_source"] = detectionSource
		settings["firmware_config"] = cloneInterfaceMap(probeResult.firmwareConfig)
		applyFirmwareConfigSummary(settings, probeResult.firmwareConfig)
	}
	state := buildControllerState(device, controllerType, connectionState)
	applyProbeRuntimeState(state, probeResult)
	if detectionSource != "" {
		state["detection_source"] = detectionSource
	}
	if probeResult != nil && probeResult.banner != "" {
		state["firmware_banner"] = probeResult.banner
	}
	if probeResult != nil && probeResult.firmwareFamily != "" {
		state["firmware_family"] = probeResult.firmwareFamily
	}
	if probeResult != nil && probeResult.firmwareVersion != "" {
		state["firmware_version"] = probeResult.firmwareVersion
	}
	homingState := buildProbeHomingState(settings, probeResult)
	alarmState, lastError := buildProbeAlarmState(probeResult)
	senderStatus := buildSenderStatus(connectionState)
	senderStatus = updateSenderExecutionState(senderStatus, workflowState)
	if workflowState == "paused" {
		senderStatus["hold"] = true
		senderStatus["holdReason"] = "controller_hold"
	}
	feederStatus := buildFeederStatus()
	feederStatus = updateFeederExecutionState(feederStatus, workflowState, false)
	if workflowState == "paused" {
		feederStatus["hold"] = true
		feederStatus["holdReason"] = "controller_hold"
	}
	return &controllerDetection{
		controllerType:    controllerType,
		connectionState:   connectionState,
		workflowState:     workflowState,
		controllerState:   state,
		controllerSetting: settings,
		senderStatus:      senderStatus,
		feederStatus:      feederStatus,
		homingState:       homingState,
		alarmState:        alarmState,
		lastError:         lastError,
	}, nil
}

func detectControllerType(ctx context.Context, device *gen.Device, payload *gen.OpenSessionPayload) (string, string, *serialProbeResult, error) {
	if device == nil {
		return "unknown", "", nil, nil
	}

	switch device.ID {
	case "sim://loopback":
		return "grblhal", "simulator", &serialProbeResult{
			controllerType: "grblhal",
			banner:         "grblHAL simulator",
			firmwareFamily: "grblHAL",
		}, nil
	case "tcp://sim.local:23":
		return "fluidnc", "simulator", &serialProbeResult{
			controllerType: "fluidnc",
			banner:         "FluidNC simulator",
			firmwareFamily: "FluidNC",
		}, nil
	}

	if device.Kind == "serial" && device.Path != nil && shouldProbeSerialPath(*device.Path) {
		probeResult, err := probeSerialController(ctx, *device.Path, payload)
		if err == nil && probeResult.controllerType != "unknown" {
			return probeResult.controllerType, "serial_probe", probeResult, nil
		}
	}

	if payload != nil && payload.DefaultFirmware != nil {
		if controllerType := normalizeControllerType(*payload.DefaultFirmware); controllerType != "unknown" {
			return controllerType, "default_firmware", nil, nil
		}
	}

	return "unknown", "", nil, nil
}

func shouldProbeSerialPath(path string) bool {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "/dev/") {
		return true
	}
	if strings.HasPrefix(strings.ToUpper(normalized), "COM") {
		return true
	}
	return false
}

func probeSerialController(ctx context.Context, path string, payload *gen.OpenSessionPayload) (*serialProbeResult, error) {
	baudRate := defaultProbeBaudRate
	if payload != nil && payload.BaudRate != nil && *payload.BaudRate > 0 {
		baudRate = int(*payload.BaudRate)
	}

	port, err := serial.Open(path, &serial.Mode{BaudRate: baudRate})
	if err != nil {
		return nil, err
	}
	defer port.Close()

	if err := port.SetReadTimeout(probeReadTimeout); err != nil {
		return nil, err
	}

	if _, err := port.Write([]byte("\n?\n$I\n$$\nversion\n")); err != nil {
		return nil, err
	}

	probeCtx := ctx
	if probeCtx == nil {
		probeCtx = context.Background()
	}
	var cancel context.CancelFunc
	probeCtx, cancel = context.WithTimeout(probeCtx, defaultProbeTimeout)
	defer cancel()

	var response strings.Builder
	buffer := make([]byte, 512)
	for {
		select {
		case <-probeCtx.Done():
			if errors.Is(probeCtx.Err(), context.DeadlineExceeded) && response.Len() > 0 {
				return buildSerialProbeResult(response.String()), nil
			}
			if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
				return &serialProbeResult{controllerType: "unknown"}, nil
			}
			return nil, probeCtx.Err()
		default:
		}

		n, err := port.Read(buffer)
		if n > 0 {
			response.Write(buffer[:n])
			probeResult := buildSerialProbeResult(response.String())
			if probeResult.controllerType != "unknown" {
				return probeResult, nil
			}
		}
		if err == nil || errors.Is(err, io.EOF) {
			continue
		}
		return nil, err
	}
}

func buildSerialProbeResult(response string) *serialProbeResult {
	banner := extractFirmwareBanner(response)
	return &serialProbeResult{
		controllerType:  classifyControllerResponse(response),
		banner:          banner,
		firmwareFamily:  extractFirmwareFamily(banner),
		firmwareVersion: extractFirmwareVersion(banner),
		firmwareConfig:  extractFirmwareConfig(response),
		activeState:     extractActiveState(response),
		subState:        extractStatusSubState(response),
		hasHomed:        extractStatusHasHomed(response),
		alarmCode:       extractAlarmCode(response),
	}
}

func extractFirmwareConfig(response string) map[string]interface{} {
	config := map[string]interface{}{}
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "$") || !strings.Contains(trimmed, "=") {
			continue
		}

		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}
		config[key] = coerceFirmwareConfigValue(value)
	}
	if len(config) == 0 {
		return nil
	}

	deriveBooleanFirmwareSetting(config, "$20", "soft_limits")
	deriveBooleanFirmwareSetting(config, "$21", "hard_limits")
	deriveBooleanFirmwareSetting(config, "$22", "homing_enabled")
	deriveAxisFirmwareSetting(config, "$130", "max_travel_x")
	deriveAxisFirmwareSetting(config, "$131", "max_travel_y")
	deriveAxisFirmwareSetting(config, "$132", "max_travel_z")
	return config
}

func coerceFirmwareConfigValue(value string) interface{} {
	if integerValue, err := strconv.ParseInt(value, 10, 64); err == nil {
		return integerValue
	}
	if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
		return floatValue
	}
	return value
}

func deriveBooleanFirmwareSetting(config map[string]interface{}, sourceKey, derivedKey string) {
	rawValue, ok := config[sourceKey]
	if !ok {
		return
	}
	switch value := rawValue.(type) {
	case int64:
		config[derivedKey] = value != 0
	case float64:
		config[derivedKey] = value != 0
	case string:
		config[derivedKey] = value != "0"
	}
}

func deriveAxisFirmwareSetting(config map[string]interface{}, sourceKey, derivedKey string) {
	rawValue, ok := config[sourceKey]
	if !ok {
		return
	}
	config[derivedKey] = rawValue
}

func cloneInterfaceMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func applyFirmwareConfigSummary(settings map[string]interface{}, config map[string]interface{}) {
	if settings == nil || config == nil {
		return
	}

	for _, key := range []string{
		"soft_limits",
		"hard_limits",
		"homing_enabled",
		"max_travel_x",
		"max_travel_y",
		"max_travel_z",
	} {
		if value, ok := config[key]; ok {
			settings[key] = value
		}
	}
}

func extractStatusLine(response string) string {
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") {
			return trimmed
		}
	}
	return ""
}

func extractActiveState(response string) string {
	line := extractStatusLine(response)
	if line == "" {
		return ""
	}
	body := strings.TrimSuffix(strings.TrimPrefix(line, "<"), ">")
	first := body
	if idx := strings.Index(first, "|"); idx >= 0 {
		first = first[:idx]
	}
	if idx := strings.Index(first, ","); idx >= 0 {
		first = first[:idx]
	}
	if idx := strings.Index(first, ":"); idx >= 0 {
		first = first[:idx]
	}
	return strings.TrimSpace(first)
}

func extractStatusSubState(response string) string {
	line := extractStatusLine(response)
	if line == "" {
		return ""
	}
	body := strings.TrimSuffix(strings.TrimPrefix(line, "<"), ">")
	first := body
	if idx := strings.Index(first, "|"); idx >= 0 {
		first = first[:idx]
	}
	if idx := strings.Index(first, ","); idx >= 0 {
		first = first[:idx]
	}
	if idx := strings.Index(first, ":"); idx >= 0 {
		return strings.TrimSpace(first[idx+1:])
	}
	return ""
}

func extractStatusHasHomed(response string) *bool {
	line := extractStatusLine(response)
	if line == "" {
		return nil
	}
	for _, part := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "<"), ">"), "|") {
		if !strings.HasPrefix(part, "H:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(part, "H:"))
		parsed := value == "1"
		return &parsed
	}
	return nil
}

func extractAlarmCode(response string) string {
	line := extractStatusLine(response)
	if line != "" {
		body := strings.TrimSuffix(strings.TrimPrefix(line, "<"), ">")
		first := body
		if idx := strings.Index(first, "|"); idx >= 0 {
			first = first[:idx]
		}
		if strings.HasPrefix(first, "Alarm:") {
			return strings.TrimSpace(strings.TrimPrefix(first, "Alarm:"))
		}
	}
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ALARM:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "ALARM:"))
		}
	}
	return ""
}

func inferWorkflowStateFromProbe(probeResult *serialProbeResult) string {
	if probeResult == nil {
		return "idle"
	}
	switch strings.ToLower(strings.TrimSpace(probeResult.activeState)) {
	case "run", "jog", "home":
		return "running"
	case "hold":
		return "paused"
	case "idle", "alarm", "door", "check", "sleep", "":
		return "idle"
	default:
		return "idle"
	}
}

func buildProbeHomingState(settings map[string]interface{}, probeResult *serialProbeResult) *gen.HomingState {
	homingState := &gen.HomingState{}
	if settings != nil {
		if value, ok := settings["homing_enabled"]; ok {
			if parsed, ok := cloneBool(value); ok {
				homingState.HomingRequired = parsed
			}
		}
	}
	if probeResult != nil && probeResult.hasHomed != nil {
		homingState.HasHomed = *probeResult.hasHomed
		if homingState.HasHomed {
			homingState.HomingRequired = false
			homingState.LastHomingAt = nowRFC3339()
		}
	}
	return homingState
}

func buildProbeAlarmState(probeResult *serialProbeResult) (interface{}, *string) {
	if probeResult == nil {
		return nil, nil
	}
	activeState := strings.ToLower(strings.TrimSpace(probeResult.activeState))
	if activeState != "alarm" && probeResult.alarmCode == "" {
		return nil, nil
	}
	message := "controller alarm"
	if probeResult.alarmCode != "" {
		message = "controller alarm " + probeResult.alarmCode
	}
	formatted := fmt.Sprintf("controller_alarm: %s", message)
	return map[string]interface{}{
		"code":    pickString(probeResult.alarmCode, "alarm"),
		"message": message,
	}, ptrString(formatted)
}

func pickString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func applyProbeRuntimeState(state map[string]interface{}, probeResult *serialProbeResult) {
	if state == nil || probeResult == nil {
		return
	}
	if probeResult.activeState != "" {
		state["active_state"] = probeResult.activeState
	}
	if probeResult.subState != "" {
		state["sub_state"] = probeResult.subState
	}
	if probeResult.hasHomed != nil {
		state["has_homed"] = *probeResult.hasHomed
	}
	if probeResult.alarmCode != "" {
		state["alarm_code"] = probeResult.alarmCode
	}
}

func classifyControllerResponse(response string) string {
	normalized := strings.ToLower(response)
	switch {
	case strings.Contains(normalized, "grblhal"):
		return "grblhal"
	case strings.Contains(normalized, "fluidnc"):
		return "fluidnc"
	case strings.Contains(normalized, "grbl"):
		return "grbl"
	default:
		return "unknown"
	}
}

func extractFirmwareBanner(response string) string {
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if strings.Contains(normalized, "grbl") || strings.Contains(normalized, "fluidnc") {
			return trimmed
		}
	}
	return ""
}

func extractFirmwareFamily(banner string) string {
	trimmed := strings.TrimSpace(banner)
	if trimmed == "" {
		return ""
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}

	return fields[0]
}

func extractFirmwareVersion(banner string) string {
	trimmed := strings.TrimSpace(banner)
	if trimmed == "" {
		return ""
	}

	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return ""
	}

	version := strings.Trim(fields[1], "[]()")
	if strings.ContainsAny(version, "0123456789") {
		return version
	}

	return ""
}
