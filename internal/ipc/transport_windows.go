//go:build windows

package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	winio "github.com/Microsoft/go-winio"
)

func (e Endpoint) pipeName() string {
	abs, err := filepath.Abs(e.Home)
	if err != nil {
		abs = e.Home
	}
	sum := sha256.Sum256([]byte(abs))
	name := os.Getenv("USERNAME")
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		name = "user"
	}
	return `\\.\pipe\spm4a-` + name + "-" + hex.EncodeToString(sum[:4])
}

func (e Endpoint) Address() string { return e.pipeName() }

func (e Endpoint) Listen() (net.Listener, error) {
	if err := os.MkdirAll(e.RunDir(), 0o700); err != nil {
		return nil, err
	}
	return winio.ListenPipe(e.pipeName(), &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;OW)",
	})
}

func (e Endpoint) Dial(ctx context.Context) (net.Conn, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	return winio.DialPipeContext(ctx, e.pipeName())
}

func (e Endpoint) Cleanup() {}
