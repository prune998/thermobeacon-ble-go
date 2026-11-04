// Package sensor defines the Reading type that all decoders produce and the
// sensor identity helpers (MAC handling, pseudo MACs, voltage derivation)
// shared by the decoders, the bridge and the TUI.
package sensor

import (
	"fmt"
	"strings"
)

// Reading is a decoded sensor measurement, independent of the advertisement
// format it came from (ThermoBeacon manufacturer data, BTHome service data or
// Govee manufacturer data).
type Reading struct {
	// MAC is the sensor identity in "aa:bb:cc:dd:ee:ff" notation. For
	// ThermoBeacons this is the MAC embedded in the payload, which works on
	// macOS too where the OS only exposes an opaque UUID. Sensors without an
	// embedded MAC (BTHome, Govee) get the advertiser MAC, or a stable pseudo
	// MAC derived from the device id on macOS.
	MAC string

	Model       string  // device model when known, e.g. "H5074"
	Temperature float64 // degC
	Humidity    float64 // %RH
	VoltageMV   int     // battery voltage in millivolt
	BatteryPct  int     // battery percentage, -1 when unknown
	Uptime      uint32  // seconds since sensor boot, 0 when unknown
	Button      bool    // button pressed flag (ThermoBeacon)
	BatteryLow  bool    // low battery flag (BTHome)

	Source string // "thermobeacon", "bthome" or "govee"
}

func (r *Reading) String() string {
	s := fmt.Sprintf("%s: %.2f degC, %.2f %%, %d mV, uptime %d s",
		r.MAC, r.Temperature, r.Humidity, r.VoltageMV, r.Uptime)
	if r.BatteryPct >= 0 {
		s += fmt.Sprintf(", battery %d %%", r.BatteryPct)
	}
	return s
}

// SourceLabel maps a Reading to the name prefix used in logs and default
// names, including the model when it is known ("Govee H5074").
func SourceLabel(r *Reading) string {
	switch r.Source {
	case "bthome":
		return "BTHome"
	case "govee":
		if r.Model != "" {
			return "Govee " + r.Model
		}
		return "Govee"
	default:
		return "ThermoBeacon"
	}
}

// DefaultName replicates the naming scheme of dbus-ble-temp:
// "<prefix> <last 4 hex digits of the MAC>", e.g. "ThermoBeacon 059B".
func DefaultName(prefix, mac string) string {
	suffix := strings.ToUpper(strings.ReplaceAll(mac, ":", ""))
	if len(suffix) > 4 {
		suffix = suffix[len(suffix)-4:]
	}
	return prefix + " " + suffix
}

// VoltageFromBatteryPct derives a plausible CR2032-style voltage from a
// battery percentage (2.50 V empty .. 3.50 V full) for sensors that only
// report a percentage (BTHome, Govee), so that the low battery alarm of
// dbus-ble-sensors has something to work with.
func VoltageFromBatteryPct(pct int) int {
	return 2500 + ClampInt(pct, 0, 100)*10
}
