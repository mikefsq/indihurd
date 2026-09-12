# Driver integration

indihurd coordinates INDI driver processes within a systemd-hosted service,
provides browser management, and maintains persistent hardware connection
configuration. It serves devices to INDI clients and provides Alpaca interfaces
through device mappings. Systemd manages indihurd; indihurd supervises its driver
children.

Install INDI drivers in a directory on the indihurd service's `PATH`, using the
normal INDI installation process. indihurd serves `indi_*` binaries without
requiring changes to the drivers or rebuilding indihurd. Alpaca mappings provide
an additional interface for supported device types.

The Add device page lists executable `indi_*` files on `PATH`. INDI profiles
apply subsets of the devices configured on the Devices page by saving enable
flags and reconciling their managed processes. The Ekos API translates configured
instance names to installed INDI XML catalog labels by executable; browser
editing and persisted profiles retain the instance names.
See [README.md](README.md) for configuration and property dumps.

Go changes are needed when an INDI property needs a new mapping or a new
Alpaca device type is added. The focuser implementation is a compact starting
point: [device.go](internal/devtype/focuser/device.go),
[bindings.go](internal/devtype/focuser/bindings.go), and
[its host builder](internal/host/builder_focuser.go).

## Inspect the INDI driver

Use `indihurd dump -exec <driver> -pre` to inspect connection properties, then
repeat without `-pre` for connected-device properties. Record exact property
and member names, vector types, permissions, ranges, and state transitions.
The browser Add device page can also inspect pre-connect properties in a
temporary session. Use Setup for a configured running process; do not launch
a second process against hardware already in use.

A driver may publish additional definitions after reporting that it is
connected. Capabilities must follow the current snapshot, including changes
when hardware disconnects or the child restarts. Use exact names or labels
for driver-specific controls to avoid matching an unrelated property.

## Package structure

Each package under `internal/devtype/` contains:

| File | Purpose |
|---|---|
| `bindings.go` | Mapping table and consumed-property set |
| `device.go` | goalpaca device interface, lifecycle, and passthrough actions |
| `register.go` | Construction inputs and `New` |
| `bindings_test.go` | Interface coverage and property-mapping tests |

Use `convert.go` for unit conversions when needed. Device packages access
properties through `binding.Kit`; the host assembles the supervisor and
registers devices with goalpaca. Device packages do not start processes or
import the host or supervisor directly.

`internal/check` tests enforce package boundaries, required files, action
methods, and references from mapping rows to implementation functions.

## Define the mapping

Add a `binding.Table` row for every type-specific ASCOM member. A setter taking
one argument shares its property's row; methods such as `SetPark()` need their
own rows. `binding.CheckTotal` checks the table against the goalpaca interface.

| Kind | Use |
|---|---|
| `Mapped` | A direct INDI property/member mapping |
| `Func` | A sequence, conversion, or state-dependent operation; set `Fn` |
| `Derived` | A capability or value inferred from property presence, permissions, or state |
| `Synthesised` | A value retained or computed by the bridge |
| `Absent` | No supported mapping; provide a short `Why` explanation |

Keep `Why` focused on the mapping constraint. Include units and meaningful
fallback behavior, without implementation history.

Build the consumed-property set with `table.Consumed(...)`. Include properties
used indirectly by multi-step operations or dynamic lookups. Pass the same set
to `binding.Validate` and `actions.New` so typed properties do not also appear
as passthrough actions. The switch mapping uses a predicate for dynamically
named property families.

## Implement the device

Embed `server.BaseDevice` and implement the relevant goalpaca interface.
`New` sets identity, interface version, and the action engine without accessing
hardware. Derive `UniqueID` through `binding.UniqueID`, using the configured
serial or the host's slot fallback.

Follow the existing lifecycle methods: `Open` starts `Kit.Run`, and `Close`
cancels it. Client `Connect` and `Disconnect` state is separate from the child
process and hardware connection.

Use the Kit's gated reads and writes. Missing mappings should report
NotImplemented (`0x400`); unavailable devices should report NotConnected
(`0x407`) with the supervisor's reason. Validate write ranges before sending
anything to the child.

For asynchronous operations, keep the completion property accurate from the
moment the command returns. `binding.Inflight` handles drivers that acknowledge
an operation without publishing Busy. Capture update timestamps before sending
when waiting for an acknowledgement, and clear pending operations if the child
becomes unavailable.

Preserve driver requirements for complete-vector writes. In particular,
`GEOGRAPHIC_COORD` and `TIME_UTC` must include all required members. Keep unit
conversions explicit, and read batched `DeviceState` values from one snapshot.

Wire `SupportedActions` and `Action` through `actions.Engine`. It exposes
eligible unconsumed properties and excludes bridge-managed, video, Light,
and BLOB properties.

## Add the host builder

Add `internal/host/builder_<type>.go`. Register the configuration's `driver`
name with the host's `register` function in `init()`.

The builder should:

1. Call `assemble` with the entry, logger, and type validator to get a supervisor
   and Kit. Use `assembleWith` for image or connection callbacks.
2. Resolve the device number with `Entry.Number`.
3. Pass the display name, executable, slot, serial, and host version to `New`.
4. Register the result with the goalpaca server and return the supervisor.

The host validates mappings when the child connects and again after its
property definitions settle. BLOB callback data belongs to the supervisor and
is valid only during the callback; copy or convert it before returning if it
must be retained.

## Validate changes

Run `make check` for formatting, vet, and unit tests. Mapping tests should cover
valid reads and writes, unsupported properties, permission and range errors,
Busy and Alert states, child loss, and action filtering. Use synthetic snapshots
and a recording sender to verify the exact commands emitted without hardware.

`make deps-head` updates mikefsq dependencies to their latest `main` commits.
`make tidy` performs that update and runs `go mod tidy`. Review dependency-file
changes before committing them.

### INDI simulator tests

The integration suite needs Linux and built INDI simulators. Install the core
build prerequisites listed in the upstream INDI README, then use the repository
build target:

```sh
make indi-drivers
INDIHURD_INDI_BUILD="$PWD/build/core" make integration
```

The build stages core drivers and libraries under `build/prefix` without sudo.
Tests use the build tree directly, so system installation is unnecessary.
`INDIHURD_INDI_BUILD` selects another tree; the test helper defaults to
`build/core`. If `INDI_BUILD_DIR` is customized, point
`INDIHURD_INDI_BUILD` at its `core` subdirectory.

To request the auxiliary simulator targets explicitly:

```sh
cmake --build build/core --target \
  indi_simulator_rotator indi_simulator_io \
  indi_simulator_lightpanel indi_simulator_dustcover
INDIHURD_INDI_BUILD="$PWD/build/core" make integration
```

Tests skip unavailable simulators, so inspect skipped tests when assessing
coverage. They require local sockets and subprocess execution.

The optional `make indi-thirdparty` target builds the selected vendor families
listed in [README.md](README.md#build-selected-third-party-indi-drivers), with
separate installation. Other vendor drivers may require their own build
prerequisites. Test real hardware for behavior the simulators do not exercise,
including reconnects and driver-specific properties.

## Management and protocol changes

Browser routes, generated property forms, configuration validation, and Web
Manager compatibility live in `internal/host`. Validate device drafts in the
context of the full configuration; names and explicit Alpaca ports must be unique,
including ports reserved by disabled entries. Validation and failed saves must
preserve the user's draft and the saved configuration.

Native Web Manager profiles have their own persistence; applying them updates
the configured device enable flags for both INDI and Alpaca. Status and running-driver endpoints
also report configured drivers served through the INDI listener. See the
[Web Manager documentation](README.md#web-manager-compatibility) for supported
operations and limitations.

`internal/indiwire` parses protocol messages; `internal/supervisor` manages child
processes; `internal/indiserve` serves INDI clients. Child parsers accept and log
plain-text diagnostics between top-level XML messages. Parsing inside an XML
message remains strict, and the parser defaults to strict behavior for other
callers. Protocol regression tests should cover fragmented input and malformed
messages without requiring hardware.

`make test` also runs the Python build and installation tests. Generated sources,
SDKs, binaries, and caches under `build/` are ignored by Git; the build scripts
and their tests in that directory are repository source files.
