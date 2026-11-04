// Package cli implements the interactive terminal UI (--cli) built with
// Charm's Bubble Tea: it lists every Bluetooth device discovered while
// scanning, shows decoded readings (ThermoBeacon / Govee / BTHome) and lets
// you explore the last advertisement of a selected device - hex dump, field
// breakdown and the MAC address identity. Nothing is forwarded in this
// mode.
package cli

import (
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/prune998/thermobeacon-ble-go/internal/capture"
	"github.com/prune998/thermobeacon-ble-go/internal/decoders"
	"github.com/prune998/thermobeacon-ble-go/internal/dump"
	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
	"tinygo.org/x/bluetooth"
)

var (
	cliTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Padding(0, 1)

	cliBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(0, 1)

	cliDetailStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	cliKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)
)

// cliColumn is one column of the device table.
type cliColumn struct {
	title string
	width int
}

// Column indexes (for the sort marker).
const (
	cliColAddress = iota
	cliColName
	cliColRSSI
	cliColCompany
	cliColReading
	cliColSeen
	cliColCount
)

var cliCols = []cliColumn{
	{"Address", 38},
	{"Name", 20},
	{"RSSI", 5},
	{"Company", 16},
	{"Reading", 26},
	{"Seen", 7},
}

// cliSortMode is one sort order for the device list. col is the table column
// whose header gets the bold marker (-1 for the default discovery order).
type cliSortMode struct {
	name string
	col  int
	less func(a, b *cliDevice) bool
}

var cliSortModes = []cliSortMode{
	{name: "discovery order", col: -1},
	{name: "address", col: cliColAddress, less: func(a, b *cliDevice) bool { return a.addr < b.addr }},
	{name: "name", col: cliColName, less: func(a, b *cliDevice) bool {
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	}},
	{name: "RSSI", col: cliColRSSI, less: func(a, b *cliDevice) bool { return a.rssi > b.rssi }},
	{name: "company", col: cliColCompany, less: func(a, b *cliDevice) bool {
		return cliCompanies(a.companies) < cliCompanies(b.companies)
	}},
	{name: "reading", col: cliColReading, less: func(a, b *cliDevice) bool {
		if (a.reading == "") != (b.reading == "") {
			return b.reading == "" // devices with readings first
		}
		return a.reading > b.reading
	}},
	{name: "last seen", col: cliColSeen, less: func(a, b *cliDevice) bool {
		return a.lastSeen.After(b.lastSeen)
	}},
}

// cli messages.

type scanMsg struct {
	result bluetooth.ScanResult
}

type cliTickMsg time.Time

// cliDevice is one discovered device, continuously updated.
type cliDevice struct {
	addr       string // platform address (UUID on macOS, MAC on Linux)
	mac        string // identity: advertiser MAC or stable pseudo MAC
	name       string
	rssi       int16
	companies  []uint16 // distinct company ids seen, in order
	reading    string   // decoded summary, "" when unknown
	readingMAC string   // sensor MAC reported by a decoded payload (ThermoBeacon)
	firstSeen  time.Time
	lastSeen   time.Time
	count      int
	last       bluetooth.ScanResult // last advertisement, for the detail view
}

// cliModel implements tea.Model (pointer receivers).
type cliModel struct {
	adapter *bluetooth.Adapter
	scanCh  chan bluetooth.ScanResult

	devices map[string]*cliDevice
	order   []string // insertion order, stable across updates

	table    table.Model
	viewport viewport.Model
	detail   bool
	sortIdx  int // index into cliSortModes

	refresh time.Duration // display refresh interval (--cli)
	cached  string        // last rendered view; rebuilt on tick/keys only

	captureWriter *capture.Writer
	captureAddr   string
	capturePath   string
	captureCount  int
	captureErr    error

	width  int
	height int
}

func newCLIModel(adapter *bluetooth.Adapter, refresh time.Duration) *cliModel {
	cols := make([]table.Column, len(cliCols))
	for i, c := range cliCols {
		cols[i] = table.Column{Title: c.title, Width: c.width}
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithHeight(12),
	)
	styles := table.DefaultStyles()
	styles.Header = styles.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	styles.Selected = styles.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(styles)

	return &cliModel{
		adapter: adapter,
		devices: map[string]*cliDevice{},
		table:   t,
		refresh: clampRefresh(refresh),
		// Sensible defaults until the first WindowSizeMsg arrives (real
		// terminals send their size right away).
		width:  100,
		height: 30,
	}
}

// clampRefresh keeps the display refresh interval usable: never zero (that
// would busy-loop the renderer) and never faster than 250 ms.
func clampRefresh(d time.Duration) time.Duration {
	const min = 250 * time.Millisecond
	if d < min {
		return min
	}
	return d
}

// Run enables the adapter and runs the interactive TUI until the user
// quits. Log output is discarded so it cannot garble the terminal.
//
// Threading matters here: cbgo services CoreBluetooth callbacks from the
// main thread, so the blocking adapter.Scan runs on this goroutine (the one
// that enabled the adapter) while the Tea program runs on its own goroutine
// and receives events through a channel.
func Run(refresh time.Duration) error {
	log.SetOutput(io.Discard)

	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("could not enable the BLE stack: %w", err)
	}

	m := newCLIModel(adapter, refresh)
	ch := make(chan bluetooth.ScanResult, 512)
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.scanCh = ch

	// The Tea event loop on its own goroutine; when the user quits, stop the
	// scan so the blocking Scan below returns.
	progDone := make(chan error, 1)
	go func() {
		_, err := p.Run()
		m.closeCapture()
		adapter.StopScan()
		progDone <- err
	}()

	// Forward advertisements from the scan to the program.
	go func() {
		for d := range ch {
			p.Send(scanMsg{result: d})
		}
	}()

	// Blocking scan: never stop the BLE callback thread (drop instead).
	err := adapter.Scan(func(_ *bluetooth.Adapter, d bluetooth.ScanResult) {
		select {
		case ch <- d:
		default:
		}
	})
	close(ch)
	if e := <-progDone; e != nil {
		return e
	}
	return err
}

// Init starts the refresh ticker that keeps the "Seen" column current.
func (m *cliModel) Init() tea.Cmd {
	return m.cliTick()
}

func (m *cliModel) cliTick() tea.Cmd {
	return tea.Tick(m.refresh, func(t time.Time) tea.Msg {
		return cliTickMsg(t)
	})
}

func (m *cliModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		m.rebuildView()
		return m, nil

	case cliTickMsg:
		m.refreshRows()
		m.rebuildView()
		return m, m.cliTick()

	case scanMsg:
		// Absorb the data now; the visible list only refreshes on the tick
		// (or on key presses), per the -refresh interval.
		m.updateDevice(msg.result)
		if m.captureWriter != nil && msg.result.Address.String() == m.captureAddr {
			m.captureAdvertisement(msg.result.Address.String(), msg.result)
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter", "d":
			if m.detail {
				m.detail = false
			} else {
				m.openDetail()
			}
		case "c":
			m.toggleCapture()
		case "s":
			m.sortIdx = (m.sortIdx + 1) % len(cliSortModes)
			m.refreshRows()
		case "esc":
			m.detail = false
		}
		// Route navigation keys to the focused widget, then rebuild the
		// cached view so every key action is visible immediately.
		var cmd tea.Cmd
		if m.detail {
			m.viewport, cmd = m.viewport.Update(msg)
		} else {
			m.table, cmd = m.table.Update(msg)
		}
		m.rebuildView()
		return m, cmd
	}
	return m, nil
}

// View returns the cached view, rebuilt only on ticks and key presses - so
// the display refreshes at the -refresh interval, not per advertisement.
func (m *cliModel) View() string {
	if m.cached == "" {
		m.rebuildView()
	}
	return m.cached
}

// rebuildView renders the current state into the cached view.
func (m *cliModel) rebuildView() {
	title := cliTitleStyle.Render("thermobeacon-ble-go — bluetooth devices")

	var body string
	if m.detail {
		body = cliDetailStyle.Render(m.viewport.View())
	} else {
		body = applySortMarker(m.table.View(), cliSortModes[m.sortIdx].col)
	}

	status := fmt.Sprintf("%d devices, %d with decoded readings, scanning • sorted by %s",
		len(m.order), m.decodedCount(), cliSortModes[m.sortIdx].name)
	if m.captureWriter != nil {
		status += fmt.Sprintf(" • ● capturing %s → %s (%d events)",
			m.captureAddr, m.capturePath, m.captureCount)
	}
	if m.captureErr != nil {
		status += fmt.Sprintf(" • capture error: %v", m.captureErr)
	}
	hint := "↑/↓ navigate • enter: explore payload • s: sort • c: capture • esc: back • q: quit • devices appear as their advertisement data changes"

	m.cached = lipgloss.JoinVertical(lipgloss.Left,
		title,
		body,
		cliBarStyle.Render(status),
		cliBarStyle.Render(hint),
	)
}

// captureAdvertisement appends one advertisement to the active capture file.
func (m *cliModel) captureAdvertisement(addr string, d bluetooth.ScanResult) {
	if err := m.captureWriter.Write(capture.FromScanResult(d)); err != nil {
		m.captureErr = err
	} else {
		m.captureCount++
	}
}

// toggleCapture starts or stops capturing the selected device's
// advertisements to a JSON Lines file (capture-<address>.jsonl) for replay.
func (m *cliModel) toggleCapture() {
	if m.captureWriter != nil {
		m.closeCapture()
		return
	}
	dev := m.selectedDevice()
	if dev == nil {
		return
	}
	path := capture.PathFor(dev.addr)
	w, err := capture.NewWriter(path)
	if err != nil {
		m.captureErr = err
		return
	}
	m.captureErr = nil
	m.captureWriter = w
	m.captureAddr = dev.addr
	m.capturePath = path
	m.captureCount = 0
}

// closeCapture stops an active capture and closes its file.
func (m *cliModel) closeCapture() {
	if m.captureWriter != nil {
		_ = m.captureWriter.Close()
		m.captureWriter = nil
		m.captureAddr = ""
		m.capturePath = ""
	}
}

// applySortMarker bolds the header cell of the sorted column in a rendered
// table view. The table renders each header cell as padding(0,1) around a
// width-padded title, so the cell boundaries are known from cliCols.
func applySortMarker(view string, col int) string {
	if col < 0 || col >= len(cliCols) {
		return view
	}
	lines := strings.SplitN(view, "\n", 3)
	if len(lines) < 2 {
		return view
	}
	header := lines[0]
	offset := 0
	for i, c := range cliCols {
		start := offset
		end := offset + c.width + 2 // padding (0, 1) per side
		if end > len(header) {
			return view
		}
		if i == col {
			cell := header[start:end]
			bolded := lipgloss.NewStyle().Bold(true).Render(strings.TrimRight(cell, " ")) +
				strings.Repeat(" ", len(cell)-len(strings.TrimRight(cell, " ")))
			lines[0] = header[:start] + bolded + header[end:]
			return strings.Join(lines, "\n")
		}
		offset = end
	}
	return view
}

func (m *cliModel) decodedCount() int {
	n := 0
	for _, addr := range m.order {
		if m.devices[addr].reading != "" {
			n++
		}
	}
	return n
}

// resize adapts the table and viewport to the terminal size.
func (m *cliModel) resize() {
	m.table.SetWidth(m.width)
	m.table.SetHeight(max(6, m.height-4))
	m.viewport.Width = max(20, m.width-4)
	m.viewport.Height = max(6, m.height-4)
}

// updateDevice folds one advertisement into the device list, keyed by the
// platform address.
func (m *cliModel) updateDevice(d bluetooth.ScanResult) {
	m.upsertDevice(d.Address.String(), d)
}

// upsertDevice folds one advertisement into the device list under the given
// address. Split from updateDevice so tests can use arbitrary addresses
// without depending on how the platform parses them.
func (m *cliModel) upsertDevice(addr string, d bluetooth.ScanResult) {
	dev, ok := m.devices[addr]
	if !ok {
		dev = &cliDevice{
			addr:      addr,
			mac:       sensor.IdentityFor(sensor.SenderMAC(addr), addr),
			firstSeen: time.Now(),
		}
		m.devices[addr] = dev
		m.order = append(m.order, addr)
	}
	dev.count++
	dev.lastSeen = time.Now()
	dev.last = d
	dev.rssi = d.RSSI
	if name := d.LocalName(); name != "" {
		dev.name = name
	}

	for _, mfg := range d.ManufacturerData() {
		seen := false
		for _, id := range dev.companies {
			if id == mfg.CompanyID {
				seen = true
				break
			}
		}
		if !seen {
			dev.companies = append(dev.companies, mfg.CompanyID)
		}
	}

	dev.reading, dev.readingMAC = cliReading(d, dev.mac)
	if dev.readingMAC == "" {
		dev.readingMAC = dev.mac
	}
}

// cliReading decodes the advertisement with the same precedence as the
// bridge (ThermoBeacon, then Govee, then BTHome) and returns a one line
// summary plus the sensor MAC reported by the payload ("" when unknown), or
// ("", "") when nothing matches.
func cliReading(d bluetooth.ScanResult, mac string) (string, string) {
	senderMAC := sensor.SenderMAC(d.Address.String())

	for _, mfg := range d.ManufacturerData() {
		if r, err := decoders.ThermoBeacon(senderMAC, mfg.CompanyID, mfg.Data); err == nil {
			return readingSummary(r), r.MAC
		}
		if r, err := decoders.Govee(mac, d.LocalName(), mfg.CompanyID, mfg.Data); err == nil {
			return readingSummary(r), r.MAC
		}
	}

	for _, sd := range d.ServiceData() {
		if sd.UUID != decoders.BTHomeServiceUUID {
			continue
		}
		if r, err := decoders.BTHome(mac, sd.Data); err == nil && r != nil {
			return readingSummary(r), r.MAC
		}
	}
	return "", ""
}

// readingSummary renders a Reading as a compact one line summary.
func readingSummary(r *sensor.Reading) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%.1f°C %.1f%%", r.Temperature, r.Humidity)
	if r.BatteryPct >= 0 {
		fmt.Fprintf(&sb, " %d%%", r.BatteryPct)
	} else if r.VoltageMV > 0 {
		fmt.Fprintf(&sb, " %.2fV", float64(r.VoltageMV)/1000)
	}
	if r.Button {
		sb.WriteString(" [btn]")
	}
	if r.BatteryLow {
		sb.WriteString(" [low]")
	}
	return sb.String()
}

// sortedOrder returns the device addresses in the current sort order.
func (m *cliModel) sortedOrder() []string {
	order := append([]string(nil), m.order...)
	mode := cliSortModes[m.sortIdx]
	if mode.less != nil {
		sort.SliceStable(order, func(i, j int) bool {
			return mode.less(m.devices[order[i]], m.devices[order[j]])
		})
	}
	return order
}

// refreshRows rebuilds the table rows from the device list.
func (m *cliModel) refreshRows() {
	order := m.sortedOrder()
	rows := make([]table.Row, 0, len(order))
	for _, addr := range order {
		d := m.devices[addr]
		rows = append(rows, table.Row{
			d.addr,
			orDash(d.name),
			fmt.Sprintf("%d", d.rssi),
			orDash(cliCompanies(d.companies)),
			orDash(d.reading),
			time.Since(d.lastSeen).Truncate(time.Second).String(),
		})
	}
	m.table.SetRows(rows)
}

func (m *cliModel) openDetail() {
	row := m.table.SelectedRow()
	if row == nil {
		return
	}
	dev, ok := m.devices[row[0]]
	if !ok {
		return
	}
	m.viewport.SetContent(m.detailText(dev))
	m.viewport.GotoTop()
	m.detail = true
}

// detailText renders the identity block plus the annotated dump of the last
// advertisement for one device.
func (m *cliModel) detailText(d *cliDevice) string {
	var sb strings.Builder

	row := func(key, value string) {
		sb.WriteString(cliKeyStyle.Render(fmt.Sprintf("%-10s", key)) + " " + value + "\n")
	}
	row("Address:", d.addr)
	row("Name:", orDash(d.name))
	row("MAC:", d.mac)
	if d.readingMAC != "" && d.readingMAC != d.mac {
		row("Sensor MAC:", d.readingMAC+" (reported by the payload)")
	}
	row("Company:", orDash(cliCompanies(d.companies)))
	row("Reading:", orDash(d.reading))
	row("RSSI:", fmt.Sprintf("%d dBm (last)", d.rssi))
	row("Seen:", fmt.Sprintf("%d advertisements, first %s, last %s",
		d.count,
		d.firstSeen.Format("15:04:05"),
		d.lastSeen.Format("15:04:05")))
	row("Updated:", time.Since(d.lastSeen).Truncate(time.Second).String())

	sb.WriteString("\n--- last advertisement ---\n\n")
	sb.WriteString(dump.ScanResultText(d.last, sensor.SenderMAC(d.addr), false))

	return sb.String()
}

func (m *cliModel) selectedDevice() *cliDevice {
	row := m.table.SelectedRow()
	if row == nil {
		return nil
	}
	return m.devices[row[0]]
}

func cliCompanies(ids []uint16) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("0x%04x", id))
	}
	if len(parts) > 3 {
		parts = append(parts[:3], fmt.Sprintf("+%d more", len(ids)-3))
	}
	return strings.Join(parts, ",")
}
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
