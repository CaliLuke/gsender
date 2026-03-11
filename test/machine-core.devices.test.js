/* eslint-env jest */

const {
    buildMachineCoreDeviceList,
    partitionDevicesForLegacySocket,
} = require('../src/server/services/machine-core/devices');

describe('machine-core device discovery helpers', () => {
    test('buildMachineCoreDeviceList normalizes serial and network devices', () => {
        const devices = buildMachineCoreDeviceList({
            serialPorts: [
                {
                    path: '/dev/ttyUSB0',
                    manufacturer: 'Sienci',
                    vendorId: '0483',
                    productId: '5740',
                    serialNumber: 'ABC123',
                },
                {
                    path: '/dev/ttyS1',
                    manufacturer: 'Unknown',
                },
            ],
            controllers: {
                '/dev/ttyUSB0': { isOpen: () => true },
                '/dev/ttyS1': { isOpen: () => false },
                '192.168.1.5': true,
            },
            networkDevices: [
                { ip: '192.168.1.5' },
            ],
        });

        expect(devices).toEqual([
            {
                id: '/dev/ttyUSB0',
                kind: 'serial',
                path: '/dev/ttyUSB0',
                manufacturer: 'Sienci',
                vendor_id: '0483',
                product_id: '5740',
                serial_number: 'ABC123',
                display_name: '/dev/ttyUSB0',
                in_use: true,
                capabilities: [],
            },
            {
                id: '/dev/ttyS1',
                kind: 'serial',
                path: '/dev/ttyS1',
                manufacturer: 'Unknown',
                vendor_id: undefined,
                product_id: undefined,
                serial_number: undefined,
                display_name: '/dev/ttyS1',
                in_use: false,
                capabilities: [],
            },
            {
                id: '192.168.1.5',
                kind: 'network',
                network_address: '192.168.1.5',
                manufacturer: undefined,
                display_name: '192.168.1.5',
                in_use: true,
                capabilities: [],
            },
        ]);
    });

    test('partitionDevicesForLegacySocket preserves legacy payload shape', () => {
        const result = partitionDevicesForLegacySocket([
            {
                kind: 'serial',
                path: '/dev/ttyUSB0',
                manufacturer: 'Sienci',
                vendor_id: '0483',
                product_id: '5740',
                in_use: true,
            },
            {
                kind: 'serial',
                path: '/dev/ttyS1',
                manufacturer: 'Unknown',
                in_use: false,
            },
            {
                kind: 'network',
                network_address: '192.168.1.5',
                manufacturer: 'LAN',
                in_use: true,
            },
        ]);

        expect(result).toEqual({
            recognizedPorts: [
                { port: '/dev/ttyUSB0', manufacturer: 'Sienci', inuse: true },
            ],
            unrecognizedPorts: [
                { port: '/dev/ttyS1', manufacturer: 'Unknown', inuse: false },
            ],
            networkPorts: [
                { port: '192.168.1.5', manufacturer: 'LAN', inuse: true },
            ],
        });
    });
});
