// Package bluescan starts the BLE scan that feeds the bridge and the TUI.
//
// On Linux it does not use tinygo.org/x/bluetooth's Adapter.Scan: that
// implementation registers a D-Bus match rule for EVERY PropertiesChanged
// signal on the system bus (no member, no arg0 filter) and delivers them
// through an unbuffered channel, which makes godbus spawn one goroutine per
// signal whenever the consumer is busy. On a Venus OS GX device the system
// bus carries constant property updates from dozens of Victron services, and
// every nearby BLE device emits a Device1 PropertiesChanged per
// advertisement; together that traffic pegs a weak armv7 core (measured at
// ~50% CPU). The Linux implementation in this package keeps the behaviour of
// upstream's scan loop but:
//
//   - filters PropertiesChanged at the D-Bus daemon level to the two
//     interfaces the scan actually reads (org.bluez.Device1 and
//     org.bluez.Adapter1), so unrelated service chatter never reaches the
//     process;
//   - uses a buffered signal channel, so bursts are queued instead of
//     spawning a goroutine per signal;
//   - drops advertisements from unfollowed devices in the scan loop, before
//     the ScanResult is built (the device filter as an accept hook).
//
// On every other platform it delegates to the bluetooth adapter directly.
package bluescan

import (
	"tinygo.org/x/bluetooth"
)

// Scan starts a BLE scan and calls callback for every discovered device,
// blocking until StopScan is called or an error occurs. adapter must be the
// enabled bluetooth.DefaultAdapter.
//
// accept, when non-nil, is consulted with the device address (as BlueZ
// reports it, e.g. "A4:C1:38:30:88:DE") before a ScanResult is built for a
// discovered device; failing addresses are dropped in the scan loop, before
// any payload construction or callback. It should be the same predicate the
// callback applies itself (bridge.Config.AcceptsDevice), hoisted to the
// earliest possible point: in dense RF environments every device in range
// emits a property change per advertisement, and most of them are not ours.
// Pass nil to see every device (dump mode, the CLI).
func Scan(adapter *bluetooth.Adapter, accept func(addr string) bool, callback func(*bluetooth.Adapter, bluetooth.ScanResult)) error {
	return scan(adapter, accept, callback)
}

// StopScan stops the scan started by Scan.
func StopScan(adapter *bluetooth.Adapter) error {
	return stopScan(adapter)
}
