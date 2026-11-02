package main

import (
	"bytes"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSender struct {
	pkts [][]byte
	err  error
}

func (f *fakeSender) send(pkt []byte) error {
	if f.err != nil {
		return f.err
	}
	cp := make([]byte, len(pkt))
	copy(cp, pkt)
	f.pkts = append(f.pkts, cp)
	return nil
}

func TestBridgeForwardAndRateLimit(t *testing.T) {
	store := loadSeenStore(filepath.Join(t.TempDir(), "sensors.json"))
	cfg := &Config{MinInterval: 5 * time.Second}
	b := NewBridge(cfg, store)
	fs := &fakeSender{}
	b.sender = fs

	r := &Reading{
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
	store := loadSeenStore("")
	cfg := &Config{MinInterval: time.Second, NoForward: true}
	b := NewBridge(cfg, store)
	fs := &fakeSender{}
	b.sender = fs

	r := &Reading{MAC: "db:5c:8d:a0:0f:21", Temperature: 20, Humidity: 50, VoltageMV: 2900, Uptime: 1, Source: "thermobeacon"}
	b.forward(r, -40, time.Now())

	if len(fs.pkts) != 0 {
		t.Errorf("sent %d packets with -no-forward, want 0", len(fs.pkts))
	}
}

func TestBridgeSenderErrorStopsForward(t *testing.T) {
	store := loadSeenStore("")
	cfg := &Config{MinInterval: time.Second}
	b := NewBridge(cfg, store)
	b.sender = &fakeSender{err: errFake}

	r := &Reading{MAC: "db:5c:8d:a0:0f:21", Temperature: 20, Humidity: 50, VoltageMV: 2900, Uptime: 1, Source: "thermobeacon"}
	b.forward(r, -40, time.Now()) // must not panic
}

var errFake = &fakeError{}

type fakeError struct{}

func (*fakeError) Error() string { return "fake send error" }

func TestBridgeSeqnoFromWallClock(t *testing.T) {
	store := loadSeenStore("")
	cfg := &Config{MinInterval: time.Second}
	b := NewBridge(cfg, store)
	fs := &fakeSender{}
	b.sender = fs

	// BTHome readings carry no uptime, so the sequence number must come from
	// the wall clock and still produce a valid packet.
	r := &Reading{MAC: "aa:bb:cc:dd:ee:ff", Temperature: 21.5, Humidity: 44.5, VoltageMV: 3370, Source: "bthome"}
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

func TestLogForeignOnce(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	oldDebug := debugEnabled
	debugEnabled = true
	defer func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
		debugEnabled = oldDebug
	}()

	cfg := &Config{Device: "aa:bb:cc:dd:ee:ff", MinInterval: time.Second}
	b := NewBridge(cfg, loadSeenStore(""))

	// Three advertisements from the same foreign device, then one from
	// another: only the first advertisement of each device may be logged.
	b.logForeignOnce("11:22:33:44:55:66", -60)
	b.logForeignOnce("11:22:33:44:55:66", -61)
	b.logForeignOnce("11:22:33:44:55:66", -62)
	b.logForeignOnce("22:33:44:55:66:77", -70)

	out := buf.String()
	if got := strings.Count(out, "does not match -device"); got != 2 {
		t.Errorf("logged %d times, want 2 (once per device):\n%s", got, out)
	}
	if !strings.Contains(out, "advertisement from 11:22:33:44:55:66 (-60 dBm) does not match -device") {
		t.Errorf("first advertisement not logged with its rssi:\n%s", out)
	}
	if !strings.Contains(out, "22:33:44:55:66:77 (-70 dBm)") {
		t.Errorf("second device not logged:\n%s", out)
	}
}

func TestLogForeignOnceSilentWithoutDebug(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	oldDebug := debugEnabled
	debugEnabled = false
	defer func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
		debugEnabled = oldDebug
	}()

	cfg := &Config{Device: "aa:bb:cc:dd:ee:ff", MinInterval: time.Second}
	b := NewBridge(cfg, loadSeenStore(""))
	b.logForeignOnce("11:22:33:44:55:66", -60)
	b.logForeignOnce("11:22:33:44:55:66", -60)

	if buf.Len() != 0 {
		t.Errorf("logged without -debug:\n%s", buf.String())
	}
}

func TestAcceptsDevice(t *testing.T) {
	cases := []struct {
		device string
		addr   string
		want   bool
	}{
		{"10243784-1324-5413-b00c-9675981a8daa", "10243784-1324-5413-b00c-9675981a8daa", true},
		{"10243784-1324-5413-b00c-9675981a8daa", "10243784-1324-5413-B00C-9675981A8DAA", true},
		{"10243784-1324-5413-b00c-9675981a8daa", "00000000-0000-0000-0000-000000000000", false},
		{"db:5c:8d:a0:0f:21", "DB:5C:8D:A0:0F:21", true},
		{"DB5C8DA00F21", "db:5c:8d:a0:0f:21", true},
		{"all", "anything", true},
		{"", "anything", true},
		{"*", "anything", true},
		{"db:5c:8d:a0:0f:21", "aa:bb:cc:dd:ee:ff", false},
	}
	for _, tc := range cases {
		cfg := &Config{Device: tc.device}
		if got := cfg.acceptsDevice(tc.addr); got != tc.want {
			t.Errorf("device %q addr %q = %v, want %v", tc.device, tc.addr, got, tc.want)
		}
	}
}

func TestDefaultName(t *testing.T) {
	if got := defaultName("ThermoBeacon", "db:5c:8d:a0:0f:21"); got != "ThermoBeacon 0F21" {
		t.Errorf("defaultName = %q, want %q", got, "ThermoBeacon 0F21")
	}
	if got := defaultName("BTHome", "aa:bb:cc:dd:0b:e1"); got != "BTHome 0BE1" {
		t.Errorf("defaultName = %q, want %q", got, "BTHome 0BE1")
	}
}

func TestPseudoMACStable(t *testing.T) {
	a := pseudoMAC("10243784-1324-5413-b00c-9675981a8daa")
	b := pseudoMAC("10243784-1324-5413-b00c-9675981a8daa")
	if a != b {
		t.Errorf("pseudoMAC not stable: %q vs %q", a, b)
	}
	if a == pseudoMAC("another-device") {
		t.Error("pseudoMAC collides for different device ids")
	}
	if !strings.Contains(a, ":") {
		t.Errorf("pseudoMAC %q is not in MAC notation", a)
	}
}

func TestSeenStoreRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sensors.json")
	store := loadSeenStore(path)
	now := time.Now()
	store.Update(&Reading{
		MAC: "db:5c:8d:a0:0f:21", Temperature: 19.25, Humidity: 58,
		VoltageMV: 2880, Source: "thermobeacon",
	}, -52, now)
	store.Flush()

	reloaded := loadSeenStore(path)
	reloaded.mu.Lock()
	defer reloaded.mu.Unlock()
	entry, ok := reloaded.data["db:5c:8d:a0:0f:21"]
	if !ok {
		t.Fatal("sensor not found after reload")
	}
	if entry.Temperature != 19.25 || entry.Humidity != 58 || entry.Voltage != 2.88 {
		t.Errorf("entry = %+v", entry)
	}
	if entry.RSSI != -52 || entry.Source != "thermobeacon" {
		t.Errorf("entry = %+v", entry)
	}
	if entry.FirstSeen != now.Unix() || entry.LastSeen != now.Unix() {
		t.Errorf("entry = %+v", entry)
	}
}
