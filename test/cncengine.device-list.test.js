/* eslint-env jest */

jest.mock('ensure-array', () => (value) => value);
jest.mock('lodash/noop', () => jest.fn());
jest.mock('serialport', () => ({
    SerialPort: {
        list: jest.fn(async () => []),
    },
}));
jest.mock('socket.io', () => jest.fn());
jest.mock('electron', () => ({
    app: null,
}));
jest.mock('../src/server/lib/EventTrigger', () => {
    return jest.fn().mockImplementation(() => ({
        trigger: jest.fn(),
    }));
});

const mockLog = {
    info: jest.fn(),
    warn: jest.fn(),
    debug: jest.fn(),
    error: jest.fn(),
};

jest.mock('../src/server/lib/logger', () => ({
    __esModule: true,
    default: jest.fn(() => mockLog),
}));
const mockStore = {
    get: jest.fn(() => ({})),
    set: jest.fn(),
    unset: jest.fn(),
};

jest.mock('../src/server/store', () => ({
    __esModule: true,
    default: mockStore,
}));
jest.mock('../src/server/services/configstore', () => ({
    __esModule: true,
    default: {
        get: jest.fn(() => []),
        on: jest.fn(),
        removeListener: jest.fn(),
    },
}));
jest.mock('../src/server/services/taskrunner', () => ({
    __esModule: true,
    default: {
        on: jest.fn(),
        removeListener: jest.fn(),
    },
}));
jest.mock('../src/server/services/machine-core/devices', () => ({
    buildMachineCoreDeviceList: jest.fn(),
    partitionDevicesForLegacySocket: jest.fn((devices) => ({
        recognizedPorts: devices,
        unrecognizedPorts: [],
        networkPorts: [],
    })),
}));
jest.mock('../src/server/services/machine-core/sidecar', () => ({
    shouldUseGoMachineCorePath: jest.fn(),
    extractSessionID: jest.fn((result) => result?.session?.id || null),
    relayMachineSessionSnapshot: jest.fn(),
    relayMachineSessionEvent: jest.fn(),
}));
jest.mock('../src/server/services/machine-core', () => ({
    __esModule: true,
    default: {
        listDevices: jest.fn(),
        openSession: jest.fn(),
        closeSession: jest.fn(),
        sendCommand: jest.fn(),
        loadFile: jest.fn(),
        unloadFile: jest.fn(),
        startJob: jest.fn(),
        pauseJob: jest.fn(),
        resumeJob: jest.fn(),
        stopJob: jest.fn(),
    },
}));
jest.mock('../src/server/lib/Firmware/Flashing/firmwareflashing', () => jest.fn());
jest.mock('../src/server/controllers', () => ({
    GrblController: jest.fn(),
    GrblHalController: jest.fn(),
}));
jest.mock('../src/server/controllers/Grbl/constants', () => ({
    GRBL: 'Grbl',
}));
jest.mock('../src/server/controllers/Grblhal/constants', () => ({
    GRBLHAL: 'grblHAL',
}));
jest.mock('../src/server/access-control', () => ({
    authorizeIPAddress: jest.fn(async () => {}),
}));
jest.mock('../src/server/lib/Firmware/Flashing/DFUFlasher', () => jest.fn());
jest.mock('../src/server/lib/delay', () => jest.fn());
jest.mock('../src/server/lib/Connection', () => jest.fn());

const CNCEngine = require('../src/server/services/cncengine/CNCEngine').default;
const machineCore = require('../src/server/services/machine-core').default;
const { shouldUseGoMachineCorePath } = require('../src/server/services/machine-core/sidecar');

describe('CNCEngine device list authority', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockStore.get.mockReturnValue({});
    });

    test('uses legacy device discovery when Go device list path is disabled', async () => {
        shouldUseGoMachineCorePath.mockReturnValue(false);

        const engine = new CNCEngine();
        engine.listDevices = jest.fn().mockResolvedValue([{ id: '/dev/ttyUSB0' }]);

        await expect(engine.listSocketDevices()).resolves.toEqual([{ id: '/dev/ttyUSB0' }]);
        expect(engine.listDevices).toHaveBeenCalledTimes(1);
        expect(machineCore.listDevices).not.toHaveBeenCalled();
    });

    test('uses machine-core device discovery when Go device list path is enabled', async () => {
        shouldUseGoMachineCorePath.mockReturnValue(true);
        machineCore.listDevices.mockResolvedValue([{ id: 'go:/dev/ttyUSB0' }]);

        const engine = new CNCEngine();
        engine.listDevices = jest.fn().mockResolvedValue([{ id: '/dev/ttyUSB0' }]);

        await expect(engine.listSocketDevices()).resolves.toEqual([{ id: 'go:/dev/ttyUSB0' }]);
        expect(machineCore.listDevices).toHaveBeenCalledTimes(1);
        expect(engine.listDevices).not.toHaveBeenCalled();
    });

    test('falls back to legacy discovery if machine-core device listing fails', async () => {
        shouldUseGoMachineCorePath.mockReturnValue(true);
        machineCore.listDevices.mockRejectedValue(new Error('grpc unavailable'));

        const engine = new CNCEngine();
        engine.listDevices = jest.fn().mockResolvedValue([{ id: '/dev/ttyUSB0' }]);

        await expect(engine.listSocketDevices()).resolves.toEqual([{ id: '/dev/ttyUSB0' }]);
        expect(machineCore.listDevices).toHaveBeenCalledTimes(1);
        expect(engine.listDevices).toHaveBeenCalledTimes(1);
        expect(mockLog.warn).toHaveBeenCalled();
    });

    test('uses machine-core as the authority for file loads before mutating local state', async () => {
        shouldUseGoMachineCorePath.mockImplementation((pathName) => pathName === 'file_shadow');
        machineCore.loadFile.mockResolvedValue({ ok: true });

        const controller = {
            loadFile: jest.fn(),
        };
        mockStore.get.mockImplementation((key) => {
            if (key === 'controllers["/dev/ttyUSB0"]') {
                return controller;
            }
            return {};
        });

        const engine = new CNCEngine();
        engine.machineCoreSessions.set('/dev/ttyUSB0', 'session-1');
        engine.emit = jest.fn();

        await engine.loadWithMachineCore({
            port: '/dev/ttyUSB0',
            gcode: 'G1 X1',
            name: 'part.nc',
            size: 5,
            visualizer: 'primary',
        });

        expect(machineCore.loadFile).toHaveBeenCalledWith('session-1', {
            name: 'part.nc',
            gcode: 'G1 X1',
            metadata: {
                size: 5,
                visualizer: 'primary',
                port: '/dev/ttyUSB0',
            },
        }, expect.objectContaining({
            action: 'file_load',
            port: '/dev/ttyUSB0',
            session_id: 'session-1',
            origin: 'node-cncengine',
        }));
        expect(engine.gcode).toBe('G1 X1');
        expect(engine.meta).toEqual({
            name: 'part.nc',
            size: 5,
            visualizer: 'primary',
        });
        expect(controller.loadFile).toHaveBeenCalledWith('G1 X1', {
            name: 'part.nc',
            size: 5,
            visualizer: 'primary',
        });
        expect(engine.emit).toHaveBeenCalledWith('file:load', 'G1 X1', 5, 'part.nc', 'primary');
    });

    test('does not mutate local file state when machine-core rejects the authoritative load', async () => {
        shouldUseGoMachineCorePath.mockImplementation((pathName) => pathName === 'file_shadow');
        machineCore.loadFile.mockRejectedValue(new Error('invalid state'));

        const controller = {
            loadFile: jest.fn(),
        };
        mockStore.get.mockImplementation((key) => {
            if (key === 'controllers["/dev/ttyUSB0"]') {
                return controller;
            }
            return {};
        });

        const engine = new CNCEngine();
        engine.machineCoreSessions.set('/dev/ttyUSB0', 'session-1');
        engine.emit = jest.fn();

        await expect(engine.loadWithMachineCore({
            port: '/dev/ttyUSB0',
            gcode: 'G1 X1',
            name: 'part.nc',
            size: 5,
            visualizer: 'primary',
        })).rejects.toThrow('invalid state');

        expect(engine.gcode).toBe(null);
        expect(engine.meta).toBe(null);
        expect(controller.loadFile).not.toHaveBeenCalled();
        expect(engine.emit).not.toHaveBeenCalled();
    });

    test('uses machine-core as the authority for job transitions before forwarding to the controller', async () => {
        shouldUseGoMachineCorePath.mockImplementation((pathName) => pathName === 'job_shadow');
        machineCore.startJob.mockResolvedValue({ accepted: true });

        const controller = {
            command: jest.fn(),
        };

        const engine = new CNCEngine();
        engine.machineCoreSessions.set('/dev/ttyUSB0', 'session-1');

        await expect(engine.dispatchControllerCommand('/dev/ttyUSB0', 'feeder:start', [], controller)).resolves.toBe(true);

        expect(machineCore.startJob).toHaveBeenCalledWith('session-1', expect.objectContaining({
            action: 'controller_command',
            port: '/dev/ttyUSB0',
            session_id: 'session-1',
            command_type: 'feeder:start',
        }));
        expect(controller.command).toHaveBeenCalledWith('feeder:start');
    });

    test('does not forward job transitions when machine-core rejects them', async () => {
        shouldUseGoMachineCorePath.mockImplementation((pathName) => pathName === 'job_shadow');
        machineCore.pauseJob.mockRejectedValue(new Error('cannot pause idle job'));

        const controller = {
            command: jest.fn(),
        };

        const engine = new CNCEngine();
        engine.machineCoreSessions.set('/dev/ttyUSB0', 'session-1');

        await expect(engine.dispatchControllerCommand('/dev/ttyUSB0', 'gcode:pause', [], controller)).rejects.toThrow('cannot pause idle job');

        expect(machineCore.pauseJob).toHaveBeenCalledWith('session-1', expect.objectContaining({
            action: 'controller_command',
            port: '/dev/ttyUSB0',
            session_id: 'session-1',
            command_type: 'gcode:pause',
        }));
        expect(controller.command).not.toHaveBeenCalled();
    });

    test('reserves Go session ownership during session open', async () => {
        shouldUseGoMachineCorePath.mockImplementation((pathName) => pathName === 'session_open');
        machineCore.openSession.mockResolvedValue({
            session: {
                id: 'session-1',
            },
        });

        const engine = new CNCEngine();
        await expect(engine.syncMachineCoreSessionOpen('/dev/ttyUSB0', { baudrate: 115200 }, { id: 'socket-1' })).resolves.toBe('session-1');

        expect(machineCore.openSession).toHaveBeenCalledWith({
            device_id: '/dev/ttyUSB0',
            baud_rate: 115200,
            client_id: 'socket-1',
        }, expect.objectContaining({
            action: 'session_open',
            port: '/dev/ttyUSB0',
            socket_id: 'socket-1',
            origin: 'node-cncengine',
        }));
        expect(engine.machineCoreSessions.get('/dev/ttyUSB0')).toBe('session-1');
    });

    test('mirrors live controller telemetry into machine-core runtime status reports', async () => {
        machineCore.sendCommand.mockResolvedValue({ accepted: true });

        const engine = new CNCEngine();
        engine.machineCoreSessions.set('/dev/ttyUSB0', 'session-1');

        const controller = {
            emit: jest.fn(),
        };
        engine.installMachineCoreTelemetryBridge(controller, '/dev/ttyUSB0');

        controller.emit('controller:state', 'grblHAL', {
            status: {
                activeState: 'Run',
                subState: 2,
            },
        });
        controller.emit('sender:status', {
            total: 100,
            sent: 25,
        });
        controller.emit('error', {
            type: 'ALARM',
            code: '2',
            description: 'Door open',
        });

        await Promise.resolve();
        await Promise.resolve();

        expect(machineCore.sendCommand).toHaveBeenCalledWith('session-1', {
            type: 'status_report',
            metadata: {
                controller_type: 'grblHAL',
                controller_state: JSON.stringify({
                    status: {
                        activeState: 'Run',
                        subState: 2,
                    },
                }),
            },
        }, expect.objectContaining({
            action: 'runtime_telemetry',
            port: '/dev/ttyUSB0',
            session_id: 'session-1',
            controller_event: 'controller:state',
            command_type: 'status_report',
        }));
        expect(machineCore.sendCommand).toHaveBeenCalledWith('session-1', {
            type: 'status_report',
            metadata: {
                sender_status: JSON.stringify({
                    total: 100,
                    sent: 25,
                }),
            },
        }, expect.objectContaining({
            action: 'runtime_telemetry',
            controller_event: 'sender:status',
        }));
        expect(machineCore.sendCommand).toHaveBeenCalledWith('session-1', {
            type: 'status_report',
            metadata: {
                error_code: '2',
                error_message: 'Door open',
                error_is_alarm: 'true',
                has_alarm: 'true',
            },
        }, expect.objectContaining({
            action: 'runtime_telemetry',
            controller_event: 'error',
        }));
    });
});
