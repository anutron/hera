package argus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"time"
)

// socketCallTimeout bounds a single Daemon.* JSON-RPC call. The unix
// socket is local to this machine; a healthy call returns in microseconds.
// Anything slower is wedged, and the watcher's per-tick budget is 1 second
// — keeping the call ceiling at 1 second lets a wedged daemon fail one
// tick and surface as a ping error without stretching the polling cadence.
const socketCallTimeout = 1 * time.Second

// rpcDispatchByte is argus's first-byte dispatch convention on
// ~/.argus/daemon.sock: 'R' for an RPC connection, 'S' for a stream
// connection. The byte MUST be written before any JSON-RPC framing or
// the daemon's accept loop drops the connection.
const rpcDispatchByte = 'R'

// PortsClient talks to argus's daemon via the local unix socket at
// `socketPath` (typically ~/.argus/daemon.sock). It is a thin wrapper
// around stdlib net/rpc/jsonrpc that opens a fresh connection per call
// and tears it down before returning — there is no long-lived rpc.Client
// or connection pool. The socket is local and the per-call overhead is
// negligible compared with the operational simplicity of stateless calls.
type PortsClient struct {
	socketPath string
}

// NewPortsClient constructs a PortsClient targeting socketPath. The path
// is not dialed until a method is called.
func NewPortsClient(socketPath string) *PortsClient {
	return &PortsClient{socketPath: socketPath}
}

// portsResp mirrors argus's daemon.PortsResp wire shape. The argus type
// carries no JSON tags, so the JSON-RPC codec serialises Go field names
// verbatim. Field names here MUST match argus exactly (`MCPPort`, `APIPort`).
type portsResp struct {
	MCPPort int
	APIPort int
}

// pongResp mirrors argus's daemon.PongResp wire shape.
type pongResp struct {
	OK bool
}

// emptyArgs mirrors argus's daemon.Empty placeholder for no-arg RPCs.
type emptyArgs struct{}

// Ports calls Daemon.Ports over the socket and returns (apiPort, mcpPort).
// A zero return value for either port means that argus server is disabled
// (e.g., MCP off → mcpPort=0); the caller decides whether that's an error.
func (c *PortsClient) Ports(ctx context.Context) (int, int, error) {
	var resp portsResp
	if err := c.call(ctx, "Daemon.Ports", &emptyArgs{}, &resp); err != nil {
		return 0, 0, fmt.Errorf("argus socket Ports: %w", err)
	}
	return resp.APIPort, resp.MCPPort, nil
}

// Ping calls Daemon.Ping over the socket. A nil return value means the
// daemon answered the call within `socketCallTimeout`. Any error — dial
// failure, write failure, server-side error, or timeout — surfaces back
// to the caller; the watcher treats any non-nil error as a restart signal.
func (c *PortsClient) Ping(ctx context.Context) error {
	var resp pongResp
	if err := c.call(ctx, "Daemon.Ping", &emptyArgs{}, &resp); err != nil {
		return fmt.Errorf("argus socket Ping: %w", err)
	}
	if !resp.OK {
		return errors.New("argus socket Ping: daemon returned OK=false")
	}
	return nil
}

// call dials the socket, writes the RPC dispatch byte, runs one JSON-RPC
// Call against the supplied method, and closes the connection. Both the
// dial and the call are bounded by min(ctx deadline, socketCallTimeout)
// so that a wedged daemon cannot wedge the watcher or the recovery loop.
func (c *PortsClient) call(ctx context.Context, method string, args, reply any) error {
	deadline := time.Now().Add(socketCallTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	var dialer net.Dialer
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	conn, err := dialer.DialContext(dialCtx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write([]byte{rpcDispatchByte}); err != nil {
		return fmt.Errorf("write dispatch byte: %w", err)
	}

	client := rpc.NewClientWithCodec(jsonrpc.NewClientCodec(conn))
	defer client.Close()

	// rpc.Client.Call blocks; the conn deadline above is what bounds it.
	if err := client.Call(method, args, reply); err != nil {
		return err
	}
	return nil
}
