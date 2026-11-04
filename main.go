// thermobeacon-ble-go decodes the BLE advertisements of ThermoBeacon, Govee
// and BTHome thermometers/hygrometers and forwards the readings as RuuviTag
// RAWv2 packets to Victron's dbus-ble-sensors UDP gateway socket.
//
// Run modes:
//
//	(default)     scan and forward readings
//	--cli         interactive TUI: browse devices, explore payloads
//	dump          log an annotated dump of every advertisement
//	list          print the seen-sensor table
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/prune998/thermobeacon-ble-go/internal/bridge"
	"github.com/prune998/thermobeacon-ble-go/internal/cli"
	"github.com/prune998/thermobeacon-ble-go/internal/config"
	"github.com/prune998/thermobeacon-ble-go/internal/logging"
	"github.com/prune998/thermobeacon-ble-go/internal/store"
)

// version is injected at build time with -ldflags "-X main.version=..." (the
// release workflow sets it to the git tag); plain `go build` reports "dev".
var version = "dev"

func main() {
	log.SetFlags(log.LstdFlags)

	var (
		cfg       config.Config
		stateFile string
		showVer   bool
	)

	flag.StringVar(&cfg.Device, "device", config.DefaultDeviceID,
		"device to follow: a CoreBluetooth UUID (macOS), a MAC address (Linux) or 'all'")
	flag.StringVar(&cfg.UDPHost, "udp-host", "127.0.0.1",
		"host of Victron's dbus-ble-sensors UDP gateway socket")
	flag.IntVar(&cfg.UDPPort, "udp-port", 18542,
		"port of Victron's dbus-ble-sensors UDP gateway socket")
	flag.BoolVar(&cfg.NoForward, "no-forward", false,
		"decode and log readings, but do not send UDP packets")
	flag.BoolVar(&cfg.CLI, "cli", false,
		"interactive TUI: browse discovered devices, get their MAC addresses and explore the payloads (no forwarding)")
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
		fmt.Fprintf(os.Stderr, `thermobeacon-ble-go - ThermoBeacon / Govee / BTHome -> Victron dbus-ble-sensors bridge

Usage:
  thermobeacon-ble-go [flags]        scan and forward readings
  thermobeacon-ble-go [flags] dump   scan and dump every advertisement in a
                                     meaningful way (decode + log only, no UDP)
  thermobeacon-ble-go --cli          interactive TUI: browse devices and
                                     payloads, get MAC addresses (no UDP)
  thermobeacon-ble-go [flags] list   print the seen-sensor table

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	logging.SetDebug(cfg.Debug)

	if showVer {
		fmt.Println("thermobeacon-ble-go", version)
		return
	}

	if flag.NArg() > 1 || (flag.NArg() == 1 && flag.Arg(0) != "list" && flag.Arg(0) != "dump") {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}
	if cfg.CLI {
		if err := cli.Run(); err != nil {
			log.Fatal(err)
		}
		return
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
		logging.SetDebug(true)
	}
	if flag.NArg() == 1 && flag.Arg(0) == "list" {
		if err := store.List(stateFile); err != nil {
			log.Fatalf("list sensors: %v", err)
		}
		return
	}

	target := fmt.Sprintf("udp %s:%d", cfg.UDPHost, cfg.UDPPort)
	if cfg.NoForward {
		target = "log only"
	}
	log.Printf("thermobeacon-ble-go %s: watching %s, forwarding to %s, min interval %s",
		version, cfg.DescribeDevice(), target, cfg.MinInterval)

	if err := bridge.Run(&cfg, store.Load(stateFile)); err != nil {
		log.Fatal(err)
	}
}
