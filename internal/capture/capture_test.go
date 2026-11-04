package capture

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tinygo.org/x/bluetooth"
)

const fixedAddr = "f1ae0297-b710-1440-2881-9274ad58590d"

// fakePayload implements bluetooth.AdvertisementPayload with structured data.
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

// goveeRealCapture: real GoveeBTTempLogger H5074 capture (25.52 degC,
// 73.27 %, battery 100 %).
var goveeRealCapture = []byte{0x00, 0xF8, 0x09, 0x9F, 0x1C, 0x64, 0x02}

// goveeIntelliRocksBlob: company id + type + length + "INTELLI_ROCKS_HW" +
// padding - the blob Govee firmware glues onto its manufacturer data.
var goveeIntelliRocksBlob = []byte{
	0x00, 0x4C, 0x02, 0x15,
	'I', 'N', 'T', 'E', 'L', 'L', 'I', '_', 'R', 'O', 'C', 'K', 'S', '_', 'H', 'W',
	0x9F, 0xF2, 0xFF, 0xC2, 0x01,
}

// thbeaconFixture: ThermoBeacon payload with the embedded MAC
// db:5c:8d:a0:0f:21, 19.25 degC, 58 %, 2880 mV.
var thbeaconFixture = []byte{
	0x80, 0x00, 0x21, 0x0F, 0xA0, 0x8D, 0x5C, 0xDB,
	0x40, 0x0B, 0x34, 0x01, 0xA0, 0x03, 0x0F, 0x87, 0x20, 0x00,
}

func TestFromAdvertisementAndDecode(t *testing.T) {
	e := FromAdvertisement(fixedAddr, -35, &fakePayload{
		name: "Govee_H5074_88DE",
		mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealCapture}},
	})

	if e.Address != fixedAddr || e.Name != "Govee_H5074_88DE" || e.RSSI != -35 {
		t.Errorf("event = %+v", e)
	}
	if len(e.ManufacturerData) != 1 ||
		e.ManufacturerData[0].CompanyID != 0xEC88 ||
		e.ManufacturerData[0].Data != "00f8099f1c6402" {
		t.Errorf("ManufacturerData = %+v", e.ManufacturerData)
	}
	if e.SenderMAC != "" {
		t.Errorf("SenderMAC = %q, want empty for a UUID address", e.SenderMAC)
	}
	if e.Identity == "" {
		t.Error("Identity not set")
	}

	// The captured frame decodes to the same values the reference
	// implementation reports for these bytes.
	r := Decode(e)
	if r == nil {
		t.Fatal("captured frame not decoded")
	}
	if r.Temperature != 25.52 || r.Humidity != 73.27 || r.BatteryPct != 100 {
		t.Errorf("Reading = %.2f / %.2f / %d, want 25.52 / 73.27 / 100",
			r.Temperature, r.Humidity, r.BatteryPct)
	}
	if r.MAC != e.Identity {
		t.Errorf("Reading MAC = %q, want %q", r.MAC, e.Identity)
	}

	// The replayed payload exposes the same manufacturer data for the dump
	// machinery.
	m := e.Payload().ManufacturerData()
	if len(m) != 1 || m[0].CompanyID != 0xEC88 || hex.EncodeToString(m[0].Data) != "00f8099f1c6402" {
		t.Errorf("replayed payload manufacturer data = %+v", m)
	}
}

func TestDecodeUnknownFrame(t *testing.T) {
	e := Event{
		Address:          fixedAddr,
		Identity:         "02:00:00:00:00:01",
		ManufacturerData: []MfgData{{CompanyID: 0xb5b5, Data: "aabbccdd"}},
	}
	if r := Decode(e); r != nil {
		t.Errorf("unknown frame decoded as %+v, want nil", r)
	}
}

// A macOS capture has no sender MAC; the ThermoBeacon decoder must fall back
// to the company-id path when replaying it, and report the embedded MAC.
func TestDecodeThermoBeaconWithoutSenderMAC(t *testing.T) {
	e := Event{
		Address:          fixedAddr,
		Identity:         "ba:e2:52:36:ff:28",
		ManufacturerData: []MfgData{{CompanyID: 0x0010, Data: hex.EncodeToString(thbeaconFixture)}},
	}
	r := Decode(e)
	if r == nil {
		t.Fatal("thermobeacon capture not decoded")
	}
	if r.Source != "thermobeacon" {
		t.Errorf("Source = %q", r.Source)
	}
	if r.MAC != "db:5c:8d:a0:0f:21" {
		t.Errorf("MAC = %q, want the embedded MAC", r.MAC)
	}
}

func TestWriterReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	e1 := FromAdvertisement(fixedAddr, -35, &fakePayload{
		name: "Govee_H5074_88DE",
		mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealCapture}},
	})
	e2 := Event{
		Address:     "aa:bb:cc:dd:ee:ff",
		SenderMAC:   "aa:bb:cc:dd:ee:ff",
		Identity:    "aa:bb:cc:dd:ee:ff",
		Name:        "BTHome",
		RSSI:        -70,
		ServiceData: []SvcData{{UUID: "0000fcd2-0000-1000-8000-00805f9b34fb", Data: "4045d700"}},
	}
	if err := w.Write(e1); err != nil {
		t.Fatalf("write e1: %v", err)
	}
	if err := w.Write(e2); err != nil {
		t.Fatalf("write e2: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	events, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("read %d events, want 2", len(events))
	}
	if events[0].Address != fixedAddr || events[0].Name != "Govee_H5074_88DE" ||
		len(events[0].ManufacturerData) != 1 ||
		events[0].ManufacturerData[0].Data != hex.EncodeToString(goveeRealCapture) {
		t.Errorf("event 0 = %+v", events[0])
	}
	if events[1].RSSI != -70 || len(events[1].ServiceData) != 1 ||
		events[1].ServiceData[0].UUID != "0000fcd2-0000-1000-8000-00805f9b34fb" {
		t.Errorf("event 1 = %+v", events[1])
	}

	// Replayed events decode exactly like live ones.
	if r := Decode(events[0]); r == nil || r.Temperature != 25.52 {
		t.Errorf("replayed event 0 not decoded: %+v", r)
	}
}

func TestReadSkipsCommentsAndBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	content := "// a hand-written note\n\n" +
		`{"address":"aa:bb:cc:dd:ee:ff","rssi":-50}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 || events[0].Address != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("events = %+v", events)
	}
}

func TestReadInvalidLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	if err := os.WriteFile(path, []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Error("invalid line accepted")
	}
}

func TestReadEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Error("empty file accepted")
	}
}

func TestPathFor(t *testing.T) {
	if got := PathFor("ba:e2:52:36:ff:28"); got != "capture-bae25236ff28.jsonl" {
		t.Errorf("PathFor(mac) = %q", got)
	}
	if got := PathFor("f1ae0297-b710-1440-2881-9274ad58590d"); got != "capture-f1ae0297-b710-1440-2881-9274ad58590d.jsonl" {
		t.Errorf("PathFor(uuid) = %q", got)
	}
	if got := PathFor("weird/id\\here"); !strings.HasPrefix(got, "capture-") ||
		strings.Contains(got, "/") || strings.Contains(got, "\\") {
		t.Errorf("PathFor sanitization = %q", got)
	}
}

// A standalone INTELLI_ROCKS blob captured as its own manufacturer data
// element must not crash the decoders (regression: negative slice panic).
func TestDecodeStandaloneIntelliRocksBlob(t *testing.T) {
	e := Event{
		Address:          fixedAddr,
		Identity:         "ba:e2:52:36:ff:28",
		Name:             "Govee_H5074_88DE",
		ManufacturerData: []MfgData{{CompanyID: 0x004C, Data: hex.EncodeToString(goveeIntelliRocksBlob[2:])}},
	}
	if r := Decode(e); r != nil {
		t.Errorf("standalone blob decoded as %+v, want nil", r)
	}
}
