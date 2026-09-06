package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
)

type Client struct {
	hc  *http.Client
	seq atomic.Int64
}

func NewClient(ep Endpoint) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return ep.Dial(ctx)
		},
	}
	return &Client{hc: &http.Client{Transport: tr}}
}

func (c *Client) newRequest(ctx context.Context, method string, params any) (*http.Request, error) {
	id := c.seq.Add(1)
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	body, _ := json.Marshal(Request{
		JSONRPC: RPCVersion,
		ID:      json.RawMessage(strconv.FormatInt(id, 10)),
		Method:  method,
		Params:  raw,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://spm4a/v1/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	req, err := c.newRequest(ctx, method, params)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
	}
	var r Response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if r.Error != nil {
		return r.Error
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

type Notification struct {
	Method string
	Params json.RawMessage
}

type Subscription struct {
	ID     string
	Events chan Notification
	body   io.Closer
}

func (s *Subscription) Close() error { return s.body.Close() }

func (c *Client) Stream(ctx context.Context, method string, params any) (*Subscription, error) {
	req, err := c.newRequest(ctx, method, params)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		defer resp.Body.Close()
		var r Response
		if err := json.NewDecoder(resp.Body).Decode(&r); err == nil && r.Error != nil {
			return nil, r.Error
		}
		return nil, fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
	}
	br := bufio.NewReader(resp.Body)
	first, err := readSSEData(br)
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("read subscription response: %w", err)
	}
	var r Response
	if err := json.Unmarshal(first, &r); err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("decode subscription response: %w", err)
	}
	if r.Error != nil {
		resp.Body.Close()
		return nil, r.Error
	}
	var meta struct {
		SubscriptionID string `json:"subscriptionId"`
	}
	if err := json.Unmarshal(r.Result, &meta); err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("decode subscriptionId: %w", err)
	}
	sub := &Subscription{ID: meta.SubscriptionID, Events: make(chan Notification, 32), body: resp.Body}
	go func() {
		defer close(sub.Events)
		defer resp.Body.Close()
		for {
			data, err := readSSEData(br)
			if err != nil {
				return
			}
			var n struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(data, &n); err != nil {
				continue
			}
			if n.Method == "stream.end" {
				return
			}
			select {
			case sub.Events <- Notification{Method: n.Method, Params: n.Params}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return sub, nil
}

func readSSEData(r *bufio.Reader) ([]byte, error) {
	var data []byte
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if data != nil {
				return data, nil
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(rest, " ")...)
		}
	}
}
