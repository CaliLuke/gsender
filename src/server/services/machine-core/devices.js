const VALID_PRODUCT_IDS = ['0483', '6015', '6001', '606D', '003D', '0042', '0043', '2341', '7523', 'EA60', '2303', '2145', '0AD8', '08D8', '5740', '0FA7'];
const VALID_VENDOR_IDS = ['16C0', '1D50', '0403', '2341', '0042', '1A86', '10C4', '067B', '03EB', '16D0', '0483'];

const caseInsensitiveEquals = (left, right) => {
    const normalizedLeft = left ? String(left).toUpperCase() : '';
    const normalizedRight = right ? String(right).toUpperCase() : '';
    return normalizedLeft === normalizedRight;
};

const caseInsensitiveIncludes = (values, candidate) => {
    return values.some((value) => caseInsensitiveEquals(value, candidate));
};

const isRecognizedSerialDevice = (device = {}) => {
    if (!device.vendor_id || !device.product_id) {
        return false;
    }

    return (
        caseInsensitiveIncludes(VALID_PRODUCT_IDS, device.product_id) &&
        caseInsensitiveIncludes(VALID_VENDOR_IDS, device.vendor_id)
    );
};

const buildDeviceFromSerialPort = (port = {}, inUse = false) => {
    const path = port.path || '';

    return {
        id: path,
        kind: 'serial',
        path,
        manufacturer: port.manufacturer,
        vendor_id: port.vendorId,
        product_id: port.productId,
        serial_number: port.serialNumber,
        display_name: path,
        in_use: Boolean(inUse),
        capabilities: [],
    };
};

const buildDeviceFromNetworkPort = (port = {}, inUse = false) => {
    const networkAddress = port.ip || port.address || '';

    return {
        id: networkAddress,
        kind: 'network',
        network_address: networkAddress,
        manufacturer: port.manufacturer,
        display_name: networkAddress,
        in_use: Boolean(inUse),
        capabilities: [],
    };
};

const buildMachineCoreDeviceList = ({ serialPorts = [], controllers = {}, networkDevices = [] } = {}) => {
    const portsInUse = Object.keys(controllers).filter((port) => {
        const controller = controllers[port];
        return controller && typeof controller.isOpen === 'function' && controller.isOpen();
    });

    const serialDevices = serialPorts.map((port) => (
        buildDeviceFromSerialPort(port, portsInUse.includes(port.path))
    ));
    const networkPortDevices = networkDevices.map((port) => (
        buildDeviceFromNetworkPort(port, Boolean(controllers[port.ip]))
    ));

    return [...serialDevices, ...networkPortDevices];
};

const toLegacyPortInfo = (device = {}) => ({
    port: device.path || device.network_address,
    manufacturer: device.manufacturer,
    inuse: Boolean(device.in_use),
});

const partitionDevicesForLegacySocket = (devices = []) => {
    const recognizedPorts = [];
    const unrecognizedPorts = [];
    const networkPorts = [];

    devices.forEach((device) => {
        if (device.kind === 'network') {
            networkPorts.push(toLegacyPortInfo(device));
            return;
        }

        if (isRecognizedSerialDevice(device)) {
            recognizedPorts.push(toLegacyPortInfo(device));
            return;
        }

        unrecognizedPorts.push(toLegacyPortInfo(device));
    });

    return {
        recognizedPorts,
        unrecognizedPorts,
        networkPorts,
    };
};

export {
    buildMachineCoreDeviceList,
    partitionDevicesForLegacySocket,
    isRecognizedSerialDevice,
};
