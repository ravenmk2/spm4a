package proc

import (
	"bytes"
	"io"
	"os"
	"strings"
)

// TailLines returns the last n lines of path, reading at most 1 MiB back.
func TailLines(path string, n int) ([]string, error) {
	if n <= 0 {
		n = 200
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := int64(0)
	if st.Size() > 1<<20 {
		start = st.Size() - 1<<20
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		return []string{}, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
