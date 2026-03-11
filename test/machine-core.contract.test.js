/* eslint-env jest */

jest.mock('../src/server/services/cncengine', () => ({
    __esModule: true,
    default: {
        start: jest.fn(),
        stop: jest.fn(),
        load: jest.fn(),
        unload: jest.fn(),
    },
}));

const loadModule = () => require('../src/server/services/machine-core');

describe('machine-core contract helpers', () => {
    beforeEach(() => {
        jest.resetModules();
        delete process.env.MACHINE_CORE_TRANSPORT;
        delete process.env.MACHINE_CORE_MODE;
    });

    test('maps openSession payload using compatibility field aliases', () => {
        const { __private__ } = loadModule();

        expect(__private__.mapOpenSessionPayload({
            path: '/dev/ttyUSB0',
            baudRate: '115200',
            rtsCTS: 1,
            networkPort: '23',
            defaultFirmware: 'grblHAL',
            clientId: 42,
        })).toEqual({
            device_id: '/dev/ttyUSB0',
            baud_rate: 115200,
            rtscts: true,
            network_port: 23,
            default_firmware: 'grblHAL',
            client_id: '42',
        });
    });

    test('drops undefined and invalid values from openSession payload', () => {
        const { __private__ } = loadModule();

        expect(__private__.mapOpenSessionPayload({
            deviceId: 'sim://loopback',
            baudRate: 'fast',
            rtscts: null,
            defaultFirmware: '',
        })).toEqual({
            device_id: 'sim://loopback',
        });
    });

    test('maps sendCommand payload to Goa shape', () => {
        const { __private__ } = loadModule();

        expect(__private__.mapSendCommandPayload('session-9', {
            type: 'gcode_line',
            rawLine: 'G1 X10',
            axisMove: { x: 10, y: 0 },
            feedOverride: '125',
            rapidOverride: 50,
            metadata: {
                source: 'ui',
                retries: 2,
                ignored: null,
            },
        })).toEqual({
            session_id: 'session-9',
            command: {
                type: 'gcode_line',
                raw_line: 'G1 X10',
                axis_move: { x: 10, y: 0 },
                feed_override: 125,
                rapid_override: 50,
                metadata: {
                    source: 'ui',
                    retries: '2',
                },
            },
        });
    });

    test('maps loadFile payload to Goa shape', () => {
        const { __private__ } = loadModule();

        expect(__private__.mapLoadFilePayload('session-9', {
            name: 'part.nc',
            gcode: 'G1 X10',
            contentType: 'text/x-gcode',
            metadata: {
                visualizer: 'primary',
                size: 42,
                ignored: null,
            },
        })).toEqual({
            session_id: 'session-9',
            name: 'part.nc',
            content: 'G1 X10',
            content_type: 'text/x-gcode',
            metadata: {
                visualizer: 'primary',
                size: '42',
            },
        });
    });

    test('normalizes machine-core address for hostless bind syntax', () => {
        const { __private__ } = loadModule();

        expect(__private__.normalizeMachineCoreAddress(':8081')).toBe('127.0.0.1:8081');
        expect(__private__.normalizeMachineCoreAddress('')).toBe('127.0.0.1:8081');
        expect(__private__.normalizeMachineCoreAddress('10.0.0.4:9000')).toBe('10.0.0.4:9000');
    });

    test('enables Go transport only for explicit grpc-style modes', () => {
        process.env.MACHINE_CORE_TRANSPORT = ' grpc ';
        let moduleRef = loadModule();
        expect(moduleRef.__private__.shouldUseGoMachineCore()).toBe(true);

        jest.resetModules();
        delete process.env.MACHINE_CORE_TRANSPORT;
        process.env.MACHINE_CORE_MODE = 'legacy';
        moduleRef = loadModule();
        expect(moduleRef.__private__.shouldUseGoMachineCore()).toBe(false);
    });

    test('unwraps grpc listDevices response into the Goa device array', () => {
        const { __private__ } = loadModule();

        expect(__private__.unwrapListDevicesResponse({
            field: [
                { id: 'sim://loopback', kind: 'serial', path: 'sim://loopback', in_use: false },
                { id: 'tcp://sim.local:23', kind: 'network', network_address: 'sim.local:23', in_use: true },
            ],
        })).toEqual([
            { id: 'sim://loopback', kind: 'serial', path: 'sim://loopback', in_use: false },
            { id: 'tcp://sim.local:23', kind: 'network', network_address: 'sim.local:23', in_use: true },
        ]);
        expect(__private__.unwrapListDevicesResponse(null)).toEqual([]);
    });

    test('merges legacy device metadata onto Go discovery results', () => {
        const { __private__ } = loadModule();

        expect(__private__.mergeGoDeviceListWithLegacyDiscovery([
            {
                id: '/dev/ttyUSB0',
                kind: 'serial',
                path: '/dev/ttyUSB0',
                manufacturer: undefined,
                vendor_id: '0483',
                product_id: '5740',
                serial_number: 'ABC123',
                in_use: false,
            },
            {
                id: 'tcp://machine.local:23',
                kind: 'network',
                network_address: 'machine.local:23',
                manufacturer: undefined,
                in_use: false,
            },
        ], [
            {
                id: '/dev/ttyUSB0',
                kind: 'serial',
                path: '/dev/ttyUSB0',
                manufacturer: 'Sienci',
                vendor_id: '0483',
                product_id: '5740',
                serial_number: 'ABC123',
                in_use: true,
            },
            {
                id: 'machine.local:23',
                kind: 'network',
                network_address: 'machine.local:23',
                manufacturer: 'LAN',
                in_use: true,
            },
        ])).toEqual([
            {
                id: '/dev/ttyUSB0',
                kind: 'serial',
                path: '/dev/ttyUSB0',
                manufacturer: 'Sienci',
                vendor_id: '0483',
                product_id: '5740',
                serial_number: 'ABC123',
                in_use: true,
            },
            {
                id: 'tcp://machine.local:23',
                kind: 'network',
                network_address: 'machine.local:23',
                manufacturer: 'LAN',
                in_use: true,
            },
        ]);
    });

    test('builds replay payload from session and client ids', () => {
        const { __private__ } = loadModule();

        expect(__private__.createEventReplayPayload('session-7', 'ui-2')).toEqual({
            session_id: 'session-7',
            client_id: 'ui-2',
        });
    });

    test('builds resolveSession payload from device and client ids', () => {
        const { __private__ } = loadModule();

        expect(__private__.mapResolveSessionPayload('/dev/ttyUSB0', 'ui-9')).toEqual({
            device_id: '/dev/ttyUSB0',
            client_id: 'ui-9',
        });
    });

    test('replays subscribe stream events into callback and result list', async () => {
        const { EventEmitter } = require('events');
        const { __private__ } = loadModule();
        const stream = new EventEmitter();
        const seen = [];

        const replayPromise = __private__.replaySubscribeEventsStream(stream, (event) => {
            seen.push(event.type);
        });

        stream.emit('data', { type: 'session_opened', sequence: 1 });
        stream.emit('data', { type: 'job_started', sequence: 2 });
        stream.emit('end');

        await expect(replayPromise).resolves.toEqual([
            { type: 'session_opened', sequence: 1 },
            { type: 'job_started', sequence: 2 },
        ]);
        expect(seen).toEqual(['session_opened', 'job_started']);
    });
});
