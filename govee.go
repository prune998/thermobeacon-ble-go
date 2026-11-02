package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Govee BLE thermometers advertise manufacturer data with two company ids:
//
//   - 0xEC88 (on-air bytes "88 EC") for the H50xx family, and
//   - 0x0001 for the H51xx family.
//
// The formats below mirror Home Assistant's govee-ble integration
// (github.com/Bluetooth-Devices/govee-ble), which is the reference parser for
// these devices.
const (
	goveeCompanyIDH50xx = 0xEC88 // H5072/H5074/H5075/H5071/H5051/H5052
	goveeCompanyIDH51xx = 0x0001 // H5101/H5102/H5104/H5108/H5174/H5177/GV5179
)

// goveeMinTemp / goveeMaxTemp is the validity window govee-ble applies to
// decoded temperatures; anything outside is treated as a garbled payload.
const (
	goveeMinTemp = -40.0
	goveeMaxTemp = 100.0
)

// Govee firmware bug: many devices glue a 25-byte INTELLI_ROCKS vendor blob
// (mislabeled with Apple's company id 0x004C) onto the end of their own
// manufacturer data. Home Assistant and ble_monitor strip exactly those 25
// bytes; without that the measurement payload length is wrong and every
// frame is rejected.
const (
	goveeCompanyIDApple = 0x004C
	intelliRocksLen     = 25
)

var intelliRocksMarker = []byte("INTELLI_ROCKS")

// stripIntelliRocks removes the glued INTELLI_ROCKS blob from the end of a
// Govee manufacturer payload, if present.
func stripIntelliRocks(data []byte) []byte {
	if len(data) > intelliRocksLen && bytes.Contains(data, intelliRocksMarker) {
		return data[:len(data)-intelliRocksLen]
	}
	return data
}

var errNotGovee = errors.New("not a Govee payload")

// Models whose local name ("Govee_H5074", ...) identifies the payload layout.
var goveeNameModels = []string{
	"H5074", "H5075", "H5072", "H5129",
	"H5101", "H5102", "H5104", "H5108", "H5174", "H5177", "GV5179",
}

func goveeModelFromName(localName string) string {
	for _, m := range goveeNameModels {
		if strings.Contains(localName, m) {
			return m
		}
	}
	return ""
}

func goveeIsH5072Family(model string) bool {
	switch model {
	case "H5072", "H5075", "H5129":
		return true
	}
	return false
}

func goveeIsH5108Family(model string) bool {
	switch model {
	case "H5101", "H5102", "H5104", "H5108", "H5174", "H5177", "GV5179":
		return true
	}
	return false
}

// decodeGovee decodes the manufacturer data of a Govee BLE thermometer and
// maps it onto a Reading. mac is the sensor identity (advertiser MAC or a
// pseudo MAC on macOS, see identityFor). localName is used to pin down the
// model ("Govee_H5074" -> "H5074") the same way govee-ble does.
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
// percentage (see voltageFromBatteryPct) for the RuuviTag battery field.
func decodeGovee(mac, localName string, companyID uint16, data []byte) (*Reading, error) {
	// Govee firmware bug: strip the glued INTELLI_ROCKS vendor blob first,
	// otherwise the payload length does not match any known layout.
	data = stripIntelliRocks(data)

	model := goveeModelFromName(localName)

	switch {
	case len(data) == 7 && (companyID == goveeCompanyIDH50xx || model == "H5074"):
		if model != "H5074" {
			model = "H5074"
		}
		return decodeGoveeH5074(mac, model, data)

	case len(data) == 6 && (companyID == goveeCompanyIDH50xx || goveeIsH5072Family(model)):
		if !goveeIsH5072Family(model) {
			model = "H5072/H5075"
		}
		return goveeDecodePacked(mac, model, data, 1)

	case (len(data) == 8 && companyID == goveeCompanyIDH51xx) ||
		(goveeIsH5108Family(model) && (len(data) == 6 || len(data) == 8)):
		if !goveeIsH5108Family(model) {
			model = "H5101/H5102/H5104/H5108/H5174/H5177/GV5179"
		}
		return goveeDecodePacked(mac, model, data, 2)
	}
	return nil, fmt.Errorf("%w: company id 0x%04x, %d byte payload, name %q",
		errNotGovee, companyID, len(data), localName)
}

// decodeGoveeH5074 handles the 7 byte H5074 layout.
func decodeGoveeH5074(mac, model string, data []byte) (*Reading, error) {
	temp := float64(int16(binary.LittleEndian.Uint16(data[1:3]))) / 100
	hum := float64(binary.LittleEndian.Uint16(data[3:5])) / 100
	batt := int(data[5])
	if temp < goveeMinTemp || temp > goveeMaxTemp {
		return nil, fmt.Errorf("govee %s: temperature %.2f degC out of range", model, temp)
	}
	return &Reading{
		MAC:         mac,
		Model:       model,
		Temperature: temp,
		Humidity:    hum,
		BatteryPct:  batt,
		VoltageMV:   voltageFromBatteryPct(batt),
		Source:      "govee",
	}, nil
}

// goveeDecodePacked handles the 6 and 8 byte layouts with the packed
// temperature/humidity value at the given offset.
func goveeDecodePacked(mac, model string, data []byte, off int) (*Reading, error) {
	temp, hum := goveeDecodeTempHumid(data[off : off+3])
	battByte := data[off+3]
	if battByte&0x80 != 0 {
		return nil, fmt.Errorf("govee %s: error bit set (byte 0x%02x)", model, battByte)
	}
	batt := int(battByte & 0x7F)
	if temp < goveeMinTemp || temp > goveeMaxTemp {
		return nil, fmt.Errorf("govee %s: temperature %.1f degC out of range", model, temp)
	}
	return &Reading{
		MAC:         mac,
		Model:       model,
		Temperature: temp,
		Humidity:    hum,
		BatteryPct:  batt,
		VoltageMV:   voltageFromBatteryPct(batt),
		Source:      "govee",
	}, nil
}

// goveeDecodeTempHumid decodes the 24 bit packed temperature/humidity value:
// magnitude = temp_x10 * 1000 + hum_x10, bit 23 marks negative temperatures.
func goveeDecodeTempHumid(b []byte) (temp float64, hum float64) {
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
