package host

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/indiserve"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

// Run builds every enabled entry and serves each on its pinned port, blocking
// until ctx ends or the first server error.
func Run(ctx context.Context, f *File, logf func(string, ...any)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var alpacaPorts []int

	errs := make(chan error, len(f.Devices)+1)
	n := 0
	var built []*Built
	for _, e := range f.Devices {
		if !e.Enabled() {
			continue
		}
		// DiscoveryOff on every server, because the HOST answers for all of them (discovery.go).
		//
		// Two things were wrong before. Discovery was never set at all, so each server took
		// goalpaca's zero value — DiscoveryRegister with no ServerAddr, registering with nothing —
		// and the host answered `configureddevices` on every port while being findable by nobody.
		// Setting each server to DiscoveryDirect instead made them each bind 32227 with
		// SO_REUSEPORT, which answers a BROADCAST from all of them and a UNICAST from exactly one:
		// a client on this machine saw a single arbitrary device.
		b, err := Build(e, server.Config{
			AlpacaPort: e.Port,
			Discovery:  server.DiscoveryConfig{Mode: server.DiscoveryOff},
		}, logf)
		if err != nil {
			return fmt.Errorf("build %q: %w", e.Name, err)
		}
		built = append(built, b)
		alpacaPorts = append(alpacaPorts, e.Port)
		n++
		if f.AlpacaEnabled() {
			go func(b *Built) {
				errs <- b.Server.Run(ctx)
			}(b)
			continue
		}
		// INDI-only: nothing calls Device.Open, so the acquire loop has to run
		// here or the driver never spawns.
		go func(b *Built) {
			supervisor.Run(ctx, b.Sup)
			errs <- nil
		}(b)
	}
	// Discovery follows the Alpaca face and has no switch of its own: serving Alpaca without
	// announcing it is not a configuration anyone wants. Started AFTER the servers, so the
	// responder only advertises ports something is listening on, and skipped when the face is off,
	// since announcing ports that serve nothing sends every client to a closed door.
	if f.AlpacaEnabled() {
		runDiscovery(ctx, alpacaPorts, logf)
	}
	if n == 0 {
		return fmt.Errorf("no enabled devices in config")
	}

	if f.IndiPort > 0 {
		children := make([]indiserve.Child, 0, len(built))
		for _, b := range built {
			children = append(children, b.Sup)
		}
		// Empty means loopback, not every interface: INDI is unauthenticated.
		listen := f.IndiListen
		if listen == "" {
			listen = "127.0.0.1"
		}
		srv := indiserve.New(net.JoinHostPort(listen, strconv.Itoa(f.IndiPort)), logf, children...)
		for _, b := range built {
			b.Sup.SetOnElement(srv.Publish)
			b.Sup.SetOnBlob(srv.PublishBlob)
		}
		n++
		go func() { errs <- srv.Serve(ctx) }()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil && ctx.Err() == nil {
			cancel()
			return err
		}
	}
	return nil
}
