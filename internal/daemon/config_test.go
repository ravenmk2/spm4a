package daemon

import (
	"testing"
	"time"
)

func TestParseIdleExit(t *testing.T) {
	cases := []struct {
		in    string
		never bool
		delay time.Duration
		fail  bool
	}{
		{"", false, idleGrace, false},
		{"immediate", false, idleGrace, false},
		{"IMMEDIATE", false, idleGrace, false},
		{"never", true, 0, false},
		{"Never", true, 0, false},
		{"30s", false, 30 * time.Second, false},
		{"10m", false, 10 * time.Minute, false},
		{"bogus", false, 0, true},
		{"-5s", false, 0, true},
		{"0s", false, 0, true},
		{"10", false, 0, true},
	}
	for _, c := range cases {
		pol, err := parseIdleExit(c.in)
		if c.fail {
			if err == nil {
				t.Errorf("%q: want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if pol.never != c.never || pol.delay != c.delay {
			t.Errorf("%q -> %+v, want never=%v delay=%v", c.in, pol, c.never, c.delay)
		}
	}
}
