package bridge

import (
	"bytes"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prune998/thermobeacon-ble-go/internal/config"
	"github.com/prune998/thermobeacon-ble-go/internal/decoders"
	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
	"github.com/prune998/thermobeacon-ble-go/internal/store"
)

func (f *fakeSender) send(pkt []byte) error {
	if f.err != nil {
		return f.err
	}
	cp := make([]byte, len(pkt))
	copy(cp, pkt)
	f.pkts = append(f.pkts, cp)
	return nil
}

func (*fakeError) Error() string { return "fake send error" }

var errFake = &fakeError{}

type fakeSender struct {
	pkts [][]byte
	err  error
}

type fakeError struct{}

func TestBridgeForwardAndRateLimit(t *testing.T) {
	store := store.Load(filepath.Join(t.TempDir(), "sensors.json"))
	cfg := &config.Config{MinInterval: 5 * time.Second}
	b := New(cfg, store)
	fs := &fakeSender{}
	b.sender = packetSender(fs)

	r := &sensor.Reading{
		MAC:         "db:5c:8d:a0:0f:21",
		Temperature: 19.25,
		Humidity:    58.0,
		VoltageMV:   2880,
		Uptime:      2131727,
		Source:      "thermobeacon",
	}
	now := time.Now()

	b.forward(r, -52, now)
	if len(fs.pkts) != 1 {
		t.Fatalf("sent %d packets, want 1", len(fs.pkts))
	}
	// The forwarded packet must match the original implementation byte for
	// byte (uptime is used as sequence number).
	want := "0101210fa08d5cdb990418050f0a5aa0ffff800080008000a01fff870fdb5c8da00f21cc"
	if got := hex.EncodeToString(fs.pkts[0]); got != want {
		t.Errorf("packet = %s, want %s", got, want)
	}

	// Within min_interval: dropped.
	b.forward(r, -52, now.Add(1*time.Second))
	if len(fs.pkts) != 1 {
		t.Fatalf("sent %d packets after %s, want 1", len(fs.pkts), 1*time.Second)
	}

	// After min_interval: forwarded again.
	b.forward(r, -52, now.Add(6*time.Second))
	if len(fs.pkts) != 2 {
		t.Fatalf("sent %d packets after %s, want 2", len(fs.pkts), 6*time.Second)
	}
}

func TestBridgeNoForward(t *testing.T) {
	store := store.Load("")
	cfg := &config.Config{MinInterval: time.Second, NoForward: true}
	b := New(cfg, store)
	fs := &fakeSender{}
	b.sender = packetSender(fs)

	r := &sensor.Reading{MAC: "db:5c:8d:a0:0f:21", Temperature: 20, Humidity: 50, VoltageMV: 2900, Uptime: 1, Source: "thermobeacon"}
	b.forward(r, -40, time.Now())

	if len(fs.pkts) != 0 {
		t.Errorf("sent %d packets with -no-forward, want 0", len(fs.pkts))
	}
}

func TestBridgeSenderErrorStopsForward(t *testing.T) {
	store := store.Load("")
	cfg := &config.Config{MinInterval: time.Second}
	b := New(cfg, store)
	b.sender = &fakeSender{err: errFake}

	r := &sensor.Reading{MAC: "db:5c:8d:a0:0f:21", Temperature: 20, Humidity: 50, VoltageMV: 2900, Uptime: 1, Source: "thermobeacon"}
	b.forward(r, -40, time.Now()) // must not panic
}

func TestBridgeSeqnoFromWallClock(t *testing.T) {
	store := store.Load("")
	cfg := &config.Config{MinInterval: time.Second}
	b := New(cfg, store)
	fs := &fakeSender{}
	b.sender = packetSender(fs)

	// BTHome readings carry no uptime, so the sequence number must come from
	// the wall clock and still produce a valid packet.
	r := &sensor.Reading{MAC: "aa:bb:cc:dd:ee:ff", Temperature: 21.5, Humidity: 44.5, VoltageMV: 3370, Source: "bthome"}
	b.forward(r, -60, time.Now())
	if len(fs.pkts) != 1 {
		t.Fatalf("sent %d packets, want 1", len(fs.pkts))
	}
	payload := fs.pkts[0][10 : 10+24]
	seqno := int(payload[16])<<8 | int(payload[17])
	if seqno == 0 {
		t.Errorf("sequence number is 0, expected wall clock seconds")
	}
}

func TestLogUnknownGoveeOnce(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	}()

	cfg := &config.Config{Device: "all", MinInterval: time.Second}
	b := New(cfg, store.Load(""))

	// A Govee-named device with an undecodable frame: logged once with the
	// raw bytes.
	data := append([]byte{0xEC, 0x00}, make([]byte, 10)...)
	_, govErr := decoders.Govee("", "Govee_H5074", 0x8803, data)
	b.logUnknownGoveeOnce("f1ae0297-0000-0000-0000-000000000000", "Govee_H5074", 0x8803, data, govErr)
	b.logUnknownGoveeOnce("f1ae0297-0000-0000-0000-000000000000", "Govee_H5074", 0x8803, data, govErr)

	out := buf.String()
	if got := strings.Count(out, "undecodable Govee frame"); got != 1 {
		t.Errorf("logged %d times, want 1:\n%s", got, out)
	}
	if !strings.Contains(out, "company id 0x8803, 12 bytes: ec0000000000000000000000") {
		t.Errorf("log line lacks the raw frame bytes:\n%s", out)
	}

	// Devices without a Govee model name are never reported this way.
	b.logUnknownGoveeOnce("other-device", "", 0x1234, data, govErr)
	if strings.Count(buf.String(), "undecodable Govee frame") != 1 {
		t.Errorf("non-Govee device logged:\n%s", buf.String())
	}
}

func TestNoteAppleBlob(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	}()

	cfg := &config.Config{Device: "all", MinInterval: time.Second}
	b := New(cfg, store.Load(""))

	if b.noteAppleBlob("f1ae0297-0000-0000-0000-000000000000", "Govee_H5074_88DE") {
		t.Error("first sighting reported as known")
	}
	if !b.noteAppleBlob("f1ae0297-0000-0000-0000-000000000000", "Govee_H5074_88DE") {
		t.Error("second sighting not reported as known")
	}

	out := buf.String()
	if got := strings.Count(out, "INTELLI_ROCKS blob"); got != 1 {
		t.Errorf("hint logged %d times, want 1:\n%s", got, out)
	}
	if !strings.Contains(out, "measurement frame arrives whenever its values change") {
		t.Errorf("hint does not explain the cadence:\n%s", out)
	}

	// Devices without a Govee model name are never hinted.
	b.noteAppleBlob("other-device", "")
	if strings.Count(buf.String(), "INTELLI_ROCKS blob") != 1 {
		t.Errorf("non-Govee device hinted:\n%s", buf.String())
	}
}
