package tui

import (
	"sync"

	"github.com/shirou/gopsutil/v4/process"
)

type appMetrics struct {
	cpu   float64 // percent since the previous measurement
	rss   uint64
	alive bool
}

// metricsCache keeps gopsutil Process handles between ticks so CPU% is
// measured per interval. Children of each root PID are included (build-tool
// process trees).
type metricsCache struct {
	mu    sync.Mutex
	procs map[int32]*process.Process
}

func newMetricsCache() *metricsCache {
	return &metricsCache{procs: map[int32]*process.Process{}}
}

// measureAll measures each root PID (plus its children); handles for PIDs no
// longer tracked are dropped.
func (mc *metricsCache) measureAll(pids []int32) map[int32]appMetrics {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	next := map[int32]*process.Process{}
	out := map[int32]appMetrics{}
	for _, pid := range pids {
		p, ok := mc.procs[pid]
		if !ok {
			np, err := process.NewProcess(pid)
			if err != nil {
				out[pid] = appMetrics{alive: false}
				continue
			}
			p = np
		}
		next[pid] = p
		var m appMetrics
		if mc.accumulate(p, next, &m, map[int32]bool{pid: true}) {
			m.alive = true
		}
		out[pid] = m
	}
	mc.procs = next
	return out
}

func (mc *metricsCache) accumulate(p *process.Process, next map[int32]*process.Process, m *appMetrics, seen map[int32]bool) bool {
	cpu, err := p.Percent(0)
	if err != nil {
		return false
	}
	m.cpu += cpu
	if mi, err := p.MemoryInfo(); err == nil && mi != nil {
		m.rss += mi.RSS
	}
	children, err := p.Children()
	if err != nil {
		return true
	}
	for _, c := range children {
		if seen[c.Pid] {
			continue
		}
		seen[c.Pid] = true
		if _, ok := next[c.Pid]; !ok {
			next[c.Pid] = c
		}
		if !mc.accumulate(c, next, m, seen) {
			delete(next, c.Pid)
		}
	}
	return true
}
