package proc

import (
	"os"
	"testing"
)

func TestMatchesCreateTime(t *testing.T) {
	self := os.Getpid()
	ct, err := CreateTimeMillis(self)
	if err != nil {
		t.Fatalf("CreateTimeMillis(self): %v", err)
	}
	if !MatchesCreateTime(self, ct) {
		t.Error("same pid + same create time must match")
	}
	if MatchesCreateTime(self, ct+1000) {
		t.Error("same pid + different create time must not match (PID reuse guard)")
	}
	if MatchesCreateTime(-1, ct) {
		t.Error("invalid pid must not match")
	}
}
