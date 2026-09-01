# indihurd

indihurd serves INDI drivers to both INDI and ASCOM Alpaca clients. It spawns each driver binary
as a child process and communicates using a file descriptor.

## Find a driver's property names

Config presets and `INDI:` actions address properties as `PROPERTY.MEMBER`. Dump them from the
driver itself:

```sh
bin/indihurd dump -exec /usr/bin/indi_lx200generic          # connect, then print everything
bin/indihurd dump -exec /usr/bin/indi_lx200generic -pre     # pre-connect burst only
```

`-pre` prints the driver properties you can set to connect, such as `DEVICE_PORT`,
`DEVICE_BAUD_RATE`, `CONNECTION_MODE`. Use this to add entries to the indihurd.conf file.

## Configure

`~/.indi/indihurd.conf`:

```json
{
  "devices": [
    { "driver": "indi-camera", "exec": "indi_asi_ccd",
      "name": "ASI462", "port": 11202, "device": 0,
      "indi": { "deviceName": "ZWO CCD ASI462MC" } },

    { "driver": "indi-telescope", "exec": "/opt/indi/indi_lx200generic",
      "name": "LX200", "port": 11214, "device": 0,
      "indi": {
        "beforeConnect": { "DEVICE_PORT.PORT": "/dev/serial/by-id/usb-FTDI_x",
                           "DEVICE_BAUD_RATE.115200": "On" },
        "afterConnect":  { "TELESCOPE_SLEW_RATE.4x": "On" } } },

    { "driver": "indi-focuser", "exec": "indi_moonlite_focus",
      "name": "Moonlite", "port": 11216, "device": 0, "enable": false }
  ]
}
```

- **`exec`**: a path, or a bare name resolved on `$PATH`.
- **`driver`**: which Alpaca device type wraps the child. One of `indi-camera`, `indi-telescope`,
  `indi-focuser`, `indi-filterwheel`, `indi-rotator`, `indi-dome`, `indi-weather`, `indi-covercal`,
  `indi-safety`, `indi-switch`.
- **`port`**: required. The unique Alpaca port for the driver.
- **`device`**: the Alpaca device number, pinned.
- **`enable: false`**: keeps an entry without spawning it.
- **`indi.deviceName`**: required only when the driver exposes more than one INDI device.

Unknown keys cause a startup error.

### Connection parameters

`beforeConnect` entries are sent as soon as the property appears. They carry whatever the driver
needs to connect, such as a serial mount's port. `afterConnect` entries apply once the device is
connected.

## Serving INDI clients too

Add `indiPort` and the same live drivers become available to INDI clients (Ekos, PHD2) too.
`indiserver` can't do both at once; it has no Alpaca side.

```json
{ "indiPort": 7624, "indiListen": "127.0.0.1", "devices": [ ... ] }
```

Set `"alpaca": false` to serve INDI only: no per-entry Alpaca servers, and `port` becomes
optional.

```json
{ "alpaca": false, "indiPort": 7624,
  "devices": [ { "driver": "indi-focuser", "exec": "indi_moonlite_focus",
                 "name": "Moonlite", "device": 0 } ] }
```

An empty `indiListen` binds loopback only. INDI is unauthenticated, so open it deliberately.
Camera BLOBs are forwarded to clients that ask for them (`enableBLOB`; the default is Never), so
imaging clients work on this face. This is not a drop-in `indiserver`: it does no remote-server
chaining and no driver-to-driver snooping, so a camera's FITS headers stay empty where a real
`indiserver` setup would have a mount fill them in.

## State files

Drivers keep their config and other data in `~/.indi`, the same directory `indiserver` uses. An
existing INDI setup keeps working. indihurd.conf lives there too.

Override the location per entry with `indi.stateDir`.

```json
"indi": { "stateDir": "/var/lib/indihurd/lx200" }
```

## Anything the typed mapping doesn't cover

Every remaining property is an Alpaca action named `INDI:<PROPERTY>`. An empty `Parameters` reads
it and returns a JSON document; a JSON object of member → value writes it. `INDI:_PROPERTIES`
lists everything reachable.

## Build and run

```sh
make build                       # bin/indihurd
bin/indihurd                     # reads ~/.indi/indihurd.conf
bin/indihurd -config other.conf
```

`make help` lists the other targets.

## third-party

Two clones live under `third-party/`. 

| Clone | Needed for |
|---|---|
| `indi` | the simulators `make integration` runs against, and the stock drivers |
| `indi-3rdparty` | vendor hardware drivers as needed |

```sh
mkdir -p third-party && cd third-party
git clone https://github.com/indilib/indi.git
git clone https://github.com/indilib/indi-3rdparty.git      # vendor drivers only
```

## Building the INDI simulators

`make integration` skips unless the simulators are built. Install INDI's
prerequisites first (upstream lists them under "Install Pre-requisites" in
`third-party/indi/README.md`), then:

```sh
cmake -B build/indi -S third-party/indi \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX=$PWD/build/prefix \
  -DIconv_IS_BUILT_IN=TRUE
cmake --build build/indi -j$(nproc)
```

The tests read `build/indi/drivers/**` directly, so `cmake --install` is only
needed to get a driver set under `build/prefix/bin`. Point elsewhere with
`INDIHURD_INDI_BUILD`.

`-DIconv_IS_BUILT_IN=TRUE` works around CMake 4.x failing to detect that glibc
carries iconv in libc; without it the configure step dies on
`Could NOT find Iconv (missing: Iconv_LIBRARY)`. Drop it if your CMake gets
this right.
