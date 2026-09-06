package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var probeClient = &http.Client{Timeout: 2 * time.Second}

// Get fetches http://127.0.0.1:<port><path> and returns the raw body.
func Get(ctx context.Context, port int, path string) ([]byte, int, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// IsUp reports whether the health endpoint answers 200 with status "UP".
func IsUp(ctx context.Context, port int, path string) bool {
	body, code, err := Get(ctx, port, path)
	if err != nil || code != http.StatusOK {
		return false
	}
	var v struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return false
	}
	return v.Status == "UP"
}

// Shutdown posts to the actuator shutdown endpoint. The path is the Spring
// Boot default; a custom management base path is not configurable in M2.
func Shutdown(ctx context.Context, port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/actuator/shutdown", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("actuator shutdown returned HTTP %d", resp.StatusCode)
	}
	return nil
}
