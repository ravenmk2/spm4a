package ipc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

type Handler func(ctx context.Context, params json.RawMessage) (any, error)

type StreamEvent struct {
	Method string
	Params map[string]any
}

type Stream struct {
	ID     string
	Method string
	Events <-chan StreamEvent
	Cancel context.CancelFunc
}

type Server struct {
	mu         sync.Mutex
	handlers   map[string]Handler
	streams    map[string]*Stream
	middleware func(Handler) Handler
	http       *http.Server
}

func NewServer() *Server {
	s := &Server{handlers: map[string]Handler{}, streams: map[string]*Stream{}}
	s.http = &http.Server{Handler: s}
	return s
}

func (s *Server) Handle(method string, h Handler) { s.handlers[method] = h }

func (s *Server) SetMiddleware(mw func(Handler) Handler) { s.middleware = mw }

func (s *Server) Serve(l net.Listener) error { return s.http.Serve(l) }

func (s *Server) Shutdown(ctx context.Context) error {
	s.CloseStreams()
	return s.http.Shutdown(ctx)
}

func (s *Server) CloseStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.streams {
		if st.Cancel != nil {
			st.Cancel()
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/rpc" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeRPCError(w, nil, CodeParseError, "read body: "+err.Error())
		return
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		writeRPCError(w, nil, CodeParseError, "empty body")
		return
	}
	if trimmed[0] == '[' {
		var reqs []Request
		if err := json.Unmarshal(trimmed, &reqs); err != nil {
			writeRPCError(w, nil, CodeParseError, err.Error())
			return
		}
		if len(reqs) == 0 {
			writeRPCError(w, nil, CodeInvalidRequest, "empty batch")
			return
		}
		var resps []*Response
		for i := range reqs {
			if resp := s.dispatch(r.Context(), &reqs[i], true); resp != nil {
				resps = append(resps, resp)
			}
		}
		if len(resps) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, resps)
		return
	}
	var req Request
	if err := json.Unmarshal(trimmed, &req); err != nil {
		writeRPCError(w, nil, CodeParseError, err.Error())
		return
	}
	if req.JSONRPC != RPCVersion || req.Method == "" {
		writeRPCError(w, req.ID, CodeInvalidRequest, "not a JSON-RPC 2.0 request")
		return
	}
	res, herr := s.call(r.Context(), &req)
	if st, ok := res.(*Stream); ok && herr == nil {
		s.serveSSE(w, r, req.ID, st)
		return
	}
	if req.ID == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, responseFor(req.ID, res, herr))
}

func (s *Server) dispatch(ctx context.Context, req *Request, batch bool) *Response {
	if req.JSONRPC != RPCVersion || req.Method == "" {
		if req.ID == nil {
			return nil
		}
		return responseFor(req.ID, nil, &Error{Code: CodeInvalidRequest, Message: "not a JSON-RPC 2.0 request"})
	}
	res, err := s.call(ctx, req)
	if _, ok := res.(*Stream); ok && err == nil {
		res, err = nil, &Error{Code: CodeInvalidRequest, Message: "streaming method not allowed in batch"}
	}
	if req.ID == nil {
		return nil
	}
	return responseFor(req.ID, res, err)
}

func (s *Server) call(ctx context.Context, req *Request) (res any, herr *Error) {
	h, ok := s.handlers[req.Method]
	if !ok {
		return nil, &Error{Code: CodeMethodNotFound, Message: "method not found: " + req.Method}
	}
	if s.middleware != nil {
		h = s.middleware(h)
	}
	defer func() {
		if r := recover(); r != nil {
			res, herr = nil, &Error{Code: CodeInternal, Message: fmt.Sprintf("panic: %v", r)}
		}
	}()
	res, err := h(ctx, req.Params)
	if err == nil {
		return res, nil
	}
	if re, ok := err.(*Error); ok {
		return nil, re
	}
	return nil, &Error{Code: CodeInternal, Message: err.Error()}
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, id json.RawMessage, st *Stream) {
	if st.ID == "" {
		st.ID = randID()
	}
	s.mu.Lock()
	s.streams[st.ID] = st
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.streams, st.ID)
		s.mu.Unlock()
		if st.Cancel != nil {
			st.Cancel()
		}
	}()

	fl, ok := w.(http.Flusher)
	if !ok {
		writeRPCError(w, id, CodeInternal, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	result, _ := json.Marshal(map[string]any{"subscriptionId": st.ID})
	writeSSE(w, fl, &Response{JSONRPC: RPCVersion, ID: id, Result: result})
	for {
		select {
		case ev, ok := <-st.Events:
			if !ok {
				writeSSE(w, fl, notificationWire{JSONRPC: RPCVersion, Method: "stream.end",
					Params: map[string]any{"subscriptionId": st.ID}})
				return
			}
			m := ev.Method
			if m == "" {
				m = st.Method
			}
			params := map[string]any{"subscriptionId": st.ID}
			for k, v := range ev.Params {
				params[k] = v
			}
			writeSSE(w, fl, notificationWire{JSONRPC: RPCVersion, Method: m, Params: params})
		case <-r.Context().Done():
			return
		}
	}
}

type notificationWire struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func writeSSE(w io.Writer, fl http.Flusher, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	fl.Flush()
}

func responseFor(id json.RawMessage, res any, herr *Error) *Response {
	resp := &Response{JSONRPC: RPCVersion, ID: id, Error: herr}
	if herr == nil {
		resp.Result, _ = json.Marshal(res)
	}
	return resp
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeJSON(w, &Response{JSONRPC: RPCVersion, ID: id, Error: &Error{Code: code, Message: msg}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func randID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
