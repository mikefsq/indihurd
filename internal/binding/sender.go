package binding

import (
	"context"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

// Sender writes device properties and waits for driver updates.
type Sender interface {
	SetNumber(ctx context.Context, device, prop string, v map[string]float64) error
	// INDI applies only the members named in a message, so turning an
	// AtMostOne vector off means listing the member in off.
	SetSwitch(ctx context.Context, device, prop string, on, off []string) error
	SetText(ctx context.Context, device, prop string, v map[string]string) error
	// WaitSettle waits for a non-Busy update after since and returns its state and message.
	// Capture since before sending; zero accepts the current state.
	WaitSettle(ctx context.Context, device, prop string, since time.Time, timeout time.Duration) (indiwire.State, string, error)
	// WaitUpdate waits for any update after since, including Busy.
	WaitUpdate(ctx context.Context, device, prop string, since time.Time, timeout time.Duration) error
}
