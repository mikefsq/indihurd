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

Without `-config`, indihurd reads `~/.indi/indihurd.conf`.
`make help` lists build and dependency-update targets. For mapping development
and simulator tests, see [DRIVERS.md](DRIVERS.md).

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
`"enable": false` to keep an entry without starting it. Restart indihurd after
editing the configuration.

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
