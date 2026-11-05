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
// address. Use "-device all" to decode every supported device in range, a
// MAC address when running on Linux, or a comma-separated list to follow
// several sensors at once ("-device aa:bb:cc:dd:ee:01,aa:bb:cc:dd:ee:02").
const DefaultDeviceID = "10243784-1324-5413-b00c-9675981a8daa"

// Config holds all runtime options.
type Config struct {
	// Device selects the sensor(s) to follow: a comma-separated list of
	// CoreBluetooth UUIDs (macOS) and/or MAC addresses (Linux/Windows), or
	// "all" for every supported device in range.
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

	// deviceFilter caches Device parsed into the lookup structures the
	// per-advertisement hot path needs. Computed at most once: AcceptsDevice
	// runs for every advertisement from every device in range and must not
	// re-split the list (or allocate an error) per advertisement.
	deviceFilter deviceFilter
	deviceOnce   sync.Once
}

// deviceFilter is the parsed form of Config.Device.
type deviceFilter struct {
	// acceptAll is set when Device is empty, "all" or "*" (alone or among
	// the entries): every device is accepted.
	acceptAll bool
	// devices holds the raw, space-trimmed entries for case-insensitive
	// matching (CoreBluetooth UUIDs and MAC addresses in any notation).
	devices []string
	// macs holds the normalized MAC form of the entries that parse as MAC
	// addresses, so "db5c8da00f21", "DB:5C:8D:A0:0F:21" and
	// "db-5c-8d-a0-0f-21" all match the same sensor.
	macs map[string]struct{}
}

// parseDeviceFilter splits a comma-separated device list into a deviceFilter.
//
// Entries are space-trimmed; empty entries ("aa:bb:cc:dd:ee:01,,bb:bb..") are
// dropped, and a list that is empty after trimming means "all". An "all" or
// "*" entry short-circuits to accept-all. Entries that are neither "all" nor
// a valid MAC address stay in devices only: like the single-device filter
// before, a garbage entry then matches nothing but itself.
func parseDeviceFilter(device string) deviceFilter {
	var f deviceFilter
	for _, d := range strings.Split(device, ",") {
		d = strings.TrimSpace(d)
		switch {
		case d == "":
			continue
		case d == "all" || d == "*":
			return deviceFilter{acceptAll: true}
		}
		f.devices = append(f.devices, d)
		if m, err := sensor.NormalizeMAC(d); err == nil {
			if f.macs == nil {
				f.macs = make(map[string]struct{}, len(f.devices))
			}
			f.macs[m] = struct{}{}
		}
	}
	if len(f.devices) == 0 {
		return deviceFilter{acceptAll: true}
	}
	return f
}

// parsedDevice returns Device parsed once and cached on c.
func (c *Config) parsedDevice() deviceFilter {
	c.deviceOnce.Do(func() { c.deviceFilter = parseDeviceFilter(c.Device) })
	return c.deviceFilter
}

// AcceptsDevice reports whether an advertisement from the given address
// (as returned by ScanResult.Address.String()) should be decoded.
//
// This runs on the scan callback for every advertisement in range, so the
// reject path must stay allocation-free whenever possible: entries are
// matched case-insensitively first (no allocation), and the incoming address
// is normalized only when the filter holds at least one MAC entry.
func (c *Config) AcceptsDevice(addr string) bool {
	f := c.parsedDevice()
	if f.acceptAll {
		return true
	}
	for _, d := range f.devices {
		if strings.EqualFold(addr, d) {
			return true
		}
	}
	if len(f.macs) == 0 {
		return false
	}
	// Compare as MAC addresses so a filter written in one notation matches
	// an address reported in another.
	n, err := sensor.NormalizeMAC(addr)
	if err != nil {
		return false
	}
	_, ok := f.macs[n]
	return ok
}

// DescribeDevice renders the device filter for log lines.
func (c *Config) DescribeDevice() string {
	f := c.parsedDevice()
	switch {
	case f.acceptAll:
		return "all ThermoBeacon, Govee and BTHome devices"
	case len(f.devices) == 1:
		return "device " + f.devices[0]
	default:
		return "devices " + strings.Join(f.devices, ", ")
	}
}
