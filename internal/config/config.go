// Package config holds the runtime options shared by all run modes
// (forwarding, dump, CLI) and the device filter.
package config

import (
	"strings"
	"sync"
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
	PProfAddr   string // serve Go pprof profiles on this address ("" = off)

	// deviceMAC caches Device in normalized MAC form ("" when Device is not
	// a MAC address). Computed at most once: AcceptsDevice runs for every
	// advertisement from every device in range and must not re-derive the
	// filter (or allocate an error) per advertisement.
	deviceMAC     string
	deviceMACOnce sync.Once
}

// AcceptsDevice reports whether an advertisement from the given address
// (as returned by ScanResult.Address.String()) should be decoded.
//
// This runs on the scan callback for every advertisement in range, so the
// reject path (the overwhelmingly common case with a single-device filter)
// must stay allocation-free.
func (c *Config) AcceptsDevice(addr string) bool {
	if c.Device == "" || c.Device == "all" || c.Device == "*" {
		return true
	}
	if strings.EqualFold(addr, c.Device) {
		return true
	}
	// Compare as MAC addresses so "db5c8da00f21", "DB:5C:8D:A0:0F:21" and
	// "db-5c-8d-a0-0f-21" all match the same sensor. The filter is
	// normalized once; a non-MAC filter (e.g. a macOS UUID) is fully covered
	// by the EqualFold above, so there is nothing left to try.
	c.deviceMACOnce.Do(func() {
		c.deviceMAC, _ = sensor.NormalizeMAC(c.Device)
	})
	if c.deviceMAC == "" {
		return false
	}
	n, err := sensor.NormalizeMAC(addr)
	return err == nil && n == c.deviceMAC
}

// DescribeDevice renders the device filter for log lines.
func (c *Config) DescribeDevice() string {
	if c.Device == "" || c.Device == "all" || c.Device == "*" {
		return "all ThermoBeacon, Govee and BTHome devices"
	}
	return "device " + c.Device
}
