/*
 * Copyright (C) 2021 Sienci Labs Inc.
 *
 * This file is part of gSender.
 *
 * gSender is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, under version 3 of the License.
 *
 * gSender is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with gSender.  If not, see <https://www.gnu.org/licenses/>.
 *
 * Contact for information regarding this program and its license
 * can be sent through gSender@sienci.com or mailed to the main office
 * of Sienci Labs Inc. in Waterloo, Ontario, Canada.
 *
 */

import fs from 'fs';
import path from 'path';
import crypto from 'crypto';

const GRPC_MODE = /^(\s*)(grpc|goa|go)\s*$/i;
const MACHINE_CORE_ADDR_FALLBACK = '127.0.0.1:8081';
const MACHINE_CORE_PROTO_PATH = path.resolve(
    process.cwd(),
    'machine-core',
    'gen',
    'grpc',
    'machine',
    'pb',
    'goagen_machine-core_machine.proto'
);
const MACHINE_CORE_PROTO_DIR = path.dirname(MACHINE_CORE_PROTO_PATH);

const getLog = (() => {
    let cachedLog = null;

    return () => {
        if (cachedLog) {
            return cachedLog;
        }

        try {
            // eslint-disable-next-line global-require
            const loggerModule = require('../../lib/logger');
            const loggerFactory = loggerModule.default || loggerModule;
            cachedLog = loggerFactory('service:machine-core');
        } catch (error) {
            cachedLog = {
                info: () => {},
                warn: () => {},
            };
        }

        return cachedLog;
    };
})();

const getCncEngine = (() => {
    let cachedModule = null;

    return () => {
        if (cachedModule) {
            return cachedModule;
        }

        // eslint-disable-next-line global-require
        const cncengineModule = require('../cncengine');
        cachedModule = cncengineModule.default || cncengineModule;
        return cachedModule;
    };
})();

// Keep a narrow compatibility boundary for machine orchestration.
// Current production implementation remains here; this will allow us to switch
// to a Go-based machine core without touching call sites.
const legacyAdapter = {
    start: (server, controller) => {
        return getCncEngine().start(server, controller);
    },
    stop: () => {
        return getCncEngine().stop();
    },
    load: (gcode) => {
        return getCncEngine().load(gcode);
    },
    unload: () => {
        return getCncEngine().unload();
    },
    listDevices: () => {
        return getCncEngine().listDevices();
    },
    openSession: (sessionConfig) => {
        throw new Error(`openSession() not implemented for legacy adapter: ${JSON.stringify(sessionConfig)}`);
    },
    closeSession: (sessionId) => {
        throw new Error(`closeSession() not implemented for legacy adapter: ${sessionId}`);
    },
    getSnapshot: (sessionId) => {
        throw new Error(`getSnapshot() not implemented for legacy adapter: ${sessionId}`);
    },
    resolveSession: (deviceId, clientId) => {
        throw new Error(`resolveSession() not implemented for legacy adapter: ${deviceId}, ${clientId}`);
    },
    loadFile: (sessionId, file) => {
        throw new Error(`loadFile() not implemented for legacy adapter: ${sessionId}, ${JSON.stringify(file)}`);
    },
    unloadFile: (sessionId) => {
        throw new Error(`unloadFile() not implemented for legacy adapter: ${sessionId}`);
    },
    startJob: (sessionId) => {
        throw new Error(`startJob() not implemented for legacy adapter: ${sessionId}`);
    },
    pauseJob: (sessionId) => {
        throw new Error(`pauseJob() not implemented for legacy adapter: ${sessionId}`);
    },
    resumeJob: (sessionId) => {
        throw new Error(`resumeJob() not implemented for legacy adapter: ${sessionId}`);
    },
    stopJob: (sessionId, options = {}) => {
        throw new Error(`stopJob() not implemented for legacy adapter: ${sessionId}, ${JSON.stringify(options)}`);
    },
    attachClient: (sessionId, clientId) => {
        throw new Error(`attachClient() not implemented for legacy adapter: ${sessionId}, ${clientId}`);
    },
    replayEvents: (sessionId, clientId) => {
        throw new Error(`replayEvents() not implemented for legacy adapter: ${sessionId}, ${clientId}`);
    },
    detachClient: (sessionId, clientId) => {
        throw new Error(`detachClient() not implemented for legacy adapter: ${sessionId}, ${clientId}`);
    },
    sendCommand: (sessionId, command) => {
        throw new Error(`sendCommand() not implemented for legacy adapter: ${sessionId}, ${command?.type || 'unknown'}`);
    },
    flashFirmware: (payload) => {
        throw new Error(`flashFirmware() not implemented for legacy adapter: ${JSON.stringify(payload)}`);
    }
};

const toGoString = (value) => {
    if (value === undefined || value === null || value === '') {
        return undefined;
    }
    return String(value);
};

const toGoInt = (value) => {
    if (value === undefined || value === null || value === '') {
        return undefined;
    }
    const parsed = Number.parseInt(value, 10);
    return Number.isNaN(parsed) ? undefined : parsed;
};

const toGoBool = (value) => {
    if (value === undefined || value === null) {
        return undefined;
    }
    return Boolean(value);
};

const pickFirst = (...values) => {
    for (const value of values) {
        if (value !== undefined && value !== null) {
            return value;
        }
    }
    return undefined;
};

const mapOpenSessionPayload = (sessionConfig = {}) => {
    const payload = {
        device_id: pickFirst(sessionConfig.device_id, sessionConfig.deviceId, sessionConfig.path, sessionConfig.port),
        baud_rate: toGoInt(pickFirst(sessionConfig.baud_rate, sessionConfig.baudRate)),
        rtscts: toGoBool(pickFirst(sessionConfig.rtscts, sessionConfig.rtsCts, sessionConfig.rtsCTS)),
        network_port: toGoInt(pickFirst(sessionConfig.network_port, sessionConfig.networkPort)),
        default_firmware: toGoString(pickFirst(sessionConfig.default_firmware, sessionConfig.defaultFirmware)),
        client_id: toGoString(pickFirst(sessionConfig.client_id, sessionConfig.clientId)),
    };

    Object.keys(payload).forEach((key) => {
        if (payload[key] === undefined) {
            delete payload[key];
        }
    });

    return payload;
};

const mapSessionIDPayload = (sessionId) => {
    return {
        session_id: String(sessionId)
    };
};

const mapResolveSessionPayload = (deviceId, clientId) => {
    return {
        device_id: String(deviceId),
        client_id: String(clientId),
    };
};

const mapMetadata = (metadata = {}) => {
    if (!metadata || typeof metadata !== 'object') {
        return undefined;
    }

    const entries = Object.entries(metadata)
        .reduce((acc, [key, value]) => {
            if (value !== undefined && value !== null) {
                acc[key] = String(value);
            }
            return acc;
        }, {});

    return Object.keys(entries).length === 0 ? undefined : entries;
};

const mapCommandPayload = (command = {}) => {
    const payload = {
        type: command.type,
        raw_line: toGoString(pickFirst(command.raw_line, command.rawLine)),
        axis_move: command.axis_move || command.axisMove,
        feed_override: toGoInt(pickFirst(command.feed_override, command.feedOverride)),
        spindle_override: toGoInt(pickFirst(command.spindle_override, command.spindleOverride)),
        rapid_override: toGoInt(pickFirst(command.rapid_override, command.rapidOverride)),
        metadata: mapMetadata(command.metadata),
    };

    Object.keys(payload).forEach((key) => {
        if (payload[key] === undefined) {
            delete payload[key];
        }
    });
    return payload;
};

const mapSendCommandPayload = (sessionId, command = {}) => {
    return {
        session_id: String(sessionId),
        command: mapCommandPayload(command),
    };
};

const mapLoadFilePayload = (sessionId, file = {}) => {
    const payload = {
        session_id: String(sessionId),
        name: String(file.name || 'untitled.nc'),
        content: String(file.content || file.gcode || ''),
        content_type: toGoString(pickFirst(file.content_type, file.contentType)),
        metadata: mapMetadata(file.metadata),
    };

    Object.keys(payload).forEach((key) => {
        if (payload[key] === undefined) {
            delete payload[key];
        }
    });

    return payload;
};

const mapFlashFirmwarePayload = (payload = {}) => {
    const result = {
        device_id: String(pickFirst(payload.device_id, payload.deviceId, payload.port) || ''),
        image: String(pickFirst(payload.image, payload.image_type, payload.imageType) || ''),
        hex: toGoString(payload.hex),
        controller_type: toGoString(pickFirst(payload.controller_type, payload.controllerType)),
    };

    Object.keys(result).forEach((key) => {
        if (result[key] === undefined) {
            delete result[key];
        }
    });

    return result;
};

const mapRPCContextMetadata = (rpcContext = {}) => {
    if (!rpcContext || typeof rpcContext !== 'object') {
        return null;
    }

    const normalized = {
        'x-machine-core-request-id': pickFirst(rpcContext.request_id, rpcContext.requestId, crypto.randomUUID()),
        'x-machine-core-action': toGoString(pickFirst(rpcContext.action, rpcContext.operation)),
        'x-machine-core-port': toGoString(pickFirst(rpcContext.port, rpcContext.device_id, rpcContext.deviceId)),
        'x-machine-core-session-id': toGoString(pickFirst(rpcContext.session_id, rpcContext.sessionId)),
        'x-machine-core-socket-id': toGoString(pickFirst(rpcContext.socket_id, rpcContext.socketId)),
        'x-machine-core-controller-event': toGoString(pickFirst(rpcContext.controller_event, rpcContext.controllerEvent)),
        'x-machine-core-command-type': toGoString(pickFirst(rpcContext.command_type, rpcContext.commandType)),
        'x-machine-core-origin': toGoString(pickFirst(rpcContext.origin, 'node-cncengine')),
    };

    Object.keys(normalized).forEach((key) => {
        if (normalized[key] === undefined) {
            delete normalized[key];
        }
    });

    return normalized;
};

const unwrapListDevicesResponse = (response) => {
    return response?.field || [];
};

const deviceDiscoveryKey = (device = {}) => {
    if (device.kind === 'network') {
        return `network:${device.network_address || device.id || ''}`;
    }
    return `serial:${device.path || device.id || ''}`;
};

const mergeGoDeviceWithLegacyMetadata = (goDevice = {}, legacyDevice = {}) => ({
    ...goDevice,
    manufacturer: pickFirst(legacyDevice.manufacturer, goDevice.manufacturer),
    vendor_id: pickFirst(legacyDevice.vendor_id, goDevice.vendor_id),
    product_id: pickFirst(legacyDevice.product_id, goDevice.product_id),
    serial_number: pickFirst(legacyDevice.serial_number, goDevice.serial_number),
    in_use: Boolean(goDevice.in_use || legacyDevice.in_use),
});

const mergeGoDeviceListWithLegacyDiscovery = (goDevices = [], legacyDevices = []) => {
    const legacyByDeviceKey = new Map(
        legacyDevices.map((device) => [deviceDiscoveryKey(device), device])
    );

    return goDevices.map((device) => {
        const legacyDevice = legacyByDeviceKey.get(deviceDiscoveryKey(device));
        if (!legacyDevice) {
            return device;
        }
        return mergeGoDeviceWithLegacyMetadata(device, legacyDevice);
    });
};

const createEventReplayPayload = (sessionId, clientId) => ({
    session_id: String(sessionId),
    client_id: String(clientId),
});

const replaySubscribeEventsStream = (stream, onEvent = () => {}) => {
    const events = [];

    if (!stream || typeof stream.on !== 'function') {
        throw new Error('machine-core subscribe stream is not readable');
    }

    return new Promise((resolve, reject) => {
        stream.on('data', (event) => {
            events.push(event);
            onEvent(event);
        });
        stream.on('error', reject);
        stream.on('end', () => resolve(events));
    });
};

const normalizeMachineCoreAddress = (input = MACHINE_CORE_ADDR_FALLBACK) => {
    if (!input || input === ':') {
        return MACHINE_CORE_ADDR_FALLBACK;
    }
    if (input.startsWith(':')) {
        return `127.0.0.1${input}`;
    }
    return input;
};

const createGoMachineCoreAdapter = () => {
    let grpc;
    let protoLoader;

    try {
        // eslint-disable-next-line global-require
        grpc = require('@grpc/grpc-js'); // eslint-disable-line import/no-extraneous-dependencies
        // eslint-disable-next-line global-require
        protoLoader = require('@grpc/proto-loader'); // eslint-disable-line import/no-extraneous-dependencies
    } catch (error) {
        throw new Error(`machine-core grpc transport unavailable: ${error.message}`);
    }

    if (!fs.existsSync(MACHINE_CORE_PROTO_PATH)) {
        throw new Error(`machine-core proto file is missing at ${MACHINE_CORE_PROTO_PATH}`);
    }

    const packageDefinition = protoLoader.loadSync(MACHINE_CORE_PROTO_PATH, {
        keepCase: true,
        includeDirs: [MACHINE_CORE_PROTO_DIR],
        longs: String,
        enums: String,
        defaults: true,
        oneofs: true,
    });

    const proto = grpc.loadPackageDefinition(packageDefinition);
    if (!proto || !proto.machine || !proto.machine.Machine) {
        throw new Error('machine-core proto did not expose machine.Machine client constructor');
    }

    const machineAddress = normalizeMachineCoreAddress(process.env.MACHINE_CORE_ADDR || MACHINE_CORE_ADDR_FALLBACK);
    const client = new proto.machine.Machine(
        machineAddress,
        grpc.credentials.createInsecure()
    );

    const call = (method, request, rpcContext = null) => new Promise((resolve, reject) => {
        const metadataValues = mapRPCContextMetadata(rpcContext);
        const metadata = metadataValues ? new grpc.Metadata() : null;
        if (metadata && metadataValues) {
            Object.entries(metadataValues).forEach(([key, value]) => {
                metadata.set(key, value);
            });
        }
        client[method](request, metadata || undefined, (error, response) => {
            if (error) {
                reject(error);
                return;
            }
            resolve(response);
        });
    });

    const timedCall = async (method, request, rpcContext = null) => {
        const startedAt = Date.now();
        const log = getLog();
        try {
            const response = await call(method, request, rpcContext);
            log.info(`machine-core grpc ${method} completed in ${Date.now() - startedAt}ms`);
            return response;
        } catch (error) {
            log.warn(`machine-core grpc ${method} failed after ${Date.now() - startedAt}ms: ${error.message}`);
            throw error;
        }
    };

    return {
        start: (server, controller) => {
            return legacyAdapter.start(server, controller);
        },
        stop: () => {
            return legacyAdapter.stop();
        },
        load: (payload) => {
            return legacyAdapter.load(payload);
        },
        unload: () => {
            return legacyAdapter.unload();
        },
        listDevices: async () => {
            const response = await timedCall('ListDevices', {});
            const goDevices = unwrapListDevicesResponse(response);
            try {
                const legacyDevices = await legacyAdapter.listDevices();
                return mergeGoDeviceListWithLegacyDiscovery(goDevices, legacyDevices);
            } catch (error) {
                return goDevices;
            }
        },
        openSession: async (sessionConfig, rpcContext = null) => {
            const response = await timedCall('OpenSession', mapOpenSessionPayload(sessionConfig), rpcContext);
            return response || null;
        },
        closeSession: (sessionId, rpcContext = null) => {
            return timedCall('CloseSession', mapSessionIDPayload(sessionId), rpcContext);
        },
        getSnapshot: (sessionId, rpcContext = null) => {
            return timedCall('GetSnapshot', mapSessionIDPayload(sessionId), rpcContext);
        },
        resolveSession: (deviceId, clientId, rpcContext = null) => {
            return timedCall('ResolveSession', mapResolveSessionPayload(deviceId, clientId), rpcContext);
        },
        loadFile: (sessionId, file, rpcContext = null) => {
            return timedCall('LoadFile', mapLoadFilePayload(sessionId, file), rpcContext);
        },
        unloadFile: (sessionId, rpcContext = null) => {
            return timedCall('UnloadFile', mapSessionIDPayload(sessionId), rpcContext);
        },
        startJob: (sessionId, rpcContext = null) => {
            return timedCall('StartJob', mapSessionIDPayload(sessionId), rpcContext);
        },
        pauseJob: (sessionId, rpcContext = null) => {
            return timedCall('PauseJob', mapSessionIDPayload(sessionId), rpcContext);
        },
        resumeJob: (sessionId, rpcContext = null) => {
            return timedCall('ResumeJob', mapSessionIDPayload(sessionId), rpcContext);
        },
        stopJob: (sessionId, options = {}, rpcContext = null) => {
            return timedCall('StopJob', {
                session_id: String(sessionId),
                force: Boolean(options.force),
            }, rpcContext);
        },
        attachClient: (sessionId, clientId, rpcContext = null) => {
            return timedCall('AttachClient', {
                session_id: String(sessionId),
                client_id: String(clientId),
            }, rpcContext);
        },
        replayEvents: async (sessionId, clientId, onEvent, rpcContext = null) => {
            const startedAt = Date.now();
            let replayCount = 0;
            const log = getLog();
            const metadataValues = mapRPCContextMetadata(rpcContext);
            const metadata = metadataValues ? new grpc.Metadata() : null;
            if (metadata && metadataValues) {
                Object.entries(metadataValues).forEach(([key, value]) => metadata.set(key, value));
            }
            const stream = client.SubscribeEvents(createEventReplayPayload(sessionId, clientId), metadata || undefined);
            try {
                const events = await replaySubscribeEventsStream(stream, (event) => {
                    replayCount += 1;
                    onEvent?.(event);
                });
                log.info(`machine-core grpc SubscribeEvents completed in ${Date.now() - startedAt}ms with ${replayCount} replay events`);
                return events;
            } catch (error) {
                log.warn(`machine-core grpc SubscribeEvents failed after ${Date.now() - startedAt}ms with ${replayCount} replay events: ${error.message}`);
                throw error;
            }
        },
        detachClient: (sessionId, clientId, rpcContext = null) => {
            return timedCall('DetachClient', {
                session_id: String(sessionId),
                client_id: String(clientId),
            }, rpcContext);
        },
        sendCommand: (sessionId, command, rpcContext = null) => {
            return timedCall('SendCommand', mapSendCommandPayload(sessionId, command), rpcContext);
        },
        flashFirmware: (payload, rpcContext = null) => {
            return timedCall('FlashFirmware', mapFlashFirmwarePayload(payload), rpcContext);
        },
    };
};

const shouldUseGoMachineCore = () => {
    return GRPC_MODE.test(String(process.env.MACHINE_CORE_TRANSPORT || process.env.MACHINE_CORE_MODE || ''));
};

let activeAdapter = null;
let activeAdapterKey = null;

const resolveDefaultAdapter = () => {
    const adapterKey = shouldUseGoMachineCore()
        ? `go:${normalizeMachineCoreAddress(process.env.MACHINE_CORE_ADDR || MACHINE_CORE_ADDR_FALLBACK)}`
        : 'legacy';

    if (!activeAdapter || activeAdapterKey !== adapterKey) {
        activeAdapter = shouldUseGoMachineCore()
            ? createGoMachineCoreAdapter()
            : legacyAdapter;
        activeAdapterKey = adapterKey;
    }

    return activeAdapter;
};

const getAdapter = () => activeAdapter || resolveDefaultAdapter();

const setAdapter = (adapter = null) => {
    activeAdapter = adapter;
    activeAdapterKey = adapter ? 'manual' : null;
};

const start = (server, controller) => {
    return getAdapter().start(server, controller);
};

const stop = () => {
    return getAdapter().stop();
};

const load = (gcode) => {
    return getAdapter().load(gcode);
};

const unload = () => {
    return getAdapter().unload();
};

const listDevices = () => {
    return getAdapter().listDevices();
};

const openSession = (sessionConfig, rpcContext) => {
    return getAdapter().openSession(sessionConfig, rpcContext);
};

const closeSession = (sessionId, rpcContext) => {
    return getAdapter().closeSession(sessionId, rpcContext);
};

const getSnapshot = (sessionId, rpcContext) => {
    return getAdapter().getSnapshot(sessionId, rpcContext);
};

const resolveSession = (deviceId, clientId, rpcContext) => {
    return getAdapter().resolveSession(deviceId, clientId, rpcContext);
};

const loadFile = (sessionId, file, rpcContext) => {
    return getAdapter().loadFile(sessionId, file, rpcContext);
};

const unloadFile = (sessionId, rpcContext) => {
    return getAdapter().unloadFile(sessionId, rpcContext);
};

const startJob = (sessionId, rpcContext) => {
    return getAdapter().startJob(sessionId, rpcContext);
};

const pauseJob = (sessionId, rpcContext) => {
    return getAdapter().pauseJob(sessionId, rpcContext);
};

const resumeJob = (sessionId, rpcContext) => {
    return getAdapter().resumeJob(sessionId, rpcContext);
};

const stopJob = (sessionId, options, rpcContext) => {
    return getAdapter().stopJob(sessionId, options, rpcContext);
};

const attachClient = (sessionId, clientId, rpcContext) => {
    return getAdapter().attachClient(sessionId, clientId, rpcContext);
};

const replayEvents = (sessionId, clientId, onEvent, rpcContext) => {
    return getAdapter().replayEvents(sessionId, clientId, onEvent, rpcContext);
};

const detachClient = (sessionId, clientId, rpcContext) => {
    return getAdapter().detachClient(sessionId, clientId, rpcContext);
};

const sendCommand = (sessionId, command, rpcContext) => {
    return getAdapter().sendCommand(sessionId, command, rpcContext);
};

const flashFirmware = (payload, rpcContext) => {
    return getAdapter().flashFirmware(payload, rpcContext);
};

export default {
    start,
    stop,
    load,
    unload,
    listDevices,
    openSession,
    closeSession,
    getSnapshot,
    resolveSession,
    loadFile,
    unloadFile,
    startJob,
    pauseJob,
    resumeJob,
    stopJob,
    attachClient,
    replayEvents,
    detachClient,
    sendCommand,
    flashFirmware
};

export {
    setAdapter
};

export const __private__ = {
    toGoString,
    toGoInt,
    toGoBool,
    pickFirst,
    mapOpenSessionPayload,
    mapSessionIDPayload,
    mapResolveSessionPayload,
    mapMetadata,
    mapCommandPayload,
    mapSendCommandPayload,
    mapLoadFilePayload,
    mapFlashFirmwarePayload,
    unwrapListDevicesResponse,
    deviceDiscoveryKey,
    mergeGoDeviceWithLegacyMetadata,
    mergeGoDeviceListWithLegacyDiscovery,
    createEventReplayPayload,
    mapRPCContextMetadata,
    replaySubscribeEventsStream,
    normalizeMachineCoreAddress,
    shouldUseGoMachineCore,
    createGoMachineCoreAdapter,
};
