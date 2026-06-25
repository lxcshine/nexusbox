package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"k8s.io/klog/v2"
)

// ServeStdio runs the MCP Hub over stdio transport.
//
// The stdio transport is the standard way for AI editors (Trae, Cursor,
// Claude Code, etc.) to launch and communicate with an MCP server: the
// editor spawns this process as a child and exchanges JSON-RPC 2.0
// messages line-by-line over stdin/stdout.
//
// All logs are written to stderr so they do not corrupt the stdout channel.
// The function blocks until stdin is closed or the context is cancelled.
func (h *Hub) ServeStdio(ctx context.Context) error {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	var writeMu sync.Mutex

	// Cancel any in-flight request when ctx is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		// Closing stdin unblocks the reader loop.
		_ = os.Stdin.Close()
	}()

	klog.Infof("MCP Hub stdio transport ready (protocol=2024-11-05)")

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				klog.Infof("MCP Hub stdio: stdin closed")
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("stdio read: %w", err)
		}

		// Skip empty/blank keepalive lines.
		if len(trimNewline(line)) == 0 {
			continue
		}

		var msg JSONRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			writeMu.Lock()
			writeMCPStdioError(writer, nil, -32700, "Parse error")
			writeMu.Unlock()
			continue
		}

		if msg.JSONRPC != "2.0" {
			writeMu.Lock()
			writeMCPStdioError(writer, msg.ID, -32600, "Invalid Request: jsonrpc must be 2.0")
			writeMu.Unlock()
			continue
		}

		// Notifications (no ID) are fire-and-forget; do not reply.
		if msg.ID == nil {
			klog.Infof("MCP Hub stdio: notification %q received", msg.Method)
			continue
		}

		result, rpcErr := h.dispatch(ctx, &msg)

		writeMu.Lock()
		if rpcErr != nil {
			writeMCPStdioError(writer, msg.ID, rpcErr.Code, rpcErr.Message)
		} else {
			resultBytes, _ := json.Marshal(result)
			resp := JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result:  resultBytes,
			}
			respBytes, _ := json.Marshal(resp)
			_, _ = writer.Write(respBytes)
			_, _ = writer.Write([]byte("\n"))
			_ = writer.Flush()
		}
		writeMu.Unlock()
	}
}

// writeMCPStdioError writes a JSON-RPC error response to the given writer.
func writeMCPStdioError(w io.Writer, id interface{}, code int, message string) {
	resp := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	respBytes, _ := json.Marshal(resp)
	_, _ = w.Write(respBytes)
	_, _ = w.Write([]byte("\n"))
	if bw, ok := w.(*bufio.Writer); ok {
		_ = bw.Flush()
	}
}

// trimNewline strips trailing CR and LF characters.
func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
