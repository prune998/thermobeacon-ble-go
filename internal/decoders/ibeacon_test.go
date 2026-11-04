package decoders

import (
	"testing"

	"tinygo.org/x/bluetooth"
)

// Classic example iBeacon: Apple company id payload = type 02, length 15,
// proximity UUID, major 1, minor 2, measured TX power -59.
var ibeaconFixture = []byte{
	0x02, 0x15,
	0xE2, 0xC5, 0x6D, 0xB5, 0xDF, 0xFB, 0x48, 0xD2,
	0xB0, 0x60, 0xD0, 0xF5, 0xA7, 0x10, 0x96, 0xE6,
	0x00, 0x01, 0x00, 0x02, 0xC5,
}

func TestIsIBeacon(t *testing.T) {
	if !IsIBeacon(CompanyIDApple, ibeaconFixture) {
		t.Error("classic iBeacon frame not detected")
	}
	// Apple continuity frame: Apple company id, but no 02 15 framing.
	continuity := []byte{0x10, 0x05, 0x4C, 0x65, 0x01}
	if IsIBeacon(CompanyIDApple, continuity) {
		t.Error("Apple continuity frame detected as iBeacon")
	}
	// Govee measurement frame: different company id.
	if IsIBeacon(GoveeCompanyID, goveeRealH5074) {
		t.Error("Govee measurement frame detected as iBeacon")
	}
	// wrong length: the 21-byte payload requires exactly 23 data bytes
	if IsIBeacon(CompanyIDApple, ibeaconFixture[:22]) {
		t.Error("truncated frame accepted")
	}
	if IsIBeacon(CompanyIDApple, append(append([]byte(nil), ibeaconFixture...), 0x00)) {
		t.Error("oversized frame accepted")
	}
	// wrong type/length prefix
	bad := append([]byte(nil), ibeaconFixture...)
	bad[0] = 0x05
	if IsIBeacon(CompanyIDApple, bad) {
		t.Error("wrong type byte accepted")
	}
}

func TestIsIBeaconAdvertisement(t *testing.T) {
	ibeaconOnly := []bluetooth.ManufacturerDataElement{
		{CompanyID: CompanyIDApple, Data: ibeaconFixture},
	}
	if !IsIBeaconAdvertisement(ibeaconOnly, nil) {
		t.Error("iBeacon-only advertisement not detected")
	}

	// Apple continuity element alongside the iBeacon: still all-Apple.
	mixed := append([]bluetooth.ManufacturerDataElement{
		{CompanyID: CompanyIDApple, Data: []byte{0x10, 0x05, 0x4C, 0x65}},
	}, ibeaconOnly...)
	if !IsIBeaconAdvertisement(mixed, nil) {
		t.Error("iBeacon + Apple continuity not detected")
	}

	// a non-Apple element means the advertisement is not pure iBeacon
	withGovee := []bluetooth.ManufacturerDataElement{
		{CompanyID: GoveeCompanyID, Data: goveeRealH5074},
		{CompanyID: CompanyIDApple, Data: ibeaconFixture},
	}
	if IsIBeaconAdvertisement(withGovee, nil) {
		t.Error("Govee + iBeacon mix detected as pure iBeacon")
	}

	// service data rules it out
	if IsIBeaconAdvertisement(ibeaconOnly, []bluetooth.ServiceDataElement{
		{UUID: BTHomeServiceUUID, Data: []byte{0x40}},
	}) {
		t.Error("advertisement with service data detected as iBeacon")
	}

	// Apple continuity only: not iBeacon
	if IsIBeaconAdvertisement([]bluetooth.ManufacturerDataElement{
		{CompanyID: CompanyIDApple, Data: []byte{0x10, 0x05, 0x4C, 0x65}},
	}, nil) {
		t.Error("Apple continuity only detected as iBeacon")
	}

	// empty
	if IsIBeaconAdvertisement(nil, nil) {
		t.Error("empty advertisement detected as iBeacon")
	}
}

// The Govee INTELLI_ROCKS blob is emitted in exactly the iBeacon framing -
// it is detected (and dropped) as an iBeacon.
func TestIsIBeaconDetectsIntelliRocksBlob(t *testing.T) {
	blob := goveeIntelliRocksBlob[2:] // drop the company id bytes: 23 bytes
	if !IsIBeacon(CompanyIDApple, blob) {
		t.Error("INTELLI_ROCKS blob not detected as iBeacon-framed")
	}
}
