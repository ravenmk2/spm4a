package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"spm4a/internal/ipc"
)

var ErrUnavailable = errors.New("daemon unavailable")

// Ensure returns a connected client, spawning the daemon on demand:
// connect → spawn lock → double-check → detached spawn → poll ping (5s).
// A major version mismatch asks the old daemon to exit and respawns.
func Ensure(ctx context.Context, home string) (*ipc.Client, error) {
	ep := ipc.Endpoint{Home: home}
	c := ipc.NewClient(ep)

	ok, err := handshake(ctx, c)
	if err == nil {
		if ok {
			return c, nil
		}
		retireOld(ctx, c, ep)
	}

	if err := os.MkdirAll(ep.RunDir(), 0o700); err != nil {
		return nil, fmt.Errorf("%w: create run dir: %v", ErrUnavailable, err)
	}
	lock := flock.New(ep.SpawnLockPath())
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("%w: acquire spawn lock: %v", ErrUnavailable, err)
	}
	defer lock.Unlock()

	ok, err = handshake(ctx, c)
	if err == nil {
		if ok {
			return c, nil
		}
		retireOld(ctx, c, ep)
	}

	if err := spawnDaemon(ep); err != nil {
		return nil, fmt.Errorf("%w: spawn: %v", ErrUnavailable, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok, err := handshake(ctx, c); err == nil && ok {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("%w: daemon did not become ready within 5s (see %s)", ErrUnavailable, ep.DaemonLogPath())
}

// handshake pings the daemon; reports whether the major version matches.
func handshake(ctx context.Context, c *ipc.Client) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var res ipc.PingResult
	if err := c.Call(ctx, "daemon.ping", nil, &res); err != nil {
		return false, err
	}
	return major(res.Version) == major(Version), nil
}

// retireOld asks an incompatible daemon to exit gracefully (apps keep
// running and get re-adopted by the new daemon) and waits for it to go away.
func retireOld(ctx context.Context, c *ipc.Client, ep ipc.Endpoint) {
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = c.Call(sctx, "daemon.shutdown", ipc.ShutdownParams{All: false}, nil)
	for i := 0; i < 50; i++ {
		conn, err := ep.Dial(sctx)
		if err != nil {
			return
		}
		conn.Close()
		select {
		case <-sctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func major(v string) string {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	return v
}
