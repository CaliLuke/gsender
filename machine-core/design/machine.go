package design

import . "goa.design/goa/v3/dsl" //nolint:staticcheck

// This file is the frozen v1 session contract.
// Change it only when a concrete parity gap or failing contract test requires it,
// then regenerate machine-core/gen/ from the Goa DSL.

var Device = Type("Device", func() {
	Description("A discoverable machine-capable device.")

	Field(1, "id", String, "Stable device identifier.")
	Field(2, "kind", String, "Device kind.", func() {
		Enum("serial", "network", "dfu")
	})
	Field(3, "path", String, "Serial path when applicable.")
	Field(4, "network_address", String, "Network address when applicable.")
	Field(5, "manufacturer", String)
	Field(6, "vendor_id", String)
	Field(7, "product_id", String)
	Field(8, "serial_number", String)
	Field(9, "display_name", String)
	Field(10, "in_use", Boolean)
	Field(11, "capabilities", ArrayOf(String))

	Required("id", "kind", "display_name", "in_use")
})

var Session = Type("Session", func() {
	Description("One active machine session.")

	Field(1, "id", String)
	Field(2, "device_id", String)
	Field(3, "controller_type", String, func() {
		Enum("unknown", "grbl", "grblhal", "fluidnc")
	})
	Field(4, "connection_state", String, func() {
		Enum(
			"disconnected",
			"opening",
			"probing_firmware",
			"connected",
			"reconnecting",
			"closing",
			"flashing",
			"errored",
		)
	})
	Field(5, "workflow_state", String, func() {
		Enum("idle", "running", "paused", "hold", "stopping")
	})
	Field(6, "created_at", String, "RFC3339 timestamp.")
	Field(7, "updated_at", String, "RFC3339 timestamp.")

	Required(
		"id",
		"device_id",
		"controller_type",
		"connection_state",
		"workflow_state",
	)
})

var LoadedFile = Type("LoadedFile", func() {
	Description("Metadata for the active file loaded into a session.")

	Field(1, "name", String)
	Field(2, "size_bytes", Int64)
	Field(3, "line_count", Int64)
	Field(4, "checksum", String)
	Field(5, "loaded_at", String, "RFC3339 timestamp.")

	Required("name", "size_bytes")
})

var HomingState = Type("HomingState", func() {
	Description("Homing-related runtime state.")

	Field(1, "homing_required", Boolean)
	Field(2, "has_homed", Boolean)
	Field(3, "last_homing_at", String, "RFC3339 timestamp.")

	Required("homing_required", "has_homed")
})

var RuntimeFlags = Type("RuntimeFlags", func() {
	Description("Derived capability flags for the current session state.")

	Field(1, "can_start_job", Boolean)
	Field(2, "can_pause_job", Boolean)
	Field(3, "can_resume_job", Boolean)
	Field(4, "can_stop_job", Boolean)
	Field(5, "can_unload_file", Boolean)
	Field(6, "can_flash_firmware", Boolean)

	Required(
		"can_start_job",
		"can_pause_job",
		"can_resume_job",
		"can_stop_job",
		"can_unload_file",
		"can_flash_firmware",
	)
})

var SessionSnapshot = Type("SessionSnapshot", func() {
	Description("Full reconnect-safe view of a machine session.")

	Field(1, "session", Session)
	Field(2, "controller_state", Any, "Controller runtime state.")
	Field(3, "controller_settings", Any, "Controller configuration/settings.")
	Field(4, "sender_status", Any, "Sender/job streamer status.")
	Field(5, "feeder_status", Any, "Feeder/queue status.")
	Field(6, "loaded_file", LoadedFile)
	Field(7, "homing_state", HomingState)
	Field(8, "alarm_state", Any, "Current alarm information.")
	Field(9, "runtime_flags", RuntimeFlags)
	Field(10, "last_error", String)

	Required("session", "runtime_flags")
})

var AxisMove = Type("AxisMove", func() {
	Description("Jog or axis move request.")

	Field(1, "x", Float64)
	Field(2, "y", Float64)
	Field(3, "z", Float64)
	Field(4, "a", Float64)
	Field(5, "feed_rate", Float64)
})

var MachineCommand = Type("MachineCommand", func() {
	Description("Structured command sent to a machine session.")

	Field(1, "type", String, func() {
		Enum(
			"unlock",
			"home",
			"reset",
			"sleep",
			"feed_hold",
			"cycle_start",
			"status_report",
			"jog",
			"set_feed_override",
			"set_spindle_override",
			"set_rapid_override",
			"raw_gcode_line",
		)
	})
	Field(2, "raw_line", String)
	Field(3, "axis_move", AxisMove)
	Field(4, "feed_override", Int)
	Field(5, "spindle_override", Int)
	Field(6, "rapid_override", Int)
	Field(7, "metadata", MapOf(String, String))

	Required("type")
})

var SessionEvent = Type("SessionEvent", func() {
	Description("Incremental runtime event emitted for a machine session.")

	Field(1, "session_id", String)
	Field(2, "sequence", Int64)
	Field(3, "occurred_at", String, "RFC3339 timestamp.")
	Field(4, "type", String, func() {
		Enum(
			"session_opened",
			"session_closed",
			"client_attached",
			"client_detached",
			"controller_detected",
			"connection_state_changed",
			"controller_state_changed",
			"controller_settings_changed",
			"workflow_state_changed",
			"sender_status_changed",
			"feeder_status_changed",
			"file_loaded",
			"file_unloaded",
			"job_started",
			"job_paused",
			"job_resumed",
			"job_stopped",
			"job_progress",
			"homing_state_changed",
			"alarm_raised",
			"error_raised",
			"flash_started",
			"flash_progress",
			"flash_completed",
		)
	})
	Field(5, "payload", Any, "Event-specific payload.")

	Required("session_id", "sequence", "occurred_at", "type")
})

func rpcContextFields(start int) {
	Field(start, "rpc_request_id", String)
	Field(start+1, "rpc_action", String)
	Field(start+2, "rpc_port", String)
	Field(start+3, "rpc_session_id", String)
	Field(start+4, "rpc_socket_id", String)
	Field(start+5, "rpc_controller_event", String)
	Field(start+6, "rpc_command_type", String)
	Field(start+7, "rpc_origin", String)
}

func rpcContextMetadata() {
	Metadata(func() {
		Attribute("rpc_request_id:x-machine-core-request-id")
		Attribute("rpc_action:x-machine-core-action")
		Attribute("rpc_port:x-machine-core-port")
		Attribute("rpc_session_id:x-machine-core-session-id")
		Attribute("rpc_socket_id:x-machine-core-socket-id")
		Attribute("rpc_controller_event:x-machine-core-controller-event")
		Attribute("rpc_command_type:x-machine-core-command-type")
		Attribute("rpc_origin:x-machine-core-origin")
	})
}

var OpenSessionPayload = Type("OpenSessionPayload", func() {
	Description("Parameters required to open a machine session.")

	Field(1, "device_id", String)
	Field(2, "baud_rate", Int)
	Field(3, "rtscts", Boolean)
	Field(4, "network_port", Int)
	Field(5, "default_firmware", String)
	Field(6, "client_id", String)
	rpcContextFields(90)

	Required("device_id")
})

var OpenSessionResult = Type("OpenSessionResult", func() {
	Field(1, "session", Session)
	Field(2, "snapshot", SessionSnapshot)

	Required("session", "snapshot")
})

var AttachClientPayload = Type("AttachClientPayload", func() {
	Field(1, "session_id", String)
	Field(2, "client_id", String)
	rpcContextFields(90)

	Required("session_id", "client_id")
})

var DetachClientPayload = Type("DetachClientPayload", func() {
	Field(1, "session_id", String)
	Field(2, "client_id", String)
	rpcContextFields(90)

	Required("session_id", "client_id")
})

var SessionIDPayload = Type("SessionIDPayload", func() {
	Field(1, "session_id", String)
	rpcContextFields(90)
	Required("session_id")
})

var ResolveSessionPayload = Type("ResolveSessionPayload", func() {
	Field(1, "device_id", String)
	Field(2, "client_id", String)
	rpcContextFields(90)

	Required("device_id", "client_id")
})

var SendCommandPayload = Type("SendCommandPayload", func() {
	Field(1, "session_id", String)
	Field(2, "command", MachineCommand)
	rpcContextFields(90)

	Required("session_id", "command")
})

var LoadFilePayload = Type("LoadFilePayload", func() {
	Field(1, "session_id", String)
	Field(2, "name", String)
	Field(3, "content", String)
	Field(4, "content_type", String)
	Field(5, "metadata", MapOf(String, String))
	rpcContextFields(90)

	Required("session_id", "name", "content")
})

var StopJobPayload = Type("StopJobPayload", func() {
	Field(1, "session_id", String)
	Field(2, "force", Boolean)
	rpcContextFields(90)

	Required("session_id")
})

var FlashFirmwarePayload = Type("FlashFirmwarePayload", func() {
	Field(1, "device_id", String)
	Field(2, "image", String, "Image identifier or artifact reference.")
	Field(3, "hex", String, "Inline payload when needed.")
	Field(4, "controller_type", String)
	rpcContextFields(90)

	Required("device_id", "image")
})

var AcceptedResult = Type("AcceptedResult", func() {
	Description("Command accepted for asynchronous execution.")

	Field(1, "accepted", Boolean)

	Required("accepted")
})

var BooleanResult = Type("BooleanResult", func() {
	Field(1, "ok", Boolean)
	Required("ok")
})

var MachineError = Type("MachineError", func() {
	Description("Domain-level machine runtime error.")

	Field(1, "code", String, func() {
		Enum(
			"device_not_found",
			"device_busy",
			"session_not_found",
			"unsupported_controller",
			"firmware_detection_failed",
			"invalid_state_transition",
			"command_rejected",
			"file_not_loaded",
			"job_not_running",
			"job_not_paused",
			"flash_not_allowed",
			"transport_error",
			"internal_error",
		)
	})
	Field(2, "message", String)
	Field(3, "retryable", Boolean)
	Field(4, "details", MapOf(String, String))

	Required("code", "message", "retryable")
})

var _ = API("machine", func() {
	Title("gSender Machine Session API")
	Description("Session-oriented API for the Go machine core.")
	Server("machine", func() {
		Host("local", func() {
			URI("grpc://localhost:8081")
		})
	})
})

var _ = Service("machine", func() {
	Description("Machine session lifecycle and runtime control.")

	Error("machine_error", MachineError, "Machine domain error.")

	Method("list_devices", func() {
		Description("List discoverable machine-capable devices.")
		Result(ArrayOf(Device))
		GRPC(func() {
			Response(CodeOK)
		})
	})

	Method("open_session", func() {
		Description("Open a device and create a machine session.")
		Payload(OpenSessionPayload)
		Result(OpenSessionResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("close_session", func() {
		Description("Close and destroy a machine session.")
		Payload(SessionIDPayload)
		Result(BooleanResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("get_session", func() {
		Description("Get session metadata.")
		Payload(SessionIDPayload)
		Result(Session)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("get_snapshot", func() {
		Description("Get full session snapshot suitable for reconnect or replay.")
		Payload(SessionIDPayload)
		Result(SessionSnapshot)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("resolve_session", func() {
		Description("Resolve the active session for a device, attach the client, and return a replay-safe snapshot.")
		Payload(ResolveSessionPayload)
		Result(SessionSnapshot)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("attach_client", func() {
		Description("Attach a logical client to a session and return a replay-safe snapshot.")
		Payload(AttachClientPayload)
		Result(SessionSnapshot)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("detach_client", func() {
		Description("Detach a logical client from a session.")
		Payload(DetachClientPayload)
		Result(BooleanResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("send_command", func() {
		Description("Submit a structured machine command.")
		Payload(SendCommandPayload)
		Result(AcceptedResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("load_file", func() {
		Description("Load G-code into the session runtime.")
		Payload(LoadFilePayload)
		Result(SessionSnapshot)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("unload_file", func() {
		Description("Unload the active file from the session.")
		Payload(SessionIDPayload)
		Result(BooleanResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("start_job", func() {
		Description("Start executing the loaded file.")
		Payload(SessionIDPayload)
		Result(AcceptedResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("pause_job", func() {
		Description("Pause the active job.")
		Payload(SessionIDPayload)
		Result(AcceptedResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("resume_job", func() {
		Description("Resume a paused job.")
		Payload(SessionIDPayload)
		Result(AcceptedResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("stop_job", func() {
		Description("Stop the active job.")
		Payload(StopJobPayload)
		Result(AcceptedResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("flash_firmware", func() {
		Description("Flash firmware to a machine device.")
		Payload(FlashFirmwarePayload)
		Result(AcceptedResult)
		Error("machine_error")
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})

	Method("subscribe_events", func() {
		Description("Stream runtime events for one session.")
		Payload(AttachClientPayload)
		Error("machine_error")
		StreamingResult(SessionEvent)
		GRPC(func() {
			rpcContextMetadata()
			Response(CodeOK)
		})
	})
})
