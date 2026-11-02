package main

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

// macPattern matches a MAC address in colon notation.
var macPattern = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)

// isMACAddress reports whether s looks like a MAC address (as opposed to a
// CoreBluetooth UUID).
func isMACAddress(s string) bool {
	return macPattern.MatchString(s)
}

// normalizeMAC canonicalizes a MAC address to lowercase colon notation.
// Accepts "DB:5C:8D:A0:0F:21", "db5c8da00f21" and "db-5c-8d-a0-0f-21".
func normalizeMAC(mac string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(mac))
	clean = strings.NewReplacer(":", "", "-", "").Replace(clean)
	if len(clean) != 12 {
		return "", fmt.Errorf("invalid MAC address %q", mac)
	}
	for _, c := range clean {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", fmt.Errorf("invalid MAC address %q", mac)
		}
	}
	var sb strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteString(clean[i : i+2])
	}
	return sb.String(), nil
}

// macFromLSBFirst formats 6 address bytes that are transmitted least
// significant byte first (as the ThermoBeacon embeds its MAC) as
// "aa:bb:cc:dd:ee:ff".
func macFromLSBFirst(b []byte) string {
	return macFromMSBFirst(reversed(b))
}

// macFromMSBFirst formats 6 address bytes in network order as
// "aa:bb:cc:dd:ee:ff".
func macFromMSBFirst(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02x", c)
	}
	return strings.Join(parts, ":")
}

// macToBytes parses a MAC address into 6 bytes in network order.
func macToBytes(mac string) ([6]byte, error) {
	var out [6]byte
	norm, err := normalizeMAC(mac)
	if err != nil {
		return out, err
	}
	for i := 0; i < 6; i++ {
		_, _ = fmt.Sscanf(norm[i*3:i*3+2], "%02x", &out[i])
	}
	return out, nil
}

// macToBDAddr parses a MAC address into the 6 byte little endian (LSB first)
// form Linux uses for bdaddr_t, which is what the dbus-ble-sensors UDP
// packet carries.
func macToBDAddr(mac string) ([6]byte, error) {
	be, err := macToBytes(mac)
	if err != nil {
		return [6]byte{}, err
	}
	var out [6]byte
	for i := 0; i < 6; i++ {
		out[i] = be[5-i]
	}
	return out, nil
}

// pseudoMAC derives a stable, locally administered MAC address from an opaque
// device id. macOS only exposes a peripheral UUID instead of a MAC address;
// a stable pseudo MAC keeps a sensor's identity (Ruuvi device id, state file
// entry) unchanged across restarts.
func pseudoMAC(deviceID string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(deviceID)))
	sum := h.Sum(nil)
	sum[0] = (sum[0] | 0x02) & 0xFE // locally administered, unicast
	return macFromMSBFirst(sum[:6])
}

func reversed(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[len(b)-1-i] = c
	}
	return out
}

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

// voltageFromBatteryPct derives a plausible CR2032-style voltage from a
// battery percentage (2.50 V empty .. 3.50 V full) for sensors that only
// report a percentage (BTHome, Govee), so that the low battery alarm of
// dbus-ble-sensors has something to work with.
func voltageFromBatteryPct(pct int) int {
	return 2500 + clampInt(pct, 0, 100)*10
}

// defaultName replicates the naming scheme of dbus-ble-temp:
// "<prefix> <last 4 hex digits of the MAC>", e.g. "ThermoBeacon 059B".
func defaultName(prefix, mac string) string {
	suffix := strings.ToUpper(strings.ReplaceAll(mac, ":", ""))
	if len(suffix) > 4 {
		suffix = suffix[len(suffix)-4:]
	}
	return prefix + " " + suffix
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
