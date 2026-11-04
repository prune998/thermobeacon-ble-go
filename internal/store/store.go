// Package store keeps the record of seen sensors (sensors.json) and renders
// it for the list subcommand - the equivalent of the sensors.json and
// bin/sensors.py list of dbus-ble-temp.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
)

const (
	// flushInterval limits how often the state file is written to spare the
	// flash memory of a GX device. A new sensor always flushes right away.
	flushInterval = 5 * time.Minute

	// expireAfter drops sensors that have not been seen for 30 days.
	expireAfter = 30 * 24 * time.Hour
)

// SensorEntry is the record of one seen sensor, as stored in sensors.json.
type SensorEntry struct {
	FirstSeen   int64   `json:"first_seen"`
	LastSeen    int64   `json:"last_seen"`
	RSSI        int16   `json:"rssi"`
	Temperature float64 `json:"temperature"`
	Humidity    float64 `json:"humidity"`
	Voltage     float64 `json:"voltage"`
	Battery     int     `json:"battery,omitempty"`
	Source      string  `json:"source"`
	Model       string  `json:"model,omitempty"`
}

// SeenStore keeps {mac: SensorEntry} in a small JSON file. It is safe for
// concurrent use.
type SeenStore struct {
	path      string
	mu        sync.Mutex
	data      map[string]SensorEntry
	dirty     bool
	lastFlush time.Time
}

// Load reads the state file (missing or broken files are not fatal) and
// expires entries older than 30 days.
func Load(path string) *SeenStore {
	s := &SeenStore{path: path, data: map[string]SensorEntry{}}
	if path == "" {
		return s
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("WARNING: could not read %s: %v", path, err)
		}
		return s
	}
	var stored map[string]SensorEntry
	if err := json.Unmarshal(b, &stored); err != nil || stored == nil {
		log.Printf("WARNING: could not parse %s: %v", path, err)
		return s
	}
	s.data = stored

	now := time.Now()
	for mac, e := range s.data {
		if now.Sub(time.Unix(e.LastSeen, 0)) > expireAfter {
			delete(s.data, mac)
			log.Printf("forgot sensor %s (not seen for more than 30 days)", mac)
		}
	}
	return s
}

// Update records a reading. New sensors are flushed immediately, everything
// else at most every flushInterval.
func (s *SeenStore) Update(r *sensor.Reading, rssi int16, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		return
	}
	entry, seen := s.data[r.MAC]
	if !seen {
		entry = SensorEntry{FirstSeen: now.Unix()}
	}
	entry.LastSeen = now.Unix()
	entry.RSSI = rssi
	entry.Temperature = math.Round(r.Temperature*100) / 100
	entry.Humidity = math.Round(r.Humidity*100) / 100
	entry.Voltage = math.Round(float64(r.VoltageMV)) / 1000
	if r.BatteryPct >= 0 {
		entry.Battery = r.BatteryPct
	}
	entry.Source = r.Source
	entry.Model = r.Model
	s.data[r.MAC] = entry
	s.dirty = true

	if !seen || now.Sub(s.lastFlush) >= flushInterval {
		s.flushLocked()
	}
}

// Flush writes the state file if there are unsaved changes.
func (s *SeenStore) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
}

func (s *SeenStore) flushLocked() {
	if !s.dirty || s.path == "" {
		return
	}
	tmp := s.path + ".tmp"
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err == nil {
		err = os.WriteFile(tmp, b, 0o644)
		if err == nil {
			err = os.Rename(tmp, s.path)
		}
	}
	if err != nil {
		log.Printf("WARNING: could not write %s: %v", s.path, err)
	} else {
		s.dirty = false
	}
	s.lastFlush = time.Now()
}

// List prints the recorded sensors, the equivalent of the
// 'bin/sensors.py list' of dbus-ble-temp.
func List(path string) error {
	if path == "" {
		return errors.New("no state file configured (-state)")
	}
	s := Load(path)
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.data) == 0 {
		fmt.Println("no sensors recorded yet")
		return nil
	}

	macs := make([]string, 0, len(s.data))
	for mac := range s.data {
		macs = append(macs, mac)
	}
	sort.Strings(macs)

	fmt.Printf("%-20s %-18s %-10s %-5s %s\n", "MAC", "SOURCE", "SEEN", "RSSI", "READING")
	fmt.Println(strings.Repeat("-", 85))
	for _, mac := range macs {
		e := s.data[mac]
		age := time.Since(time.Unix(e.LastSeen, 0)).Truncate(time.Second)
		source := e.Source
		if e.Model != "" {
			source = e.Source + " " + e.Model
		}
		fmt.Printf("%-20s %-18s %-10s %-5d %.2f°C %.2f%% %.3fV\n",
			mac, source, age, e.RSSI, e.Temperature, e.Humidity, e.Voltage)
	}
	return nil
}
