package supervisor

import "time"

// backoff applies capped exponential retry delays.
type backoff struct {
	base, cap, cur time.Duration
}

func newBackoff(base, cap time.Duration) *backoff {
	if base <= 0 {
		base = time.Second
	}
	if cap <= 0 {
		cap = 30 * time.Second
	}
	return &backoff{base: base, cap: cap}
}

// next returns the current delay, then doubles it for the following failure.
func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = b.base
		return b.cur
	}
	d := b.cur
	b.cur *= 2
	if b.cur > b.cap {
		b.cur = b.cap
	}
	return d
}

func (b *backoff) reset() { b.cur = 0 }
