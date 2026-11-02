package main

import (
	"log"
	"strings"
	"time"
)

// Config holds all runtime options.
type Config struct {
	// Device selects the sensor to follow: a CoreBluetooth UUID (macOS), a
	// MAC address (Linux/Windows) or "all" for every ThermoBeacon / BTHome
	// device in range.
	Device string

	UDPHost     string
	UDPPort     int
	NoForward   bool
	CLI         bool
	MinInterval time.Duration
	IdleWarning time.Duration
	Debug       bool
}

// acceptsDevice reports whether an advertisement from the given address
// (as returned by ScanResult.Address.String()) should be decoded.
func (c *Config) acceptsDevice(addr string) bool {
	if c.Device == "" || c.Device == "all" || c.Device == "*" {
		return true
	}
	if strings.EqualFold(addr, c.Device) {
		return true
	}
	// Compare as MAC addresses so "db5c8da00f21", "DB:5C:8D:A0:0F:21" and
	// "db-5c-8d-a0-0f-21" all match the same sensor.
	if n, err := normalizeMAC(addr); err == nil {
		if d, err := normalizeMAC(c.Device); err == nil && n == d {
			return true
		}
	}
	return false
}

func (c *Config) describeDevice() string {
	if c.Device == "" || c.Device == "all" || c.Device == "*" {
		return "all ThermoBeacon and BTHome devices"
	}
	return "device " + c.Device
}

// debugEnabled is set from the -debug flag.
var debugEnabled bool

func debugf(format string, args ...any) {
	if debugEnabled {
		log.Printf("DEBUG "+format, args...)
	}
}
