package binding

import (
	"context"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

// Sender is how a device writes to its driver, satisfied structurally by the
// supervisor.
type Sender interface {
	SetNumber(ctx context.Context, device, prop string, v map[string]float64) error
	// INDI applies only the members named in a message, so turning an
	// AtMostOne vector off means listing the member in off.
	SetSwitch(ctx context.Context, device, prop string, on, off []string) error
	SetText(ctx context.Context, device, prop string, v map[string]string) error
	// WaitSettle blocks until prop has been updated after since and is no
	// longer Busy.
	WaitSettle(ctx context.Context, device, prop string, since time.Time, timeout time.Duration) (indiwire.State, string, error)
	// WaitUpdate blocks until any update to prop after since.
	WaitUpdate(ctx context.Context, device, prop string, since time.Time, timeout time.Duration) error
}
