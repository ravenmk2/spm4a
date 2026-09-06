package ipc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Endpoint locates the daemon transport. daemon.json / spawn.lock /
// daemon.log live under <home>/run; unix sockets follow the three-tier rule
// in unixSockPath (XDG_RUNTIME_DIR → home run dir → /tmp fallback); Windows
// uses \\.\pipe\spm4a-<user>-<hash8>.
type Endpoint struct {
	Home string
}

func (e Endpoint) RunDir() string         { return filepath.Join(e.Home, "run") }
func (e Endpoint) DaemonInfoPath() string { return filepath.Join(e.RunDir(), "daemon.json") }
func (e Endpoint) DaemonLogPath() string  { return filepath.Join(e.RunDir(), "daemon.log") }
func (e Endpoint) SpawnLockPath() string  { return filepath.Join(e.RunDir(), "spawn.lock") }

func (e Endpoint) absHome() string {
	abs, err := filepath.Abs(e.Home)
	if err != nil {
		return e.Home
	}
	return abs
}

// hash8 is the 8-char hex prefix of sha256(home absolute path) — the naming
// material shared by the Windows pipe and the unix /tmp fallback.
func hash8(homeAbs string) string {
	sum := sha256.Sum256([]byte(homeAbs))
	return hex.EncodeToString(sum[:4])
}

// identity returns the sanitized current username and the home hash.
func (e Endpoint) identity() (username, hash string) {
	hash = hash8(e.absHome())
	username = os.Getenv("USERNAME")
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = u.Username
	}
	if i := strings.LastIndexAny(username, `/\`); i >= 0 {
		username = username[i+1:]
	}
	if username == "" {
		username = "user"
	}
	return username, hash
}

// maxSockPathLen: macOS/BSD sun_path is 104 bytes (Linux 108); keep margin.
const maxSockPathLen = 100

// unixSockPath selects the unix socket path, three tiers:
//  1. <xdgRuntimeDir>/spm4a/spm4a-<hash8>.sock when xdgRuntimeDir is non-empty
//     (hash8 distinguishes instances with different SPM4A_HOME — the XDG dir
//     is shared per user);
//  2. <home>/run/spm4a.sock when that full path fits maxSockPathLen;
//  3. /tmp/spm4a-<username>-<hash8>.sock otherwise (constant ~30 chars;
//     long macOS-runner TMPDIRs nested under test tempdirs blow past 104).
//
// Pure function shared by Listen and Dial, testable on any GOOS.
//
// Note: the path depends on the process env (XDG_RUNTIME_DIR). The daemon is
// spawned by the CLI and inherits its environment, so within a session both
// sides agree; a session with a different XDG value (rare) would spawn a
// second daemon — the spawn lock keeps that safe.
func unixSockPath(homeAbs, username, xdgRuntimeDir string) string {
	if xdgRuntimeDir != "" {
		return filepath.Join(xdgRuntimeDir, "spm4a", "spm4a-"+hash8(homeAbs)+".sock")
	}
	def := filepath.Join(homeAbs, "run", "spm4a.sock")
	if len(def) <= maxSockPathLen {
		return def
	}
	return sockAddr("/tmp", username, hash8(homeAbs))
}

// sockAddr renders the /tmp fallback socket path.
func sockAddr(dir, username, hash string) string {
	return filepath.Join(dir, "spm4a-"+username+"-"+hash+".sock")
}
