package decoders

import (
	"tinygo.org/x/bluetooth"
)

// ibeaconDataLen is the iBeacon manufacturer payload after the company id:
// type (1) + length (1) + proximity UUID (16) + major (2) + minor (2) +
// measured TX power (1).
const ibeaconDataLen = 23

// IsIBeacon reports whether the manufacturer data element is an iBeacon
// (Apple's proximity protocol): Apple company id, type 0x02 + length 0x15,
// followed by a 16-byte proximity UUID, 2-byte major, 2-byte minor and a
// 1-byte measured TX power.
//
// Note that Govee devices emit a vendor blob in exactly this framing (the
// "INTELLI_ROCKS" payload) - it is an iBeacon-format frame and is skipped
// like any other iBeacon.
func IsIBeacon(companyID uint16, data []byte) bool {
	return companyID == CompanyIDApple &&
		len(data) == ibeaconDataLen &&
		data[0] == 0x02 && data[1] == 0x15
}

// IsIBeaconAdvertisement reports whether an advertisement carries nothing
// but iBeacon data: only Apple company id manufacturer elements (at least
// one of them an actual iBeacon frame) and no service data. Such
// advertisements carry no sensor data and are dropped by the bridge.
func IsIBeaconAdvertisement(mfg []bluetooth.ManufacturerDataElement, svc []bluetooth.ServiceDataElement) bool {
	if len(svc) > 0 || len(mfg) == 0 {
		return false
	}
	sawIBeacon := false
	for _, m := range mfg {
		if m.CompanyID != CompanyIDApple {
			return false
		}
		if IsIBeacon(m.CompanyID, m.Data) {
			sawIBeacon = true
		}
	}
	return sawIBeacon
}
