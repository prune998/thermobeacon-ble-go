package decoders

import (
	"encoding/binary"
	"errors"

	bthome "github.com/hybridgroup/go-bthome"
	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
)

// BTHomeServiceUUID is the service data UUID all BTHome devices advertise.
var BTHomeServiceUUID = bthome.ServiceUUID // 0xFCD2

var (
	// ErrBadBTHomeData covers payloads go-bthome cannot safely parse: the
	// package panics on unknown measurement type ids and on values that run
	// past the end of the packet, so the payload is walked first.
	ErrBadBTHomeData = errors.New("malformed or unsupported BTHome payload")

	ErrNotBTHomeV2 = errors.New("not an unencrypted BTHome v2 payload")
)

// BTHome decodes a BTHome v2 service data payload using
// github.com/hybridgroup/go-bthome and maps the measurements it understands
// onto a Reading.
//
// mac is the sensor identity: the advertiser's MAC address where the platform
// provides one, otherwise a stable pseudo MAC derived from the opaque device
// id.
//
// Only unencrypted BTHome v2 is supported. A payload that parses cleanly but
// contains none of the mappable measurements yields (nil, nil).
func BTHome(mac string, data []byte) (*sensor.Reading, error) {
	if len(data) < 1 {
		return nil, ErrNotBTHomeV2
	}
	// The first byte is the BTHome device information byte: bit 7 marks
	// encrypted payloads, bits 5-6 the format version (0x40 = v2).
	if data[0]&0x80 != 0 || data[0]&0x60 != 0x40 {
		return nil, ErrNotBTHomeV2
	}

	filtered, ok := supportedBTHomeTLVs(data)
	if !ok {
		return nil, ErrBadBTHomeData
	}
	if len(filtered) < 2 {
		return nil, nil // valid BTHome, but nothing we can map
	}

	values, err := bthome.NewPayload(filtered).Parse()
	if err != nil {
		return nil, err
	}

	r := &sensor.Reading{MAC: mac, Source: "bthome", BatteryPct: -1}
	battery := -1
	for _, v := range values {
		if v == nil {
			continue
		}
		switch v.Type().ID() {
		case bthome.Temperature.ID(), bthome.Temperature2.ID(), bthome.Temperature8.ID():
			// TemperatureInt (0x57) is deliberately not handled: go-bthome
			// converts its int8 unsigned, which breaks negative values.
			if f, ok := v.Get().(float32); ok {
				r.Temperature = float64(f)
			}
		case bthome.Humidity16.ID():
			// go-bthome scales type 0x03 by 1.0, but the BTHome format
			// defines a factor of 0.01 - take the raw value and apply the
			// spec factor here.
			if len(v.Data()) == 2 {
				r.Humidity = float64(binary.LittleEndian.Uint16(v.Data())) * 0.01
			}
		case bthome.Humidity8.ID():
			if i, ok := v.Get().(int); ok {
				r.Humidity = float64(i)
			}
		case bthome.Voltage.ID(), bthome.Voltage10.ID():
			if f, ok := v.Get().(float32); ok {
				r.VoltageMV = int(f * 1000)
			}
		case bthome.Battery.ID():
			if i, ok := v.Get().(int); ok {
				battery = i
			}
		case bthome.BatteryLow.ID():
			if b, ok := v.Get().(bool); ok {
				r.BatteryLow = b
			}
		}
	}

	if r.VoltageMV == 0 && battery >= 0 {
		// No voltage in the packet: derive a plausible CR2032 voltage from
		// the battery percentage (see sensor.VoltageFromBatteryPct) so that
		// the low battery alarm of dbus-ble-sensors has something to work
		// with.
		r.BatteryPct = battery
		r.VoltageMV = sensor.VoltageFromBatteryPct(battery)
	}
	if r.Temperature == 0 && r.Humidity == 0 && r.VoltageMV == 0 && !r.BatteryLow {
		return nil, nil
	}
	return r, nil
}

// supportedBTHomeTLVs validates the measurement TLVs of a BTHome payload
// (data[0] is the device information byte). It returns a copy holding only
// TLVs that go-bthome can convert, or ok=false for anything malformed.
//
// Rationale: bthome.Payload.Parse panics on unknown type ids and on values
// that run past the end of the packet, and its GetDataValue returns nil for
// the uint8/16/32 and string data types. Real-world weather payloads
// (temperature, humidity, battery, voltage, binary sensors) are all covered;
// exotic TLVs are dropped instead of rejecting the whole packet.
func supportedBTHomeTLVs(data []byte) ([]byte, bool) {
	if len(data) < 2 || len(data) > 31 {
		return nil, false
	}
	out := make([]byte, 0, len(data))
	out = append(out, data[0])
	for i := 1; i < len(data); {
		dt := bthome.FindDataType(data[i])
		if dt == nil {
			return nil, false // unknown measurement type, size unknowable
		}
		size := dt.Size()
		if size < 0 || i+1+size > len(data) {
			return nil, false // variable length or truncated value
		}
		switch dt.TypeID() {
		case bthome.TypeFloat32, bthome.TypeInt8, bthome.TypeInt16, bthome.TypeBool:
			out = append(out, data[i:i+1+size]...)
		default:
			// convertible by the BTHome format but not by go-bthome:
			// drop this TLV, keep the rest of the packet
		}
		i += 1 + size
	}
	return out, true
}
