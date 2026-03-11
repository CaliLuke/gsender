const GO_MACHINE_CORE_MODE = /^(\s*)(grpc|goa|go)\s*$/i;
const FALSE_PATTERN = /^(\s*)(0|false|off|no)\s*$/i;

const shouldUseGoMachineCoreSidecar = (env = process.env) => {
    return GO_MACHINE_CORE_MODE.test(String(env.MACHINE_CORE_TRANSPORT || env.MACHINE_CORE_MODE || ''));
};

const shouldUseGoMachineCorePath = (pathName, env = process.env) => {
    if (!shouldUseGoMachineCoreSidecar(env)) {
        return false;
    }

    const scopedFlag = env[`MACHINE_CORE_${String(pathName).toUpperCase()}_ENABLED`];
    if (scopedFlag === undefined) {
        return true;
    }

    return !FALSE_PATTERN.test(String(scopedFlag));
};

const extractSessionID = (openSessionResult) => {
    return openSessionResult?.session?.id || null;
};

const buildCanonicalLoadedFile = (engine) => {
    if (!engine?.gcode || !engine?.meta) {
        return null;
    }

    return {
        content: engine.gcode,
        size: engine.meta.size,
        name: engine.meta.name,
        visualizer: engine.meta.visualizer,
    };
};

const buildCanonicalReplayEvent = (event = {}, engine) => {
    if (event?.type !== 'file_loaded') {
        return event;
    }

    const loadedFile = buildCanonicalLoadedFile(engine);
    if (!loadedFile) {
        return event;
    }

    return {
        ...event,
        payload: {
            ...(extractEventPayload(event) || {}),
            file: loadedFile,
        },
    };
};

const buildCanonicalSnapshot = (snapshot = {}, engine) => {
    const loadedFile = buildCanonicalLoadedFile(engine);
    if (!snapshot?.loaded_file || !loadedFile) {
        return snapshot;
    }

    return {
        ...snapshot,
        loaded_file: {
            ...snapshot.loaded_file,
            ...loadedFile,
        },
    };
};

const emitReplayEventToSocket = (socket, event) => {
    if (!socket || typeof socket.emit !== 'function' || !event) {
        return;
    }

    socket.emit('machine:session:event', event);
};

const buildLegacyWorkflowState = (event = {}) => {
    switch (event.type) {
    case 'job_started':
    case 'job_resumed':
        return 'running';
    case 'job_paused':
        return 'paused';
    case 'job_stopped':
    case 'file_loaded':
    case 'file_unloaded':
        return 'idle';
    default:
        return null;
    }
};

const buildLegacySenderStatus = (event = {}) => {
    const workflowState = buildLegacyWorkflowState(event);

    if (!workflowState) {
        return null;
    }

    return {
        workflowState,
        active: workflowState === 'running' || workflowState === 'paused',
    };
};

const buildLegacyHomingState = (input = {}) => {
    const homingRequired = input.homing_required ?? input.homingRequired;
    const hasHomed = input.has_homed ?? input.hasHomed;

    if (homingRequired === undefined && hasHomed === undefined) {
        return null;
    }

    return {
        homingRequired: Boolean(homingRequired),
        hasHomed: Boolean(hasHomed),
    };
};

const extractEventPayload = (event = {}) => {
    return event.payload && typeof event.payload === 'object' ? event.payload : null;
};

const extractControllerType = (input = {}) => {
    const controllerType = input.controller_type || input.controllerType;
    return controllerType ? String(controllerType) : null;
};

const emitControllerSettings = (socket, payload, controllerTypeOverride = null) => {
    const controllerType = controllerTypeOverride || extractControllerType(payload);
    if (controllerType) {
        socket.emit('controller:settings', controllerType, payload);
    }
};

const emitControllerState = (socket, payload, controllerTypeOverride = null) => {
    const controllerType = controllerTypeOverride || extractControllerType(payload);
    if (controllerType) {
        socket.emit('controller:state', controllerType, payload);
    }
};

const replayEventHandlers = {
    file_loaded: (socket, _payload, engine) => {
        if (engine?.gcode && engine?.meta) {
            socket.emit('file:load', engine.gcode, engine.meta.size, engine.meta.name, engine.meta.visualizer);
        }
    },
    file_unloaded: (socket) => {
        socket.emit('file:unload');
    },
    session_closed: (socket, payload) => {
        const port = payload?.device_id || payload?.deviceId;
        socket.emit('serialport:close', { port });
    },
    controller_settings_changed: (socket, payload) => {
        emitControllerSettings(socket, payload);
    },
    controller_state_changed: (socket, payload) => {
        emitControllerState(socket, payload);
    },
    sender_status_changed: (socket, payload) => {
        socket.emit('sender:status', payload);
    },
    feeder_status_changed: (socket, payload) => {
        socket.emit('feeder:status', payload);
    },
    error_raised: (socket, payload) => {
        socket.emit('error', payload);
    },
    alarm_raised: (socket, payload) => {
        socket.emit('error', payload);
    },
    homing_state_changed: (socket, payload) => {
        const homingState = buildLegacyHomingState(payload);
        if (homingState) {
            socket.emit('homing:has-homed', homingState.hasHomed);
        }
    },
    flash_started: (socket, payload) => {
        const deviceId = payload?.device_id || payload?.deviceId || 'device';
        socket.emit('flash:message', {
            type: 'Info',
            content: `Starting flash on ${deviceId}.`,
        });
    },
    flash_progress: (socket, payload) => {
        const progress = Number(payload?.progress);
        if (!Number.isNaN(progress)) {
            socket.emit('flash:progress', progress, 100);
        }
    },
    flash_completed: (socket) => {
        socket.emit('flash:end');
    },
};

const relayMachineSessionEvent = (socket, event, engine) => {
    emitReplayEventToSocket(socket, buildCanonicalReplayEvent(event, engine));
};

const relayMachineSessionSnapshot = (socket, snapshot = {}, engine) => {
    if (!socket || typeof socket.emit !== 'function' || !snapshot || typeof snapshot !== 'object') {
        return;
    }

    socket.emit('machine:session:snapshot', buildCanonicalSnapshot(snapshot, engine));
};

export {
    shouldUseGoMachineCoreSidecar,
    shouldUseGoMachineCorePath,
    extractSessionID,
    emitReplayEventToSocket,
    buildLegacyWorkflowState,
    buildLegacySenderStatus,
    buildLegacyHomingState,
    extractControllerType,
    emitControllerSettings,
    emitControllerState,
    buildCanonicalReplayEvent,
    buildCanonicalSnapshot,
    relayMachineSessionSnapshot,
    relayMachineSessionEvent,
};
