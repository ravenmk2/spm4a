package ipc

import "encoding/json"

const RPCVersion = "2.0"

const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603

	CodeAppNotFound  = -32001
	CodeAppExists    = -32002
	CodeInvalidState = -32003
	CodePortConflict = -32004
	CodeReadyTimeout = -32006
)

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func NewError(code int, msg string) *Error { return &Error{Code: code, Message: msg} }

func ErrParams(msg string) *Error { return &Error{Code: CodeInvalidParams, Message: msg} }

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func DecodeParams(raw json.RawMessage, v any) *Error {
	if len(raw) == 0 {
		return ErrParams("missing params")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return &Error{Code: CodeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return nil
}
