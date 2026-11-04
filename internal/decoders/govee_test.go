package decoders

import (
	"bytes"
	"errors"
	"testing"
)

// H5074 payload (after the company id): frame byte 01, temperature 22.59 degC
// (2259, little endian), humidity 45.67 % (4567, little endian), battery 88 %,
// trailing byte 0a.
var goveeSampleH5074 = []byte{0x01, 0xD3, 0x08, 0xD7, 0x11, 0x58, 0x0A}

// The 25-byte INTELLI_ROCKS blob some Govee firmwares glue onto the end of
// their manufacturer data.
var goveeIntelliRocksBlob = []byte{
	0x00, 0x4C, 0x02, 0x15,
	'I', 'N', 'T', 'E', 'L', 'L', 'I', '_', 'R', 'O', 'C', 'K', 'S', '_', 'H', 'W',
	0x9F, 0xF2, 0xFF, 0xC2, 0x01,
}

// Real capture from GoveeBTTempLogger (2024-08-12, Govee_H5074_5C0F):
// ec88:00f8099f1c6402 -> 25.52 degC, 73.27 %, battery 100 %.
var goveeRealH5074 = []byte{0x00, 0xF8, 0x09, 0x9F, 0x1C, 0x64, 0x02}

func TestDecodeGoveeH5074(t *testing.T) {
	r, err := Govee("aa:bb:cc:dd:ee:ff", "Govee_H5074", GoveeCompanyID, goveeSampleH5074)
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
	r, err := Govee("", "Govee_H5074", GoveeCompanyID, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Temperature != -5.32 {
		t.Errorf("Temperature = %v, want -5.32", r.Temperature)
	}
}

// The Govee firmware glues the INTELLI_ROCKS blob onto the measurement
// payload; without stripping it, the frame is rejected as unknown.

func TestDecodeGoveeH5074IntelliRocksGlued(t *testing.T) {
	glued := append(append([]byte(nil), goveeRealH5074...), goveeIntelliRocksBlob...)
	if len(glued) != 7+25 {
		t.Fatalf("glued payload = %d bytes, want 32", len(glued))
	}
	r, err := Govee("e0:12:1d:61:bb:aa", "Govee_H5074_5C0F", GoveeCompanyID, glued)
	if err != nil {
		t.Fatalf("glued payload rejected: %v", err)
	}
	if r.Temperature != 25.52 {
		t.Errorf("Temperature = %v, want 25.52", r.Temperature)
	}
	if r.Humidity != 73.27 {
		t.Errorf("Humidity = %v, want 73.27", r.Humidity)
	}
	if r.BatteryPct != 100 {
		t.Errorf("BatteryPct = %v, want 100", r.BatteryPct)
	}
}

// The real capture from GoveeBTTempLogger must decode with the same values it
// reports.

func TestDecodeGoveeH5074RealCapture(t *testing.T) {
	r, err := Govee("", "Govee_H5074_5C0F", GoveeCompanyID, goveeRealH5074)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Temperature != 25.52 || r.Humidity != 73.27 || r.BatteryPct != 100 {
		t.Errorf("Reading = %.2f degC, %.2f %%, %d %%, want 25.52 / 73.27 / 100",
			r.Temperature, r.Humidity, r.BatteryPct)
	}
}

// A blob without a measurement payload in front of it is not a Govee frame.

func TestDecodeGoveeIntelliRocksOnly(t *testing.T) {
	if _, err := Govee("", "Govee_H5074", GoveeCompanyID, goveeIntelliRocksBlob); err == nil {
		t.Error("bare INTELLI_ROCKS blob accepted")
	}
}

// Newer firmware broadcasts encrypted "v3" frames that carry no decodable
// values; they must be identified instead of being reported as unknown.

func TestDecodeGoveeEncryptedV3(t *testing.T) {
	// company id 0x8803 (a flags byte Govee puts after the AD type), 24-byte
	// payload with the EC 00 marker
	data := append([]byte{0xEC, 0x00, 0x01, 0x01, 0x12, 0x34}, make([]byte, 20)...)
	_, err := Govee("", "Govee_H5074", 0x8803, data)
	if !errors.Is(err, ErrGoveeEncrypted) {
		t.Errorf("v3 frame by company id: err = %v, want ErrGoveeEncrypted", err)
	}
	// the EC 00 marker alone also identifies the frame
	_, err = Govee("", "", 0xEC88, append([]byte{0xEC, 0x00}, make([]byte, 10)...))
	if !errors.Is(err, ErrGoveeEncrypted) {
		t.Errorf("v3 frame by marker: err = %v, want ErrGoveeEncrypted", err)
	}
	// real measurement payloads never start with EC 00
	if _, err = Govee("", "Govee_H5074", GoveeCompanyID, goveeRealH5074); err != nil {
		t.Errorf("classic frame misidentified as v3: %v", err)
	}
}

// Frames that are longer than the canonical layout (unknown trailing vendor
// data) still decode when the model is known from the local name.

func TestDecodeGoveePrefixFallback(t *testing.T) {
	junk := []byte{0x03, 0x88, 0xEC, 0x00, 0x01, 0x01, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	glued := append(append([]byte(nil), goveeRealH5074...), junk...)
	r, err := Govee("", "Govee_H5074_5C0F", GoveeCompanyID, glued)
	if err != nil {
		t.Fatalf("prefix fallback failed: %v", err)
	}
	if r.Temperature != 25.52 || r.Humidity != 73.27 || r.BatteryPct != 100 {
		t.Errorf("Reading = %.2f / %.2f / %d, want 25.52 / 73.27 / 100",
			r.Temperature, r.Humidity, r.BatteryPct)
	}
}

// The prefix fallback must not report garbage: a frame whose leading bytes
// decode to an impossible humidity is rejected.

func TestDecodeGoveePrefixRejectsGarbage(t *testing.T) {
	data := bytes.Repeat([]byte{0xFF}, 30)
	if _, err := Govee("", "Govee_H5074", GoveeCompanyID, data); err == nil {
		t.Error("garbage prefix accepted")
	}
}

func TestDecodeGoveeH5074OutOfRange(t *testing.T) { // 150.00 degC is outside the -40..100 window govee-ble uses
	data := []byte{0x01, 0x98, 0x3A, 0xD7, 0x11, 0x58, 0x0A}
	if _, err := Govee("", "Govee_H5074", GoveeCompanyID, data); err == nil {
		t.Error("out of range temperature accepted")
	}
}

// The H5074 branch also matches by local name alone (some firmwares use a
// different company id); without the name, unknown company ids are rejected.

func TestDecodeGoveeH5074ByName(t *testing.T) {
	if _, err := Govee("", "Govee_H5074", 0x1234, goveeSampleH5074); err != nil {
		t.Errorf("name match failed: %v", err)
	}
	if _, err := Govee("", "", 0x1234, goveeSampleH5074); err == nil {
		t.Error("unknown company id and no name accepted")
	}
}

// H5075 layout: packed 24 bit value 234567 = 23.4 degC and 56.7 %, battery 88.

func TestDecodeGoveeH5075(t *testing.T) {
	data := []byte{0x01, 0x03, 0x94, 0x47, 0x58, 0xAA}
	r, err := Govee("", "", GoveeCompanyID, data)
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
	r, err := Govee("", "Govee_H5075", GoveeCompanyID, data)
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
	if _, err := Govee("", "", GoveeCompanyID, data); err == nil {
		t.Error("error bit accepted")
	}
}

// H51xx family (H5101/H5102/H5104/H5108/...): company id 0x0001, 8 bytes.

func TestDecodeGoveeH51xxFamily(t *testing.T) {
	data := []byte{0x01, 0x00, 0x03, 0x49, 0x95, 0x64, 0x00, 0x00}
	r, err := Govee("", "", GoveeCompanyIDAlt, data)
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
	if _, err := Govee("", "", 0xEC88, make([]byte, 9)); err == nil {
		t.Error("9 byte payload accepted")
	}
	if _, err := Govee("", "", 0x004C, make([]byte, 8)); err == nil {
		t.Error("Apple company id with 8 bytes accepted")
	}
}
