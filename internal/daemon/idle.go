package daemon

import (
	"sync"
	"time"
)

// idleTracker exits the daemon when there are no active apps and no
// connected clients (in-flight requests incl. SSE subscriptions). The policy
// (config.yaml idle-exit) selects immediate (short grace to flush the final
// response), never, or a delayed exit; daemon.shutdown forces a prompt exit
// regardless.
type idleTracker struct {
	mu       sync.Mutex
	inflight int
	apps     int
	seen     bool
	force    bool
	policy   idlePolicy
	timer    *time.Timer
	onIdle   func()
}

const idleGrace = 200 * time.Millisecond

func newIdleTracker(onIdle func(), policy idlePolicy) *idleTracker {
	return &idleTracker{onIdle: onIdle, policy: policy}
}

func (t *idleTracker) enter() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight++
	t.seen = true
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

func (t *idleTracker) leave() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight--
	t.checkLocked()
}

func (t *idleTracker) setApps(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.apps = n
	t.checkLocked()
}

func (t *idleTracker) forceShutdown() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.force = true
	t.checkLocked()
}

func (t *idleTracker) checkLocked() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	switch {
	case t.force:
		t.timer = time.AfterFunc(idleGrace, t.onIdle)
	case t.policy.never:
		// idle-exit: never — only daemon.shutdown exits the daemon
	case t.seen && t.inflight == 0 && t.apps == 0:
		t.timer = time.AfterFunc(t.policy.delay, t.onIdle)
	}
}
