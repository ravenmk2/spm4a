package daemon

import (
	"sync"

	"spm4a/internal/state"
)

const (
	EventAppStatus  = "app.status"
	EventAppDeleted = "app.deleted"
)

type Event struct {
	Type      string     `json:"type"`
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	App       *state.App `json:"app,omitempty"`
}

type busSub struct {
	ns string
	ch chan Event
}

type Bus struct {
	mu   sync.Mutex
	seq  int
	subs map[int]busSub
}

func NewBus() *Bus { return &Bus{subs: map[int]busSub{}} }

// Subscribe registers for events; ns == "" receives all namespaces.
func (b *Bus) Subscribe(ns string) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := b.seq
	ch := make(chan Event, 64)
	b.subs[id] = busSub{ns: ns, ch: ch}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.subs, id)
			close(ch)
		})
	}
}

func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if s.ns != "" && s.ns != ev.Namespace {
			continue
		}
		select {
		case s.ch <- ev:
		default:
		}
	}
}
