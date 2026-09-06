package ipc

import "path/filepath"

type Endpoint struct {
	Home string
}

func (e Endpoint) RunDir() string         { return filepath.Join(e.Home, "run") }
func (e Endpoint) DaemonInfoPath() string { return filepath.Join(e.RunDir(), "daemon.json") }
func (e Endpoint) DaemonLogPath() string  { return filepath.Join(e.RunDir(), "daemon.log") }
func (e Endpoint) SpawnLockPath() string  { return filepath.Join(e.RunDir(), "spawn.lock") }
