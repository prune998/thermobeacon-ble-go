package main

import (
	"regexp"
	"strings"
	"testing"

	"tinygo.org/x/bluetooth"

	bthome "github.com/hybridgroup/go-bthome"
)

// ansiRe strips terminal styling from rendered strings in assertions.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestCLIReadingSummary(t *testing.T) {
	// ThermoBeacon (macOS path: no sender MAC, known company id) - the
	// summary shows voltage and the payload's embedded MAC is returned.
	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Oria",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0x0010, Data: sampleThermoBeacon}},
		},
	}
	summary, readingMAC := cliReading(res, "ba:e2:52:36:ff:28")
	if summary != "19.2°C 58.0% 2.88V [btn]" {
		t.Errorf("summary = %q, want %q", summary, "19.2°C 58.0% 2.88V [btn]")
	}
	if readingMAC != "db:5c:8d:a0:0f:21" {
		t.Errorf("readingMAC = %q, want the embedded MAC", readingMAC)
	}

	// Govee real capture: battery percentage instead of voltage.
	res.AdvertisementPayload = &fakePayload{
		name: "Govee_H5074_88DE",
		mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: goveeCompanyIDH50xx, Data: goveeRealH5074}},
	}
	summary, readingMAC = cliReading(res, "ba:e2:52:36:ff:28")
	if summary != "25.5°C 73.3% 100%" {
		t.Errorf("summary = %q, want %q", summary, "25.5°C 73.3% 100%")
	}
	if readingMAC != "ba:e2:52:36:ff:28" {
		t.Errorf("readingMAC = %q, want the identity MAC", readingMAC)
	}

	// BTHome service data.
	res.AdvertisementPayload = &fakePayload{
		svc: []bluetooth.ServiceDataElement{{UUID: bthome.ServiceUUID, Data: sampleBTHome}},
	}
	summary, _ = cliReading(res, "aa:bb:cc:dd:ee:ff")
	if summary != "21.5°C 44.5% 87% [low]" {
		t.Errorf("summary = %q, want %q", summary, "21.5°C 44.5% 87% [low]")
	}

	// Unknown payload: empty summary.
	res.AdvertisementPayload = &fakePayload{
		mfg: []bluetooth.ManufacturerDataElement{{CompanyID: 0xb5b5, Data: make([]byte, 40)}},
	}
	if summary, _ = cliReading(res, ""); summary != "" {
		t.Errorf("unknown payload summary = %q, want empty", summary)
	}
}

func TestCLIUpdateDevice(t *testing.T) {
	m := newCLIModel(nil)

	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_88DE",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: goveeCompanyIDH50xx, Data: goveeRealH5074}},
		},
	}
	addr := "f1ae0297-b710-1440-2881-9274ad58590d"
	res.Address.Set(addr)
	m.updateDevice(res)
	res.RSSI = -40
	m.updateDevice(res)

	if len(m.order) != 1 {
		t.Fatalf("device list has %d entries, want 1", len(m.order))
	}
	dev := m.devices[addr]
	if dev.count != 2 {
		t.Errorf("count = %d, want 2", dev.count)
	}
	if dev.rssi != -40 {
		t.Errorf("rssi = %d, want -40 (last seen)", dev.rssi)
	}
	if dev.name != "Govee_H5074_88DE" {
		t.Errorf("name = %q", dev.name)
	}
	if dev.mac == "" {
		t.Error("identity MAC not set")
	}
	if len(dev.companies) != 1 || dev.companies[0] != goveeCompanyIDH50xx {
		t.Errorf("companies = %v", dev.companies)
	}
	if dev.reading == "" {
		t.Error("reading summary missing")
	}

	// A second device keeps the insertion order stable.
	res2 := bluetooth.ScanResult{
		RSSI: -60,
		AdvertisementPayload: &fakePayload{
			mfg: []bluetooth.ManufacturerDataElement{{CompanyID: 0x0010, Data: sampleThermoBeacon}},
		},
	}
	res2.Address.Set("00000000-0000-0000-0000-000000000001")
	m.updateDevice(res2)
	if len(m.order) != 2 || m.order[0] != addr || m.order[1] != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("order = %v", m.order)
	}
}

func TestCLICompaniesAndDetail(t *testing.T) {
	if got := cliCompanies([]uint16{0x004c, 0xec88}); got != "0x004c,0xec88" {
		t.Errorf("cliCompanies = %q", got)
	}
	if got := cliCompanies([]uint16{1, 2, 3, 4, 5}); !strings.HasSuffix(got, "+2 more") {
		t.Errorf("cliCompanies truncation = %q", got)
	}

	m := newCLIModel(nil)
	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_88DE",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: goveeCompanyIDH50xx, Data: goveeRealH5074}},
		},
	}
	res.Address.Set("f1ae0297-b710-1440-2881-9274ad58590d")
	m.updateDevice(res)
	dev := m.devices["f1ae0297-b710-1440-2881-9274ad58590d"]

	detail := m.detailText(dev)
	// strip ANSI styling before matching text
	detail = ansiRe.ReplaceAllString(detail, "")
	for _, want := range []string{
		"Address:   f1ae0297-b710-1440-2881-9274ad58590d",
		"Name:      Govee_H5074_88DE",
		"MAC:",
		"last advertisement ---",
		"Govee H5074 decoded: 25.52 degC, 73.27 %, battery 100 %",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q\ngot:\n%s", want, detail)
		}
	}
}
