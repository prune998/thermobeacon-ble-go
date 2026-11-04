// Package decoders decodes the manufacturer/service data of the supported
// BLE sensor formats (ThermoBeacon, Govee, BTHome) into sensor.Reading.
//
// All decoders take the raw payload as delivered by the BLE stack and return
// a *sensor.Reading; they are strict — malformed payloads, out of range
// values and frames of unknown shape are rejected with descriptive errors so
// the payload dump can explain exactly why a frame was not decoded.
package decoders

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/prune998/thermobeacon-ble-go/internal/sensor"
)

// thermobeaconDataLen is the length of the ThermoBeacon manufacturer specific
// data excluding the 2 byte company id.
const thermobeaconDataLen = 18

// Company ids used by known ThermoBeacon models (Oria, Brifit, SensorBlue,
// ...). The original dbus-ble-temp does not need them because it can compare
// the MAC embedded in the payload with the advertiser address; macOS only
// exposes an opaque UUID, so there a known company id is required as well.
const (
	companyIDThermoBeacon1 = 0x0010
	companyIDThermoBeacon2 = 0x0011
	companyIDThermoBeacon3 = 0x0015
)

var ErrNotThermoBeacon = errors.New("not a ThermoBeacon payload")

// IsThermoBeaconCompanyID reports whether id belongs to a known ThermoBeacon
// model.
func IsThermoBeaconCompanyID(id uint16) bool {
	switch id {
	case companyIDThermoBeacon1, companyIDThermoBeacon2, companyIDThermoBeacon3:
		return true
	}
	return false
}

// ThermoBeacon decodes the manufacturer specific data of a ThermoBeacon
// (Oria, Brifit, SensorBlue and other clones).
//
// data is the payload after the 2 byte company id (18 bytes, little endian):
//
//	 0-1   flags (bit 7 of byte 0 = button pressed)
//	 2-7   device MAC, LSB first
//	 8-9   battery voltage [mV]
//	10-11  temperature [1/16 degC], signed
//	12-13  humidity [1/16 %]
//	14-17  uptime [s]
//
// senderMAC is the advertiser's MAC address, empty on platforms that only
// expose an opaque device id (macOS). When set, the embedded MAC must match
// it in either byte order - this is the original's signature to tell
// ThermoBeacons apart from other 18 byte manufacturer data. Without a sender
// MAC, companyID must be one of the known ThermoBeacon ids.
//
// ThermoBeacons also send a 22 byte min/max packet, which fails the length
// check and is ignored, as in the original.
func ThermoBeacon(senderMAC string, companyID uint16, data []byte) (*sensor.Reading, error) {
	if len(data) != thermobeaconDataLen {
		return nil, fmt.Errorf("%w: wrong length %d, want %d",
			ErrNotThermoBeacon, len(data), thermobeaconDataLen)
	}

	embeddedLSB := sensor.MACFromLSBFirst(data[2:8])
	mac := embeddedLSB

	if senderMAC != "" {
		embeddedMSB := sensor.MACFromMSBFirst(data[2:8])
		if senderMAC != embeddedLSB && senderMAC != embeddedMSB {
			return nil, fmt.Errorf("%w: embedded MAC does not match advertiser %s",
				ErrNotThermoBeacon, senderMAC)
		}
		mac = senderMAC
	} else if !IsThermoBeaconCompanyID(companyID) {
		return nil, fmt.Errorf("%w: unknown company id 0x%04x", ErrNotThermoBeacon, companyID)
	}

	flags := binary.LittleEndian.Uint16(data[0:2])

	return &sensor.Reading{
		MAC:         mac,
		Temperature: float64(int16(binary.LittleEndian.Uint16(data[10:12]))) / 16.0,
		Humidity:    float64(binary.LittleEndian.Uint16(data[12:14])) / 16.0,
		VoltageMV:   int(binary.LittleEndian.Uint16(data[8:10])),
		BatteryPct:  -1, // the ThermoBeacon reports a voltage, not a percentage
		Uptime:      binary.LittleEndian.Uint32(data[14:18]),
		Button:      flags&0x80 != 0,
		Source:      "thermobeacon",
	}, nil
}
