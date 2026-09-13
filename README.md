# indihurd

indihurd manages INDI drivers as a systemd-hosted service and serves both the
native INDI interface and a mapped Alpaca interface for each enabled driver.
It provides a web interface to configure how drivers attach to hardware, to
start and stop the drivers, and view their status and logs. It maintains
persistent hardware connection settings and advertises consistent device
names to clients.

For INDI clients such as Ekos, indihurd can replace indiserver. It also provides
ASCOM Alpaca interfaces through its device mappings. Systemd manages indihurd;
indihurd supervises the individual driver processes and restarts them if they exit.

## Build and run

Requires Go 1.25 or later. `make` builds a native binary on Linux or macOS.
Driver process supervision, socket transport, and BLOB recording/replay support
both platforms. The packaged systemd service and installation scripts target Linux.
On macOS, run `bin/indihurd -config /path/to/indihurd.conf` directly with locally
installed INDI drivers. Linux cross-builds remain explicit, for example
`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 make`.

Drivers from an ordinary INDI
installation will work; all binary dependencies of those drivers must be
installed. Driver executables must be available on the indihurd service’s `PATH`.

```sh
git clone https://github.com/mikefsq/indihurd
cd indihurd
make build
bin/indihurd -config /path/to/indihurd.conf
```

Without `-config`, indihurd reads `/etc/indihurd/indihurd.conf`.
Existing configurations elsewhere can still be used with `-config`.
Open `http://<host>:8624/setup` for browser management. The same HTTP listener
serves the Ekos Web Manager API. Use `-web 127.0.0.1:8624` to bind locally,
another address to change the port, or `-web ""` to disable HTTP.
The web interface
has no authentication; its listen address determines where administration
is accessible.
`make help` lists build and dependency-update targets. For mapping development
and simulator tests, see [DRIVERS.md](DRIVERS.md).

## Installation

Install indihurd as a systemd service using either a
[Debian package built with `make deb`](#install-a-debian-package) or a
[direct source installation](#install-from-source). Both preserve existing
configuration. Install INDI drivers and their binary dependencies separately.
An APT repository is planned; repository installation instructions will be
added when it is available.

### Install from source

```sh
make build
sudo make install
sudo systemctl enable --now indihurd
```

Installation puts the binary in `/usr/local/bin/indihurd`, the unit in
`/etc/systemd/system/indihurd.service`, and an initially empty configuration in
`/etc/indihurd/indihurd.conf`. Existing configuration is preserved on reinstall.
The default enables INDI on loopback port 7624 and the shared web interface
and Web Manager listener on port 8624;
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

`sudo make install` reloads systemd unit definitions but does not start or
restart the service automatically. `DESTDIR=/tmp/indihurd-package make install` stages files
without changing accounts or systemd services.

### Install a Debian package

Run `make deb` to build an installable Debian package. It builds static Linux
packages for **amd64** and
**arm64** in `dist/`, without sudo. It requires Go, `dpkg-deb`, `dpkg`, and `file`.
The packages support Debian Trixie. INDI drivers and their binary dependencies
are installed separately.

```sh
make deb
sudo apt install ./dist/indihurd_<version>_<architecture>.deb
```

Replace `<version>` and `<architecture>` with the package filename printed by
`make deb`. Use `arm64` for a 64-bit Raspberry Pi or `amd64` for an x86-64 machine;
`dpkg --print-architecture` reports the local architecture. Package installation
starts the service; installing an updated package restarts it.

To select an architecture and explicit version, for example:

```sh
make deb DEB_ARGS='-a arm64 -v 0.1.0'
sudo apt install ./dist/indihurd_0.1.0_arm64.deb
```

The build script also supports `armhf` and a custom output directory:
`build/build-deb -a "amd64 arm64 armhf" -o dist`.

Without `-v`, the version comes from a release tag or a Git snapshot identifier.
The package installs `/usr/bin/indihurd` and
`/usr/lib/systemd/system/indihurd.service`. Installation creates the service
account and seeds configuration only when missing. Upgrades preserve device
configuration and profiles and restart the service, interrupting its drivers.
Removal stops the service; configuration and driver state remain even on purge.

When switching from `sudo make install`, its unit at
`/etc/systemd/system/indihurd.service` takes precedence over the packaged unit.
Remove that source-installed unit after reviewing any local changes, then run
`sudo systemctl daemon-reload` and `sudo systemctl restart indihurd` to use the
packaged executable. An old `/usr/local/bin/indihurd` may also take precedence
when invoking the command from a shell.

### CI packages and releases

The **Debian Packages** workflow follows alpacahurd's release process. Pull
requests run `make check`, build static **amd64** and **arm64** packages, and
test installation on native Ubuntu runners for both architectures. Installation
checks cover the service, web interface, embedded version, reinstall behavior,
and preservation of configuration and driver state on purge. These checks do
not require INDI hardware or install separate driver packages.

After the workflow is available on GitHub, publish a new version manually:

```sh
gh workflow run build-deb.yml --repo mikefsq/indihurd --ref main -f version=0.1.0
```

Choose an unused version without a leading `v`. Add `-f prerelease=true` for
a prerelease. After all checks pass, the workflow creates the `v<version>` tag
at the tested commit and a GitHub release containing both `.deb` files. Do not
pre-create the tag. Existing tags or releases are never overwritten. Pushing
a tag alone does not run this workflow.

Publishing to an APT archive is a separate step; this workflow only attaches
packages to the GitHub release. Prereleases are excluded from GitHub's
`releases/latest` endpoint.

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
- **INDI profiles:** select named subsets of configured devices to expose to
  Ekos through the server INDI listener, by applying their saved device selections.
- **Configuration:** a form selects INDI only, Alpaca only, or both, with the
  INDI port and listen address. It preserves device entries and checks before
  saving. An advanced JSON editor remains available for recovery.
- **Edit device / Advanced JSON:** syntax feedback updates while typing. Check
  configuration validates the schema and enabled entries' executable availability
  and mapping configuration before enabling Save. Explicit Alpaca ports must be
  unique across enabled and disabled entries when Alpaca is enabled. Conflicts
  identify both devices and the port. The server repeats validation
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
  including driver stderr and diagnostic text between XML messages on stdout;
  it does not read historical systemd logs.

Saved changes to running entries require their Restart action. Enabling and
disabling take effect immediately and update the configuration file. Disable all
devices before changing global Alpaca/INDI listener settings. Stop an active
Web Manager profile before saving device configuration. Names must be unique.

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
(such as `indi_lx200_10micron`) and the core XML catalog in
`/usr/local/share/indi`, and refreshes the library cache.
It does not rebuild, install third-party drivers, or restart services. Reopen Add
device to refresh the executable list. Hardware access uses existing system udev
rules because this build disables udev-rule installation.

## Build selected third-party INDI drivers

The optional target builds the Astroasis Oasis focuser and filter wheel, ZWO ASI
cameras, Player One cameras, and QHY cameras from `indilib/indi-3rdparty`.
It builds only these
four vendor families and stages their SDK libraries alongside INDI core.
Upstream ASI and Player One projects also include their other driver variants.

```sh
make indi-drivers              # once, if core is not already staged
make indi-thirdparty          # no sudo; output stays under build/prefix
sudo make install-indi-drivers # install matching core libraries
sudo make install-indi-thirdparty
```

The installation copies only files listed by these selected projects, including
XML driver catalogs and USB permission rules, into `/usr/local`. It reloads udev
rules; reconnect USB hardware for new permissions to take effect. It does not
start drivers or enable devices. Refresh Add device or INDI profiles to see the
installed drivers. `INDI_JOBS`, `INDI_BUILD_DIR`, and `INDI_THIRDPARTY_REF` can
control parallelism, workspace, and the third-party Git revision.

The installer reports any missing system prerequisites before copying files.

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
| `port` | Alpaca HTTP port; required for enabled entries when Alpaca is on. Explicit ports must also be unique across disabled entries |
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

Indihurd stores startup presets in its own configuration file, normally
`/etc/indihurd/indihurd.conf`. The service account can write this file, so the
web interface can save connection settings without editing driver-owned files.

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

### Driver configuration files

Startup presets and driver-saved configuration are separate. Settings saved by
the web interface as `indi.beforeConnect` or `indi.afterConnect` remain in
indihurd's configuration. Changes made after startup through an INDI client are
not automatically copied back into that file. If the driver saves those changes,
it writes its own configuration files wherever that driver normally stores them.
A live property change is not necessarily persistent; saving depends on the
driver and the client's configuration-save operation.

Many INDI drivers use `$HOME/.indi`, but the exact location is driver-specific.
With the supplied systemd unit, indihurd runs as the `indihurd` account with
`HOME=/var/lib/indihurd`. Its child drivers inherit that environment, so drivers
using `$HOME/.indi` write to `/var/lib/indihurd/.indi`, not the logged-in user's
home directory. Systemd itself does not supply a universal driver configuration
location; the service's user and environment determine it.

An `indiserver` launched from a shell as user `pi` typically gives its drivers
`HOME=/home/pi`, so the same drivers may have existing configuration under
`/home/pi/.indi`. Switching to the indihurd service does not move or copy those
files. To reuse them, copy the relevant driver files into the service's expected
location and ensure the `indihurd` account can read and write them. When run
manually as the same user as the previous INDI setup, indihurd instead inherits
that user's `HOME` unless overridden.

For an isolated configuration, `indi.stateDir` overrides the child process's
`HOME`. For example, `/var/lib/indihurd/mount` makes `$HOME/.indi` resolve to
`/var/lib/indihurd/mount/.indi`. Set it to the parent of `.indi`, not `.indi`
itself, and ensure it is writable by the service account. This changes `HOME`;
it does not relocate files for drivers that use a different storage convention.
The dump command accepts the same optional override as `-statedir`.

### Device identity

Set `indi.serial` to a stable identifier for Alpaca identity when available.
This field does not select hardware; selection uses the INDI driver’s connection
properties. Otherwise,
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

To change the serving mode or INDI listener, use **Stop all devices** on
Configuration. It saves every device as disabled and stops their processes,
while preserving pending edits in the settings form. Check and save the new
mode, then enable the desired devices again. This interrupts INDI and Alpaca
hardware access. Profiles and per-device connection settings are preserved.

## Web Manager compatibility

In Ekos, select a remote INDI server, enter the indihurd host, and enable
INDI Web Manager on port **8624**. The INDI connection uses **7624** by default;
HTTP management and INDI device traffic use separate ports.

The **INDI profiles** page selects subsets of configured devices to present to
Ekos. Labels are the instance names on the Devices page, not the installed
INDI catalog. Add devices there first; unconfigured binaries and custom catalog
aliases are not offered. Profiles are stored in `webmanager.json` beside
`indihurd.conf` (normally `/etc/indihurd/webmanager.json`).

The main Devices page has a **Profile** dropdown. Selecting a saved profile
validates and saves its device enable flags in `indihurd.conf`: selected devices
are enabled and all others are disabled. Newly enabled devices start; devices
outside the selection stop. Already enabled devices that remain selected keep
running. This affects both native INDI and Alpaca availability. Hardware
connection settings and mappings remain in the individual device entries.

The dropdown shows **Custom selection** when the enable flags do not match a
saved profile. Individual enable switches continue to work. Saved enable flags
survive a daemon restart; a profile marked for autostart is reapplied at startup.
Clearing the profile label leaves the current enable flags unchanged.

Profiles use the server's configured INDI port and listen address. Changing the
exposed set disconnects existing INDI clients so they can reconnect with a fresh
device list. For remote clients, set **INDI listen address** in Configuration to
`0.0.0.0` or the host's network address.

The Web Manager API supports profile CRUD, application/label clearing through the
start/stop endpoints, and lists of configured/exposed devices. Per-driver
start/stop/restart and custom driver creation are rejected: manage devices on
the Devices page. The compatibility `autoconnect` field is accepted but does
not override the configured device connection behavior. Autostart selects the
profile at daemon startup. Profiles saved with old catalog labels or separate
ports must be edited to use configured instance names and the server INDI port;
unavailable selections remain visible in the editor until explicitly removed.

The browser and stored profiles use configured instance names. The Ekos API
(`/api/`) translates those names to INDI catalog labels using the configured
executable, and translates incoming selections back to instance names. Catalogs
are read from `/usr/share/indi` and `/usr/local/share/indi` (or `INDI_DATA_DIR`).
They provide labels only; unconfigured drivers are never added to the list.
Missing or ambiguous catalog mappings produce an explicit error. Ekos must also
have the corresponding labels in its own installed catalog.

For example, `ASI6200MM` is exported as `ZWO CCD`, and `QHY CCD POLEMASTER` as
`QHY CCD`. Where multiple configured instances share a catalog label, an Ekos
round trip preserves the existing profile selection. A new ambiguous selection
must first be made in indihurd. The browser uses `/setup/api/` to retain instance
names throughout editing.

Ekos may still skip profile activation when its required drivers are already
running. Select the
profile in indihurd before connecting Ekos; automatic Ekos profile switching
is not fully compatible with this server-owned configuration model.

Server status reflects an available INDI listener, independently of profiles.
Running-driver responses include configuration-owned drivers available through
INDI, using executable names that Ekos recognizes. `active_profile` is empty
when no named profile is active. Server availability does not establish hardware
readiness; use the device status for that.

Profile scripts and remote-driver chaining are not supported and return explicit
errors. This is Web Manager API compatibility, not the full StellarMate service.

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

## License

indihurd is released under the [GNU General Public License, version 3](LICENSE)
(SPDX: `GPL-3.0-only`).
Separately installed INDI drivers, libraries, and vendor SDKs retain their own
licenses. Building them with the supplied scripts does not change those terms.
