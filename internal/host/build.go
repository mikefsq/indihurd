// Package host holds config, one Alpaca server per entry, and the store, supervisor and Kit assembly.
package host

import (
	"context"
	"fmt"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/snapshot"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

// Built is one assembled entry: the goalpaca server carrying its devices, plus
// the supervisor handle.
type Built struct {
	Server *server.Server
	Sup    *supervisor.Supervisor
}

type builder func(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error)

var builders = map[string]builder{}

func register(driver string, b builder) { builders[driver] = b }

// Build assembles one enabled entry onto a fresh goalpaca server.
func Build(e Entry, cfg server.Config, logf func(string, ...any)) (*Built, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	srv := server.New(cfg)
	b, ok := builders[e.Driver]
	if !ok {
		// Load validates driver names, but Build is callable on its own.
		return nil, fmt.Errorf("entry %q: unknown driver %q", e.Name, e.Driver)
	}
	sup, err := b(e, srv, logf)
	if err != nil {
		return nil, err
	}
	return &Built{Server: srv, Sup: sup}, nil
}

// The connected def burst trails the Serving transition, so re-validation
// waits for defQuiet of silence instead of a fixed sleep; defSettleMax bounds
// a driver that redefines forever.
const (
	defQuiet     = time.Second
	defSettleMax = 15 * time.Second
)

func afterDefBurst(sup *supervisor.Supervisor, fn func(*snapshot.Snapshot)) {
	go func() {
		deadline := time.Now().Add(defSettleMax)
		for {
			idle := time.Since(sup.LastDef())
			if idle >= defQuiet || time.Now().After(deadline) {
				break
			}
			time.Sleep(defQuiet - idle)
		}
		if sup.Serving() {
			fn(sup.Snapshot())
		}
	}()
}

type extras struct {
	onBlob    func(device, prop, member string, data []byte, format string)
	onServing func(*snapshot.Snapshot)
}

func assemble(e Entry, logf func(string, ...any), validate func(*snapshot.Snapshot, string) []string) (*supervisor.Supervisor, *binding.Kit) {
	return assembleWith(e, logf, validate, extras{})
}

func assembleWith(e Entry, logf func(string, ...any), validate func(*snapshot.Snapshot, string) []string, x extras) (*supervisor.Supervisor, *binding.Kit) {
	st := snapshot.NewStore()
	var sup *supervisor.Supervisor
	onServing := func(snap *snapshot.Snapshot) {
		device := e.Indi.DeviceName
		if device == "" {
			if ds := snap.Devices(); len(ds) == 1 {
				device = ds[0]
			}
		}
		for _, note := range validate(snap, device) {
			logf("%s: %s", e.Name, note)
		}
		if x.onServing != nil {
			x.onServing(snap)
		}
	}
	sup = supervisor.New(supervisor.Config{
		Name:                 e.Name,
		Argv:                 []string{e.Exec},
		Env:                  stateEnv(e),
		Device:               e.Indi.DeviceName,
		PollingPeriodMs:      e.Indi.PollingPeriodMs,
		PresetsBeforeConnect: e.Indi.BeforeConnect,
		PresetsAfterConnect:  e.Indi.AfterConnect,
		RecordPath:           e.Indi.Record,
		Logf:                 logf,
		OnBlob:               x.onBlob,
		OnServing: func(snap *snapshot.Snapshot) {
			onServing(snap)
			afterDefBurst(sup, onServing)
		},
	}, st)

	kit := &binding.Kit{
		Device: e.Indi.DeviceName,
		Snap:   st.Current,
		Send:   sup,
		Avail:  func() (bool, string) { return sup.Serving(), sup.Reason() },
		Run:    func(ctx context.Context) { supervisor.Run(ctx, sup) },
		Log:    logf,
	}
	return sup, kit
}

func stateEnv(e Entry) []string {
	if e.Indi.StateDir == "" {
		return nil
	}
	return []string{"HOME=" + e.Indi.StateDir}
}

// Version is stamped by the build; the default marks development binaries.
var Version = "dev"
