package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"spm4a/internal/ipc"
	"spm4a/internal/state"
)

// Client abstracts the daemon RPC surface the TUI needs; tests inject a fake.
type Client interface {
	ListApps(ctx context.Context, ns string, all bool) ([]*state.App, error)
	StopApp(ctx context.Context, ns, name string) error
	RestartApp(ctx context.Context, ns, name string) error
	// ReloadApp returns a short human summary (build exit + health).
	ReloadApp(ctx context.Context, ns, name string) (string, error)
	DeleteApp(ctx context.Context, ns, name string) error
	// FollowLogs yields log chunks (starts with the last `lines` lines).
	FollowLogs(ctx context.Context, ns, name string, lines int) (<-chan string, func(), error)
	// Events subscribes to the event bus (ns "" = all namespaces).
	Events(ctx context.Context, ns string) (<-chan AppEvent, func(), error)
}

type AppEvent struct {
	Type      string     `json:"type"`
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	App       *state.App `json:"app"`
}

// IPCClient is the production Client backed by the daemon RPC connection.
type IPCClient struct {
	C *ipc.Client
}

func NewIPCClient(c *ipc.Client) *IPCClient { return &IPCClient{C: c} }

func (c *IPCClient) ListApps(ctx context.Context, ns string, all bool) ([]*state.App, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var res struct {
		Apps []*state.App `json:"apps"`
	}
	if err := c.C.Call(ctx, "app.list", ipc.ListParams{Namespace: ns, All: all}, &res); err != nil {
		return nil, err
	}
	return res.Apps, nil
}

func (c *IPCClient) StopApp(ctx context.Context, ns, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return c.C.Call(ctx, "app.stop", ipc.StopParams{Namespace: ns, Name: name}, nil)
}

func (c *IPCClient) RestartApp(ctx context.Context, ns, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return c.C.Call(ctx, "app.restart", ipc.RestartParams{Namespace: ns, Name: name}, nil)
}

func (c *IPCClient) ReloadApp(ctx context.Context, ns, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	var res ipc.ReloadResult
	if err := c.C.Call(ctx, "app.reload", ipc.ReloadParams{Namespace: ns, Name: name}, &res); err != nil {
		return "", err
	}
	health := "unknown"
	if m, ok := res.Health.(map[string]any); ok {
		if s, ok := m["status"].(string); ok {
			health = s
		}
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("build failed (exit %d)", res.ExitCode)
	}
	return fmt.Sprintf("build exit 0, health %s", health), nil
}

func (c *IPCClient) DeleteApp(ctx context.Context, ns, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.C.Call(ctx, "app.delete", ipc.NameParams{Namespace: ns, Name: name}, nil)
}

func (c *IPCClient) FollowLogs(ctx context.Context, ns, name string, lines int) (<-chan string, func(), error) {
	sub, err := c.C.Stream(ctx, "logs.follow", ipc.LogsParams{Namespace: ns, Name: name, Lines: lines})
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan string, 32)
	go func() {
		defer close(ch)
		for ev := range sub.Events {
			var p struct {
				Data string `json:"data"`
			}
			if err := json.Unmarshal(ev.Params, &p); err == nil && p.Data != "" {
				select {
				case ch <- p.Data:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, func() { sub.Close() }, nil
}

func (c *IPCClient) Events(ctx context.Context, ns string) (<-chan AppEvent, func(), error) {
	sub, err := c.C.Stream(ctx, "events.subscribe", ipc.SubscribeParams{Namespace: ns})
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan AppEvent, 32)
	go func() {
		defer close(ch)
		for ev := range sub.Events {
			var ae AppEvent
			if err := json.Unmarshal(ev.Params, &ae); err == nil {
				select {
				case ch <- ae:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, func() { sub.Close() }, nil
}
