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

// threadNamePoll is how often WaitForThreadName checks for Codex's title. thread/read doesn't subscribe the
// connection to the thread, so no name notification arrives to wait on.
const threadNamePoll = 250 * time.Millisecond

// SetThreadName names a thread through the running Codex app-server, which shows the name wherever it serves the
// thread (the TUI, `codex resume`, the app). `codex app-server proxy` pipes stdio to the app-server's control socket,
// which speaks JSON-RPC over WebSocket. With no app-server running (`codex --no-daemon`), the proxy fails and so does
// this.
func SetThreadName(ctx context.Context, threadID, name string) error {
	return withAppServer(ctx, titleTimeout, func(conn io.ReadWriteCloser) error {
		return setThreadName(conn, threadID, name)
	})
}

// WaitForThreadName returns the thread's saved name, checking for up to wait while Codex generates its title, or ""
// if it has none by then. Codex's clients (the TUI and the app) title only a thread with no name and drop their title
// if the name changed meanwhile, so a saved name means Codex is done with it.
func WaitForThreadName(ctx context.Context, threadID string, wait time.Duration) (string, error) {
	var name string
	err := withAppServer(ctx, wait+titleTimeout, func(conn io.ReadWriteCloser) (err error) {
		name, err = waitForThreadName(conn, threadID, wait)
		return err
	})
	return name, err
}

// withAppServer runs exchange over a connection to the Codex app-server, abandoning it after timeout.
func withAppServer(ctx context.Context, timeout time.Duration, exchange func(conn io.ReadWriteCloser) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
	go func() { done <- exchange(pipe{stdout, stdin}) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("talking to the Codex app-server: %w", ctx.Err())
	}
}

// setThreadName speaks the app-server protocol over conn: initialize, then thread/name/set.
func setThreadName(conn io.ReadWriteCloser, threadID, name string) error {
	return session(conn, func(ws *websocket.Conn) error {
		_, err := request(ws, rpcRequest{ID: 2, Method: "thread/name/set", Params: map[string]any{"threadId": threadID, "name": name}})
		return err
	})
}

// waitForThreadName speaks the app-server protocol over conn: initialize, then thread/read until the thread has a
// name or wait has passed.
func waitForThreadName(conn io.ReadWriteCloser, threadID string, wait time.Duration) (string, error) {
	var name string
	err := session(conn, func(ws *websocket.Conn) error {
		deadline := time.Now().Add(wait)
		for {
			result, err := request(ws, rpcRequest{ID: 2, Method: "thread/read", Params: map[string]any{"threadId": threadID, "includeTurns": false}})
			if err != nil {
				return err
			}
			var read struct {
				Thread struct {
					Name *string `json:"name"`
				} `json:"thread"`
			}
			if err := json.Unmarshal(result, &read); err != nil {
				return fmt.Errorf("Codex app-server thread/read: %w", err)
			}
			if read.Thread.Name != nil && *read.Thread.Name != "" {
				name = *read.Thread.Name
				return nil
			}
			if time.Now().After(deadline) {
				return nil
			}
			time.Sleep(threadNamePoll)
		}
	})
	return name, err
}

// session opens a WebSocket over conn, initializes the app-server connection, then runs requests.
func session(conn io.ReadWriteCloser, requests func(ws *websocket.Conn) error) error {
	config, err := websocket.NewConfig("ws://localhost/", "http://localhost/")
	if err != nil {
		return err
	}
	ws, err := websocket.NewClient(config, conn)
	if err != nil {
		return fmt.Errorf("connecting to the Codex app-server: %w", err)
	}
	defer ws.Close()

	if _, err := request(ws, rpcRequest{ID: 1, Method: "initialize", Params: map[string]any{
		"clientInfo": map[string]any{"name": "rostr", "title": "rostr", "version": "1"},
	}}); err != nil {
		return err
	}
	if err := websocket.JSON.Send(ws, rpcRequest{Method: "initialized"}); err != nil {
		return err
	}
	return requests(ws)
}

// request sends req and returns its reply's result, skipping notifications and the server's own requests.
func request(ws *websocket.Conn, req rpcRequest) (json.RawMessage, error) {
	if err := websocket.JSON.Send(ws, req); err != nil {
		return nil, err
	}
	for {
		var reply rpcReply
		if err := websocket.JSON.Receive(ws, &reply); err != nil {
			return nil, err
		}
		if string(reply.ID) != strconv.Itoa(req.ID) {
			continue
		}
		if reply.Error != nil {
			return nil, fmt.Errorf("Codex app-server %s: %s", req.Method, reply.Error.Message)
		}
		return reply.Result, nil
	}
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
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// pipe joins a process's stdout and stdin into one connection.
type pipe struct {
	io.Reader
	io.WriteCloser
}
