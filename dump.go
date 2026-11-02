package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"strings"
	"time"

	bthome "github.com/hybridgroup/go-bthome"
	"tinygo.org/x/bluetooth"
)

// adTypeNames maps AD structure type ids to human readable names (the subset
// that shows up in sensor advertisements). See the Bluetooth Core
// Specification Supplement, "Generic Access Profile data types".
var adTypeNames = map[byte]string{
	0x01: "flags",
	0x02: "incomplete 16-bit service UUIDs",
	0x03: "complete 16-bit service UUIDs",
	0x06: "incomplete 128-bit service UUIDs",
	0x07: "complete 128-bit service UUIDs",
	0x08: "shortened local name",
	0x09: "complete local name",
	0x0A: "tx power level",
	0x16: "service data (16-bit UUID)",
	0xFF: "manufacturer data",
}

// debugDumpScan logs an annotated dump of one advertisement: the raw AD
// structures (when the platform exposes the raw packet), the manufacturer
// data with a ThermoBeacon field breakdown, and the service data with a
// BTHome TLV breakdown. No-op unless -debug is set.
func debugDumpScan(device bluetooth.ScanResult, senderMAC string) {
	if !debugEnabled {
		return
	}
	text := dumpScanResultText(device, senderMAC)
	for _, line := range strings.Split(text, "\n") {
		log.Print("DEBUG " + line)
	}
}

// debugDumpRuuvi logs an annotated breakdown of an encoded RuuviTag RAWv2
// payload. No-op unless -debug is set.
func debugDumpRuuvi(payload []byte) {
	if !debugEnabled {
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "ruuvi RAWv2 payload, %d bytes:\n", len(payload))
	writeHexDump(&sb, payload, "  ")
	writeRuuviFields(&sb, payload)
	for _, line := range strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n") {
		log.Print("DEBUG " + line)
	}
}

// dumpScanResultText renders the annotated dump of one advertisement.
func dumpScanResultText(device bluetooth.ScanResult, senderMAC string) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "advertisement %s rssi %d dBm", device.Address.String(), device.RSSI)
	if name := device.LocalName(); name != "" {
		fmt.Fprintf(&sb, ", name %q", name)
	}
	sb.WriteString("\n")

	// Raw advertisement bytes are only available on platforms with a raw
	// packet payload (e.g. Linux); structured payloads return nil here.
	if raw := device.Bytes(); len(raw) > 0 {
		sb.WriteString("  raw AD structures:\n")
		iterADStructures(raw, func(typ byte, field []byte) {
			name, ok := adTypeNames[typ]
			if !ok {
				name = fmt.Sprintf("type 0x%02x", typ)
			}
			fmt.Fprintf(&sb, "    [%s] %s\n", name, hexSpaced(field))
		})
	}

	mfg := device.ManufacturerData()
	svc := device.ServiceData()
	if len(mfg) == 0 && len(svc) == 0 {
		sb.WriteString("  no manufacturer or service data")
		return strings.TrimSuffix(sb.String(), "\n")
	}

	for _, element := range mfg {
		if element.CompanyID == goveeCompanyIDApple {
			fmt.Fprintf(&sb, "  manufacturer data: company id 0x%04x (Apple / INTELLI_ROCKS blob emitted by Govee firmware), %d bytes - ignored\n",
				element.CompanyID, len(element.Data))
			continue
		}
		known := ""
		if isThermoBeaconCompanyID(element.CompanyID) {
			known = ", known ThermoBeacon model"
		} else if element.CompanyID == goveeCompanyIDH50xx || element.CompanyID == goveeCompanyIDH51xx {
			known = ", Govee company id"
		}
		fmt.Fprintf(&sb, "  manufacturer data: company id 0x%04x%s, %d byte payload:\n",
			element.CompanyID, known, len(element.Data))
		writeHexDump(&sb, element.Data, "    ")
		// Reuse the decoders so the dump shows exactly what the bridge would
		// accept, including why a payload is rejected.
		tbReading, tbErr := decodeThermoBeacon(senderMAC, element.CompanyID, element.Data)
		if tbErr == nil {
			fmt.Fprintf(&sb, "    ThermoBeacon decoded: %s\n", tbReading)
			writeThermoBeaconFields(&sb, element.Data)
			continue
		}
		goveeReading, govErr := decodeGovee("", device.LocalName(), element.CompanyID, element.Data)
		if govErr == nil {
			fmt.Fprintf(&sb, "    Govee %s decoded: %.2f degC, %.2f %%, battery %d %%\n",
				goveeReading.Model, goveeReading.Temperature, goveeReading.Humidity, goveeReading.BatteryPct)
			writeGoveeFields(&sb, element.Data)
			continue
		}
		fmt.Fprintf(&sb, "    not a ThermoBeacon/Govee payload:\n      %v\n      %v\n", tbErr, govErr)
	}

	for _, element := range svc {
		fmt.Fprintf(&sb, "  service data %s, %d bytes:\n", element.UUID.String(), len(element.Data))
		writeHexDump(&sb, element.Data, "    ")
		if element.UUID == bthome.ServiceUUID {
			writeBTHomeFields(&sb, element.Data)
		}
	}

	return strings.TrimSuffix(sb.String(), "\n")
}

// writeThermoBeaconFields prints the field-by-field breakdown of an 18 byte
// ThermoBeacon manufacturer payload (excluding the company id).
func writeThermoBeaconFields(sb *strings.Builder, data []byte) {
	flags := binary.LittleEndian.Uint16(data[0:2])
	button := "released"
	if flags&0x80 != 0 {
		button = "pressed"
	}
	fmt.Fprintf(sb, "    0-1    flags        0x%04x  button %s\n", flags, button)
	fmt.Fprintf(sb, "    2-7    MAC (LSB)    %s  -> %s\n", hexSpaced(data[2:8]), macFromLSBFirst(data[2:8]))
	voltage := binary.LittleEndian.Uint16(data[8:10])
	fmt.Fprintf(sb, "    8-9    battery      0x%04x  %d mV (%.3f V)\n", voltage, voltage, float64(voltage)/1000)
	tempRaw := int16(binary.LittleEndian.Uint16(data[10:12]))
	fmt.Fprintf(sb, "    10-11  temperature  0x%04x  %d x 1/16 degC = %.4f degC\n",
		uint16(tempRaw), tempRaw, float64(tempRaw)/16)
	humRaw := binary.LittleEndian.Uint16(data[12:14])
	fmt.Fprintf(sb, "    12-13  humidity     0x%04x  %d x 1/16 %% = %.4f %%\n", humRaw, humRaw, float64(humRaw)/16)
	uptime := binary.LittleEndian.Uint32(data[14:18])
	fmt.Fprintf(sb, "    14-17  uptime       0x%08x  %d s (%s)\n",
		uptime, uptime, humanDuration(time.Duration(uptime)*time.Second))
}

// writeBTHomeFields prints the TLV breakdown of a BTHome service data payload
// (data[0] is the device information byte). Unlike the decoder, it shows
// unsupported and truncated TLVs as well, so the dump explains the whole
// packet.
func writeBTHomeFields(sb *strings.Builder, data []byte) {
	if len(data) < 1 {
		return
	}
	di := data[0]
	encrypted := "unencrypted"
	if di&0x80 != 0 {
		encrypted = "ENCRYPTED (not supported)"
	}
	fmt.Fprintf(sb, "    device information 0x%02x (BTHome v%d, %s)\n", di, (di>>5)&0b11, encrypted)

	for i := 1; i < len(data); {
		dt := bthome.FindDataType(data[i])
		if dt == nil {
			fmt.Fprintf(sb, "    type 0x%02x  unknown type, length unknowable - stopping\n", data[i])
			return
		}
		size := dt.Size()
		if size < 0 || i+1+size > len(data) {
			fmt.Fprintf(sb, "    type 0x%02x  %-14s truncated, %d byte(s) left\n",
				data[i], dt.Name(), len(data)-i-1)
			return
		}
		raw := data[i+1 : i+1+size]
		fmt.Fprintf(sb, "    type 0x%02x  %-14s %d byte  %s  -> %s\n",
			data[i], dt.Name(), size, hexSpaced(raw), bthomeValueText(dt, raw))
		i += 1 + size
	}
}

// bthomeValueText renders one BTHome measurement value, applying the same
// corrections as decodeBTHome.
func bthomeValueText(dt bthome.DataType, raw []byte) string {
	// go-bthome scales type 0x03 by 1.0, but the BTHome format defines 0.01.
	if dt.ID() == bthome.Humidity16.ID() && len(raw) == 2 {
		v := binary.LittleEndian.Uint16(raw)
		return fmt.Sprintf("%.2f %% (raw %d x 0.01)", float64(v)*0.01, v)
	}
	switch dt.TypeID() {
	case bthome.TypeFloat32, bthome.TypeInt8, bthome.TypeInt16, bthome.TypeBool:
		v := bthome.GetDataValue(dt, raw)
		if v == nil {
			return "?"
		}
		if dt.Unit() == "" {
			return fmt.Sprintf("%v", v.Get())
		}
		return fmt.Sprintf("%v %s", v.Get(), dt.Unit())
	default:
		return "not convertible by go-bthome"
	}
}

// writeGoveeFields prints the field-by-field breakdown of a Govee payload.
// The layout is selected by payload length, matching the branches of
// decodeGovee.
func writeGoveeFields(sb *strings.Builder, data []byte) {
	switch len(data) {
	case 7: // H5074
		goveeRow(sb, "0", "reserved", hexSpaced(data[0:1]))
		temp := int16(binary.LittleEndian.Uint16(data[1:3]))
		goveeRow(sb, "1-2", "temperature", fmt.Sprintf("%s  -> %d / 100 = %.2f degC", hexSpaced(data[1:3]), temp, float64(temp)/100))
		hum := binary.LittleEndian.Uint16(data[3:5])
		goveeRow(sb, "3-4", "humidity", fmt.Sprintf("%s  -> %d / 100 = %.2f %%", hexSpaced(data[3:5]), hum, float64(hum)/100))
		goveeRow(sb, "5", "battery", fmt.Sprintf("%02x  -> %d %%", data[5], data[5]))
		goveeRow(sb, "6", "reserved", hexSpaced(data[6:7]))
	case 6: // H5072/H5075/H5129
		goveeRow(sb, "0", "reserved", hexSpaced(data[0:1]))
		writeGoveePackedFields(sb, "1-3", data[1:4])
		writeGoveeBatteryField(sb, "4", data[4])
		goveeRow(sb, "5", "reserved", hexSpaced(data[5:6]))
	case 8: // H5101/H5102/H5104/H5108/H5174/H5177/GV5179
		goveeRow(sb, "0-1", "reserved", hexSpaced(data[0:2]))
		writeGoveePackedFields(sb, "2-4", data[2:5])
		writeGoveeBatteryField(sb, "5", data[5])
		goveeRow(sb, "6-7", "reserved", hexSpaced(data[6:8]))
	default:
		fmt.Fprintf(sb, "    unknown Govee payload layout (%d bytes)\n", len(data))
	}
}

func goveeRow(sb *strings.Builder, offset, name, value string) {
	fmt.Fprintf(sb, "    %-6s %-12s %s\n", offset, name, value)
}

// writeGoveePackedFields prints the packed 24 bit temperature/humidity value
// of the H5072/H5075 and H51xx layouts.
func writeGoveePackedFields(sb *strings.Builder, offset string, b []byte) {
	packed := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	negative := ""
	if packed&0x800000 != 0 {
		negative = "-"
	}
	mag := packed & 0x7FFFFF
	goveeRow(sb, offset, "temp+humid", fmt.Sprintf("%s  -> mag %s%d: %.1f degC, %.1f %%",
		hexSpaced(b), negative, mag, float64(mag/1000)/10, float64(mag%1000)/10))
}

func writeGoveeBatteryField(sb *strings.Builder, offset string, b byte) {
	flag := ""
	if b&0x80 != 0 {
		flag = ", error bit set"
	}
	goveeRow(sb, offset, "battery", fmt.Sprintf("%02x  -> %d %%%s", b, b&0x7F, flag))
}

// writeRuuviFields prints the field-by-field breakdown of a 24 byte RuuviTag
// RAWv2 payload as encoded by encodeRuuviRAWv2.
func writeRuuviFields(sb *strings.Builder, p []byte) {
	if len(p) != ruuviPayloadLen {
		fmt.Fprintf(sb, "  unexpected payload length %d, want %d\n", len(p), ruuviPayloadLen)
		return
	}
	temp := int16(binary.BigEndian.Uint16(p[1:3]))
	hum := binary.BigEndian.Uint16(p[3:5])
	pressure := binary.BigEndian.Uint16(p[5:7])
	accelX := int16(binary.BigEndian.Uint16(p[7:9]))
	accelY := int16(binary.BigEndian.Uint16(p[9:11]))
	accelZ := int16(binary.BigEndian.Uint16(p[11:13]))
	power := binary.BigEndian.Uint16(p[13:15])
	movement := p[15]
	seqno := binary.BigEndian.Uint16(p[16:18])

	fmt.Fprintf(sb, "  format       %d (RAWv2)\n", p[0])
	if temp == -0x8000 {
		sb.WriteString("  temperature  invalid (0x8000)\n")
	} else {
		fmt.Fprintf(sb, "  temperature  %.4f degC (raw %d)\n", float64(temp)/200, temp)
	}
	if hum == 0xFFFF {
		sb.WriteString("  humidity     invalid (0xFFFF)\n")
	} else {
		fmt.Fprintf(sb, "  humidity     %.4f %% (raw %d)\n", float64(hum)/400, hum)
	}
	if pressure == 0xFFFF {
		sb.WriteString("  pressure     invalid (0xFFFF)\n")
	} else {
		fmt.Fprintf(sb, "  pressure     %.2f hPa (raw %d)\n", float64(pressure)/100, pressure)
	}
	if accelX == -0x8000 && accelY == -0x8000 && accelZ == -0x8000 {
		sb.WriteString("  acceleration x/y/z invalid (0x8000)\n")
	} else {
		fmt.Fprintf(sb, "  acceleration x=%.0f y=%.0f z=%.0f mG\n",
			float64(accelX), float64(accelY), float64(accelZ))
	}
	batt := power >> 5
	txPower := power & 0x1F
	fmt.Fprintf(sb, "  battery      %d mV\n", batt+1600)
	if txPower == 0x1F {
		sb.WriteString("  tx power     invalid (0x1F)\n")
	} else {
		fmt.Fprintf(sb, "  tx power     %d dBm\n", -40+int(txPower)*2)
	}
	if movement == 0xFF {
		sb.WriteString("  movement     invalid (0xFF)\n")
	} else {
		fmt.Fprintf(sb, "  movement     %d\n", movement)
	}
	fmt.Fprintf(sb, "  sequence     %d (0x%04x)\n", seqno, seqno)
	fmt.Fprintf(sb, "  MAC          %s\n", macFromMSBFirst(p[18:24]))
}

// writeHexDump renders data as offset / hex / ASCII rows, 16 bytes per line.
func writeHexDump(sb *strings.Builder, data []byte, indent string) {
	for off := 0; off < len(data); off += 16 {
		end := min(off+16, len(data))
		row := data[off:end]

		var hexCol strings.Builder
		for i := 0; i < 16; i++ {
			if i == 8 {
				hexCol.WriteByte(' ')
			}
			if off+i < end {
				fmt.Fprintf(&hexCol, " %02x", row[i])
			} else {
				hexCol.WriteString("   ")
			}
		}
		var ascii strings.Builder
		for i := 0; i < 16; i++ {
			if off+i < end {
				if b := row[i]; b >= 0x20 && b < 0x7F {
					ascii.WriteByte(b)
				} else {
					ascii.WriteByte('.')
				}
			} else {
				ascii.WriteByte(' ')
			}
		}
		fmt.Fprintf(sb, "%s%04x %s  %s\n", indent, off, hexCol.String(), ascii.String())
	}
}

// iterADStructures walks the TLV structures of a raw advertisement packet,
// yielding (type, field) pairs. Malformed data stops the walk: advertisement
// data comes straight off the air and must not be trusted.
func iterADStructures(data []byte, fn func(typ byte, field []byte)) {
	i := 0
	for i < len(data) {
		length := int(data[i])
		if length < 1 || i+1+length > len(data) {
			return
		}
		fn(data[i+1], data[i+2:i+1+length])
		i += 1 + length
	}
}

func hexSpaced(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02x", c)
	}
	return strings.Join(parts, " ")
}

// humanDuration renders a duration as "24d 16h 8m 47s".
func humanDuration(d time.Duration) string {
	const day = 24 * time.Hour
	var parts []string
	if days := d / day; days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
		d -= days * day
	}
	if h := d / time.Hour; h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
		d -= h * time.Hour
	}
	if m := d / time.Minute; m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
		d -= m * time.Minute
	}
	if s := d / time.Second; s > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	return strings.Join(parts, " ")
}
