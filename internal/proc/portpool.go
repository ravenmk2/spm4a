package proc

import (
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// PortPool hands out random ports in [lo, hi]: probe-bind, release, then
// reserve in memory until the caller reports the outcome.
type PortPool struct {
	mu       sync.Mutex
	lo, hi   int
	reserved map[int]struct{}
}

func NewPortPool(lo, hi int) *PortPool {
	return &PortPool{lo: lo, hi: hi, reserved: map[int]struct{}{}}
}

func (p *PortPool) Acquire() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for range 64 {
		port := p.lo + rand.IntN(p.hi-p.lo+1)
		if _, ok := p.reserved[port]; ok {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		p.reserved[port] = struct{}{}
		return port, nil
	}
	return 0, fmt.Errorf("no free port in pool range %d-%d", p.lo, p.hi)
}

func (p *PortPool) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.reserved, port)
}

// WaitDead polls until the PID is gone or d elapses; returns true if dead.
func WaitDead(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !Alive(pid)
}
