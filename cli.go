package main

// Interactive terminal UI (`--cli`) built with Charm's Bubble Tea: it lists
// every Bluetooth device discovered while scanning, shows decoded readings
// (ThermoBeacon / Govee / BTHome) and lets you explore the last advertisement
// of a selected device - hex dump, field breakdown and the MAC address
// identity. Nothing is forwarded in this mode.

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	bthome "github.com/hybridgroup/go-bthome"
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

	width  int
	height int
}

func newCLIModel(adapter *bluetooth.Adapter) *cliModel {
	cols := []table.Column{
		{Title: "Address", Width: 38},
		{Title: "Name", Width: 20},
		{Title: "RSSI", Width: 5},
		{Title: "Company", Width: 16},
		{Title: "Reading", Width: 26},
		{Title: "Seen", Width: 7},
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
		// Sensible defaults until the first WindowSizeMsg arrives (real
		// terminals send their size right away).
		width:  100,
		height: 30,
	}
}

// runCLI enables the adapter and runs the interactive TUI until the user
// quits. Log output is discarded so it cannot garble the terminal.
//
// Threading matters here: cbgo services CoreBluetooth callbacks from the
// main thread, so the blocking adapter.Scan runs on this goroutine (the one
// that enabled the adapter) while the Tea program runs on its own goroutine
// and receives events through a channel.
func runCLI(cfg *Config) error {
	log.SetOutput(io.Discard)

	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("could not enable the BLE stack: %w", err)
	}

	m := newCLIModel(adapter)
	ch := make(chan bluetooth.ScanResult, 512)
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.scanCh = ch

	// The Tea event loop on its own goroutine; when the user quits, stop the
	// scan so the blocking Scan below returns.
	progDone := make(chan error, 1)
	go func() {
		_, err := p.Run()
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
	return cliTick()
}

func cliTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return cliTickMsg(t)
	})
}

func (m *cliModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case cliTickMsg:
		m.refreshRows()
		return m, cliTick()

	case scanMsg:
		m.updateDevice(msg.result)
		m.refreshRows()
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
			return m, nil
		case "esc":
			m.detail = false
			return m, nil
		}
		// Route navigation keys to the focused widget.
		var cmd tea.Cmd
		if m.detail {
			m.viewport, cmd = m.viewport.Update(msg)
		} else {
			m.table, cmd = m.table.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m *cliModel) View() string {
	title := cliTitleStyle.Render("thermobeacon-ble-go — bluetooth devices")

	var body string
	if m.detail {
		body = cliDetailStyle.Render(m.viewport.View())
	} else {
		body = m.table.View()
	}

	status := fmt.Sprintf("%d devices, %d with decoded readings, scanning",
		len(m.order), m.decodedCount())
	hint := "↑/↓ navigate • enter: explore payload • esc: back • q: quit • devices appear as their advertisement data changes"

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		body,
		cliBarStyle.Render(status),
		cliBarStyle.Render(hint),
	)
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

// updateDevice folds one advertisement into the device list.
func (m *cliModel) updateDevice(d bluetooth.ScanResult) {
	addr := d.Address.String()
	dev, ok := m.devices[addr]
	if !ok {
		dev = &cliDevice{
			addr:      addr,
			mac:       identityFor(senderMACOf(addr), addr),
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
	senderMAC := senderMACOf(d.Address.String())

	for _, mfg := range d.ManufacturerData() {
		if r, err := decodeThermoBeacon(senderMAC, mfg.CompanyID, mfg.Data); err == nil {
			return readingSummary(r), r.MAC
		}
		if r, err := decodeGovee(mac, d.LocalName(), mfg.CompanyID, mfg.Data); err == nil {
			return readingSummary(r), r.MAC
		}
	}

	for _, sd := range d.ServiceData() {
		if sd.UUID != bthome.ServiceUUID {
			continue
		}
		if r, err := decodeBTHome(mac, sd.Data); err == nil && r != nil {
			return readingSummary(r), r.MAC
		}
	}
	return "", ""
}

// readingSummary renders a Reading as a compact one line summary.
func readingSummary(r *Reading) string {
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

// refreshRows rebuilds the table rows from the device list.
func (m *cliModel) refreshRows() {
	rows := make([]table.Row, 0, len(m.order))
	for _, addr := range m.order {
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
	sb.WriteString(dumpScanResultText(d.last, senderMACOf(d.addr), false))

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

func senderMACOf(addr string) string {
	if isMACAddress(addr) {
		if n, err := normalizeMAC(addr); err == nil {
			return n
		}
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
