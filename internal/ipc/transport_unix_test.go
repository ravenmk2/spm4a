//go:build !windows

package ipc

import (
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end through Endpoint (unix CI): XDG tier, run-dir default, /tmp
// fallback, Listen/Dial symmetry.
func TestSockPathThreeTier(t *testing.T) {
	short := Endpoint{Home: "/tmp/spm4a-home-short"}

	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	p := short.sockPath()
	if !strings.HasPrefix(p, "/run/user/1000/spm4a/spm4a-") || !strings.HasSuffix(p, ".sock") {
		t.Errorf("XDG tier -> %q, want /run/user/1000/spm4a/spm4a-<hash8>.sock", p)
	}
	if !strings.Contains(p, hash8(short.absHome())) {
		t.Errorf("XDG tier %q must carry the home hash", p)
	}

	t.Setenv("XDG_RUNTIME_DIR", "")
	if p := short.sockPath(); p != filepath.Join(short.Home, "run", "spm4a.sock") {
		t.Errorf("short home -> %q, want run dir", p)
	}

	long := Endpoint{Home: longCIHome}
	p = long.sockPath()
	if !strings.HasPrefix(p, "/tmp/spm4a-") {
		t.Errorf("long home without XDG -> %q, want /tmp fallback", p)
	}
	if len(p) >= 100 {
		t.Errorf("fallback %q is %d bytes, want < 100 (sun_path limit 104)", p, len(p))
	}
	if p != long.sockPath() {
		t.Error("sockPath not deterministic")
	}
	if (Endpoint{Home: longCIHome + "x"}).sockPath() == p {
		t.Error("different homes must yield different fallback sockets")
	}
}
