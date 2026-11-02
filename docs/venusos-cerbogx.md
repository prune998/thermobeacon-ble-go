# Running on a Victron Cerbo GX (Venus OS)

How to install `thermobeacon-ble-go` on a Cerbo GX so it starts automatically
at boot, forwards readings to Victron's `dbus-ble-sensors`, and survives
firmware updates.

- [Prerequisites](#prerequisites)
- [Step 0 - enable SSH and connect to the GX](#step-0---enable-ssh-and-connect-to-the-gx)
- [Step 1 - put the binary on the GX](#step-1---put-the-binary-on-the-gx)
- [Step 2 - find the sensor MAC addresses](#step-2---find-the-sensor-mac-addresses)
- [Step 3 - create the service](#step-3---create-the-service)
- [Step 4 - start it automatically at boot](#step-4---start-it-automatically-at-boot)
- [Step 5 - enable the sensors in the GUI](#step-5---enable-the-sensors-in-the-gui)
- [Updating](#updating)
- [Uninstalling](#uninstalling)
- [Notes and caveats](#notes-and-caveats)

Everything lives under `/data` (the only place that survives firmware
updates) and uses the same daemontools mechanism Victron's own services and
community drivers use. All commands run as root over SSH (step 0).

## Prerequisites

- A Cerbo GX (or Ekrano GX) running Venus OS **v3.85 or newer** — the
  firmware must have Victron's Bluetooth sensor service with its UDP gateway
  socket. Check:

  ```
  dbus -y com.victronenergy.settings /Settings/BleSensors/Socket/BindAddress GetValue
  # -> 127.0.0.1
  ```

- *Settings → Integrations → Bluetooth sensors* **enabled** (this runs
  `dbus-ble-sensors` and its scan). *Continuous scanning* is recommended.
- Working BlueZ, which the app uses to scan: `bluetoothctl list` must show
  the built-in adapter (`hci0`).
- The right binary for the CPU: `uname -m` on a Cerbo GX prints `armv7l` →
  use the **`linux-armv7`** release asset (statically linked, runs on Venus
  OS regardless of its libc). An Ekrano GX / aarch64 system uses
  **`linux-arm64`**.
- The MAC addresses of your sensors (step 2).

## Step 0 - enable SSH and connect to the GX

SSH is disabled by default on Venus OS, and there is no default root
password. This follows Victron's official
[Root access](https://www.victronenergy.com/live/ccgx:root_access) guide;
work on the GX display / GX Touch, or via the Remote Console.

**1. Unlock Superuser access**

1. Settings → General: set *Access Level* to *User and installer* - the
   password is `ZZZ`.
2. Back on the General page, highlight *Access Level* (do not open it) and
   press and hold the **right button** of the center pad until the level
   changes to **Superuser**.
   - GX Touch (no buttons): drag the list down and keep holding it for about
     five seconds.
   - Remote Console: hold the **right arrow key** on your keyboard (holding
     the mouse button does not work).
   - New UI: select the whole *Access & Security* group, drag down and hold
     for five seconds.

**2. Set a temporary root password**

Settings → General → *Set root password* (minimum 6 characters).

Note: the password lives on the rootfs and is **reset by firmware updates**.
Use it to log in once and install an SSH key (step 4 below); keys live on
`/data` and survive updates.

**3. Enable SSH**

Settings → General → *SSH on LAN* → *Enabled*.

**4. Find the GX IP address**

On the GX: Settings → Ethernet or Wi-Fi shows the address. Alternatively use
your router's DHCP list or the VRM portal.

**5. Connect**

```sh
ssh root@192.168.1.23     # replace with the GX's IP
```

- macOS/Linux: from a terminal. Windows 10/11 ships OpenSSH (PowerShell:
  `ssh root@192.168.1.23`); PuTTY works as well.
- The first connection asks to confirm the host key (`yes`), then asks for
  the temporary root password. You are now root on the GX.

**6. Recommended: install an SSH key**

Keys survive firmware updates (the password does not):

```sh
# on your computer:
ssh-copy-id root@192.168.1.23   # or append your public key to
                                # /root/.ssh/authorized_keys on the GX
ssh root@192.168.1.23           # should no longer ask for a password
```

Once key login works, password logins can be disabled on the GX with
`echo 'root:*' | chpasswd -e`.

**7. After firmware updates**

Firmware updates reset the root password and may reset the *Access Level*;
the SSH keys on `/data` and the enabled *SSH on LAN* switch survive. Repeat
steps 1-2 to set a new password if key login is not set up yet.

**Fallback**: if the network is unreachable, the Cerbo GX has a serial
console (header JP201 on the CPU board, 115200 baud, GND/RX/TX) - see
[Serial console](https://www.victronenergy.com/live/ccgx:root_access#connecting_on_the_serial_console)
in Victron's guide.

## Step 1 - put the binary on the GX

Either download the release directly on the GX:

```sh
VER=$(wget -qO- https://api.github.com/repos/prune998/thermobeacon-ble-go/releases/latest \
  | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4)
mkdir -p /data/drivers/thermobeacon-ble-go/bin
wget -O /tmp/tbg.tar.gz \
  "https://github.com/prune998/thermobeacon-ble-go/releases/download/$VER/thermobeacon-ble-go-$VER-linux-armv7.tar.gz"
tar xzf /tmp/tbg.tar.gz -C /data/drivers/thermobeacon-ble-go/bin
chmod +x /data/drivers/thermobeacon-ble-go/bin/thermobeacon-ble-go
```

...or copy one you built/downloaded on your computer:

```sh
scp thermobeacon-ble-go-v0.1.1-linux-armv7.tar.gz root@<gx-ip>:/tmp/
```

Verify the checksum against the release's `SHA256SUMS.txt`:

```sh
sha256sum /data/drivers/thermobeacon-ble-go/bin/thermobeacon-ble-go
/data/drivers/thermobeacon-ble-go/bin/thermobeacon-ble-go -version
```

## Step 2 - find the sensor MAC addresses

Run the app in the foreground once and note the `found:` lines - on Linux the
sensors appear under their **MAC addresses** (the default `-device` value is
a macOS UUID and is useless here):

```sh
/data/drivers/thermobeacon-ble-go/bin/thermobeacon-ble-go -device all -debug
# ThermoBeacon db:5c:8d:a0:0f:21 found: ...
# Govee H5074 ba:e2:52:36:ff:28 found: ...
```

`-device all` forwards every supported sensor; a comma-free alternative is to
keep `all` and use Victron's own enable/ignore switches in the GUI. To pin
individual sensors, use `-device <mac>` (one MAC) - see the README flags.

Govee sensors only broadcast measurements when their values change, so give
it a minute. Ctrl-C when you have what you need.

## Step 3 - create the service

Venus OS supervises services with daemontools: anything linked into
`/service/<name>` with an executable `run` script is started and kept alive.
Create the service under `/data` (persistent) and point a log service at
`/var/log`:

```sh
mkdir -p /data/drivers/thermobeacon-ble-go/bin/service/log

cat > /data/drivers/thermobeacon-ble-go/bin/service/run <<'EOF'
#!/bin/sh
exec /data/drivers/thermobeacon-ble-go/bin/thermobeacon-ble-go \
  -device aa:bb:cc:dd:ee:ff \
  -udp-host 127.0.0.1 \
  -udp-port 18542 \
  -state /data/drivers/thermobeacon-ble-go/sensors.json \
  2>&1
EOF

cat > /data/drivers/thermobeacon-ble-go/bin/service/log/run <<'EOF'
#!/bin/sh
exec multilog t s25000 n4 /var/log/thermobeacon-ble-go
EOF

chmod +x /data/drivers/thermobeacon-ble-go/bin/service/run \
         /data/drivers/thermobeacon-ble-go/bin/service/log/run
```

Notes:

- `-device`: one MAC per app instance (run a second service with another
  `run` script for more), or `all`.
- `-state` points into `/data` so the seen-sensor record survives reboots.
- `2>&1` sends the app's log output to the log service (rotated: 4 × 25 kB).

## Step 4 - start it automatically at boot

Link the service into `/service` (starts it now - `svscan` picks it up
within seconds):

```sh
ln -snf /data/drivers/thermobeacon-ble-go/bin/service /service/thermobeacon-ble-go
svc -u /service/thermobeacon-ble-go
svstat /service/thermobeacon-ble-go
# /service/thermobeacon-ble-go: up (pid ...) ... seconds
```

`/service` is a tmpfs and is empty after every reboot, so make the link be
recreated at boot via `/data/rc.local` (executed by Venus OS at startup).
If the file does not exist yet, create it; if it exists, append the line:

```sh
touch /data/rc.local
chmod +x /data/rc.local
grep -q thermobeacon-ble-go /data/rc.local || cat >> /data/rc.local <<'EOF'

# thermobeacon-ble-go: recreate the daemontools link at boot (/service is a tmpfs)
ln -snf /data/drivers/thermobeacon-ble-go/bin/service /service/thermobeacon-ble-go
EOF
cat /data/rc.local
```

> The `/data/rc.local` mechanism requires Settings → General →
> *Modification checks* → *Modifications enabled* to stay on - see the notes
> at the bottom of this guide.

Reboot to prove it comes up on its own:

```sh
reboot
# after the boot:
svstat /service/thermobeacon-ble-go
```

## Step 5 - enable the sensors in the GUI

- Settings → Integrations → **Bluetooth sensors**: each sensor appears
  (initially as *RuuviTag*) and is **disabled** - enable the ones that are
  yours. Foreign sensors in range stay disabled and never show up anywhere.
- Renaming in the GUI sticks; the app never touches names.
- Enabled sensors appear in the device list, on VRM and in Node-RED with
  `/Temperature`, `/Humidity`, `/BatteryVoltage`, `/Connected` and the
  low-battery alarm.

Useful commands afterwards:

```
svstat /service/thermobeacon-ble-go                          # running?
tail -F /var/log/thermobeacon-ble-go/current | tai64nlocal    # live log
/data/drivers/thermobeacon-ble-go/bin/thermobeacon-ble-go list  # seen sensors
```

## Updating

Replace the binary and restart the service (config and `sensors.json` are
kept):

```sh
wget -O /tmp/tbg.tar.gz "<new release linux-armv7 url>"
tar xzf /tmp/tbg.tar.gz -C /data/drivers/thermobeacon-ble-go/bin
chmod +x /data/drivers/thermobeacon-ble-go/bin/thermobeacon-ble-go
svc -t /service/thermobeacon-ble-go
```

## Uninstalling

```sh
rm /service/thermobeacon-ble-go          # daemontools stops it
sed -i '/thermobeacon-ble-go/d' /data/rc.local
rm -rf /data/drivers/thermobeacon-ble-go /var/log/thermobeacon-ble-go
```

Sensors stay in Victron's Bluetooth sensors list until they time out
(30 minutes after their last packet).

## Notes and caveats

- **Architecture**: the Cerbo GX is ARMv7 (`uname -m` → `armv7l`); use the
  `linux-armv7` asset. The release binaries are built with `CGO_ENABLED=0`
  and are statically linked, so they run on Venus OS without any extra
  packages.
- **BlueZ required**: the app scans through BlueZ (`org.bluez` on the system
  bus), which Venus OS ships for the GX's own Bluetooth. If `bluetoothctl
  list` shows no adapter, check that Bluetooth is not disabled in the GX
  settings (the same switch VictronConnect uses).
- **Coexistence with `dbus-ble-sensors`**: Victron's service reads the
  adapter with raw HCI sockets while this app scans through BlueZ - two
  scanners on one adapter. In practice both receive broadcasts, but if you
  notice Victron's own Bluetooth sensors (Mopeka, Ruuvi, ...) updating less
  often than before, that is scan interference; toggling *Continuous
  scanning* changes the duty cycles involved.
- **Govee cadence**: Govee devices fill most broadcasts with an
  INTELLI_ROCKS blob and only send measurements when the values change, so
  readings arrive in bursts every ~10-60 s. That is the device's behaviour,
  not a fault - `dbus-ble-sensors` handles the timeout side.
- **macOS device IDs do not apply here**: the default `-device` value is a
  CoreBluetooth UUID. On the GX, always use MAC addresses or `all`.
- **Firmware updates**: everything under `/data` survives; the `/service`
  link is recreated by `/data/rc.local` at every boot. After a major
  firmware update, re-check that the service is up (`svstat`) and that
  `bluetoothctl list` still works.
- **Root password vs SSH keys**: firmware updates reset the root password
  (it lives on the rootfs) while SSH keys on `/data` survive - install a key
  in step 0.6 so you are never locked out after an update.
- **Keep Modifications enabled**: Settings → General → *Modification checks*
  → *Modifications enabled* must stay on. When it is off, Venus OS renames
  `/data/rc.local` to `/data/rc.local.disabled` at every boot and the
  autostart link is never recreated - the app simply stops starting after a
  reboot.
