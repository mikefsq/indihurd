# indihurd

indihurd runs INDI astronomy drivers and makes their devices available to
ASCOM Alpaca clients. It can also serve the same devices to INDI clients such
as Ekos. Each driver runs in its own process and is restarted if it exits.

## Build and run

Requires Linux, Go 1.25 or later, and the INDI driver binaries for your hardware.
The driver transport uses Linux file-descriptor passing.

```sh
git clone https://github.com/mikefsq/indihurd
cd indihurd
make build
bin/indihurd -config /path/to/indihurd.conf
```

Without `-config`, indihurd reads `/etc/indihurd/indihurd.conf`.
Existing configurations elsewhere can still be used with `-config`.
Open `http://<host>:32228/setup` for browser management. Use
`-web 127.0.0.1:32228` to bind locally, another address to change the port,
or `-web ""` to run without the management interface. The web interface
has no authentication; its listen address determines where administration
is accessible.
`make help` lists build and dependency-update targets. For mapping development
and simulator tests, see [DRIVERS.md](DRIVERS.md).

## Install as a systemd service

```sh
make build
sudo make install
sudo systemctl enable --now indihurd
```

Installation puts the binary in `/usr/local/bin/indihurd`, the unit in
`/etc/systemd/system/indihurd.service`, and an initially empty configuration in
`/etc/indihurd/indihurd.conf`. Existing configuration is preserved on reinstall.
The default enables INDI on loopback port 7624 and web management on port 32228;
add and enable devices through the browser. Set `indiListen` in Configuration
if INDI clients need network access.

The service runs as the dedicated `indihurd` account, with `/var/lib/indihurd`
as its home. INDI drivers therefore use `/var/lib/indihurd/.indi` by default.
The account owns the configuration directory so browser saves can replace the
file atomically. The installer adds it to existing `dialout`, `video`, and
`plugdev` groups; device-specific udev permissions still apply. Existing driver
settings in your own `~/.indi` are not copied automatically.

There is one systemd service. indihurd supervises each driver child internally;
restarting one driver in the browser leaves the others running. A full
`systemctl restart indihurd` interrupts all its drivers.

```sh
sudo systemctl status indihurd
sudo journalctl -u indihurd -f
# After rebuilding and reinstalling an update:
sudo systemctl restart indihurd
```

Installation reloads systemd unit definitions but does not start or restart the
service automatically. `DESTDIR=/tmp/indihurd-package make install` stages files
without changing accounts or systemd services.

## Browser management

The interface uses embedded HTML, CSS, and JavaScript, with no external assets
or frontend dependencies. It stays available when configuration is missing or
invalid, no devices are enabled, or a driver fails to start.

- **Devices:** connection status, enabled filter, persistent enable/disable
  switches, and a per-device menu for Setup, Edit, Restart, and Delete.
  Restart replaces only that child process; other drivers keep running.
  Delete requires disabling first and removes the configuration entry, leaving
  the installed binary and its state files intact.
- **Add device:** choose an Alpaca mapping and an installed `indi_*` executable
  found on the service's `PATH`, or enter an executable manually. Selecting a
  binary starts a temporary configuration session with hardware connection held
  off; **Read pre-connect settings** repeats this for a manually entered path.
  Pre-connect XML metadata generates grouped fields with driver labels, numeric
  bounds, and switch selection rules. Read-only values are displayed without
  editable controls. Name and Alpaca Port have dedicated fields, while Advanced
  JSON remains available. Writable values populate `indi.beforeConnect`;
  rereading the same driver replays the draft values, with connection mode first. Changing the
  executable resets the entire `indi` section and uses the new driver's reported
  device name as the entry name. Read-only, write-only, and bridge-managed
  properties are excluded. Review the values before enabling. Multi-device
  drivers require `indi.deviceName`; inspection refuses binaries already enabled
  in indihurd to avoid launching a competing instance. New entries
  are disabled until enabled explicitly. Listing binaries does not probe hardware. Changes in the generated form are sent
  to the temporary driver; added and removed properties update the form. Save or
  leaving the page closes the process. Abandoned sessions expire after five
  minutes without requests. No connection or driver configuration-save command
  is sent by the editor.
- **Configuration:** a form selects INDI only, Alpaca only, or both, with the
  INDI port and listen address. It preserves device entries and checks before
  saving. An advanced JSON editor remains available for recovery.
- **Edit device / Advanced JSON:** syntax feedback updates while typing. Check
  configuration validates the schema and enabled entries' executable availability
  and mapping configuration before enabling Save. The server repeats validation
  before atomically replacing the file. Failed submissions preserve the draft;
  changes made in another editor prevent overwriting its saved version.
- **Setup:** controls generated from the running driver's INDI property metadata,
  including labels, ranges, switches, and permissions. Apply live sends temporary
  values; Save before/after connection stores startup presets. Read-only and
  bridge-managed properties cannot be changed here. Properties become available
  when the child publishes them, including before hardware connects. This page
  uses the existing process rather than launching a second discovery process.
- **Logs:** a separate tab with a device selector, pause, refresh, and follow.
  It contains the most recent 1,000 log messages from the current indihurd session,
  including driver stderr; it does not read historical systemd logs.

Saved changes to running entries require their Restart action. Enabling and
disabling take effect immediately and update the configuration file. Disable all
devices before changing global Alpaca/INDI listener settings. Names must be unique.

Configuration checks do not connect hardware or verify property availability.
Disabled entries may remain incomplete until enabled. Working means the driver
has reached its connected serving state; it does not verify every hardware
function. All management functionality lives in indihurd; INDI binaries need no
modification.

## Build INDI core drivers from Git

```sh
make indi-drivers
```

This fetches [INDI core](https://github.com/indilib/indi) and builds its library,
server, and bundled drivers. It does not fetch or build third-party drivers or
their SDK libraries. Install the core development dependencies listed in the
upstream README first.

Sources and generated output stay under `build/`; binaries and libraries are
staged under `build/prefix`. No sudo or system installation is performed, and
no service is restarted. Previously built third-party artifacts are left alone.

Each invocation fetches the selected Git ref, records its commit in
`build/sources.txt`, and refuses to overwrite local source edits. Defaults are
`master` and two parallel build jobs. Compiler warnings remain visible but do
not stop compilation (`FIX_WARNINGS=OFF`).

```sh
make indi-drivers INDI_JOBS=4
make indi-drivers INDI_REF=<tag-or-commit>
make indi-drivers INDI_CMAKE_ARGS='-DFIX_WARNINGS=ON'
```

`INDI_BUILD_DIR` changes the workspace. Udev-rule installation is disabled.
Install the built core drivers separately:

```sh
sudo make install-indi-drivers
```

Core binaries carry a relative runtime library path so they use the matching
`../lib` libraries in staging and after installation, instead of mixing with a
different system INDI version.

This installs from `build/core` into `/usr/local`, installs the core driver aliases
(such as `indi_lx200_10micron`), and refreshes the library cache.
It does not rebuild, install third-party drivers, or restart services. Reopen Add
device to refresh the executable list. Hardware access uses existing system udev
rules because this build disables udev-rule installation.

## Configure devices

The configuration is strict JSON: comments, trailing commas, and unknown keys
are rejected. For example:

```json
{
  "devices": [
    {
      "driver": "indi-telescope",
      "exec": "indi_lx200generic",
      "name": "Mount",
      "port": 11214,
      "device": 0,
      "indi": {
        "beforeConnect": {
          "DEVICE_PORT.PORT": "/dev/serial/by-id/usb-FTDI_x"
        }
      }
    }
  ]
}
```

Replace `exec` with an installed INDI driver and set its connection properties
for your hardware. Find property and member names with the `dump` command below.

| Field | Meaning |
|---|---|
| `driver` | Alpaca mapping to use; see supported types below |
| `exec` | Driver executable path or name on `PATH` |
| `name` | Alpaca display name |
| `port` | Unique Alpaca HTTP port for this entry; required unless Alpaca is disabled |
| `device` | Explicit Alpaca device number, usually `0` |
| `enable` | Whether to start the entry; defaults to `true` |
| `indi.deviceName` | INDI device name; required when the child exposes multiple devices |

`driver`, `exec`, and `name` are required even for disabled entries. Set
`"enable": false` to keep an entry without starting it. Browser edits mark
running devices as needing restart; use their Restart action to apply changes.
After editing the file externally, reopen Configuration and save to load it
into management, or restart indihurd.

Supported mappings:

| `driver` | Alpaca type |
|---|---|
| `indi-camera` | Camera |
| `indi-telescope` | Telescope |
| `indi-focuser` | Focuser |
| `indi-filterwheel` | Filter wheel |
| `indi-rotator` | Rotator |
| `indi-dome` | Dome |
| `indi-weather` | Observing conditions |
| `indi-covercal` | Cover calibrator |
| `indi-safety` | Safety monitor |
| `indi-switch` | Switch |

Available capabilities depend on the properties the INDI driver publishes.
An unmapped or unavailable capability returns the appropriate Alpaca error.

### Connection settings

Use `indi.beforeConnect` for values the driver needs before opening hardware,
such as its serial port, baud rate, or connection mode. Each key is
`PROPERTY.MEMBER`, and each value is a string. The driver must define the
property before indihurd sends its preset; all before-connect presets must be
applied before connection proceeds.

`indi.afterConnect` applies settings after connection, such as a slew rate.
Presets are reapplied when the driver reconnects. `indi.pollingPeriodMs` sets
the driver's polling interval when greater than zero.

### Find property names

```sh
bin/indihurd dump -exec indi_lx200generic -pre
bin/indihurd dump -exec indi_lx200generic
```

`-pre` lists properties without requesting a hardware connection. The default
mode attempts to connect and then prints the available properties, including
ranges and permissions. If it cannot connect, it prints the pre-connect set
with a warning. Use `-timeout 30s` for a slower driver.

Dump output uses `DEVICE.PROPERTY.MEMBER`; omit the device prefix when writing
presets. Set `indi.deviceName` separately if needed.

### State and device identity

Run indihurd as the same user as your existing INDI setup and leave
`indi.stateDir` unset. Drivers inherit that user's `HOME` and use the existing
`~/.indi` directory without moving or copying files.

For an isolated driver configuration, `indi.stateDir` overrides the child
process's `HOME`. For example, `/var/lib/indihurd/mount` puts driver files in
`/var/lib/indihurd/mount/.indi`. Set it to the parent of `.indi`, not `.indi`
itself, and ensure it is writable. The dump command accepts the same optional
override as `-statedir`.

Set `indi.serial` to a stable hardware identifier when available. Otherwise,
the Alpaca identity uses the configured port and device number as a fallback;
replacing hardware in the same slot retains that identity.

## Connect clients

Alpaca servers listen on the configured entry ports. A shared responder
advertises them over UDP port 32227. If discovery cannot bind, the error is
logged and the device servers remain available by address and port.

To serve INDI clients as well, set `indiPort`:

```json
{
  "indiPort": 7624,
  "indiListen": "127.0.0.1",
  "devices": [
    {
      "driver": "indi-focuser",
      "exec": "indi_moonlite_focus",
      "name": "Moonlite",
      "port": 11216,
      "device": 0
    }
  ]
}
```

An omitted `indiListen` binds loopback. Set it to a network address to allow
remote INDI clients; the INDI connection has no authentication. Camera images
are forwarded to clients that request BLOBs through `enableBLOB`.

Set `"alpaca": false` to serve INDI only. In that mode, `indiPort` is required,
per-entry `port` values are optional, and Alpaca discovery is disabled.
`device` remains required for enabled entries.

indihurd does not provide remote-server chaining or driver-to-driver snooping.
Camera FITS headers therefore do not receive mount metadata through snooping.

## Additional driver properties

Number, switch, and text properties outside the typed mapping can be exposed
as Alpaca actions named `INDI:<PROPERTY>`. An empty `Parameters` value reads a
property as JSON. To write it, pass a JSON object mapping member names to
values. Read-only properties remain read-only.

`INDI:_PROPERTIES` lists the available properties and their shapes. Properties
reserved for bridge management or video, and Light and BLOB vectors, are
excluded from these actions.

## Diagnostics

Driver stderr, connection changes, and mapping warnings are logged to stderr.
A missing before-connect property can leave a driver waiting to connect; check
the preset names against `dump -pre` output.

Set `indi.record` to a file path to capture a driver session for debugging.
Recordings include property traffic and image payloads and can grow large.

### Selected third-party INDI drivers

The optional target builds the Astroasis Oasis focuser and filter wheel, ZWO ASI
cameras, Player One cameras, and QHY cameras (including PoleMaster) from `indilib/indi-3rdparty`. It builds only these
four vendor families and stages their SDK libraries alongside INDI core.
Upstream ASI and Player One projects also include their other driver variants.

```sh
make indi-drivers              # once, if core is not already staged
make indi-thirdparty          # no sudo; output stays under build/prefix
sudo make install-indi-drivers # install matching core libraries
sudo apt install fxload        # firmware loader required by PoleMaster
sudo make install-indi-thirdparty
```

The installation copies only files listed by these selected projects, including
XML driver catalogs and USB permission rules, into `/usr/local`. It reloads udev
rules; reconnect USB hardware for new permissions to take effect. It does not
start drivers or enable devices. Refresh Add device or INDI profiles to see the
installed drivers. `INDI_JOBS`, `INDI_BUILD_DIR`, and `INDI_THIRDPARTY_REF` can
control parallelism, workspace, and the third-party Git revision.

Unihedron SQM is a core driver (`indi_sqm_weather`), installed by
`sudo make install-indi-drivers`. Its catalog label is **SQM**. The core install
also copies `drivers.xml` into `/usr/local/share/indi` for Web Manager discovery.
StellarMate Power and Stepper are not in the checked-out upstream core or
third-party source trees; adding their build requires a separate source repository.

QHY PoleMaster uses `indi_qhy_ccd` (catalog label **QHY CCD**). The selected
installation includes QHY firmware under `/usr/local/lib/firmware/qhy` and rewrites
its USB rules to that installed path. `fxload` is required separately; the installer
checks for it before copying files. Reconnect PoleMaster after installation so udev
can load its firmware. Building or installing does not open the camera.
