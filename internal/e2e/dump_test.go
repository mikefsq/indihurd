//go:build integration

package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/host"
)

// TestDump checks the dump subcommand's two modes: -pre stops at the
// pre-connect burst, full connects and includes the trailing def burst.
func TestDump(t *testing.T) {
	exe := filepath.Join(indiBuild(t), focusSim)
	run := func(pre bool) string {
		var sb strings.Builder
		err := host.Dump(context.Background(), host.DumpOptions{
			Exec: exe, Pre: pre, StateDir: t.TempDir(), Timeout: 15 * time.Second,
		}, &sb, t.Logf)
		if err != nil {
			t.Fatalf("Dump(pre=%v): %v", pre, err)
		}
		return sb.String()
	}

	pre := run(true)
	if !strings.Contains(pre, "Focuser Simulator.CONNECTION.CONNECT=Off") {
		t.Errorf("pre dump: not left unconnected:\n%s", pre)
	}
	if !strings.Contains(pre, "DEVICE_BAUD_RATE") {
		t.Errorf("pre dump: connection knobs missing:\n%s", pre)
	}
	if strings.Contains(pre, "ABS_FOCUS_POSITION") {
		t.Errorf("pre dump: contains post-connect properties:\n%s", pre)
	}

	full := run(false)
	if !strings.Contains(full, "Focuser Simulator.CONNECTION.CONNECT=On") {
		t.Errorf("full dump: never connected:\n%s", full)
	}
	if !strings.Contains(full, "ABS_FOCUS_POSITION.FOCUS_ABSOLUTE_POSITION") {
		t.Errorf("full dump: post-connect properties missing:\n%s", full)
	}
}
