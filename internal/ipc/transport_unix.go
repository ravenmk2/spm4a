//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"time"
)

// sockPath: three-tier rule (see unixSockPath in transport.go — the single
// point shared by Listen and Dial).
func (e Endpoint) sockPath() string {
	username, _ := e.identity()
	return unixSockPath(e.absHome(), username, os.Getenv("XDG_RUNTIME_DIR"))
}

func (e Endpoint) Address() string { return e.sockPath() }

func (e Endpoint) Listen() (net.Listener, error) {
	if err := os.MkdirAll(e.RunDir(), 0o700); err != nil {
		return nil, err
	}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		// tier 1 socket directory (0700); Dial only computes the path.
		if err := os.MkdirAll(filepath.Join(xdg, "spm4a"), 0o700); err != nil {
			return nil, err
		}
	}
	_ = os.Remove(e.sockPath())
	l, err := net.Listen("unix", e.sockPath())
	if err != nil {
		return nil, err
	}
	// /tmp and XDG dirs are shared; the socket file itself is owner-only.
	if err := os.Chmod(e.sockPath(), 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func (e Endpoint) Dial(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: time.Second}
	return d.DialContext(ctx, "unix", e.sockPath())
}

func (e Endpoint) Cleanup() { _ = os.Remove(e.sockPath()) }
