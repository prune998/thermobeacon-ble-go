package main

import (
	"strings"
	"testing"
	"time"

	bthome "github.com/hybridgroup/go-bthome"
	"tinygo.org/x/bluetooth"
)

// fakePayload implements bluetooth.AdvertisementPayload with structured data,
// the way the macOS stack delivers it (Bytes() returns nil).
type fakePayload struct {
	name string
	mfg  []bluetooth.ManufacturerDataElement
	svc  []bluetooth.ServiceDataElement
}

func (f *fakePayload) LocalName() string                                     { return f.name }
func (f *fakePayload) HasServiceUUID(bluetooth.UUID) bool                    { return false }
func (f *fakePayload) ServiceUUIDs() []bluetooth.UUID                        { return nil }
func (f *fakePayload) Bytes() []byte                                         { return nil }
func (f *fakePayload) ManufacturerData() []bluetooth.ManufacturerDataElement { return f.mfg }
func (f *fakePayload) ServiceData() []bluetooth.ServiceDataElement           { return f.svc }

func TestDumpScanResultText(t *testing.T) {
	res := bluetooth.ScanResult{
		RSSI: -52,
		AdvertisementPayload: &fakePayload{
			name: "Oria",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0x0010, Data: sampleThermoBeacon}},
			svc:  []bluetooth.ServiceDataElement{{UUID: bthome.ServiceUUID, Data: sampleBTHome}},
		},
	}
	text := dumpScanResultText(res, "db:5c:8d:a0:0f:21", false)

	for _, want := range []string{
		"rssi -52 dBm",
		`name "Oria"`,
		"company id 0x0010, known ThermoBeacon model, 18 byte payload",
		"80 00 21 0f a0 8d 5c db", // hex dump row
		"ThermoBeacon decoded",
		"button pressed",
		"-> db:5c:8d:a0:0f:21",
		"2880 mV (2.880 V)",
		"= 19.2500 degC",
		"= 58.0000 %",
		"2131727 s (24d 16h 8m 47s)",
		"service data 0000fcd2-0000-1000-8000-00805f9b34fb",
		"BTHome v2, unencrypted",
		"temperature",
		"44.50 % (raw 4450 x 0.01)",
		"0x15",                         // battery low flag
		"not convertible by go-bthome", // the count16 TLV in the sample
	} {
		if !strings.Contains(text, want) {
			t.Errorf("dump does not contain %q\ngot:\n%s", want, text)
		}
	}
}

func TestDumpScanResultTextRejectsForeignPayload(t *testing.T) {
	// 18 bytes but the embedded MAC does not match the sender: the dump must
	// say why the payload is not forwarded.
	res := bluetooth.ScanResult{
		RSSI: -70,
		AdvertisementPayload: &fakePayload{
			mfg: []bluetooth.ManufacturerDataElement{{CompanyID: 0x0010, Data: sampleThermoBeacon}},
		},
	}
	text := dumpScanResultText(res, "aa:bb:cc:dd:ee:ff", false)
	if !strings.Contains(text, "not a ThermoBeacon/Govee payload") {
		t.Errorf("dump does not explain rejection:\n%s", text)
	}
	if !strings.Contains(text, "embedded MAC does not match advertiser") {
		t.Errorf("dump does not explain why it is not a ThermoBeacon:\n%s", text)
	}
	if strings.Contains(text, "ThermoBeacon decoded") {
		t.Errorf("dump shows a decoded reading despite rejection:\n%s", text)
	}
}

func TestDumpScanResultTextEmpty(t *testing.T) {
	res := bluetooth.ScanResult{RSSI: -80, AdvertisementPayload: &fakePayload{name: "empty"}}
	text := dumpScanResultText(res, "", false)
	if !strings.Contains(text, "no manufacturer or service data") {
		t.Errorf("dump does not mention missing data:\n%s", text)
	}
}

func TestDumpRuuvi(t *testing.T) {
	r := &Reading{
		MAC:         "db:5c:8d:a0:0f:21",
		Temperature: 19.25,
		Humidity:    58.0,
		VoltageMV:   2880,
		Uptime:      2131727,
	}
	payload, err := encodeRuuviRAWv2(r, 34575)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var sb strings.Builder
	writeRuuviFields(&sb, payload)
	text := sb.String()

	for _, want := range []string{
		"format       5 (RAWv2)",
		"temperature  19.2500 degC (raw 3850)",
		"humidity     58.0000 % (raw 23200)",
		"pressure     invalid (0xFFFF)",
		"acceleration x/y/z invalid (0x8000)",
		"battery      2880 mV",
		"tx power     invalid (0x1F)",
		"movement     invalid (0xFF)",
		"sequence     34575 (0x870f)",
		"MAC          db:5c:8d:a0:0f:21",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("ruuvi dump does not contain %q\ngot:\n%s", want, text)
		}
	}
}

func TestWriteHexDump(t *testing.T) {
	var sb strings.Builder
	writeHexDump(&sb, sampleThermoBeacon, "")
	lines := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	if len(lines) != 2 { // 18 bytes -> two rows
		t.Fatalf("got %d rows, want 2:\n%s", len(lines), sb.String())
	}
	if !strings.HasPrefix(lines[0], "0000 ") {
		t.Errorf("first row lacks offset: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "0010 ") {
		t.Errorf("second row lacks offset: %q", lines[1])
	}
	if !strings.Contains(lines[0], "a0 03 0f 87") {
		t.Errorf("first row hex column wrong: %q", lines[0])
	}
	if !strings.Contains(lines[1], " 20 00 ") {
		t.Errorf("second row hex column wrong: %q", lines[1])
	}
	// alignment: hex column of a partial row must be padded
	if len(lines[1]) != len(lines[0]) {
		t.Errorf("rows differ in length: %d vs %d\n%s\n%s", len(lines[1]), len(lines[0]), lines[0], lines[1])
	}
}

func TestIterADStructures(t *testing.T) {
	// flags (3 bytes) + manufacturer data (16 bytes) + a malformed tail
	data := []byte{
		0x02, 0x01, 0x06,
		0x0E, 0xFF, 0x10, 0x00, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12,
		0x7F, 0xFF, // length 0x7f runs past the end
	}
	var types []byte
	iterADStructures(data, func(typ byte, field []byte) {
		types = append(types, typ)
	})
	if len(types) != 2 || types[0] != 0x01 || types[1] != 0xFF {
		t.Errorf("iterADStructures types = %v, want [1 255]", types)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{47 * time.Second, "47s"},
		{90 * time.Second, "1m 30s"},
		{3*time.Hour + 2*time.Minute, "3h 2m"},
		{2131727 * time.Second, "24d 16h 8m 47s"},
	}
	for _, tc := range cases {
		if got := humanDuration(tc.in); got != tc.want {
			t.Errorf("humanDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDebugDumpScanNoopWithoutDebug(t *testing.T) {
	// Must not panic and must not log anything when debug is off; with debug
	// off the dump functions return before doing any work.
	old := debugEnabled
	debugEnabled = false
	defer func() { debugEnabled = old }()

	debugDumpScan(bluetooth.ScanResult{AdvertisementPayload: &fakePayload{}}, "", false)
	debugDumpRuuvi(make([]byte, ruuviPayloadLen))
}
