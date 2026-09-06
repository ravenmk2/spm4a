//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"time"
)

func (e Endpoint) sockPath() string { return filepath.Join(e.RunDir(), "spm4a.sock") }

func (e Endpoint) Address() string { return e.sockPath() }

func (e Endpoint) Listen() (net.Listener, error) {
	if err := os.MkdirAll(e.RunDir(), 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(e.sockPath())
	l, err := net.Listen("unix", e.sockPath())
	if err != nil {
		return nil, err
	}
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
