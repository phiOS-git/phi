package agent

import (
	"sync"
	"time"
)

// bucket is a fixed-window request counter: at most n requests per window.
// A local limit is a complement to the provider spending cap, not a
// replacement (phios-agente.md §6.3 / §6.4): the cap limits the damage, the
// window limits how fast a compromised agent can spend against it.
//
// A fixed window (rather than a token bucket) is deliberate: it is trivial
// to reason about from the meter log — "at most n per window" — and the
// small burst allowance at a window boundary does not matter for a budget
// guard.
type bucket struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	count   int
	resetAt time.Time
	nowFn   func() time.Time // swappable for tests
}

func newBucket(limit int, window time.Duration) *bucket {
	return &bucket{limit: limit, window: window, nowFn: time.Now}
}

// allow reports whether a request may proceed and, if so, counts it.
func (b *bucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.nowFn()
	if now.After(b.resetAt) {
		b.count = 0
		b.resetAt = now.Add(b.window)
	}
	if b.count >= b.limit {
		return false
	}
	b.count++
	return true
}
