//go:build linux

package bluescan

import (
	"errors"
	"log"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

// Match rule constants, mirroring upstream gap_linux.go.
const (
	dbusPropertiesChangedInterfaceName = 0
	dbusPropertiesChangedDictionary    = 1

	dbusSignalInterfacesAdded   = "org.freedesktop.DBus.ObjectManager.InterfacesAdded"
	dbusSignalPropertiesChanged = "org.freedesktop.DBus.Properties.PropertiesChanged"

	bluezDevice1Interface  = "org.bluez.Device1"
	bluezAdapter1Interface = "org.bluez.Adapter1"
)

var (
	errScanning          = errors.New("bluetooth: already scanning")
	errNotScanning       = errors.New("bluetooth: not scanning")
	errAdaptorNotPowered = errors.New("bluetooth: adaptor is not powered")
	errScanStopped       = errors.New("bluetooth: scan stopped")
)

// scanState mirrors upstream's Adapter.scanCancelChan: at most one scan per
// process (the bridge and the CLI are mutually exclusive run modes).
var (
	scanStateMu    sync.Mutex
	scanCancelChan chan struct{}
)

// Signal channel capacity: absorbs advertisement bursts without the
// goroutine-per-signal churn of an unbuffered channel (godbus spawns a
// goroutine whenever its non-blocking send to the scan channel fails).
const signalQueueLen = 256

func scan(adapter *bluetooth.Adapter, accept func(addr string) bool, callback func(*bluetooth.Adapter, bluetooth.ScanResult)) error {
	scanStateMu.Lock()
	if scanCancelChan != nil {
		scanStateMu.Unlock()
		return errScanning
	}
	scanStateMu.Unlock()

	bus, err := dbus.SystemBus()
	if err != nil {
		return err
	}
	// bluetooth.DefaultAdapter pins the default adapter path (hci0); resolve
	// the same objects here.
	adapterObj := bus.Object("org.bluez", dbus.ObjectPath("/org/bluez/hci0"))
	bluez := bus.Object("org.bluez", dbus.ObjectPath("/"))

	log.Printf("scan: BlueZ D-Bus scan on hci0 (Device1-filtered match rules, early device drop)")

	// Only the signals the scan loop actually reads. The PropertiesChanged
	// rules carry an arg0 filter, so the D-Bus daemon itself drops everything
	// else (on Venus OS the system bus carries constant property updates from
	// dozens of Victron services) instead of it being parsed, delivered and
	// discarded here.
	signal := make(chan *dbus.Signal, signalQueueLen)
	bus.Signal(signal)
	defer bus.RemoveSignal(signal)

	propertiesChangedOptions := []dbus.MatchOption{
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
	}
	propertiesChangedDevice := append(append([]dbus.MatchOption{}, propertiesChangedOptions...),
		dbus.WithMatchArg(dbusPropertiesChangedInterfaceName, bluezDevice1Interface))
	propertiesChangedAdapter := append(append([]dbus.MatchOption{}, propertiesChangedOptions...),
		dbus.WithMatchArg(dbusPropertiesChangedInterfaceName, bluezAdapter1Interface))
	if err := bus.AddMatchSignal(propertiesChangedDevice...); err != nil {
		return err
	}
	defer bus.RemoveMatchSignal(propertiesChangedDevice...)
	if err := bus.AddMatchSignal(propertiesChangedAdapter...); err != nil {
		return err
	}
	defer bus.RemoveMatchSignal(propertiesChangedAdapter...)

	newObjectMatchOptions := []dbus.MatchOption{
		dbus.WithMatchInterface("org.freedesktop.DBus.ObjectManager"),
		dbus.WithMatchMember("InterfacesAdded"),
	}
	if err := bus.AddMatchSignal(newObjectMatchOptions...); err != nil {
		return err
	}
	defer bus.RemoveMatchSignal(newObjectMatchOptions...)

	// Check if the adapter is powered on.
	powered, err := adapterObj.GetProperty("org.bluez.Adapter1.Powered")
	if err != nil {
		return err
	}
	if !powered.Value().(bool) {
		return errAdaptorNotPowered
	}

	// Channel that will be closed when the scan is stopped. Detecting whether
	// the scan is stopped can be done by doing a non-blocking read from it:
	// if it succeeds, the scan is stopped.
	cancelChan := make(chan struct{})
	scanStateMu.Lock()
	scanCancelChan = cancelChan
	scanStateMu.Unlock()
	defer func() {
		scanStateMu.Lock()
		scanCancelChan = nil
		scanStateMu.Unlock()
	}()

	// closeCancel closes the cancel channel exactly once (the power-off and
	// scan-stopped paths race with a concurrent StopScan).
	closeCancel := func() {
		scanStateMu.Lock()
		if scanCancelChan == cancelChan {
			close(cancelChan)
			scanCancelChan = nil
		}
		scanStateMu.Unlock()
	}

	// This appears to be necessary to receive any BLE discovery results at all.
	defer adapterObj.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0)
	err = adapterObj.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, map[string]interface{}{
		"Transport": "le",
	}).Err
	if err != nil {
		return err
	}

	// Go through all connected devices and present the connected devices as
	// scan results. Also save the properties so that the full list of
	// properties is known on a PropertiesChanged signal. We can't present the
	// list of cached devices as scan results as devices may be cached for a
	// long time, long after they have moved out of range.
	var deviceList map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err = bluez.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&deviceList)
	if err != nil {
		return err
	}
	devices := make(map[dbus.ObjectPath]map[string]dbus.Variant)
	for path, v := range deviceList {
		device, ok := v[bluezDevice1Interface]
		if !ok {
			continue // not a device
		}
		if !strings.HasPrefix(string(path), "/org/bluez/hci0") {
			continue // not part of our adapter
		}
		if device["Connected"].Value().(bool) {
			if accept != nil && !accept(deviceAddress(device)) {
				continue
			}
			callback(adapter, makeScanResult(device))
			select {
			case <-cancelChan:
				return nil
			default:
			}
		}
		devices[path] = device
	}

	// Instruct BlueZ to start discovering.
	// NOTE: Go, not Call: it can block if the adapter is powered off, or was
	// recently powered off.
	startDiscovery := adapterObj.Go("org.bluez.Adapter1.StartDiscovery", 0, nil)

	for {
		// Check whether the scan is stopped. This is necessary to avoid a
		// race condition between the signal channel and the cancel channel
		// when the callback calls StopScan (no new callbacks may be called
		// after StopScan is called).
		select {
		case <-cancelChan:
			return adapterObj.Call("org.bluez.Adapter1.StopDiscovery", 0).Err
		default:
		}

		select {
		case <-startDiscovery.Done:
			if startDiscovery.Err != nil {
				closeCancel()
				return startDiscovery.Err
			}
		case sig := <-signal:
			// The match rules above already limit what arrives here to
			// Device1/Adapter1 PropertiesChanged and Device1 InterfacesAdded.
			switch sig.Name {
			case dbusSignalInterfacesAdded:
				objectPath := sig.Body[0].(dbus.ObjectPath)
				interfaces := sig.Body[1].(map[string]map[string]dbus.Variant)
				rawprops, ok := interfaces[bluezDevice1Interface]
				if !ok {
					continue
				}
				devices[objectPath] = rawprops
				if accept != nil && !accept(deviceAddress(rawprops)) {
					continue
				}
				callback(adapter, makeScanResult(rawprops))
			case dbusSignalPropertiesChanged:
				interfaceName := sig.Body[0].(string)
				switch interfaceName {
				case bluezAdapter1Interface:
					// check power state
					changes := sig.Body[1].(map[string]dbus.Variant)
					if powered, ok := changes["Powered"]; ok && !powered.Value().(bool) {
						// adapter is powered off, stop the scan
						closeCancel()
						return errAdaptorNotPowered
					} else if discovering, ok := changes["Discovering"]; ok && !discovering.Value().(bool) {
						// adapter stopped discovering unexpectedly (e.g. due
						// to an external event)
						closeCancel()
						return errScanStopped
					}
				case bluezDevice1Interface:
					changes := sig.Body[1].(map[string]dbus.Variant)
					device, ok := devices[sig.Path]
					if !ok {
						// This shouldn't happen, but protect against it just
						// in case.
						continue
					}
					for k, v := range changes {
						device[k] = v
					}
					// Early drop: devices this instance does not follow are
					// not decoded (makeScanResult), not passed to the
					// callback and not allocated for. This is the hottest
					// path of the whole app: every advertisement from every
					// device in range lands here as a property change.
					if accept != nil && !accept(deviceAddress(device)) {
						continue
					}
					callback(adapter, makeScanResult(device))
				}
			}
		case <-cancelChan:
			continue
		}
	}
}

// deviceAddress returns the BlueZ-reported address ("A4:C1:38:30:88:DE") of
// a cached Device1 property map, or "" when it has none (the accept hook then
// sees an address it cannot match, matching the callback-side filter).
func deviceAddress(props map[string]dbus.Variant) string {
	if v, ok := props["Address"]; ok {
		if addr, ok := v.Value().(string); ok {
			return addr
		}
	}
	return ""
}

func stopScan(_ *bluetooth.Adapter) error {
	scanStateMu.Lock()
	defer scanStateMu.Unlock()
	if scanCancelChan == nil {
		return errNotScanning
	}
	close(scanCancelChan)
	scanCancelChan = nil
	return nil
}

// makeScanResult creates a bluetooth.ScanResult from raw D-Bus device
// properties. Port of upstream's makeScanResult; BlueZ always provides the
// Address/AddressType/UUIDs properties on Device1 objects.
func makeScanResult(props map[string]dbus.Variant) bluetooth.ScanResult {
	// Assume the Address property is well-formed.
	addr, _ := bluetooth.ParseMAC(props["Address"].Value().(string))

	// Create a list of UUIDs.
	var serviceUUIDs []bluetooth.UUID
	for _, uuid := range props["UUIDs"].Value().([]string) {
		// Assume the UUID is well-formed.
		parsedUUID, _ := bluetooth.ParseUUID(uuid)
		serviceUUIDs = append(serviceUUIDs, parsedUUID)
	}

	a := bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: addr}}
	a.SetRandom(props["AddressType"].Value().(string) == "random")

	var manufacturerData []bluetooth.ManufacturerDataElement
	if mdata, ok := props["ManufacturerData"].Value().(map[uint16]dbus.Variant); ok {
		for k, v := range mdata {
			manufacturerData = append(manufacturerData, bluetooth.ManufacturerDataElement{
				CompanyID: k,
				Data:      v.Value().([]byte),
			})
		}
	}

	// Get optional properties.
	localName, _ := props["Name"].Value().(string)
	rssi, _ := props["RSSI"].Value().(int16)

	var serviceData []bluetooth.ServiceDataElement
	if sdata, ok := props["ServiceData"].Value().(map[string]dbus.Variant); ok {
		for k, v := range sdata {
			uuid, err := bluetooth.ParseUUID(k)
			if err != nil {
				continue
			}
			serviceData = append(serviceData, bluetooth.ServiceDataElement{
				UUID: uuid,
				Data: v.Value().([]byte),
			})
		}
	}

	return bluetooth.ScanResult{
		RSSI:    rssi,
		Address: a,
		AdvertisementPayload: &fieldsPayload{
			fields: bluetooth.AdvertisementFields{
				LocalName:        localName,
				ServiceUUIDs:     serviceUUIDs,
				ManufacturerData: manufacturerData,
				ServiceData:      serviceData,
			},
		},
	}
}

// fieldsPayload implements bluetooth.AdvertisementPayload over structured
// advertisement fields (upstream keeps an equivalent type private).
type fieldsPayload struct {
	fields bluetooth.AdvertisementFields
}

func (p *fieldsPayload) LocalName() string { return p.fields.LocalName }

func (p *fieldsPayload) HasServiceUUID(uuid bluetooth.UUID) bool {
	for _, u := range p.fields.ServiceUUIDs {
		if u == uuid {
			return true
		}
	}
	return false
}

func (p *fieldsPayload) ServiceUUIDs() []bluetooth.UUID { return p.fields.ServiceUUIDs }

func (p *fieldsPayload) Bytes() []byte { return nil }

func (p *fieldsPayload) ManufacturerData() []bluetooth.ManufacturerDataElement {
	return p.fields.ManufacturerData
}

func (p *fieldsPayload) ServiceData() []bluetooth.ServiceDataElement {
	return p.fields.ServiceData
}

// Compile-time interface check.
var _ bluetooth.AdvertisementPayload = (*fieldsPayload)(nil)
