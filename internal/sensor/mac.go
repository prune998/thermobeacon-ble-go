package sensor

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

// macPattern matches a MAC address in colon notation.
var macPattern = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)

// IsMACAddress reports whether s looks like a MAC address (as opposed to a
// CoreBluetooth UUID).
func IsMACAddress(s string) bool {
	return macPattern.MatchString(s)
}

// NormalizeMAC canonicalizes a MAC address to lowercase colon notation.
// Accepts "DB:5C:8D:A0:0F:21", "db5c8da00f21" and "db-5c-8d-a0-0f-21".
func NormalizeMAC(mac string) (string, error) {
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

// MACFromLSBFirst formats 6 address bytes that are transmitted least
// significant byte first (as the ThermoBeacon embeds its MAC) as
// "aa:bb:cc:dd:ee:ff".
func MACFromLSBFirst(b []byte) string {
	return MACFromMSBFirst(reversed(b))
}

// MACFromMSBFirst formats 6 address bytes in network order as
// "aa:bb:cc:dd:ee:ff".
func MACFromMSBFirst(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02x", c)
	}
	return strings.Join(parts, ":")
}

// MACToBytes parses a MAC address into 6 bytes in network order.
func MACToBytes(mac string) ([6]byte, error) {
	var out [6]byte
	norm, err := NormalizeMAC(mac)
	if err != nil {
		return out, err
	}
	for i := 0; i < 6; i++ {
		_, _ = fmt.Sscanf(norm[i*3:i*3+2], "%02x", &out[i])
	}
	return out, nil
}

// MACToBDAddr parses a MAC address into the 6 byte little endian (LSB first)
// form Linux uses for bdaddr_t, which is what the dbus-ble-sensors UDP
// packet carries.
func MACToBDAddr(mac string) ([6]byte, error) {
	be, err := MACToBytes(mac)
	if err != nil {
		return [6]byte{}, err
	}
	var out [6]byte
	for i := 0; i < 6; i++ {
		out[i] = be[5-i]
	}
	return out, nil
}

// PseudoMAC derives a stable, locally administered MAC address from an opaque
// device id. macOS only exposes a peripheral UUID instead of a MAC address;
// a stable pseudo MAC keeps a sensor's identity (Ruuvi device id, state file
// entry) unchanged across restarts.
func PseudoMAC(deviceID string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(deviceID)))
	sum := h.Sum(nil)
	sum[0] = (sum[0] | 0x02) & 0xFE // locally administered, unicast
	return MACFromMSBFirst(sum[:6])
}

// SenderMAC returns the normalized MAC address of a platform address, or ""
// when the platform does not expose MAC addresses (macOS) or the address is
// unset (all-zero - on Linux an unset bluetooth.Address renders as
// "00:00:00:00:00:00", which must not be mistaken for a real MAC).
func SenderMAC(addr string) string {
	if !IsMACAddress(addr) {
		return ""
	}
	n, err := NormalizeMAC(addr)
	if err != nil || n == "00:00:00:00:00:00" {
		return ""
	}
	return n
}

// IdentityFor returns the sensor identity: the advertiser MAC where the
// platform provides one, otherwise a stable pseudo MAC derived from the
// opaque device id (macOS).
func IdentityFor(senderMAC, addr string) string {
	if senderMAC != "" {
		return senderMAC
	}
	return PseudoMAC(addr)
}

func reversed(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[len(b)-1-i] = c
	}
	return out
}

func Clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func ClampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
