// Package main implements thermobeacon-ble-go, a Go port of
// WookyDO/dbus-ble-temp.
//
// It listens to BLE advertisements of ThermoBeacon thermometers/hygrometers
// (Oria, Brifit, SensorBlue and other clones) and of BTHome v2 devices,
// decodes their readings and re-encodes them as RuuviTag RAWv2 packets for
// Victron's dbus-ble-sensors UDP gateway socket.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tinygo.org/x/bluetooth"
)

// defaultDeviceID is the CoreBluetooth peripheral UUID of the sensor to
// follow. On macOS devices are identified by such an UUID instead of a MAC
// address. Use "-device all" to decode every ThermoBeacon / BTHome device in
// range, or a MAC address when running on Linux.
const defaultDeviceID = "10243784-1324-5413-b00c-9675981a8daa"

// version is injected at build time with -ldflags "-X main.version=..." (the
// release workflow sets it to the git tag); plain `go build` reports "dev".
var version = "dev"

func main() {
	log.SetFlags(log.LstdFlags)

	var (
		cfg       Config
		stateFile string
		showVer   bool
	)

	flag.StringVar(&cfg.Device, "device", defaultDeviceID,
		"device to follow: a CoreBluetooth UUID (macOS), a MAC address (Linux) or 'all'")
	flag.StringVar(&cfg.UDPHost, "udp-host", "127.0.0.1",
		"host of Victron's dbus-ble-sensors UDP gateway socket")
	flag.IntVar(&cfg.UDPPort, "udp-port", 18542,
		"port of Victron's dbus-ble-sensors UDP gateway socket")
	flag.BoolVar(&cfg.NoForward, "no-forward", false,
		"decode and log readings, but do not send UDP packets")
	flag.DurationVar(&cfg.MinInterval, "min-interval", 5*time.Second,
		"at most one forwarded packet per sensor every N seconds")
	flag.DurationVar(&cfg.IdleWarning, "idle-warning", 120*time.Second,
		"log a warning when no BLE advertisement at all was seen for N seconds")
	flag.StringVar(&stateFile, "state", "sensors.json",
		"record of seen sensors ('' disables it); 'thermobeacon-ble-go list' prints it")
	flag.BoolVar(&cfg.Debug, "debug", false,
		"log every advertisement and dump its payload in a meaningful way")
	flag.BoolVar(&showVer, "version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `thermobeacon-ble-go - ThermoBeacon / BTHome -> Victron dbus-ble-sensors bridge

Usage:
  thermobeacon-ble-go [flags]        scan and forward readings
  thermobeacon-ble-go [flags] dump   scan and dump every advertisement in a
                                     meaningful way (decode + log only, no UDP)
  thermobeacon-ble-go [flags] list   print the seen-sensor table

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	debugEnabled = cfg.Debug

	if showVer {
		fmt.Println("thermobeacon-ble-go", version)
		return
	}

	if flag.NArg() > 1 || (flag.NArg() == 1 && flag.Arg(0) != "list" && flag.Arg(0) != "dump") {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}
	if flag.Arg(0) == "dump" {
		// Discovery mode: decode and dump everything, forward nothing. The
		// default device filter is lifted unless -device was given.
		cfg.Debug = true
		cfg.NoForward = true
		deviceSet := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "device" {
				deviceSet = true
			}
		})
		if !deviceSet {
			cfg.Device = "all"
		}
		debugEnabled = true
	}
	if flag.NArg() == 1 {
		if err := listSensors(stateFile); err != nil {
			log.Fatalf("list sensors: %v", err)
		}
		return
	}

	if err := run(&cfg, stateFile); err != nil {
		log.Fatal(err)
	}
}

func run(cfg *Config, stateFile string) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("could not enable the BLE stack: %w", err)
	}

	store := loadSeenStore(stateFile)
	bridge := NewBridge(cfg, store)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Print("shutting down...")
		_ = adapter.StopScan()
		time.Sleep(500 * time.Millisecond) // give the scan a moment to stop
		store.Flush()
		os.Exit(0)
	}()

	go bridge.WatchIdle(ctx)

	target := fmt.Sprintf("udp %s:%d", cfg.UDPHost, cfg.UDPPort)
	if cfg.NoForward {
		target = "log only"
	}
	log.Printf("thermobeacon-ble-go %s: watching %s, forwarding to %s, min interval %s",
		version, cfg.describeDevice(), target, cfg.MinInterval)

	// Scan blocks until StopScan is called.
	if err := adapter.Scan(bridge.HandleScan); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	store.Flush()
	return nil
}
