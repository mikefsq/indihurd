# Driver integration

indihurd wraps existing INDI binaries. To use another driver of a supported
type, install the binary and add a configuration entry; no Go registration or
rebuild is needed. See [README.md](README.md) for configuration and property dumps.

Go changes are needed when an INDI property needs a new mapping or a new
Alpaca device type is added. The focuser implementation is a compact starting
point: [device.go](internal/devtype/focuser/device.go),
[bindings.go](internal/devtype/focuser/bindings.go), and
[its host builder](internal/host/builder_focuser.go).

## Inspect the INDI driver

Use `indihurd dump -exec <driver> -pre` to inspect connection properties, then
repeat without `-pre` for connected-device properties. Record exact property
and member names, vector types, permissions, ranges, and state transitions.

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

The integration suite needs Linux and built INDI simulators. Clone INDI and
install its documented build prerequisites:

```sh
mkdir -p third-party
git clone https://github.com/indilib/indi.git third-party/indi
cmake -B build/indi -S third-party/indi \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$PWD/build/prefix"
cmake --build build/indi --parallel
```

If CMake cannot find Iconv on a glibc system, try configuring with
`-DIconv_IS_BUILT_IN=TRUE`.

Some tests use simulators outside the default build set:

```sh
cmake --build build/indi --target \
  indi_simulator_rotator indi_simulator_io \
  indi_simulator_lightpanel indi_simulator_dustcover
make integration
```

Tests read executables under `build/indi/drivers/`; installation is unnecessary.
Set `INDIHURD_INDI_BUILD` to use another build tree. Tests skip unavailable
simulators, so inspect the skipped tests when assessing coverage.

Vendor drivers may also require an
[indi-3rdparty](https://github.com/indilib/indi-3rdparty) checkout and its build
prerequisites. Test real hardware for behavior the simulators do not exercise,
including reconnects and driver-specific properties.
