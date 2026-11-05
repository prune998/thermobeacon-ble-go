//go:build !linux

package bluescan

import (
	"tinygo.org/x/bluetooth"
)

// scan delegates to the bluetooth adapter's own scan on platforms where
// TinyGo Bluetooth's implementation has no D-Bus bottleneck (CoreBluetooth on
// macOS coalesces advertisements in the stack, WinRT delivers events
// directly). The accept hook is applied by the callback instead.
func scan(adapter *bluetooth.Adapter, accept func(addr string) bool, callback func(*bluetooth.Adapter, bluetooth.ScanResult)) error {
	return adapter.Scan(callback)
}

func stopScan(adapter *bluetooth.Adapter) error {
	return adapter.StopScan()
}
