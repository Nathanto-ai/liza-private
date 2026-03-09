package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/mcp/protocol"
)

type runReadResult struct {
	req *protocol.JSONRPCRequest
	err error
}

type fakeRunTransport struct {
	readResults []runReadResult

	writeErrorErr error

	writeErrorCalls   int
	writeErrorCode    int
	writeErrorMessage string
}

func (t *fakeRunTransport) ReadRequest() (*protocol.JSONRPCRequest, error) {
	if len(t.readResults) == 0 {
		return nil, io.EOF
	}

	result := t.readResults[0]
	t.readResults = t.readResults[1:]
	return result.req, result.err
}

func (t *fakeRunTransport) WriteResponse(*protocol.JSONRPCResponse) error {
	return nil
}

func (t *fakeRunTransport) WriteError(_ json.RawMessage, code int, message string, _ any) error {
	t.writeErrorCalls++
	t.writeErrorCode = code
	t.writeErrorMessage = message
	return t.writeErrorErr
}

func TestServerRun_ParseErrorWriteSuccessContinues(t *testing.T) {
	server := NewServer("/tmp/test", "/tmp/test/.liza/log.yaml", "")
	transport := &fakeRunTransport{
		readResults: []runReadResult{
			{err: errors.New("failed to parse JSON-RPC request: invalid character")},
			{err: io.EOF},
		},
	}

	err := server.runWithTransport(transport)
	if err != nil {
		t.Fatalf("runWithTransport returned error: %v", err)
	}

	if transport.writeErrorCalls != 1 {
		t.Fatalf("WriteError call count = %d, want 1", transport.writeErrorCalls)
	}
	if transport.writeErrorCode != protocol.ParseError {
		t.Fatalf("WriteError code = %d, want %d", transport.writeErrorCode, protocol.ParseError)
	}
	if !strings.Contains(transport.writeErrorMessage, "failed to parse JSON-RPC request") {
		t.Fatalf("WriteError message = %q, want parse error details", transport.writeErrorMessage)
	}
}

func TestServerRun_ParseErrorWriteFailureIsTerminal(t *testing.T) {
	server := NewServer("/tmp/test", "/tmp/test/.liza/log.yaml", "")
	writeErr := errors.New("broken pipe")
	transport := &fakeRunTransport{
		readResults: []runReadResult{
			{err: errors.New("failed to parse JSON-RPC request: invalid character")},
		},
		writeErrorErr: writeErr,
	}

	err := server.runWithTransport(transport)
	if err == nil {
		t.Fatal("expected runWithTransport to return error")
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want wrapped write error %v", err, writeErr)
	}
	if !strings.Contains(err.Error(), "failed to write error response") {
		t.Fatalf("error = %q, want write-error context", err.Error())
	}
	if transport.writeErrorCalls != 1 {
		t.Fatalf("WriteError call count = %d, want 1", transport.writeErrorCalls)
	}
}
