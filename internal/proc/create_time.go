package proc

import (
	"github.com/shirou/gopsutil/v4/process"
)

// CreateTimeMillis returns the process start time in unix milliseconds,
// recorded at spawn so a daemon restart can detect PID reuse.
func CreateTimeMillis(pid int) (int64, error) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, err
	}
	return p.CreateTime()
}

// MatchesCreateTime reports whether the process currently occupying pid has
// exactly the recorded start time. A mismatch (or a dead pid) means the
// original process is gone and the pid may have been recycled.
func MatchesCreateTime(pid int, ms int64) bool {
	ct, err := CreateTimeMillis(pid)
	return err == nil && ct == ms
}
