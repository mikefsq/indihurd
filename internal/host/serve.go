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
		// The host answers discovery for all device ports.
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
		// Without an Alpaca server, start the supervisor directly.
		go func(b *Built) {
			supervisor.Run(ctx, b.Sup)
			errs <- nil
		}(b)
	}
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
