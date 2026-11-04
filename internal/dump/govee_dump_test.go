package dump

import (
	"strings"
	"testing"

	"github.com/prune998/thermobeacon-ble-go/internal/decoders"
	"tinygo.org/x/bluetooth"
)

func TestDumpScanResultTextGovee(t *testing.T) {
	res := bluetooth.ScanResult{
		RSSI: -60,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: decoders.GoveeCompanyID, Data: goveeSampleH5074}},
		},
	}
	text := ScanResultText(res, "", false)
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
			mfg: []bluetooth.ManufacturerDataElement{{CompanyID: decoders.GoveeCompanyID, Data: []byte{0x01, 0x03, 0x94, 0x47, 0x58, 0xAA}}},
		},
	}
	text := ScanResultText(res, "", false)
	for _, want := range []string{
		"Govee H5072/H5075 decoded: 23.40 degC, 56.70 %, battery 88 %",
		"temp+humid   03 94 47  -> mag 234567: 23.4 degC, 56.7 %",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("dump does not contain %q\ngot:\n%s", want, text)
		}
	}
}

func TestDumpScanResultTextGoveeIntelliRocks(t *testing.T) {
	// The glued blob must not appear as "not a payload": it is stripped and
	// the measurement decodes.
	glued := append(append([]byte(nil), goveeRealH5074...), goveeIntelliRocksBlob...)
	res := bluetooth.ScanResult{
		RSSI: -60,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_5C0F",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: decoders.GoveeCompanyID, Data: glued}},
		},
	}
	text := ScanResultText(res, "", false)
	if !strings.Contains(text, "Govee H5074 decoded: 25.52 degC, 73.27 %, battery 100 %") {
		t.Errorf("glued payload not decoded:\n%s", text)
	}
	if strings.Contains(text, "not a ThermoBeacon/Govee payload") {
		t.Errorf("glued payload still reported as unknown:\n%s", text)
	}

	// The INTELLI_ROCKS blob as its own manufacturer data element (BlueZ
	// delivers it separately on Linux) must be labelled as ignored.
	res.AdvertisementPayload = &fakePayload{
		name: "Govee_H5074_5C0F",
		mfg: []bluetooth.ManufacturerDataElement{
			{CompanyID: decoders.GoveeCompanyID, Data: goveeRealH5074},
			{CompanyID: decoders.CompanyIDApple, Data: goveeIntelliRocksBlob[2:]},
		},
	}
	text = ScanResultText(res, "", false)
	if !strings.Contains(text, "carrying Govee's glued INTELLI_ROCKS blob") {
		t.Errorf("Apple blob not labelled:\n%s", text)
	}

	// After the first explanation, repeat blobs stay out of the dump.
	text = ScanResultText(res, "", true)
	if strings.Contains(text, "INTELLI_ROCKS blob") {
		t.Errorf("repeat Apple blob still dumped:\n%s", text)
	}
	if !strings.Contains(text, "Govee H5074 decoded") {
		t.Errorf("measurement no longer decoded:\n%s", text)
	}

	// A plain Apple blob (iPhones and friends) is not INTELLI_ROCKS.
	res.AdvertisementPayload = &fakePayload{
		mfg: []bluetooth.ManufacturerDataElement{{CompanyID: decoders.CompanyIDApple, Data: []byte{0x10, 0x05, 0x4C, 0x65}}},
	}
	text = ScanResultText(res, "", false)
	if !strings.Contains(text, "(Apple), 4 bytes - ignored") {
		t.Errorf("plain Apple blob mislabelled:\n%s", text)
	}
	if strings.Contains(text, "INTELLI_ROCKS") {
		t.Errorf("plain Apple blob labelled as INTELLI_ROCKS:\n%s", text)
	}
}
