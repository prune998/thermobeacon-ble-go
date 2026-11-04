// Package capture stores BLE advertisements as JSON Lines files that can be
// replayed through the decoders - the workflow for adding support for new
// devices: capture real frames, replay them, implement the decoder, keep the
// capture as a test fixture.
//
// File format: one JSON object per line (JSON Lines):
//
//	{"time":"2026-11-04T19:00:01.123+01:00",
//	 "address":"f1ae0297-b710-1440-2881-9274ad58590d",
//	 "sender_mac":"","identity":"ba:e2:52:36:ff:28",
//	 "name":"Govee_H5074_88DE","rssi":-35,
//	 "manufacturer_data":[{"company_id":60552,"data":"00f8099f1c6402"}],
//	 "service_data":[]}
//
// data fields are hex encoded; sender_mac is the platform MAC (empty on
// macOS); identity is the stable sensor identity used for decoding.
package capture

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prune998/thermobeacon-ble-go/internal/decoders"
	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
	"tinygo.org/x/bluetooth"
)

// MfgData is one manufacturer specific data element (hex encoded payload
// without the company id).
type MfgData struct {
	CompanyID uint16 `json:"company_id"`
	Data      string `json:"data"`
}

// SvcData is one service data element (hex encoded payload).
type SvcData struct {
	UUID string `json:"uuid"`
	Data string `json:"data"`
}

// Event is one captured advertisement.
type Event struct {
	Time             time.Time `json:"time"`
	Address          string    `json:"address"`
	SenderMAC        string    `json:"sender_mac,omitempty"`
	Identity         string    `json:"identity,omitempty"`
	Name             string    `json:"name,omitempty"`
	RSSI             int16     `json:"rssi"`
	ManufacturerData []MfgData `json:"manufacturer_data,omitempty"`
	ServiceData      []SvcData `json:"service_data,omitempty"`
}

// FromScanResult builds an Event from a scan result.
func FromScanResult(d bluetooth.ScanResult) Event {
	return FromAdvertisement(d.Address.String(), d.RSSI, d)
}

// FromAdvertisement builds an Event from an address, RSSI and payload - the
// platform-independent form used by tests and replays.
func FromAdvertisement(addr string, rssi int16, payload bluetooth.AdvertisementPayload) Event {
	senderMAC := sensor.SenderMAC(addr)
	e := Event{
		Time:      time.Now(),
		Address:   addr,
		SenderMAC: senderMAC,
		Identity:  sensor.IdentityFor(senderMAC, addr),
		Name:      payload.LocalName(),
		RSSI:      rssi,
	}
	for _, m := range payload.ManufacturerData() {
		e.ManufacturerData = append(e.ManufacturerData, MfgData{
			CompanyID: m.CompanyID,
			Data:      hex.EncodeToString(m.Data),
		})
	}
	for _, s := range payload.ServiceData() {
		e.ServiceData = append(e.ServiceData, SvcData{
			UUID: s.UUID.String(),
			Data: hex.EncodeToString(s.Data),
		})
	}
	return e
}

// Payload returns the captured advertisement as an AdvertisementPayload, so
// replayed events flow through the same dump and decode machinery as live
// scans.
func (e Event) Payload() bluetooth.AdvertisementPayload {
	return &replayPayload{e: e}
}

type replayPayload struct {
	e Event
}

func (p *replayPayload) LocalName() string                  { return p.e.Name }
func (p *replayPayload) HasServiceUUID(bluetooth.UUID) bool { return false }
func (p *replayPayload) ServiceUUIDs() []bluetooth.UUID     { return nil }
func (p *replayPayload) Bytes() []byte                      { return nil }

func (p *replayPayload) ManufacturerData() []bluetooth.ManufacturerDataElement {
	out := make([]bluetooth.ManufacturerDataElement, 0, len(p.e.ManufacturerData))
	for _, m := range p.e.ManufacturerData {
		data, err := hex.DecodeString(m.Data)
		if err != nil {
			continue
		}
		out = append(out, bluetooth.ManufacturerDataElement{CompanyID: m.CompanyID, Data: data})
	}
	return out
}

func (p *replayPayload) ServiceData() []bluetooth.ServiceDataElement {
	out := make([]bluetooth.ServiceDataElement, 0, len(p.e.ServiceData))
	for _, s := range p.e.ServiceData {
		data, err := hex.DecodeString(s.Data)
		if err != nil {
			continue
		}
		u, err := bluetooth.ParseUUID(s.UUID)
		if err != nil {
			continue
		}
		out = append(out, bluetooth.ServiceDataElement{UUID: u, Data: data})
	}
	return out
}

// Decode runs the captured frame through the decoders with the same
// precedence as the bridge (ThermoBeacon, then Govee, then BTHome). Returns
// nil when no decoder accepts the frame.
func Decode(e Event) *sensor.Reading {
	for _, m := range e.ManufacturerData {
		if m.CompanyID == decoders.CompanyIDApple {
			// Apple's company id: the INTELLI_ROCKS blob some Govee
			// firmwares emit next to their own data - never a measurement.
			continue
		}
		data, err := hex.DecodeString(m.Data)
		if err != nil {
			continue
		}
		if r, err := decoders.ThermoBeacon(e.SenderMAC, m.CompanyID, data); err == nil {
			return r
		}
		if r, err := decoders.Govee(e.Identity, e.Name, m.CompanyID, data); err == nil {
			return r
		}
	}
	for _, s := range e.ServiceData {
		data, err := hex.DecodeString(s.Data)
		if err != nil {
			continue
		}
		if s.UUID != decoders.BTHomeServiceUUID.String() {
			continue
		}
		if r, err := decoders.BTHome(e.Identity, data); err == nil && r != nil {
			return r
		}
	}
	return nil
}

// PathFor derives a capture file name for a device address:
// "capture-<sanitized address>.jsonl".
func PathFor(addr string) string {
	var sb strings.Builder
	sb.WriteString("capture-")
	for _, c := range strings.ToLower(addr) {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || c == '-' {
			sb.WriteRune(c)
		}
	}
	sb.WriteString(".jsonl")
	return sb.String()
}

// Writer appends events to a JSON Lines capture file. Safe for concurrent
// use.
type Writer struct {
	mu sync.Mutex
	f  *os.File
}

// NewWriter opens (or creates) a capture file for appending.
func NewWriter(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f}, nil
}

// Write appends one event as a single JSON line.
func (w *Writer) Write(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.f.Write(append(b, '\n'))
	return err
}

// Close closes the capture file.
func (w *Writer) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.f.Close()
	w.f = nil
	return err
}

// Read parses a capture file. Lines that are empty or start with "//" are
// skipped, so hand-edited notes survive a replay.
func Read(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Event
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		b := strings.TrimSpace(sc.Text())
		if b == "" || strings.HasPrefix(b, "//") {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(b), &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no events in " + path)
	}
	return out, nil
}
