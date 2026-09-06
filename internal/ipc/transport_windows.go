//go:build windows

package ipc

import (
	"context"
	"net"
	"os"
	"time"

	winio "github.com/Microsoft/go-winio"
)

func (e Endpoint) pipeName() string {
	username, hash8 := e.identity()
	return `\\.\pipe\spm4a-` + username + "-" + hash8
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
