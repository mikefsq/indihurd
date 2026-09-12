// Command indihurd serves INDI drivers to INDI and ASCOM Alpaca clients.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mikefsq/indihurd/internal/host"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "dump" {
		runDump(os.Args[2:])
		return
	}
	cfgPath := flag.String("config", "/etc/indihurd/indihurd.conf", "config file")
	web := flag.String("web", ":8624", "web interface and Ekos Web Manager HTTP address; empty disables HTTP")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *web != "" {
		if err := host.RunManaged(ctx, *cfgPath, *web, log.Printf); err != nil && ctx.Err() == nil {
			log.Fatalf("manage: %v", err)
		}
		return
	}
	cfg, err := host.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := host.Run(ctx, cfg, log.Printf); err != nil && ctx.Err() == nil {
		log.Fatalf("serve: %v", err)
	}

}

func runDump(args []string) {
	fs := flag.NewFlagSet("dump", flag.ExitOnError)
	exe := fs.String("exec", "", "driver executable (path, or bare name via $PATH)")
	pre := fs.Bool("pre", false, "list properties without requesting a hardware connection")
	stateDir := fs.String("statedir", "", "child HOME override (driver state uses HOME/.indi)")
	timeout := fs.Duration("timeout", 10*time.Second, "maximum duration")
	fs.Parse(args)
	if *exe == "" {
		fmt.Fprintln(os.Stderr, "usage: indihurd dump -exec <path> [-pre] [-statedir <dir>] [-timeout <d>]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	if err := host.Dump(ctx, host.DumpOptions{
		Exec: *exe, Pre: *pre, StateDir: *stateDir, Timeout: *timeout,
	}, os.Stdout, logf); err != nil {
		log.Fatalf("dump: %v", err)
	}
}
