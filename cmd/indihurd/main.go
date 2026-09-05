// Command indihurd serves INDI drivers to INDI and ASCOM Alpaca clients.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mikefsq/indihurd/internal/host"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "dump" {
		runDump(os.Args[2:])
		return
	}
	cfgPath := flag.String("config", "", "config file (default ~/.indi/indihurd.conf)")
	flag.Parse()
	if *cfgPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "indihurd: no home directory; use -config <file>")
			os.Exit(2)
		}
		*cfgPath = filepath.Join(home, ".indi", "indihurd.conf")
	}
	cfg, err := host.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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
