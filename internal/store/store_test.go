package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
)

func TestSeenStoreRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sensors.json")
	store := Load(path)
	now := time.Now()
	store.Update(&sensor.Reading{
		MAC: "db:5c:8d:a0:0f:21", Temperature: 19.25, Humidity: 58,
		VoltageMV: 2880, Source: "thermobeacon",
	}, -52, now)
	store.Flush()

	reloaded := Load(path)
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
