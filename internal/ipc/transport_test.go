package ipc

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityDeterministic(t *testing.T) {
	ep := Endpoint{Home: "/some/home/dir"}
	u1, h1 := ep.identity()
	u2, h2 := ep.identity()
	if u1 != u2 || h1 != h2 {
		t.Errorf("identity not deterministic: (%q,%q) vs (%q,%q)", u1, h1, u2, h2)
	}
	if len(h1) != 8 {
		t.Errorf("hash8 = %q, want 8 hex chars", h1)
	}
	if u1 == "" || strings.ContainsAny(u1, `/\`) {
		t.Errorf("username = %q, want non-empty without path separators", u1)
	}
}

func TestIdentityDistinctHomes(t *testing.T) {
	_, h1 := Endpoint{Home: "/tmp/spm4a-home-a"}.identity()
	_, h2 := Endpoint{Home: "/tmp/spm4a-home-b"}.identity()
	if h1 == h2 {
		t.Errorf("different homes produced same hash %q", h1)
	}
}

func TestSockAddrShape(t *testing.T) {
	p := sockAddr("/tmp", "someone", "0123abcd")
	if want := filepath.Join("/tmp", "spm4a-someone-0123abcd.sock"); p != want {
		t.Errorf("sockAddr = %q, want %q", p, want)
	}
	if len(p) >= 100 {
		t.Errorf("sockAddr %q is %d bytes, want < 100", p, len(p))
	}
}

const longCIHome = "/var/folders/d8/hvxvltxn0fl4rmnd52sncbth0000gn/T/TestGradleLauncherLifecycle919047534/001"

func slash(p string) string { return filepath.ToSlash(p) }

func TestUnixSockPathXDG(t *testing.T) {
	home := "/home/u/.spm4a"
	p := unixSockPath(home, "someone", "/run/user/1000")
	want := filepath.Join("/run/user/1000", "spm4a", "spm4a-"+hash8(home)+".sock")
	if p != want {
		t.Errorf("XDG tier -> %q, want %q", p, want)
	}
	if !strings.Contains(p, hash8(home)) {
		t.Errorf("XDG tier %q must carry the home hash %q", p, hash8(home))
	}
}

func TestUnixSockPathShortHome(t *testing.T) {
	home := "/home/u/.spm4a"
	p := unixSockPath(home, "someone", "")
	want := filepath.Join(home, "run", "spm4a.sock")
	if p != want {
		t.Errorf("short home -> %q, want default %q", p, want)
	}
}

func TestUnixSockPathLongHomeFallback(t *testing.T) {
	p := unixSockPath(longCIHome, "someone", "")
	if !strings.HasPrefix(slash(p), "/tmp/spm4a-") {
		t.Errorf("long home -> %q, want /tmp fallback", p)
	}
	if len(p) >= 100 {
		t.Errorf("fallback %q is %d bytes, want < 100", p, len(p))
	}
	want := sockAddr("/tmp", "someone", hash8(longCIHome))
	if p != want {
		t.Errorf("fallback = %q, want %q", p, want)
	}
}

// XDG wins over the home rule even when the home path is over the limit.
func TestUnixSockPathPriority(t *testing.T) {
	p := unixSockPath(longCIHome, "someone", "/run/user/1000")
	if !strings.HasPrefix(slash(p), "/run/user/1000/spm4a/") {
		t.Errorf("XDG set + long home -> %q, want XDG tier", p)
	}
}

func TestUnixSockPathDeterministic(t *testing.T) {
	for _, xdg := range []string{"", "/run/user/1000"} {
		if unixSockPath(longCIHome, "someone", xdg) != unixSockPath(longCIHome, "someone", xdg) {
			t.Errorf("xdg=%q: same inputs must give same path", xdg)
		}
	}
	// distinct homes are distinguishable on every tier
	a := unixSockPath(longCIHome, "someone", "")
	b := unixSockPath(longCIHome+"x", "someone", "")
	if a == b {
		t.Error("/tmp tier: different homes must yield different sockets")
	}
	if unixSockPath("/home/u/a", "someone", "/run/user/1000") == unixSockPath("/home/u/b", "someone", "/run/user/1000") {
		t.Error("XDG tier: different homes must yield different sockets")
	}
}

func TestUnixSockPathThreshold(t *testing.T) {
	suffix := filepath.Join("/", "run", "spm4a.sock") // "/run/spm4a.sock", 15 bytes
	for _, total := range []int{99, 100, 101, 102} {
		home := "/" + strings.Repeat("a", total-len(suffix)-1)
		p := unixSockPath(home, "someone", "")
		isFallback := strings.HasPrefix(slash(p), "/tmp/spm4a-")
		wantFallback := total > 100
		if isFallback != wantFallback {
			t.Errorf("total=%d -> %q, fallback=%v, want fallback=%v", total, p, isFallback, wantFallback)
		}
		if !isFallback && len(p) != total {
			t.Errorf("total=%d -> default path len %d", total, len(p))
		}
	}
}
