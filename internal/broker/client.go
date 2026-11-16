package broker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

// Every error the client returns is a broker error: the caller must treat it
// as a failed broker call and must not retry it. The sentinels below say how
// far the call got.
var (
	// ErrUnavailable means the request never reached the helper: nothing was
	// listening on the socket, or the request could not be written in full.
	ErrUnavailable = errors.New("broker helper unavailable")
	// ErrNoReply means the request was sent but no reply came back: the helper
	// stayed silent past the deadline or closed the connection. The call's
	// outcome is unknown.
	ErrNoReply = errors.New("broker helper did not reply")
	// ErrBadReply means the helper answered with something that is not a valid
	// reply to this request. The call's outcome is unknown.
	ErrBadReply = errors.New("broker helper sent a bad reply")
)

// Error codes the helper can put in a reply. Later operations add their own.
const (
	CodeBadRequest = "bad_request"
	CodeUnknownOp  = "unknown_op"
	CodeInternal   = "internal"
)

// HelperError is an error the helper itself reported in its reply.
type HelperError struct {
	Op      string // the operation that failed
	Code    string // one of the Code constants, or an operation's own code
	Message string // for people
}

func (e *HelperError) Error() string {
	return fmt.Sprintf("broker helper: %s: %s: %s", e.Op, e.Code, e.Message)
}

// DefaultCallTimeout bounds a call when the caller's context has no earlier
// deadline.
const DefaultCallTimeout = 10 * time.Second

// maxReplyBytes is the largest reply the client reads.
const maxReplyBytes = 1 << 20

// Client sends calls to the broker helper over its Unix socket, one request and
// one reply per connection. It is safe for concurrent use; the helper answers
// calls one at a time.
type Client struct {
	socketPath string
	timeout    time.Duration
	nextID     atomic.Uint64
}

// NewClient returns a client for the helper listening on socketPath. Each call
// gets at most timeout to complete, or less if its context ends sooner; a
// timeout of zero or less means DefaultCallTimeout.
func NewClient(socketPath string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	return &Client{socketPath: socketPath, timeout: timeout}
}

type request struct {
	ID   string `json:"id"`
	Op   string `json:"op"`
	Args any    `json:"args"`
}

type reply struct {
	ID     *string         `json:"id"`
	OK     *bool           `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Call sends one request and waits for its reply. args is encoded as the
// request's args object (nil sends an empty object), and on success the reply's
// result is decoded into result unless result is nil. Call never retries.
func (c *Client) Call(ctx context.Context, op string, args, result any) error {
	if args == nil {
		args = struct{}{}
	}
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	line, err := json.Marshal(request{ID: id, Op: op, Args: args})
	if err != nil {
		return fmt.Errorf("broker helper: %s: encode request: %w", op, err)
	}
	line = append(line, '\n')

	// The call ends at the client's timeout or the caller's deadline,
	// whichever is sooner. The cause says which, for the error message.
	deadline := time.Now().Add(c.timeout)
	var cancel context.CancelFunc
	if d, ok := ctx.Deadline(); ok && !deadline.Before(d) {
		deadline = d
		ctx, cancel = context.WithCancel(ctx)
	} else {
		ctx, cancel = context.WithDeadlineCause(ctx, deadline, fmt.Errorf("no reply within %s", c.timeout))
	}
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, op, err)
	}
	defer func() { _ = conn.Close() }()

	// The deadline covers the write and the read. Ending the context early
	// (for example on shutdown) ends the call too.
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, op, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	if _, err := conn.Write(line); err != nil {
		// The closing newline was not delivered, so the helper never ran it.
		return fmt.Errorf("%w: %s: send request: %w", ErrUnavailable, op, err)
	}

	raw, err := bufio.NewReader(io.LimitReader(conn, maxReplyBytes+1)).ReadBytes('\n')
	if err != nil {
		switch {
		case errors.Is(err, io.EOF) && len(raw) > maxReplyBytes:
			return fmt.Errorf("%w: %s: reply is larger than %d bytes", ErrBadReply, op, maxReplyBytes)
		case errors.Is(err, io.EOF):
			return fmt.Errorf("%w: %s: connection closed before a reply", ErrNoReply, op)
		case errors.Is(err, os.ErrDeadlineExceeded) || ctx.Err() != nil:
			// The socket deadline is the context's deadline, so the context
			// is done or about to be; wait for it to learn the cause.
			<-ctx.Done()
			return fmt.Errorf("%w: %s: %w", ErrNoReply, op, context.Cause(ctx))
		default:
			return fmt.Errorf("%w: %s: %w", ErrNoReply, op, err)
		}
	}
	return decodeReply(op, id, bytes.TrimSuffix(raw, []byte("\n")), result)
}

func decodeReply(op, id string, raw []byte, result any) error {
	var r reply
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrBadReply, op, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: %s: reply has more than one JSON value", ErrBadReply, op)
	}
	if r.OK == nil {
		return fmt.Errorf("%w: %s: reply has no ok field", ErrBadReply, op)
	}
	if !*r.OK {
		if r.Error == nil || r.Error.Code == "" {
			return fmt.Errorf("%w: %s: error reply has no error code", ErrBadReply, op)
		}
		// A request too broken to read comes back with a null id. Any other
		// mismatch is a reply to some other call.
		if r.ID != nil && *r.ID != id {
			return fmt.Errorf("%w: %s: reply id %q does not match request id %q", ErrBadReply, op, *r.ID, id)
		}
		return &HelperError{Op: op, Code: r.Error.Code, Message: r.Error.Message}
	}
	if r.ID == nil || *r.ID != id {
		return fmt.Errorf("%w: %s: reply id does not match request id %q", ErrBadReply, op, id)
	}
	if result != nil {
		if len(r.Result) == 0 {
			return fmt.Errorf("%w: %s: reply has no result", ErrBadReply, op)
		}
		if err := json.Unmarshal(r.Result, result); err != nil {
			return fmt.Errorf("%w: %s: decode result: %w", ErrBadReply, op, err)
		}
	}
	return nil
}

// PingResult is the helper's answer to a ping.
type PingResult struct {
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

// Ping asks the helper whether it is alive.
func (c *Client) Ping(ctx context.Context) (PingResult, error) {
	var res PingResult
	err := c.Call(ctx, "ping", nil, &res)
	return res, err
}
