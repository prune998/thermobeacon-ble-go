//go:build linux

package bluescan

import (
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

// TestMakeScanResult checks the D-Bus property -> ScanResult port against the
// property shapes BlueZ sends in Device1 PropertiesChanged signals.
func TestMakeScanResult(t *testing.T) {
	props := map[string]dbus.Variant{
		"Address":     dbus.MakeVariant("A4:C1:38:30:88:DE"),
		"AddressType": dbus.MakeVariant("public"),
		"UUIDs":       dbus.MakeVariant([]string{}),
		"Name":        dbus.MakeVariant("Oria"),
		"RSSI":        dbus.MakeVariant(int16(-52)),
		"ManufacturerData": dbus.MakeVariant(map[uint16]dbus.Variant{
			0x0010: dbus.MakeVariant([]byte{
				0x80, 0x00, 0xde, 0x88, 0x30, 0x38, 0xc1, 0xa4,
				0x40, 0x0b, 0x34, 0x01, 0xa0, 0x03, 0x0f, 0x87,
				0x20, 0x00,
			}),
		}),
		"ServiceData": dbus.MakeVariant(map[string]dbus.Variant{}),
	}

	r := makeScanResult(props)

	// bluetooth.MAC.String() renders uppercase hex (v0.17.0: hexDigit =
	// "0123456789ABCDEF"). The app only matches addresses case-insensitively
	// (AcceptsDevice / NormalizeMAC), so assert the round-trip, not the case.
	if got := r.Address.String(); !strings.EqualFold(got, "a4:c1:38:30:88:de") {
		t.Errorf("Address.String() = %q, want a4:c1:38:30:88:de (case-insensitive)", got)
	}
	if r.RSSI != -52 {
		t.Errorf("RSSI = %d, want -52", r.RSSI)
	}
	if name := r.LocalName(); name != "Oria" {
		t.Errorf("LocalName() = %q, want Oria", name)
	}
	if r.HasServiceUUID(bluetooth.ServiceUUIDBattery) {
		t.Error("HasServiceUUID = true for an empty UUID list")
	}
	mfg := r.ManufacturerData()
	if len(mfg) != 1 || mfg[0].CompanyID != 0x0010 {
		t.Fatalf("ManufacturerData() = %+v, want one 0x0010 element", mfg)
	}
	if len(mfg[0].Data) != 18 {
		t.Errorf("manufacturer data length = %d, want 18", len(mfg[0].Data))
	}
	if sd := r.ServiceData(); len(sd) != 0 {
		t.Errorf("ServiceData() = %+v, want empty", sd)
	}
	if r.Bytes() != nil {
		t.Error("Bytes() = non-nil for structured fields")
	}
}
