package main

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ruuviManufacturerID is Ruuvi Innovations' Bluetooth SIG company id. Victron's
// dbus-ble-sensors has no ThermoBeacon decoder, so the readings are handed to
// it in the open RuuviTag data format 5 ("RAWv2") of a sensor type it supports.
const ruuviManufacturerID = 0x0499

const (
	udpPacketVersion1 = 1
	udpFlagRSSI       = 0x01
	ruuviPayloadLen   = 24
)

// encodeRuuviRAWv2 encodes a reading as RuuviTag data format 5
// (24 bytes, big endian).
//
// Fields the sensor does not have are sent as "invalid", so dbus-ble-sensors
// does not publish them:
//
//   - temperature: 0.005 degC steps
//   - humidity: clamped to 0-100 %, 0.0025 % steps
//   - pressure, acceleration, TX power, movement counter: invalid
//   - battery: clamped to 1.600-3.646 V
//   - sequence number: caller's choice (uptime for ThermoBeacons)
//   - MAC: sensor MAC in network order
func encodeRuuviRAWv2(r *Reading, seqno uint16) ([]byte, error) {
	if _, err := macToBytes(r.MAC); err != nil {
		return nil, err
	}

	buf := make([]byte, ruuviPayloadLen)
	buf[0] = 5

	temp := int16(math.Round(clamp(r.Temperature, -163.835, 163.835) * 200))
	hum := uint16(math.Round(clamp(r.Humidity, 0, 100) * 400))

	binary.BigEndian.PutUint16(buf[1:3], uint16(temp))
	binary.BigEndian.PutUint16(buf[3:5], hum)

	binary.BigEndian.PutUint16(buf[5:7], 0xFFFF)   // pressure: invalid
	binary.BigEndian.PutUint16(buf[7:9], 0x8000)   // acceleration x: invalid
	binary.BigEndian.PutUint16(buf[9:11], 0x8000)  // acceleration y: invalid
	binary.BigEndian.PutUint16(buf[11:13], 0x8000) // acceleration z: invalid

	batt := clampInt(r.VoltageMV, 1600, 3646) - 1600
	if batt == 0x3FF {
		batt = 0x3FE // dbus-ble-sensors treats raw 0x3ff as invalid - avoid it
	}
	binary.BigEndian.PutUint16(buf[13:15], uint16(batt<<5|0x1F)) // tx power: invalid

	buf[15] = 0xFF // movement counter: invalid
	binary.BigEndian.PutUint16(buf[16:18], seqno)

	mac, _ := macToBytes(r.MAC)
	copy(buf[18:24], mac[:])
	return buf, nil
}

// buildUDPPacket wraps a manufacturer payload in the UDP gateway packet
// format v1 of Victron's dbus-ble-sensors:
//
//	version (1) | flags (1) | address (6, LSB first) |
//	manufacturer id (2, LE) | payload length (1) | payload | rssi (1)
//
// rssi is always included (flag 0x01).
func buildUDPPacket(mac string, mfgID uint16, payload []byte, rssi int16) ([]byte, error) {
	bd, err := macToBDAddr(mac)
	if err != nil {
		return nil, fmt.Errorf("udp packet: %w", err)
	}
	if len(payload) > 255 {
		return nil, fmt.Errorf("udp packet: payload too long (%d bytes)", len(payload))
	}

	pkt := make([]byte, 0, 10+len(payload))
	pkt = append(pkt, udpPacketVersion1, udpFlagRSSI)
	pkt = append(pkt, bd[:]...)
	var hdr [3]byte
	binary.LittleEndian.PutUint16(hdr[0:2], mfgID)
	hdr[2] = byte(len(payload))
	pkt = append(pkt, hdr[:]...)
	pkt = append(pkt, payload...)
	pkt = append(pkt, byte(int8(clampInt(int(rssi), -128, 127))))
	return pkt, nil
}
