package host

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/mikefsq/indihurd/internal/snapshot"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

// DumpOptions configures one diagnostic session (`indihurd dump`).
type DumpOptions struct {
	Exec     string        // driver executable; path, or bare name via $PATH
	Pre      bool          // pre-connect burst only: never send CONNECT
	StateDir string        // child $HOME override; empty = inherit ~/.indi
	Timeout  time.Duration // total budget; zero = 10s
}

// Dump spawns a driver, waits for its properties to settle, and prints them.
func Dump(ctx context.Context, o DumpOptions, out io.Writer, logf func(string, ...any)) error {
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Second
	}
	st := snapshot.NewStore()
	sup := supervisor.New(supervisor.Config{
		Name:        filepath.Base(o.Exec),
		Argv:        []string{o.Exec},
		Env:         stateEnv(Entry{Indi: IndiBlock{StateDir: o.StateDir}}),
		HoldConnect: o.Pre,
		Logf:        logf,
	}, st)

	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	done := make(chan struct{})
	go func() { supervisor.Run(ctx, sup); close(done) }()

	const quiet = 1200 * time.Millisecond
	lastGen, lastChange := uint64(0), time.Now()
	for {
		select {
		case <-ctx.Done():
			goto render
		case <-time.After(50 * time.Millisecond):
		}
		g := st.Current().Generation()
		if g != lastGen {
			lastGen, lastChange = g, time.Now()
			continue
		}
		if g > 0 && time.Since(lastChange) > quiet && (o.Pre || sup.Serving()) {
			goto render
		}
	}

render:
	snap := st.Current()
	if !o.Pre && !sup.Serving() {
		fmt.Fprintf(out, "# WARNING: never reached connected state: %s\n", sup.Reason())
		fmt.Fprintf(out, "# (the properties below are the pre-connect set)\n")
	}
	devices := snap.Devices()
	sort.Strings(devices)
	if len(devices) == 0 {
		cancel()
		<-done
		return fmt.Errorf("no properties received from %s (is it an INDI driver?)", o.Exec)
	}
	for _, dev := range devices {
		props := snap.Properties(dev)
		sort.Strings(props)
		for _, prop := range props {
			v, ok := snap.Vector(dev, prop)
			if !ok {
				continue
			}
			fmt.Fprintf(out, "# %s.%s — %v %v %q state=%v\n", dev, prop, v.Type, v.Perm, v.Label, v.State)
			for _, m := range v.Members {
				fmt.Fprintf(out, "%s.%s.%s=%s\n", dev, prop, m.Name, memberString(v.Type.String(), m))
			}
		}
	}
	cancel()
	<-done
	return nil
}

// memberString matches on the type's String form: host may not import indiwire.
func memberString(vtype string, m snapshot.MemberVal) string {
	switch vtype {
	case "Switch":
		if m.On {
			return "On"
		}
		return "Off"
	case "Number":
		if m.HasRange {
			return fmt.Sprintf("%g  (min %g max %g step %g)", m.Value, m.Min, m.Max, m.Step)
		}
		return fmt.Sprintf("%g", m.Value)
	case "BLOB":
		return fmt.Sprintf("<blob %s %d bytes>", m.BlobFormat, m.Size)
	case "Light":
		return m.Text
	}
	return m.Text
}
