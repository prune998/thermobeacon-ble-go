package main

import (
	"strings"
	"testing"

	"tinygo.org/x/bluetooth"
)

// H5074 payload (after the company id): frame byte 01, temperature 22.59 degC
// (2259, little endian), humidity 45.67 % (4567, little endian), battery 88 %,
// trailing byte 0a.
var goveeSampleH5074 = []byte{0x01, 0xD3, 0x08, 0xD7, 0x11, 0x58, 0x0A}

func TestDecodeGoveeH5074(t *testing.T) {
	r, err := decodeGovee("aa:bb:cc:dd:ee:ff", "Govee_H5074", goveeCompanyIDH50xx, goveeSampleH5074)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC = %q", r.MAC)
	}
	if r.Model != "H5074" {
		t.Errorf("Model = %q", r.Model)
	}
	if r.Source != "govee" {
		t.Errorf("Source = %q", r.Source)
	}
	if r.Temperature != 22.59 {
		t.Errorf("Temperature = %v, want 22.59", r.Temperature)
	}
	if r.Humidity != 45.67 {
		t.Errorf("Humidity = %v, want 45.67", r.Humidity)
	}
	if r.BatteryPct != 88 {
		t.Errorf("BatteryPct = %v, want 88", r.BatteryPct)
	}
	// Derived voltage: 2500 mV + 88 * 10
	if r.VoltageMV != 3380 {
		t.Errorf("VoltageMV = %v, want 3380", r.VoltageMV)
	}
}

func TestDecodeGoveeH5074NegativeTemperature(t *testing.T) {
	// -5.32 degC = -532 = 0xFDEC, little endian
	data := []byte{0x01, 0xEC, 0xFD, 0xD7, 0x11, 0x58, 0x0A}
	r, err := decodeGovee("", "Govee_H5074", goveeCompanyIDH50xx, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Temperature != -5.32 {
		t.Errorf("Temperature = %v, want -5.32", r.Temperature)
	}
}

func TestDecodeGoveeH5074OutOfRange(t *testing.T) {
	// 150.00 degC is outside the -40..100 window govee-ble uses
	data := []byte{0x01, 0x98, 0x3A, 0xD7, 0x11, 0x58, 0x0A}
	if _, err := decodeGovee("", "Govee_H5074", goveeCompanyIDH50xx, data); err == nil {
		t.Error("out of range temperature accepted")
	}
}

// The H5074 branch also matches by local name alone (some firmwares use a
// different company id); without the name, unknown company ids are rejected.
func TestDecodeGoveeH5074ByName(t *testing.T) {
	if _, err := decodeGovee("", "Govee_H5074", 0x1234, goveeSampleH5074); err != nil {
		t.Errorf("name match failed: %v", err)
	}
	if _, err := decodeGovee("", "", 0x1234, goveeSampleH5074); err == nil {
		t.Error("unknown company id and no name accepted")
	}
}

// H5075 layout: packed 24 bit value 234567 = 23.4 degC and 56.7 %, battery 88.
func TestDecodeGoveeH5075(t *testing.T) {
	data := []byte{0x01, 0x03, 0x94, 0x47, 0x58, 0xAA}
	r, err := decodeGovee("", "", goveeCompanyIDH50xx, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Model != "H5072/H5075" {
		t.Errorf("Model = %q", r.Model)
	}
	if r.Temperature != 23.4 {
		t.Errorf("Temperature = %v, want 23.4", r.Temperature)
	}
	if r.Humidity != 56.7 {
		t.Errorf("Humidity = %v, want 56.7", r.Humidity)
	}
	if r.BatteryPct != 88 {
		t.Errorf("BatteryPct = %v, want 88", r.BatteryPct)
	}
}

func TestDecodeGoveeH5075Negative(t *testing.T) {
	// mag 50500 = -5.0 degC (bit 23 set) and 50.0 %
	data := []byte{0x01, 0x80, 0xC5, 0x44, 0x32, 0x00}
	r, err := decodeGovee("", "Govee_H5075", goveeCompanyIDH50xx, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Temperature != -5.0 {
		t.Errorf("Temperature = %v, want -5.0", r.Temperature)
	}
	if r.Humidity != 50.0 {
		t.Errorf("Humidity = %v, want 50.0", r.Humidity)
	}
	if r.BatteryPct != 50 {
		t.Errorf("BatteryPct = %v, want 50", r.BatteryPct)
	}
}

func TestDecodeGoveeErrorBit(t *testing.T) {
	// battery byte 0xD8 = battery 88 + error bit
	data := []byte{0x01, 0x03, 0x94, 0x87, 0xD8, 0xAA}
	if _, err := decodeGovee("", "", goveeCompanyIDH50xx, data); err == nil {
		t.Error("error bit accepted")
	}
}

// H51xx family (H5101/H5102/H5104/H5108/...): company id 0x0001, 8 bytes.
func TestDecodeGoveeH51xxFamily(t *testing.T) {
	data := []byte{0x01, 0x00, 0x03, 0x49, 0x95, 0x64, 0x00, 0x00}
	r, err := decodeGovee("", "", goveeCompanyIDH51xx, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Temperature != 21.5 {
		t.Errorf("Temperature = %v, want 21.5", r.Temperature)
	}
	if r.Humidity != 44.5 {
		t.Errorf("Humidity = %v, want 44.5", r.Humidity)
	}
	if r.BatteryPct != 100 {
		t.Errorf("BatteryPct = %v, want 100", r.BatteryPct)
	}
	if r.VoltageMV != 3500 {
		t.Errorf("VoltageMV = %v, want 3500", r.VoltageMV)
	}
}

func TestDecodeGoveeUnknown(t *testing.T) {
	if _, err := decodeGovee("", "", 0xEC88, make([]byte, 9)); err == nil {
		t.Error("9 byte payload accepted")
	}
	if _, err := decodeGovee("", "", 0x004C, make([]byte, 8)); err == nil {
		t.Error("Apple company id with 8 bytes accepted")
	}
}

func TestSourceLabel(t *testing.T) {
	if got := sourceLabel(&Reading{Source: "govee", Model: "H5074"}); got != "Govee H5074" {
		t.Errorf("sourceLabel = %q, want %q", got, "Govee H5074")
	}
	if got := sourceLabel(&Reading{Source: "govee"}); got != "Govee" {
		t.Errorf("sourceLabel = %q, want %q", got, "Govee")
	}
	if got := sourceLabel(&Reading{Source: "bthome"}); got != "BTHome" {
		t.Errorf("sourceLabel = %q, want %q", got, "BTHome")
	}
	if got := sourceLabel(&Reading{Source: "thermobeacon"}); got != "ThermoBeacon" {
		t.Errorf("sourceLabel = %q, want %q", got, "ThermoBeacon")
	}
}

func TestDumpScanResultTextGovee(t *testing.T) {
	res := bluetooth.ScanResult{
		RSSI: -60,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: goveeCompanyIDH50xx, Data: goveeSampleH5074}},
		},
	}
	text := dumpScanResultText(res, "")
	for _, want := range []string{
		"company id 0xec88, Govee company id, 7 byte payload",
		"01 d3 08 d7 11 58 0a",
		"Govee H5074 decoded: 22.59 degC, 45.67 %, battery 88 %",
		"temperature  d3 08  -> 2259 / 100 = 22.59 degC",
		"humidity     d7 11  -> 4567 / 100 = 45.67 %",
		"battery      58  -> 88 %",
		"reserved     0a",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("dump does not contain %q\ngot:\n%s", want, text)
		}
	}
}

func TestDumpScanResultTextGoveePacked(t *testing.T) {
	res := bluetooth.ScanResult{
		RSSI: -60,
		AdvertisementPayload: &fakePayload{
			mfg: []bluetooth.ManufacturerDataElement{{CompanyID: goveeCompanyIDH50xx, Data: []byte{0x01, 0x03, 0x94, 0x47, 0x58, 0xAA}}},
		},
	}
	text := dumpScanResultText(res, "")
	for _, want := range []string{
		"Govee H5072/H5075 decoded: 23.40 degC, 56.70 %, battery 88 %",
		"temp+humid   03 94 47  -> mag 234567: 23.4 degC, 56.7 %",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("dump does not contain %q\ngot:\n%s", want, text)
		}
	}
}

func TestVoltageFromBatteryPct(t *testing.T) {
	if got := voltageFromBatteryPct(88); got != 3380 {
		t.Errorf("voltageFromBatteryPct(88) = %d, want 3380", got)
	}
	if got := voltageFromBatteryPct(0); got != 2500 {
		t.Errorf("voltageFromBatteryPct(0) = %d, want 2500", got)
	}
	if got := voltageFromBatteryPct(100); got != 3500 {
		t.Errorf("voltageFromBatteryPct(100) = %d, want 3500", got)
	}
	if got := voltageFromBatteryPct(150); got != 3500 {
		t.Errorf("voltageFromBatteryPct(150) = %d, want 3500 (clamped)", got)
	}
}
