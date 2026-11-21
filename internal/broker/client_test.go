package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// replyWith returns a fake helper that reads one request, checks it, and writes
// the reply build returns for it.
func replyWith(t *testing.T, build func(req map[string]any) string) func(net.Conn) {
	return func(conn net.Conn) {
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			t.Errorf("fake helper: read request: %v", err)
			return
		}
		var req map[string]any
		if err := json.Unmarshal(line, &req); err != nil {
			t.Errorf("fake helper: request is not JSON: %v", err)
			return
		}
		_, _ = conn.Write([]byte(build(req)))
	}
}

func TestCallSendsOneRequestAndDecodesTheResult(t *testing.T) {
	var got map[string]any
	path := fakeHelper(t, replyWith(t, func(req map[string]any) string {
		got = req
		return `{"id":"` + req["id"].(string) + `","ok":true,"result":{"version":"9.9","pid":42}}` + "\n"
	}))
	c := NewClient(path, time.Second)

	res, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if res != (PingResult{Version: "9.9", PID: 42}) {
		t.Errorf("Ping result = %+v", res)
	}
	if got["op"] != "ping" || got["id"] == "" {
		t.Errorf("request = %v, want op ping with an id", got)
	}
	if args, ok := got["args"].(map[string]any); !ok || len(args) != 0 {
		t.Errorf("request args = %#v, want an empty object", got["args"])
	}
	if len(got) != 3 {
		t.Errorf("request has fields %v, want only id, op and args", got)
	}
}

func TestEachCallGetsItsOwnID(t *testing.T) {
	ids := make(chan string, 2)
	path := fakeHelper(t, replyWith(t, func(req map[string]any) string {
		id := req["id"].(string)
		ids <- id
		return `{"id":"` + id + `","ok":true,"result":{}}` + "\n"
	}))
	c := NewClient(path, time.Second)
	for range 2 {
		if err := c.Call(context.Background(), "ping", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if a, b := <-ids, <-ids; a == b {
		t.Errorf("two calls shared id %q", a)
	}
}

func TestMoneyArgsTravelAsStrings(t *testing.T) {
	var got map[string]any
	path := fakeHelper(t, replyWith(t, func(req map[string]any) string {
		got = req
		return `{"id":"` + req["id"].(string) + `","ok":true,"result":{}}` + "\n"
	}))
	args := struct {
		Price string `json:"price"`
	}{Price: "101.25"}
	if err := NewClient(path, time.Second).Call(context.Background(), "quote", args, nil); err != nil {
		t.Fatal(err)
	}
	if price := got["args"].(map[string]any)["price"]; price != "101.25" {
		t.Errorf("price arrived as %#v", price)
	}
}

func TestHelperErrorsAreTyped(t *testing.T) {
	for name, reply := range map[string]string{
		"with id": `{"id":"1","ok":false,"error":{"code":"unknown_op","message":"unknown op: buy"}}`,
		"null id": `{"id":null,"ok":false,"error":{"code":"unknown_op","message":"unknown op: buy"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := fakeHelper(t, replyWith(t, func(map[string]any) string { return reply + "\n" }))
			err := NewClient(path, time.Second).Call(context.Background(), "buy", nil, nil)
			var he *HelperError
			if !errors.As(err, &he) {
				t.Fatalf("err = %v, want a HelperError", err)
			}
			if he.Op != "buy" || he.Code != CodeUnknownOp || he.Message != "unknown op: buy" {
				t.Errorf("HelperError = %+v", he)
			}
		})
	}
}

func TestBadRepliesAreErrors(t *testing.T) {
	cases := map[string]string{
		"not json":           `nope`,
		"wrong id":           `{"id":"999","ok":true,"result":{}}`,
		"missing id":         `{"ok":true,"result":{}}`,
		"missing ok":         `{"id":"1","result":{}}`,
		"unknown field":      `{"id":"1","ok":true,"result":{},"extra":1}`,
		"missing result":     `{"id":"1","ok":true}`,
		"result wrong shape": `{"id":"1","ok":true,"result":{"pid":"forty-two"}}`,
		"error without code": `{"id":"1","ok":false,"error":{"message":"x"}}`,
		"error wrong id":     `{"id":"999","ok":false,"error":{"code":"internal","message":"x"}}`,
		"two values":         `{"id":"1","ok":true,"result":{}} {}`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			path := fakeHelper(t, replyWith(t, func(map[string]any) string { return reply + "\n" }))
			_, err := NewClient(path, time.Second).Ping(context.Background())
			if !errors.Is(err, ErrBadReply) {
				t.Fatalf("err = %v, want ErrBadReply", err)
			}
		})
	}
}

func TestOversizedReplyIsAnError(t *testing.T) {
	path := fakeHelper(t, replyWith(t, func(map[string]any) string {
		return `{"id":"1","ok":true,"result":{"pad":"` + strings.Repeat("x", maxReplyBytes) + `"}}` + "\n"
	}))
	_, err := NewClient(path, time.Second).Ping(context.Background())
	if !errors.Is(err, ErrBadReply) {
		t.Fatalf("err = %v, want ErrBadReply", err)
	}
}

func TestNothingListeningIsUnavailable(t *testing.T) {
	_, err := NewClient(shortSocketPath(t), time.Second).Ping(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestClosedSocketIsNoReply(t *testing.T) {
	cases := map[string]func(net.Conn){
		"closed at once": func(net.Conn) {},
		"closed after reading": func(conn net.Conn) {
			_, _ = bufio.NewReader(conn).ReadBytes('\n')
		},
		"closed mid-reply": func(conn net.Conn) {
			_, _ = bufio.NewReader(conn).ReadBytes('\n')
			_, _ = conn.Write([]byte(`{"id":"1","ok":tr`))
		},
	}
	for name, serve := range cases {
		t.Run(name, func(t *testing.T) {
			path := fakeHelper(t, serve)
			_, err := NewClient(path, time.Second).Ping(context.Background())
			if !errors.Is(err, ErrNoReply) && !errors.Is(err, ErrUnavailable) {
				t.Fatalf("err = %v, want ErrNoReply", err)
			}
			if name != "closed at once" && !errors.Is(err, ErrNoReply) {
				t.Fatalf("err = %v, want ErrNoReply", err)
			}
		})
	}
}

// silentHelper reads the request and then says nothing until the test ends.
func silentHelper(t *testing.T) string {
	done := make(chan struct{})
	path := fakeHelper(t, func(conn net.Conn) {
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		<-done
	})
	t.Cleanup(func() { close(done) })
	return path
}

func TestSilenceIsNoReply(t *testing.T) {
	path := silentHelper(t)
	start := time.Now()
	_, err := NewClient(path, 50*time.Millisecond).Ping(context.Background())
	if !errors.Is(err, ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
	if elapsed := time.Since(start); elapsed > within/2 {
		t.Errorf("silent call took %s; the call timeout did not hold", elapsed)
	}
}

func TestEarlierContextDeadlineWins(t *testing.T) {
	path := silentHelper(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := NewClient(path, time.Hour).Ping(ctx)
	if !errors.Is(err, ErrNoReply) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want ErrNoReply from the context deadline", err)
	}
}

func TestCancelEndsTheCall(t *testing.T) {
	path := silentHelper(t)
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := NewClient(path, time.Hour).Ping(ctx)
		errs <- err
	}()
	cancel()
	err := receive(t, errs, "the canceled call to return")
	if !errors.Is(err, ErrNoReply) && !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want a broker error", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
}

func TestCallForSetsItsOwnTimeLimit(t *testing.T) {
	path := silentHelper(t)
	start := time.Now()
	err := NewClient(path, time.Hour).CallFor(context.Background(), "ping", 50*time.Millisecond, nil, nil)
	if !errors.Is(err, ErrNoReply) || !strings.Contains(err.Error(), "no reply within 50ms") {
		t.Fatalf("err = %v, want ErrNoReply after 50ms", err)
	}
	if elapsed := time.Since(start); elapsed > within/2 {
		t.Errorf("silent call took %s; CallFor's time limit did not hold", elapsed)
	}
}

func TestCallForLongerThanTheClientsLimit(t *testing.T) {
	path := fakeHelper(t, replyWith(t, func(req map[string]any) string {
		return `{"id":"` + req["id"].(string) + `","ok":true,"result":{}}` + "\n"
	}))
	// A client limit of zero or less falls back to the default; CallFor's
	// own limit is used as given.
	if err := NewClient(path, time.Second).CallFor(context.Background(), "ping", time.Minute, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := NewClient(path, time.Second).CallFor(context.Background(), "ping", 0, nil, nil); err != nil {
		t.Fatal(err)
	}
}
