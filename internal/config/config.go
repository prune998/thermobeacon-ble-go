// Package config holds the runtime options shared by all run modes
// (forwarding, dump, CLI) and the device filter.
package config

import (
	"strings"
	"time"

	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
)

// DefaultDeviceID is the CoreBluetooth peripheral UUID of the sensor to
// follow. On macOS devices are identified by such an UUID instead of a MAC
// address. Use "-device all" to decode every supported device in range, or a
// MAC address when running on Linux.
const DefaultDeviceID = "10243784-1324-5413-b00c-9675981a8daa"

// Config holds all runtime options.
type Config struct {
	// Device selects the sensor to follow: a CoreBluetooth UUID (macOS), a
	// MAC address (Linux/Windows) or "all" for every supported device in
	// range.
	Device string

	UDPHost     string
	UDPPort     int
	NoForward   bool
	CLI         bool
	Capture     string
	Refresh     time.Duration // TUI display refresh interval (--cli)
	MinInterval time.Duration
	IdleWarning time.Duration
	Debug       bool
}

// AcceptsDevice reports whether an advertisement from the given address
// (as returned by ScanResult.Address.String()) should be decoded.
func (c *Config) AcceptsDevice(addr string) bool {
	if c.Device == "" || c.Device == "all" || c.Device == "*" {
		return true
	}
	if strings.EqualFold(addr, c.Device) {
		return true
	}
	// Compare as MAC addresses so "db5c8da00f21", "DB:5C:8D:A0:0F:21" and
	// "db-5c-8d-a0-0f-21" all match the same sensor.
	if n, err := sensor.NormalizeMAC(addr); err == nil {
		if d, err := sensor.NormalizeMAC(c.Device); err == nil && n == d {
			return true
		}
	}
	return false
}

// DescribeDevice renders the device filter for log lines.
func (c *Config) DescribeDevice() string {
	if c.Device == "" || c.Device == "all" || c.Device == "*" {
		return "all ThermoBeacon, Govee and BTHome devices"
	}
	return "device " + c.Device
}
