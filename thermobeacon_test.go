package main

import (
	"encoding/hex"
	"testing"
)

// Reference payload generated with the decoder layout of the original
// dbus-ble-temp (struct "<2xH6sHhHI"): button pressed, MAC db:5c:8d:a0:0f:21
// (LSB first), 2880 mV, 19.25 degC (308), 58 % (928), uptime 2131727 s.
var sampleThermoBeacon = mustHex("8000210fa08d5cdb400b3401a0030f872000")

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestDecodeThermoBeacon(t *testing.T) {
	r, err := decodeThermoBeacon("db:5c:8d:a0:0f:21", 0x0010, sampleThermoBeacon)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.MAC != "db:5c:8d:a0:0f:21" {
		t.Errorf("MAC = %q", r.MAC)
	}
	if r.Temperature != 19.25 {
		t.Errorf("Temperature = %v, want 19.25", r.Temperature)
	}
	if r.Humidity != 58.0 {
		t.Errorf("Humidity = %v, want 58", r.Humidity)
	}
	if r.VoltageMV != 2880 {
		t.Errorf("VoltageMV = %v, want 2880", r.VoltageMV)
	}
	if r.Uptime != 2131727 {
		t.Errorf("Uptime = %v, want 2131727", r.Uptime)
	}
	if !r.Button {
		t.Error("Button = false, want true")
	}
	if r.Source != "thermobeacon" {
		t.Errorf("Source = %q", r.Source)
	}
}

func TestDecodeThermoBeaconNoButton(t *testing.T) {
	data := append([]byte(nil), sampleThermoBeacon...)
	data[0] &^= 0x80
	r, err := decodeThermoBeacon("db:5c:8d:a0:0f:21", 0x0010, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Button {
		t.Error("Button = true, want false")
	}
}

// All known models embed the MAC LSB first, but the original accepts MSB
// first as well.
func TestDecodeThermoBeaconMSBFirstMAC(t *testing.T) {
	if _, err := decodeThermoBeacon("21:0f:a0:8d:5c:db", 0x0010, sampleThermoBeacon); err != nil {
		t.Fatalf("decode with reversed sender MAC: %v", err)
	}
}

// macOS only exposes an opaque device id, so the embedded MAC cannot be
// verified and a known company id is required instead.
func TestDecodeThermoBeaconWithoutSenderMAC(t *testing.T) {
	for _, id := range []uint16{0x0010, 0x0011, 0x0015} {
		r, err := decodeThermoBeacon("", id, sampleThermoBeacon)
		if err != nil {
			t.Fatalf("company id 0x%04x: %v", id, err)
		}
		if r.MAC != "db:5c:8d:a0:0f:21" {
			t.Errorf("company id 0x%04x: MAC = %q", id, r.MAC)
		}
	}
	if _, err := decodeThermoBeacon("", 0x1234, sampleThermoBeacon); err == nil {
		t.Error("unknown company id accepted")
	}
}

func TestDecodeThermoBeaconRejects(t *testing.T) {
	cases := []struct {
		name      string
		senderMAC string
		data      []byte
	}{
		{"wrong length", "db:5c:8d:a0:0f:21", sampleThermoBeacon[:17]},
		// ThermoBeacons also send a 22 byte min/max packet - ignored.
		{"min/max packet", "db:5c:8d:a0:0f:21", append(append([]byte(nil), sampleThermoBeacon...), 0x00)},
		{"embedded MAC mismatch", "aa:bb:cc:dd:ee:ff", sampleThermoBeacon},
	}
	for _, tc := range cases {
		if _, err := decodeThermoBeacon(tc.senderMAC, 0x0010, tc.data); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

// Negative temperatures are transmitted as signed 1/16 degC.
func TestDecodeThermoBeaconNegativeTemperature(t *testing.T) {
	data := append([]byte(nil), sampleThermoBeacon...)
	// -0.5 degC = -8
	data[10] = 0xf8
	data[11] = 0xff
	r, err := decodeThermoBeacon("db:5c:8d:a0:0f:21", 0x0010, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Temperature != -0.5 {
		t.Errorf("Temperature = %v, want -0.5", r.Temperature)
	}
}
