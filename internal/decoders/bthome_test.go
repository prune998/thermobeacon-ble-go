package decoders

import (
	"math"
	"testing"
)

// A BTHome v2 payload in the layout of real weather sensors:
//
//	40                          device information (v2, unencrypted)
//	45 D7 00                    temperature 0x45, 215 * 0.1 = 21.5 degC
//	03 62 11                    humidity 0x03, 4450 * 0.01 = 44.5 %
//	01 57                       battery 0x01, 87 %
//	15 01                       battery low 0x15, true
//	3D 05 00                    count 0x3D (u16, unsupported by go-bthome)
var sampleBTHome = []byte{
	0x40,
	0x45, 0xD7, 0x00,
	0x03, 0x62, 0x11,
	0x01, 0x57,
	0x15, 0x01,
	0x3D, 0x05, 0x00,
}

func TestDecodeBTHome(t *testing.T) {
	r, err := BTHome("aa:bb:cc:dd:ee:ff", sampleBTHome)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r == nil {
		t.Fatal("no reading returned")
	}
	if r.MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC = %q", r.MAC)
	}
	if r.Source != "bthome" {
		t.Errorf("Source = %q", r.Source)
	}
	if math.Abs(r.Temperature-21.5) > 0.01 {
		t.Errorf("Temperature = %v, want 21.5", r.Temperature)
	}
	if math.Abs(r.Humidity-44.5) > 0.01 {
		t.Errorf("Humidity = %v, want 44.5 (BTHome factor 0.01)", r.Humidity)
	}
	if r.VoltageMV != 3370 {
		t.Errorf("VoltageMV = %v, want 3370 (2500 + 87 * 10)", r.VoltageMV)
	}
	if !r.BatteryLow {
		t.Error("BatteryLow = false, want true")
	}
	if r.Uptime != 0 {
		t.Errorf("Uptime = %v, want 0 (BTHome has no uptime)", r.Uptime)
	}
}

// A packet with an explicit voltage must not overwrite it with the value
// derived from the battery percentage.
func TestDecodeBTHomeExplicitVoltage(t *testing.T) {
	data := []byte{
		0x40,
		0x45, 0xD7, 0x00, // temperature 21.5 degC
		0x0C, 0x64, 0x0A, // voltage 0x0C, 2660 mV
		0x01, 0x32, // battery 50 %
	}
	r, err := BTHome("aa:bb:cc:dd:ee:ff", data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.VoltageMV != 2660 {
		t.Errorf("VoltageMV = %v, want 2660", r.VoltageMV)
	}
}

func TestDecodeBTHomeTemperature2(t *testing.T) {
	// temperature 0x02, factor 0.01: 2345 -> 23.45 degC
	data := []byte{0x40, 0x02, 0x29, 0x09}
	r, err := BTHome("aa:bb:cc:dd:ee:ff", data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if math.Abs(r.Temperature-23.45) > 0.01 {
		t.Errorf("Temperature = %v, want 23.45", r.Temperature)
	}
}

func TestDecodeBTHomeRejects(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"encrypted (bit 7 set)", []byte{0xC0, 0x45, 0x01, 0x00}},
		{"v1 device information", []byte{0x00, 0x45, 0x01, 0x00}},
		{"unknown type id", []byte{0x40, 0x77, 0x01}},
		{"truncated value", []byte{0x40, 0x45, 0xD7}},
	}
	for _, tc := range cases {
		r, err := BTHome("aa:bb:cc:dd:ee:ff", tc.data)
		if err == nil {
			t.Errorf("%s: accepted (reading %+v)", tc.name, r)
		}
	}
}

// A valid BTHome packet without any measurement we map yields (nil, nil).
func TestDecodeBTHomeNothingUsable(t *testing.T) {
	data := []byte{0x40, 0x3D, 0x05, 0x00} // only a count16
	r, err := BTHome("aa:bb:cc:dd:ee:ff", data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil reading, got %+v", r)
	}
}
