// Package bridge ties everything together: it receives scan results, decodes
// ThermoBeacon, Govee and BTHome payloads, rate limits per sensor,
// re-encodes the readings as RuuviTag RAWv2 and forwards them to
// dbus-ble-sensors. It also owns the idle watchdog and the once-per-device
// diagnostics for unknown frames.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prune998/thermobeacon-ble-go/internal/capture"
	"github.com/prune998/thermobeacon-ble-go/internal/config"
	"github.com/prune998/thermobeacon-ble-go/internal/decoders"
	"github.com/prune998/thermobeacon-ble-go/internal/dump"
	"github.com/prune998/thermobeacon-ble-go/internal/logging"
	"github.com/prune998/thermobeacon-ble-go/internal/ruuvi"
	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
	"github.com/prune998/thermobeacon-ble-go/internal/store"
	"tinygo.org/x/bluetooth"
)

// packetSender hands an encoded UDP packet to the transport. Split into an
// interface so tests can capture packets.
type packetSender interface {
	send(pkt []byte) error
}

// Bridge decodes advertisements and forwards readings.
type Bridge struct {
	cfg     *config.Config
	store   *store.SeenStore
	sender  packetSender
	capture *capture.Writer

	mu            sync.Mutex
	lastSent      map[string]time.Time
	known         map[string]bool
	unknownLogged map[string]bool // devices already logged with an undecodable Govee frame
	appleLogged   map[string]bool // devices already told about the INTELLI_ROCKS blob
	lastAdv       time.Time
	idleWarned    bool
}

// New creates a Bridge for the given configuration and state store.
func New(cfg *config.Config, store *store.SeenStore) *Bridge {
	b := &Bridge{
		cfg:           cfg,
		store:         store,
		sender:        newUDPSender(cfg.UDPHost, cfg.UDPPort),
		lastSent:      map[string]time.Time{},
		known:         map[string]bool{},
		unknownLogged: map[string]bool{},
		appleLogged:   map[string]bool{},
		lastAdv:       time.Now(),
	}
	if cfg.Capture != "" {
		w, err := capture.NewWriter(cfg.Capture)
		if err != nil {
			log.Printf("WARNING: could not open capture file %s: %v", cfg.Capture, err)
		} else {
			b.capture = w
			log.Printf("capturing matching advertisements to %s", cfg.Capture)
		}
	}
	return b
}

// closeCapture closes the capture file, if one is open.
func (b *Bridge) closeCapture() {
	if b.capture != nil {
		_ = b.capture.Close()
		b.capture = nil
	}
}

// Run enables the adapter, starts the bridge and blocks until the scan ends
// (i.e. until the signal handler stops it).
func Run(cfg *config.Config, st *store.SeenStore) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("could not enable the BLE stack: %w", err)
	}

	b := New(cfg, st)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Print("shutting down...")
		_ = adapter.StopScan()
		time.Sleep(500 * time.Millisecond) // give the scan a moment to stop
		st.Flush()
		os.Exit(0)
	}()

	go b.WatchIdle(ctx)

	// Scan blocks until StopScan is called.
	if err := adapter.Scan(b.HandleScan); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	st.Flush()
	b.closeCapture()
	return nil
}

// HandleScan is the adapter.Scan callback. It processes one advertisement.
func (b *Bridge) HandleScan(adapter *bluetooth.Adapter, device bluetooth.ScanResult) {
	now := time.Now()

	// Any advertisement counts as BLE traffic, matching or not.
	b.mu.Lock()
	b.lastAdv = now
	if b.idleWarned {
		b.idleWarned = false
		log.Print("BLE advertisements received again")
	}
	b.mu.Unlock()

	addr := device.Address.String()
	if !b.cfg.AcceptsDevice(addr) {
		return
	}

	// iBeacon advertisements (Apple proximity beacons, including the
	// INTELLI_ROCKS frames Govee devices emit) carry no sensor data: skip
	// them entirely - not captured, not dumped, not decoded.
	if decoders.IsIBeaconAdvertisement(device.ManufacturerData(), device.ServiceData()) {
		logging.Debugf("%s iBeacon advertisement - skipped", addr)
		return
	}

	// Capture mode: append the raw advertisement for later replay.
	if b.capture != nil {
		if err := b.capture.Write(capture.FromScanResult(device)); err != nil {
			logging.Debugf("capture write failed: %v", err)
		}
	}

	senderMAC := sensor.SenderMAC(addr)

	// Govee devices fill most broadcasts with the INTELLI_ROCKS blob; the
	// measurement frame only arrives when its values change. Explain that
	// once per device, and keep the dump quiet about repeat blobs.
	appleKnown := false
	for _, mfg := range device.ManufacturerData() {
		if mfg.CompanyID == decoders.CompanyIDApple {
			appleKnown = b.noteAppleBlob(addr, device.LocalName())
			break
		}
	}

	// Annotated dump of the whole advertisement: hex plus a field-by-field
	// breakdown of ThermoBeacon and BTHome payloads (no-op without -debug).
	dump.DebugDumpScan(device, senderMAC, appleKnown)

	// 1. Manufacturer data formats: ThermoBeacon first (its 18 byte signature
	//    with the embedded MAC), then Govee thermometers (H5074 and friends).
	for _, mfg := range device.ManufacturerData() {
		if mfg.CompanyID == decoders.CompanyIDApple {
			// Apple's company id: the INTELLI_ROCKS blob some Govee
			// firmwares emit next to their own data - never a measurement.
			continue
		}
		reading, tbErr := decoders.ThermoBeacon(senderMAC, mfg.CompanyID, mfg.Data)
		if tbErr == nil {
			b.forward(reading, device.RSSI, now)
			return
		}
		reading, govErr := decoders.Govee(sensor.IdentityFor(senderMAC, addr), device.LocalName(), mfg.CompanyID, mfg.Data)
		if govErr == nil {
			b.forward(reading, device.RSSI, now)
			return
		}
		// A Govee-named device whose frame cannot be decoded is logged once
		// at INFO level with the raw bytes, so unknown frame formats can be
		// identified (and supported) from the log alone.
		b.logUnknownGoveeOnce(addr, device.LocalName(), mfg.CompanyID, mfg.Data, govErr)
		logging.Debugf("%s manufacturer data 0x%04x (%d bytes): %v; %v",
			addr, mfg.CompanyID, len(mfg.Data), tbErr, govErr)
	}

	// 2. BTHome v2 service data.
	for _, sd := range device.ServiceData() {
		if sd.UUID != decoders.BTHomeServiceUUID {
			continue
		}
		reading, err := decoders.BTHome(sensor.IdentityFor(senderMAC, addr), sd.Data)
		if err != nil {
			logging.Debugf("%s BTHome service data: %v", addr, err)
			continue
		}
		if reading == nil {
			logging.Debugf("%s BTHome payload without usable measurements", addr)
			continue
		}
		b.forward(reading, device.RSSI, now)
		return
	}
}

// noteAppleBlob marks a device whose advertisement carries the INTELLI_ROCKS
// blob and returns whether it was seen before. The first time, it explains at
// INFO level why most broadcasts are ignored and when readings arrive.
func (b *Bridge) noteAppleBlob(addr, localName string) bool {
	b.mu.Lock()
	seenBefore := b.appleLogged[addr]
	b.appleLogged[addr] = true
	b.mu.Unlock()
	if !seenBefore && decoders.ModelFromName(localName) != "" {
		log.Printf("%s: most broadcasts carry the INTELLI_ROCKS blob (company id 0x004c) instead of measurements; the measurement frame arrives whenever its values change - readings are forwarded then",
			localName)
	}
	return seenBefore
}

// logUnknownGoveeOnce reports undecodable manufacturer data from a device
// whose local name matches a known Govee model - once per device, at INFO
// level, with the raw bytes, so unknown frame formats can be identified and
// supported from the log alone.
func (b *Bridge) logUnknownGoveeOnce(addr, localName string, companyID uint16, data []byte, decodeErr error) {
	if decoders.ModelFromName(localName) == "" {
		return
	}
	b.mu.Lock()
	first := !b.unknownLogged[addr]
	b.unknownLogged[addr] = true
	b.mu.Unlock()
	if first {
		log.Printf("%s sent an undecodable Govee frame: company id 0x%04x, %d bytes: %x (%v) - please report this at https://github.com/prune998/thermobeacon-ble-go/issues",
			localName, companyID, len(data), data, decodeErr)
	}
}

// forward rate limits, re-encodes and sends one reading.
func (b *Bridge) forward(r *sensor.Reading, rssi int16, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if last, ok := b.lastSent[r.MAC]; ok && now.Sub(last) < b.cfg.MinInterval {
		logging.Debugf("rate limited %s (last packet %s ago)", r.MAC, now.Sub(last).Truncate(time.Millisecond))
		return
	}
	b.lastSent[r.MAC] = now

	if !b.known[r.MAC] {
		b.known[r.MAC] = true
		log.Printf("%s %s found: %v (enable it once in Settings > Integrations > Bluetooth sensors)",
			sensor.SourceLabel(r), r.MAC, r)
	}

	// The sequence number must never go backwards: dbus-ble-sensors drops
	// packets whose sequence number does. The ThermoBeacon uptime increases
	// with every measurement and survives restarts of this bridge; BTHome
	// packets carry no uptime, so the wall clock is used instead.
	seqno := uint16(r.Uptime)
	if r.Uptime == 0 {
		seqno = uint16(now.Unix())
	}

	payload, err := ruuvi.EncodeRAWv2(r, seqno)
	if err != nil {
		log.Printf("could not encode %s: %v", r.MAC, err)
		return
	}
	pkt, err := ruuvi.BuildUDPPacket(r.MAC, ruuvi.ManufacturerID, payload, rssi)
	if err != nil {
		log.Printf("could not build UDP packet for %s: %v", r.MAC, err)
		return
	}

	if !b.cfg.NoForward {
		if err := b.sender.send(pkt); err != nil {
			log.Printf("UDP send to dbus-ble-sensors failed: %v", err)
			return
		}
	}
	logging.Debugf("forwarded %s rssi %d (%d byte packet)", r, rssi, len(pkt))
	dump.DebugDumpRuuvi(payload)

	if b.store != nil {
		b.store.Update(r, rssi, now)
	}
}

// WatchIdle logs a warning when no BLE advertisement at all has been seen for
// idleWarning seconds, which on a GX device usually means "Bluetooth sensors"
// is disabled or the adapter is down.
func (b *Bridge) WatchIdle(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.mu.Lock()
			silent := time.Since(b.lastAdv)
			warned := b.idleWarned
			b.mu.Unlock()
			if silent > b.cfg.IdleWarning && !warned {
				log.Printf("WARNING: no BLE advertisements received for %s - is Bluetooth turned on and scanning?",
					silent.Truncate(time.Second))
				b.mu.Lock()
				b.idleWarned = true
				b.mu.Unlock()
			}
		}
	}
}

// udpSender is a fire-and-forget UDP sender, equivalent to the unconnected
// sendto() the original uses: an unreachable destination (nobody listening on
// the GX device) is not an error.
type udpSender struct {
	addr *net.UDPAddr
	conn net.PacketConn
}

func newUDPSender(host string, port int) *udpSender {
	s := &udpSender{}
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		log.Printf("WARNING: invalid UDP target %s:%d: %v", host, port, err)
		return s
	}
	s.addr = addr
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		log.Printf("WARNING: could not open UDP socket: %v", err)
		return s
	}
	s.conn = conn
	return s
}

func (u *udpSender) send(pkt []byte) error {
	if u == nil || u.addr == nil || u.conn == nil {
		return errors.New("UDP sender unavailable")
	}
	_, err := u.conn.WriteTo(pkt, u.addr)
	return err
}
