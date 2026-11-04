package decoders

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
)

// Govee BLE thermometers advertise manufacturer data with two company ids:
//
//   - 0xEC88 (on-air bytes "88 EC") for the H50xx family, and
//   - 0x0001 for the H51xx family.
//
// The formats below mirror Home Assistant's govee-ble integration
// (github.com/Bluetooth-Devices/govee-ble), which is the reference parser
// for these devices.
const (
	GoveeCompanyID    = 0xEC88 // H5072/H5074/H5075/H5071/H5051/H5052
	GoveeCompanyIDAlt = 0x0001 // H5101/H5102/H5104/H5108/H5174/H5177/GV5179

	// CompanyIDApple is Apple's company id, which Govee's glued
	// INTELLI_ROCKS blob uses (see stripIntelliRocks).
	CompanyIDApple = 0x004C
)

// goveeMinTemp / goveeMaxTemp is the validity window govee-ble applies to
// decoded temperatures; anything outside is treated as a garbled payload.
const (
	goveeMinTemp = -40.0
	goveeMaxTemp = 100.0
)

var (
	// ErrNotGovee covers payloads that no Govee layout matches.
	ErrNotGovee = errors.New("not a Govee payload")

	// ErrGoveeEncrypted marks the newer encrypted "broadcast v3"
	// advertisements, which carry no decodable values (yet). The company ids
	// 0x8803/0x8843 are not real Bluetooth SIG ids: Govee puts a flags byte
	// (0x03/0x43) right after the AD type, which BLE stacks read as the low
	// byte of a little endian company id. The payload starts with an EC 00
	// marker.
	ErrGoveeEncrypted = errors.New("govee encrypted v3 advertisement")
)

// Models whose local name ("Govee_H5074", ...) identifies the payload layout.
var goveeNameModels = []string{
	"H5074", "H5075", "H5072", "H5129",
	"H5101", "H5102", "H5104", "H5108", "H5174", "H5177", "GV5179",
}

// ModelFromName extracts the Govee model from a local name
// ("Govee_H5074_88DE" -> "H5074"), or "" when the name names no known model.
func ModelFromName(localName string) string {
	for _, m := range goveeNameModels {
		if strings.Contains(localName, m) {
			return m
		}
	}
	return ""
}

func isH5072Family(model string) bool {
	switch model {
	case "H5072", "H5075", "H5129":
		return true
	}
	return false
}

func isH5108Family(model string) bool {
	switch model {
	case "H5101", "H5102", "H5104", "H5108", "H5174", "H5177", "GV5179":
		return true
	}
	return false
}

// EncryptedV3 reports whether the frame is one of the newer encrypted
// "broadcast v3" advertisements.
func EncryptedV3(companyID uint16, data []byte) bool {
	if companyID == 0x8803 || companyID == 0x8843 {
		return true
	}
	return len(data) >= 2 && data[0] == 0xEC && data[1] == 0x00
}

// Govee firmware bug: many devices glue a 25-byte INTELLI_ROCKS vendor blob
// (mislabeled with Apple's company id) onto the end of their own
// manufacturer data. Home Assistant and ble_monitor strip exactly those 25
// bytes; without that the measurement payload length is wrong and every
// frame is rejected.
const intelliRocksLen = 25

var intelliRocksMarker = []byte("INTELLI_ROCKS")

// HasIntelliRocksMarker reports whether the payload contains the
// INTELLI_ROCKS vendor marker.
func HasIntelliRocksMarker(data []byte) bool {
	return bytes.Contains(data, intelliRocksMarker)
}

// stripIntelliRocks removes the glued INTELLI_ROCKS blob from the end of a
// Govee manufacturer payload, if present. The length gate keeps a bare blob
// (23 bytes, no measurement payload in front of it) from being stripped into
// a negative slice.
func stripIntelliRocks(data []byte) []byte {
	if len(data) > intelliRocksLen && HasIntelliRocksMarker(data) {
		return data[:len(data)-intelliRocksLen]
	}
	return data
}

// Govee decodes the manufacturer data of a Govee BLE thermometer and maps it
// onto a Reading. mac is the sensor identity (advertiser MAC or a pseudo MAC
// on macOS). localName is used to pin down the model ("Govee_H5074" ->
// "H5074") the same way govee-ble does.
//
// Supported payload layouts (data = manufacturer data after the company id):
//
//	H5074, 7 bytes (company id 0xEC88, or local name contains "H5074"):
//	  0      reserved
//	  1-2    temperature [1/100 degC], signed, little endian
//	  3-4    humidity    [1/100 %], little endian
//	  5      battery     [%]
//	  6      reserved
//
//	H5072/H5075/H5129, 6 bytes (company id 0xEC88, or local name):
//	  0      reserved
//	  1-3    temperature x 10 and humidity x 10 packed into 24 bit
//	         big endian: mag = temp_x10 * 1000 + hum_x10, bit 23 = sign
//	  4      battery [%] in bits 0-6, error flag in bit 7
//	  5      reserved
//
//	H5101/H5102/H5104/H5108/H5174/H5177/GV5179, 8 bytes (company id
//	0x0001, or local name):
//	  0-1    reserved
//	  2-4    packed temperature/humidity as above
//	  5      battery [%] in bits 0-6, error flag in bit 7
//	  6-7    reserved
//
// Govee does not report a battery voltage, so one is derived from the
// percentage (see sensor.VoltageFromBatteryPct) for the RuuviTag battery
// field.
func Govee(mac, localName string, companyID uint16, data []byte) (*sensor.Reading, error) {
	// Govee firmware bug: strip the glued INTELLI_ROCKS vendor blob first,
	// otherwise the payload length does not match any known layout.
	data = stripIntelliRocks(data)

	if EncryptedV3(companyID, data) {
		return nil, fmt.Errorf("%w: company id 0x%04x, %d byte payload",
			ErrGoveeEncrypted, companyID, len(data))
	}

	model := ModelFromName(localName)

	reading, frameErr := goveeFrame(mac, model, companyID, data)
	if frameErr == nil {
		return reading, nil
	}

	// The frame does not match any known layout. When the model is known from
	// the local name, retry the canonical layout on the leading bytes: some
	// firmwares append vendor data of unexpected size after the measurement
	// (INTELLI_ROCKS of a different size, encrypted v3 blobs, ...). The
	// temperature/humidity range gates keep garbage from being reported.
	if model != "" {
		if reading, err := goveePrefix(mac, model, data); err == nil {
			return reading, nil
		}
	}
	return nil, frameErr
}

// goveePrefix decodes the canonical layout of a name-matched model from the
// leading bytes of a longer frame.
func goveePrefix(mac, model string, data []byte) (*sensor.Reading, error) {
	var (
		r   *sensor.Reading
		err error
	)
	switch model {
	case "H5074":
		if len(data) < 7 {
			return nil, ErrNotGovee
		}
		r, err = goveeH5074(mac, model, data[:7])
	case "H5072", "H5075", "H5129":
		if len(data) < 6 {
			return nil, ErrNotGovee
		}
		r, err = goveePacked(mac, "H5072/H5075", data[:6], 1)
	default:
		if len(data) < 8 {
			return nil, ErrNotGovee
		}
		r, err = goveePacked(mac, model, data[:8], 2)
	}
	if err != nil {
		return nil, err
	}
	// The prefix path must not report garbage: humidity is only plausible
	// between 0 and 100 %.
	if r.Humidity < 0 || r.Humidity > 100 {
		return nil, fmt.Errorf("govee %s: humidity %.2f %% out of range", model, r.Humidity)
	}
	return r, nil
}

// goveeFrame decodes the known Govee payload layouts, selected by payload
// length, company id and local name.
func goveeFrame(mac, model string, companyID uint16, data []byte) (*sensor.Reading, error) {
	switch {
	case len(data) == 7 && (companyID == GoveeCompanyID || model == "H5074"):
		if model != "H5074" {
			model = "H5074"
		}
		return goveeH5074(mac, model, data)

	case len(data) == 6 && (companyID == GoveeCompanyID || isH5072Family(model)):
		if !isH5072Family(model) {
			model = "H5072/H5075"
		}
		return goveePacked(mac, model, data, 1)

	case (len(data) == 8 && companyID == GoveeCompanyIDAlt) ||
		(isH5108Family(model) && (len(data) == 6 || len(data) == 8)):
		if !isH5108Family(model) {
			model = "H5101/H5102/H5104/H5108/H5174/H5177/GV5179"
		}
		return goveePacked(mac, model, data, 2)
	}
	return nil, fmt.Errorf("%w: company id 0x%04x, %d byte payload",
		ErrNotGovee, companyID, len(data))
}

// goveeH5074 handles the 7 byte H5074 layout.
func goveeH5074(mac, model string, data []byte) (*sensor.Reading, error) {
	temp := float64(int16(binary.LittleEndian.Uint16(data[1:3]))) / 100
	hum := float64(binary.LittleEndian.Uint16(data[3:5])) / 100
	batt := int(data[5])
	if temp < goveeMinTemp || temp > goveeMaxTemp {
		return nil, fmt.Errorf("govee %s: temperature %.2f degC out of range", model, temp)
	}
	return &sensor.Reading{
		MAC:         mac,
		Model:       model,
		Temperature: temp,
		Humidity:    hum,
		BatteryPct:  batt,
		VoltageMV:   sensor.VoltageFromBatteryPct(batt),
		Source:      "govee",
	}, nil
}

// goveePacked handles the 6 and 8 byte layouts with the packed
// temperature/humidity value at the given offset.
func goveePacked(mac, model string, data []byte, off int) (*sensor.Reading, error) {
	temp, hum := goveeTempHumid(data[off : off+3])
	battByte := data[off+3]
	if battByte&0x80 != 0 {
		return nil, fmt.Errorf("govee %s: error bit set (byte 0x%02x)", model, battByte)
	}
	batt := int(battByte & 0x7F)
	if temp < goveeMinTemp || temp > goveeMaxTemp {
		return nil, fmt.Errorf("govee %s: temperature %.1f degC out of range", model, temp)
	}
	return &sensor.Reading{
		MAC:         mac,
		Model:       model,
		Temperature: temp,
		Humidity:    hum,
		BatteryPct:  batt,
		VoltageMV:   sensor.VoltageFromBatteryPct(batt),
		Source:      "govee",
	}, nil
}

// goveeTempHumid decodes the 24 bit packed temperature/humidity value:
// magnitude = temp_x10 * 1000 + hum_x10, bit 23 marks negative temperatures.
func goveeTempHumid(b []byte) (temp float64, hum float64) {
	base := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	negative := base&0x800000 != 0
	mag := base & 0x7FFFFF
	temp = float64(mag/1000) / 10
	hum = float64(mag%1000) / 10
	if negative {
		temp = -temp
	}
	return temp, hum
}
