package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/prune998/thermobeacon-ble-go/internal/capture"
	"github.com/prune998/thermobeacon-ble-go/internal/decoders"
	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
	"tinygo.org/x/bluetooth"
)

// ansiRe strips terminal styling from rendered strings in assertions.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// addrFixture is the CoreBluetooth UUID of the test Govee sensor.
const addrFixture = "f1ae0297-b710-1440-2881-9274ad58590d"

// Test fixtures (duplicated per package; see internal/decoders tests).

var sampleThermoBeacon = []byte{0x80, 0x00, 0x21, 0x0F, 0xA0, 0x8D, 0x5C, 0xDB, 0x40, 0x0B, 0x34, 0x01, 0xA0, 0x03, 0x0F, 0x87, 0x20, 0x00}

var goveeRealH5074 = []byte{0x00, 0xF8, 0x09, 0x9F, 0x1C, 0x64, 0x02}

var sampleBTHome = []byte{
	0x40,
	0x45, 0xD7, 0x00,
	0x03, 0x62, 0x11,
	0x01, 0x57,
	0x15, 0x01,
	0x3D, 0x05, 0x00,
}

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
		mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealH5074}},
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
		svc: []bluetooth.ServiceDataElement{{UUID: decoders.BTHomeServiceUUID, Data: sampleBTHome}},
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
	m := newCLIModel(nil, time.Second)

	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_88DE",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealH5074}},
		},
	}
	addr := "f1ae0297-b710-1440-2881-9274ad58590d"
	m.upsertDevice(addr, res)
	res.RSSI = -40
	m.upsertDevice(addr, res)

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
	if len(dev.companies) != 1 || dev.companies[0] != decoders.GoveeCompanyID {
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
	m.upsertDevice("00000000-0000-0000-0000-000000000001", res2)
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

	m := newCLIModel(nil, time.Second)
	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_88DE",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealH5074}},
		},
	}
	m.upsertDevice("f1ae0297-b710-1440-2881-9274ad58590d", res)
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

func TestCaptureToggle(t *testing.T) {
	oldWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	m := newCLIModel(nil, time.Second)
	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_88DE",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealH5074}},
		},
	}
	m.upsertDevice(addrFixture, res)
	m.refreshRows()

	// press "c": capture starts for the selected device
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.captureWriter == nil {
		t.Fatal("capture did not start")
	}
	if m.captureAddr != addrFixture {
		t.Errorf("captureAddr = %q, want %q", m.captureAddr, addrFixture)
	}
	if m.capturePath != "capture-f1ae0297-b710-1440-2881-9274ad58590d.jsonl" {
		t.Errorf("capturePath = %q", m.capturePath)
	}

	// a matching advertisement is captured
	res.RSSI = -44
	m.captureAdvertisement(addrFixture, res)
	if m.captureCount != 1 {
		t.Errorf("captureCount = %d, want 1", m.captureCount)
	}

	capturePath := m.capturePath

	// press "c" again: capture stops, file keeps the events
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.captureWriter != nil {
		t.Error("capture still active after toggle off")
	}
	if _, err := os.Stat(capturePath); err != nil {
		t.Errorf("capture file missing: %v", err)
	}

	// the captured file contains the raw frame and replays through the
	// decoders
	events, err := capture.Read(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if len(events) != 1 || len(events[0].ManufacturerData) != 1 {
		t.Fatalf("captured events = %+v", events)
	}
	if r := capture.Decode(events[0]); r == nil || r.Temperature != 25.52 {
		t.Errorf("captured event not decodable: %+v", r)
	}
}

func TestCLISortCycling(t *testing.T) {
	m := newCLIModel(nil, time.Second)

	// Insert devices in "wrong" order for every sort key.
	devices := map[string]struct {
		addr, name string
		rssi       int16
	}{
		"c0:ff:ee:00:00:01": {"c0:ff:ee:00:00:01", "Zulu", -30},
		"aa:bb:cc:dd:ee:01": {"aa:bb:cc:dd:ee:01", "Alpha", -70},
		"bb:cc:dd:ee:ff:02": {"bb:cc:dd:ee:ff:02", "Mike", -50},
	}
	keys := []string{"c0:ff:ee:00:00:01", "aa:bb:cc:dd:ee:01", "bb:cc:dd:ee:ff:02"}
	for _, k := range keys {
		d := devices[k]
		res := bluetooth.ScanResult{
			RSSI: d.rssi,
			AdvertisementPayload: &fakePayload{
				name: d.name,
				mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0x0053, Data: []byte{0x01}}},
			},
		}
		res.Address.Set(k)
		m.upsertDevice(sensor.SenderMAC(k), res)
	}

	// discovery order (default): insertion order
	got := m.sortedOrder()
	want := []string{"c0:ff:ee:00:00:01", "aa:bb:cc:dd:ee:01", "bb:cc:dd:ee:ff:02"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("discovery order = %v, want %v", got, want)
		}
	}

	// address sort
	m.sortIdx = 1 // address
	got = m.sortedOrder()
	if !strings.HasPrefix(got[0], "aa:") || !strings.HasPrefix(got[2], "c0:") {
		t.Errorf("address order = %v", got)
	}

	// name sort: Alpha, Mike, Zulu
	m.sortIdx = 2
	got = m.sortedOrder()
	if len(got) != 3 {
		t.Fatalf("order = %v", got)
	}
	names := []string{
		m.devices[got[0]].name, m.devices[got[1]].name, m.devices[got[2]].name,
	}
	if !strings.EqualFold(strings.Join(names, ","), "Alpha,Mike,Zulu") {
		t.Errorf("name order = %v", names)
	}

	// RSSI sort: strongest first (-30, -50, -70)
	m.sortIdx = 3
	got = m.sortedOrder()
	rssis := []int16{
		m.devices[got[0]].rssi, m.devices[got[1]].rssi, m.devices[got[2]].rssi,
	}
	if rssis[0] != -30 || rssis[1] != -50 || rssis[2] != -70 {
		t.Errorf("rssi order = %v", rssis)
	}

	// cycling wraps back to discovery order
	m.sortIdx = (m.sortIdx + 1) % len(cliSortModes) // company
	m.sortIdx = (m.sortIdx + 1) % len(cliSortModes) // reading
	m.sortIdx = (m.sortIdx + 1) % len(cliSortModes) // last seen
	m.sortIdx = (m.sortIdx + 1) % len(cliSortModes) // wraps to discovery
	if m.sortIdx != 0 {
		t.Errorf("sortIdx = %d, want 0 after wrapping", m.sortIdx)
	}
	if got = m.sortedOrder(); got[0] != "c0:ff:ee:00:00:01" {
		t.Errorf("wrapped order = %v", got)
	}
}

func TestCLISortHeaderMarker(t *testing.T) {
	// force a color profile so the bold marker survives rendering (tests run
	// without a TTY, where lipgloss would strip all ANSI)
	r := lipgloss.DefaultRenderer()
	oldProfile := r.ColorProfile()
	r.SetColorProfile(termenv.TrueColor)
	defer r.SetColorProfile(oldProfile)

	m := newCLIModel(nil, time.Second)
	m.width, m.height = 140, 40
	m.resize()
	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_88DE",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealH5074}},
		},
	}
	m.upsertDevice(addrFixture, res)
	m.refreshRows()

	// discovery order: no bold marker in the header
	view := m.View()
	headerLine := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Address") && strings.Contains(line, "Reading") {
			headerLine = line
			break
		}
	}
	if headerLine == "" {
		t.Fatal("table header not found in view")
	}
	if strings.Contains(headerLine, "\x1b[1m") {
		t.Errorf("header bolded without a column sort:\n%s", headerLine)
	}

	// RSSI sort: the RSSI header cell is bold
	m.sortIdx = 3   // RSSI
	m.rebuildView() // a key press or tick would rebuild the cached view
	view = m.View()
	headerLine = ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Address") && strings.Contains(line, "Reading") {
			headerLine = line
			break
		}
	}
	if !strings.Contains(headerLine, "\x1b[1m") {
		t.Errorf("sorted header cell not bold:\n%s", headerLine)
	}
	stripped := ansiRe.ReplaceAllString(headerLine, "")
	if !strings.Contains(stripped, "RSSI") {
		t.Errorf("bold cell is not the RSSI column:\n%s", stripped)
	}

	// the status line announces the active sort
	if !strings.Contains(view, "sorted by RSSI") {
		t.Errorf("status line missing the sort announcement")
	}
}

func TestCLIRefreshClamp(t *testing.T) {
	if got := clampRefresh(0); got != 250*time.Millisecond {
		t.Errorf("clampRefresh(0) = %v, want 250ms", got)
	}
	if got := clampRefresh(-5 * time.Second); got != 250*time.Millisecond {
		t.Errorf("clampRefresh(-5s) = %v, want 250ms", got)
	}
	if got := clampRefresh(100 * time.Millisecond); got != 250*time.Millisecond {
		t.Errorf("clampRefresh(100ms) = %v, want 250ms (floor)", got)
	}
	if got := clampRefresh(5 * time.Second); got != 5*time.Second {
		t.Errorf("clampRefresh(5s) = %v, want 5s", got)
	}
}

// With a long refresh interval, new devices are absorbed into the data but
// the displayed view only updates on the tick.
// With a long refresh interval, new devices are absorbed into the data but
// the displayed view only updates on the tick.
func TestCLIRefreshThrottle(t *testing.T) {
	m := newCLIModel(nil, time.Hour) // effectively never ticks
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})

	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_88DE",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealH5074}},
		},
	}
	m.upsertDevice(addrFixture, res)
	m.refreshRows()
	m.rebuildView()

	if !strings.Contains(m.View(), "Govee_H5074_88DE") {
		t.Fatal("initial view missing the device")
	}

	// a second device arrives via scanMsg: absorbed into the data, but the
	// displayed view stays stale until the tick
	res2 := bluetooth.ScanResult{
		RSSI: -60,
		AdvertisementPayload: &fakePayload{
			name: "SecondDevice",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0x0059, Data: []byte{0x01}}},
		},
	}
	m.Update(scanMsg{result: res2})
	if strings.Contains(m.View(), "SecondDevice") {
		t.Error("view refreshed before the tick")
	}

	// the tick rebuilds the view
	m.Update(cliTickMsg(time.Now()))
	if !strings.Contains(m.View(), "SecondDevice") {
		t.Error("view not refreshed after the tick")
	}
	if !strings.Contains(m.View(), "2 devices, 1 with decoded readings") {
		t.Errorf("status not updated:\n%s", m.View())
	}
}

// Key presses always rebuild the view immediately, regardless of the refresh
// interval.
func TestCLIRefreshImmediateOnKey(t *testing.T) {
	m := newCLIModel(nil, time.Hour)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})

	res := bluetooth.ScanResult{
		RSSI: -50,
		AdvertisementPayload: &fakePayload{
			name: "Govee_H5074_88DE",
			mfg:  []bluetooth.ManufacturerDataElement{{CompanyID: 0xEC88, Data: goveeRealH5074}},
		},
	}
	m.upsertDevice(addrFixture, res)
	m.refreshRows()
	m.rebuildView()

	// opening the detail view renders immediately
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "last advertisement") {
		t.Errorf("detail view not rendered immediately on key press; view:\n%s", ansiRe.ReplaceAllString(m.View(), ""))
	}
}
