package capture

import (
	"fmt"

	"github.com/prune998/thermobeacon-ble-go/internal/decoders"
	"github.com/prune998/thermobeacon-ble-go/internal/dump"
	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
)

// Replay reads a capture file and feeds every event through the decoders and
// the annotated dump - the workflow for adding support for new devices:
// capture real frames, implement a decoder, replay until everything decodes,
// then keep the capture as test fixtures.
func Replay(path string) error {
	events, err := Read(path)
	if err != nil {
		return err
	}

	type tally struct {
		events, decoded int
	}
	tallies := map[string]*tally{}
	var order []string
	decodedTotal := 0

	for _, e := range events {
		t, ok := tallies[e.Address]
		if !ok {
			t = &tally{}
			tallies[e.Address] = t
			order = append(order, e.Address)
		}
		t.events++

		fmt.Printf("── %s  %s  rssi %d dBm  name %q\n",
			e.Time.Format("15:04:05.000"), e.Address, e.RSSI, orEmpty(e.Name))
		fmt.Print(dump.AdvertisementText(e.Address, e.RSSI, e.Payload(), e.SenderMAC, false))

		r := Decode(e)
		if r == nil {
			switch {
			case e.IsIBeacon():
				fmt.Println("  -> iBeacon advertisement (skipped)")
			case len(e.ManufacturerData) == 0 && len(e.ServiceData) == 0:
				fmt.Println("  -> empty advertisement (no manufacturer or service data)")
			case appleOnly(e.ManufacturerData) && len(e.ServiceData) == 0:
				fmt.Println("  -> ignored (Apple company id data only)")
			default:
				fmt.Println("  -> NOT DECODED - implement a decoder for this frame (hex above)")
			}
		} else {
			t.decoded++
			decodedTotal++
			fmt.Printf("  -> %s %s\n", sensor.SourceLabel(r), r.String())
		}
		fmt.Println()
	}

	fmt.Printf("%d events from %d devices, %d decoded\n", len(events), len(order), decodedTotal)
	for _, addr := range order {
		t := tallies[addr]
		if t.decoded < t.events {
			fmt.Printf("  undecoded frames from %s (%d of %d decoded) - add a decoder in internal/decoders and use the hex dumps as test fixtures\n",
				addr, t.decoded, t.events)
		}
	}
	return nil
}

func appleOnly(mfg []MfgData) bool {
	if len(mfg) == 0 {
		return false
	}
	for _, m := range mfg {
		if m.CompanyID != decoders.CompanyIDApple {
			return false
		}
	}
	return true
}

func orEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
