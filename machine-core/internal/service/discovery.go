package service

import (
	"os"
	"strings"

	"go.bug.st/serial/enumerator"

	gen "github.com/Sienci-Labs/gsender/machine-core/gen/machine"
)

type deviceDiscovery interface {
	ListDevices() ([]*gen.Device, error)
}

type hostDeviceDiscovery struct{}

func (d hostDeviceDiscovery) ListDevices() ([]*gen.Device, error) {
	devices := make([]*gen.Device, 0)

	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	for _, port := range ports {
		if port == nil || port.Name == "" {
			continue
		}
		device := &gen.Device{
			ID:           port.Name,
			Kind:         "serial",
			Path:         ptrString(port.Name),
			VendorID:     stringPointerOrNil(port.VID),
			ProductID:    stringPointerOrNil(port.PID),
			SerialNumber: stringPointerOrNil(port.SerialNumber),
			DisplayName:  port.Name,
			InUse:        false,
			Capabilities: []string{"open_session"},
		}
		devices = append(devices, device)
	}

	for _, address := range parseNetworkDeviceEnv(os.Getenv("MACHINE_CORE_NETWORK_DEVICES")) {
		devices = append(devices, &gen.Device{
			ID:             "tcp://" + address,
			Kind:           "network",
			NetworkAddress: ptrString(address),
			DisplayName:    address,
			InUse:          false,
			Capabilities:   []string{"open_session"},
		})
	}

	if len(devices) == 0 {
		return cloneDevices(defaultKnownDevices), nil
	}

	return devices, nil
}

func parseNetworkDeviceEnv(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	addresses := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		addresses = append(addresses, trimmed)
	}
	return addresses
}

func stringPointerOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return ptrString(value)
}
