package main

import (
	"context"
	"errors"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	bthome "github.com/hybridgroup/go-bthome"
	"tinygo.org/x/bluetooth"
)

// packetSender hands an encoded UDP packet to the transport. Split into an
// interface so tests can capture packets.
type packetSender interface {
	send(pkt []byte) error
}

// Bridge ties everything together: it receives scan results, decodes
// ThermoBeacon and BTHome payloads, rate limits per sensor, re-encodes the
// readings as RuuviTag RAWv2 and forwards them to dbus-ble-sensors.
type Bridge struct {
	cfg    *Config
	store  *SeenStore
	sender packetSender

	mu            sync.Mutex
	lastSent      map[string]time.Time
	known         map[string]bool
	foreignLogged map[string]bool // devices already logged as not matching -device
	unknownLogged map[string]bool // devices already logged with an undecodable Govee frame
	appleLogged   map[string]bool // devices already told about the INTELLI_ROCKS blob
	lastAdv       time.Time
	idleWarned    bool
}

func NewBridge(cfg *Config, store *SeenStore) *Bridge {
	return &Bridge{
		cfg:           cfg,
		store:         store,
		sender:        newUDPSender(cfg.UDPHost, cfg.UDPPort),
		lastSent:      map[string]time.Time{},
		known:         map[string]bool{},
		foreignLogged: map[string]bool{},
		unknownLogged: map[string]bool{},
		appleLogged:   map[string]bool{},
		lastAdv:       time.Now(),
	}
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
	if !b.cfg.acceptsDevice(addr) {
		b.logForeignOnce(addr, device.RSSI)
		return
	}

	senderMAC := ""
	if isMACAddress(addr) {
		if n, err := normalizeMAC(addr); err == nil {
			senderMAC = n
		}
	}

	// Govee devices fill most broadcasts with the INTELLI_ROCKS blob; the
	// measurement frame only arrives when its values change. Explain that
	// once per device, and keep the dump quiet about repeat blobs.
	appleKnown := false
	for _, mfg := range device.ManufacturerData() {
		if mfg.CompanyID == goveeCompanyIDApple {
			appleKnown = b.noteAppleBlob(addr, device.LocalName())
			break
		}
	}

	// Annotated dump of the whole advertisement: hex plus a field-by-field
	// breakdown of ThermoBeacon and BTHome payloads (no-op without -debug).
	debugDumpScan(device, senderMAC, appleKnown)

	// 1. Manufacturer data formats: ThermoBeacon first (its 18 byte signature
	//    with the embedded MAC), then Govee thermometers (H5074 and friends).
	for _, mfg := range device.ManufacturerData() {
		if mfg.CompanyID == goveeCompanyIDApple {
			// Apple's company id: the INTELLI_ROCKS blob some Govee
			// firmwares emit next to their own data - never a measurement.
			continue
		}
		reading, tbErr := decodeThermoBeacon(senderMAC, mfg.CompanyID, mfg.Data)
		if tbErr == nil {
			b.forward(reading, device.RSSI, now)
			return
		}
		reading, govErr := decodeGovee(identityFor(senderMAC, addr), device.LocalName(), mfg.CompanyID, mfg.Data)
		if govErr == nil {
			b.forward(reading, device.RSSI, now)
			return
		}
		// A Govee-named device whose frame cannot be decoded is logged once
		// at INFO level with the raw bytes, so unknown frame formats can be
		// identified (and supported) from the log alone.
		b.logUnknownGoveeOnce(addr, device.LocalName(), mfg.CompanyID, mfg.Data, govErr)
		debugf("%s manufacturer data 0x%04x (%d bytes): %v; %v",
			addr, mfg.CompanyID, len(mfg.Data), tbErr, govErr)
	}

	// 2. BTHome v2 service data.
	for _, sd := range device.ServiceData() {
		if sd.UUID != bthome.ServiceUUID {
			continue
		}
		reading, err := decodeBTHome(identityFor(senderMAC, addr), sd.Data)
		if err != nil {
			debugf("%s BTHome service data: %v", addr, err)
			continue
		}
		if reading == nil {
			debugf("%s BTHome payload without usable measurements", addr)
			continue
		}
		b.forward(reading, device.RSSI, now)
		return
	}
}

// identityFor returns the sensor identity: the advertiser MAC where the
// platform provides one, otherwise a stable pseudo MAC derived from the
// opaque device id (macOS).
func identityFor(senderMAC, addr string) string {
	if senderMAC != "" {
		return senderMAC
	}
	return pseudoMAC(addr)
}

// logForeignOnce logs the first advertisement of a device that does not match
// -device, once per device: busy radio environments (phones, TVs, neighbour
// sensors) would otherwise flood the debug output with every advertisement.
func (b *Bridge) logForeignOnce(addr string, rssi int16) {
	if !debugEnabled {
		return
	}
	b.mu.Lock()
	first := !b.foreignLogged[addr]
	b.foreignLogged[addr] = true
	b.mu.Unlock()
	if first {
		debugf("advertisement from %s (%d dBm) does not match -device (further advertisements from it are not logged)",
			addr, rssi)
	}
}

// forward rate limits, re-encodes and sends one reading.
func (b *Bridge) forward(r *Reading, rssi int16, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if last, ok := b.lastSent[r.MAC]; ok && now.Sub(last) < b.cfg.MinInterval {
		debugf("rate limited %s (last packet %s ago)", r.MAC, now.Sub(last).Truncate(time.Millisecond))
		return
	}
	b.lastSent[r.MAC] = now

	if !b.known[r.MAC] {
		b.known[r.MAC] = true
		log.Printf("%s %s found: %v (enable it once in Settings > Integrations > Bluetooth sensors)",
			sourceLabel(r), r.MAC, r)
	}

	// The sequence number must never go backwards: dbus-ble-sensors drops
	// packets whose sequence number does. The ThermoBeacon uptime increases
	// with every measurement and survives restarts of this bridge; BTHome
	// packets carry no uptime, so the wall clock is used instead.
	seqno := uint16(r.Uptime)
	if r.Uptime == 0 {
		seqno = uint16(now.Unix())
	}

	payload, err := encodeRuuviRAWv2(r, seqno)
	if err != nil {
		log.Printf("could not encode %s: %v", r.MAC, err)
		return
	}
	pkt, err := buildUDPPacket(r.MAC, ruuviManufacturerID, payload, rssi)
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
	debugf("forwarded %s rssi %d (%d byte packet)", r, rssi, len(pkt))
	debugDumpRuuvi(payload)

	if b.store != nil {
		b.store.Update(r, rssi, now)
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
	if !seenBefore && goveeModelFromName(localName) != "" {
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
	if goveeModelFromName(localName) == "" {
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

// sourceLabel maps a Reading to the name prefix used in logs and default
// names, including the model when it is known ("Govee H5074").
func sourceLabel(r *Reading) string {
	switch r.Source {
	case "bthome":
		return "BTHome"
	case "govee":
		if r.Model != "" {
			return "Govee " + r.Model
		}
		return "Govee"
	default:
		return "ThermoBeacon"
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
