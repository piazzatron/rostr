package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/net/websocket"
)

// titleTimeout bounds setting a thread's name, so a missing or stuck app-server never holds up a hook for long.
const titleTimeout = 2 * time.Second

// SetThreadName names a thread through the running Codex app-server, which shows the name wherever it serves the
// thread (the TUI, `codex resume`, the app). `codex app-server proxy` pipes stdio to the app-server's control socket,
// which speaks JSON-RPC over WebSocket. With no app-server running (`codex --no-daemon`), the proxy fails and so does
// this.
func SetThreadName(ctx context.Context, threadID, name string) error {
	ctx, cancel := context.WithTimeout(ctx, titleTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "app-server", "proxy")
	cmd.WaitDelay = 100 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// The proxy is only a relay, so it's killed once the exchange is over, whether or not it got a reply
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	// The exchange blocks on reads, so it runs aside and is abandoned when the context ends
	done := make(chan error, 1)
	go func() { done <- setThreadName(pipe{stdout, stdin}, threadID, name) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("setting the Codex thread name: %w", ctx.Err())
	}
}

// setThreadName speaks the app-server protocol over conn: initialize, then thread/name/set, waiting for each reply.
func setThreadName(conn io.ReadWriteCloser, threadID, name string) error {
	config, err := websocket.NewConfig("ws://localhost/", "http://localhost/")
	if err != nil {
		return err
	}
	ws, err := websocket.NewClient(config, conn)
	if err != nil {
		return fmt.Errorf("connecting to the Codex app-server: %w", err)
	}
	defer ws.Close()

	requests := []rpcRequest{
		{ID: 1, Method: "initialize", Params: map[string]any{"clientInfo": map[string]any{"name": "rostr", "title": "rostr", "version": "1"}}},
		{Method: "initialized"},
		{ID: 2, Method: "thread/name/set", Params: map[string]any{"threadId": threadID, "name": name}},
	}
	for _, request := range requests {
		if err := websocket.JSON.Send(ws, request); err != nil {
			return err
		}
		if request.ID == 0 {
			continue
		}

		// Skip notifications until this request's reply
		for {
			var reply rpcReply
			if err := websocket.JSON.Receive(ws, &reply); err != nil {
				return err
			}
			if string(reply.ID) != strconv.Itoa(request.ID) {
				continue
			}
			if reply.Error != nil {
				return fmt.Errorf("Codex app-server %s: %s", request.Method, reply.Error.Message)
			}
			break
		}
	}
	return nil
}

// rpcRequest is a JSON-RPC request to the Codex app-server, which leaves out the "jsonrpc" version field. An ID of 0
// makes it a notification.
type rpcRequest struct {
	ID     int    `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// rpcReply is any message from the app-server: replies carry their request's ID, and notifications have none.
type rpcReply struct {
	ID    json.RawMessage `json:"id"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// pipe joins a process's stdout and stdin into one connection.
type pipe struct {
	io.Reader
	io.WriteCloser
}
