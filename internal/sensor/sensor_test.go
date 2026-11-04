package sensor

import (
	"strings"
	"testing"
)

func TestNormalizeMAC(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"DB:5C:8D:A0:0F:21", "db:5c:8d:a0:0f:21", false},
		{"db5c8da00f21", "db:5c:8d:a0:0f:21", false},
		{"db-5c-8d-a0-0f-21", "db:5c:8d:a0:0f:21", false},
		{"aa:bb:cc:dd:ee", "", true},
		{"aa:bb:cc:dd:ee:ff:00", "", true},
		{"zz:bb:cc:dd:ee:ff", "", true},
		{"10243784-1324-5413-b00c-9675981a8daa", "", true}, // a UUID, not a MAC
	}
	for _, tc := range cases {
		got, err := NormalizeMAC(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeMAC(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeMAC(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsMACAddress(t *testing.T) {
	if !IsMACAddress("db:5c:8d:a0:0f:21") {
		t.Error("MAC not recognized")
	}
	if IsMACAddress("10243784-1324-5413-b00c-9675981a8daa") {
		t.Error("UUID recognized as MAC")
	}
}

func TestMACByteConversions(t *testing.T) {
	mac := "db:5c:8d:a0:0f:21"

	be, err := MACToBytes(mac)
	if err != nil {
		t.Fatalf("macToBytes: %v", err)
	}
	wantBE := []byte{0xdb, 0x5c, 0x8d, 0xa0, 0x0f, 0x21}
	for i, w := range wantBE {
		if be[i] != w {
			t.Errorf("macToBytes[%d] = %#02x, want %#02x", i, be[i], w)
		}
	}

	bd, err := MACToBDAddr(mac)
	if err != nil {
		t.Fatalf("macToBDAddr: %v", err)
	}
	wantBD := []byte{0x21, 0x0f, 0xa0, 0x8d, 0x5c, 0xdb}
	for i, w := range wantBD {
		if bd[i] != w {
			t.Errorf("macToBDAddr[%d] = %#02x, want %#02x", i, bd[i], w)
		}
	}

	if got := MACFromLSBFirst(wantBD); got != mac {
		t.Errorf("macFromLSBFirst = %q, want %q", got, mac)
	}
	if got := MACFromMSBFirst(wantBE); got != mac {
		t.Errorf("macFromMSBFirst = %q, want %q", got, mac)
	}
}

func TestReadingString(t *testing.T) {
	r := &Reading{MAC: "db:5c:8d:a0:0f:21", Temperature: 19.25, Humidity: 58, VoltageMV: 2880, Uptime: 2131727, BatteryPct: -1}
	want := "db:5c:8d:a0:0f:21: 19.25 degC, 58.00 %, 2880 mV, uptime 2131727 s"
	if got := r.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	r.BatteryPct = 88
	want += ", battery 88 %"
	if got := r.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestDefaultName(t *testing.T) {
	if got := DefaultName("ThermoBeacon", "db:5c:8d:a0:0f:21"); got != "ThermoBeacon 0F21" {
		t.Errorf("defaultName = %q, want %q", got, "ThermoBeacon 0F21")
	}
	if got := DefaultName("BTHome", "aa:bb:cc:dd:0b:e1"); got != "BTHome 0BE1" {
		t.Errorf("defaultName = %q, want %q", got, "BTHome 0BE1")
	}
}

func TestPseudoMACStable(t *testing.T) {
	a := PseudoMAC("10243784-1324-5413-b00c-9675981a8daa")
	b := PseudoMAC("10243784-1324-5413-b00c-9675981a8daa")
	if a != b {
		t.Errorf("pseudoMAC not stable: %q vs %q", a, b)
	}
	if a == PseudoMAC("another-device") {
		t.Error("pseudoMAC collides for different device ids")
	}
	if !strings.Contains(a, ":") {
		t.Errorf("pseudoMAC %q is not in MAC notation", a)
	}
}

func TestVoltageFromBatteryPct(t *testing.T) {
	if got := VoltageFromBatteryPct(88); got != 3380 {
		t.Errorf("VoltageFromBatteryPct(88) = %d, want 3380", got)
	}
	if got := VoltageFromBatteryPct(0); got != 2500 {
		t.Errorf("VoltageFromBatteryPct(0) = %d, want 2500", got)
	}
	if got := VoltageFromBatteryPct(100); got != 3500 {
		t.Errorf("VoltageFromBatteryPct(100) = %d, want 3500", got)
	}
	if got := VoltageFromBatteryPct(150); got != 3500 {
		t.Errorf("VoltageFromBatteryPct(150) = %d, want 3500 (clamped)", got)
	}
}

func TestSourceLabel(t *testing.T) {
	if got := SourceLabel(&Reading{Source: "govee", Model: "H5074"}); got != "Govee H5074" {
		t.Errorf("sourceLabel = %q, want %q", got, "Govee H5074")
	}
	if got := SourceLabel(&Reading{Source: "govee"}); got != "Govee" {
		t.Errorf("sourceLabel = %q, want %q", got, "Govee")
	}
	if got := SourceLabel(&Reading{Source: "bthome"}); got != "BTHome" {
		t.Errorf("sourceLabel = %q, want %q", got, "BTHome")
	}
	if got := SourceLabel(&Reading{Source: "thermobeacon"}); got != "ThermoBeacon" {
		t.Errorf("sourceLabel = %q, want %q", got, "ThermoBeacon")
	}
}

func TestSenderMACZeroAddress(t *testing.T) {
	// On Linux an unset bluetooth.Address renders as 00:00:00:00:00:00; it
	// must not be mistaken for a real sensor MAC (this broke the ThermoBeacon
	// path in the CLI tests on Linux).
	if got := SenderMAC("00:00:00:00:00:00"); got != "" {
		t.Errorf("SenderMAC(zero MAC) = %q, want empty", got)
	}
	if got := SenderMAC("db:5c:8d:a0:0f:21"); got != "db:5c:8d:a0:0f:21" {
		t.Errorf("SenderMAC = %q, want the normalized MAC", got)
	}
	if got := SenderMAC("00000000-0000-0000-0000-000000000000"); got != "" {
		t.Errorf("SenderMAC(uuid) = %q, want empty", got)
	}
}
