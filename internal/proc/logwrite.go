package proc

import (
	"os"
	"path/filepath"
	"sync"
)

// LogWriter opens the log file lazily once the child PID is known, so
// log-file expressions containing ${pid} can be honored. Writes block until
// SetPID is called.
type LogWriter struct {
	pathFor func(pid int) (string, error)
	pidCh   chan int
	once    sync.Once
	file    *os.File
	err     error
}

func NewLogWriter(pathFor func(pid int) (string, error)) *LogWriter {
	return &LogWriter{pathFor: pathFor, pidCh: make(chan int, 1)}
}

func (w *LogWriter) SetPID(pid int) { w.pidCh <- pid }

func (w *LogWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		pid := <-w.pidCh
		path, err := w.pathFor(pid)
		if err != nil {
			w.err = err
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			w.err = err
			return
		}
		w.file, w.err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	})
	if w.err != nil {
		return 0, w.err
	}
	return w.file.Write(p)
}

func (w *LogWriter) Close() error {
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
