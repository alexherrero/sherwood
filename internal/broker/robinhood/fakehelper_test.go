package robinhood

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// fakeHelper is a stand-in for the broker helper on a real Unix socket. It
// speaks the helper's wire contract (live-task-1's note and ops.py) from an
// in-memory book, so the adapter's own tests need neither Python nor a
// broker. The contract test checks the same calls against the real helper.
// Every id, symbol, price and amount is made up.
type fakeHelper struct {
	path string

	mu       sync.Mutex
	orders   map[string]*fakeOrder // by client reference
	foreign  []map[string]any      // open orders sherwood never sent
	nextID   int
	refused  map[string]string // symbol → why the broker will not take it
	holdings []map[string]string
	cash     string
	value    string
	fail     map[string][]string // op → error codes for its next calls
	results  map[string][]string // op → raw results for its next calls
	silent   map[string]bool     // op → read the request and never answer
	requests []map[string]any
	done     chan struct{}
}

type fakeOrder struct {
	key, clientRef, brokerID string
	status, filled           string
	fillPrice                *string
}

func newFakeHelper(t *testing.T) *fakeHelper {
	t.Helper()
	dir, err := os.MkdirTemp("", "rh") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHelper{
		path:    filepath.Join(dir, "h.sock"),
		orders:  map[string]*fakeOrder{},
		refused: map[string]string{},
		cash:    "0", value: "0",
		holdings: []map[string]string{},
		fail:     map[string][]string{},
		results:  map[string][]string{},
		silent:   map[string]bool{},
		done:     make(chan struct{}),
	}
	ln, err := net.Listen("unix", f.path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		close(f.done)
		_ = ln.Close()
		wg.Wait()
		_ = os.RemoveAll(dir)
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close() }()
				f.serve(t, conn)
			}()
		}
	}()
	return f
}

func (f *fakeHelper) serve(t *testing.T, conn net.Conn) {
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		ID   string         `json:"id"`
		Op   string         `json:"op"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		t.Errorf("fake helper: request is not JSON: %v", err)
		return
	}
	f.mu.Lock()
	var whole map[string]any
	_ = json.Unmarshal(line, &whole)
	f.requests = append(f.requests, whole)
	silent := f.silent[req.Op]
	f.mu.Unlock()
	if silent {
		<-f.done
		return
	}
	result, code := f.answer(req.Op, req.Args)
	var reply []byte
	if code != "" {
		reply, _ = json.Marshal(map[string]any{"id": req.ID, "ok": false,
			"error": map[string]string{"code": code, "message": "made-up " + code}})
	} else {
		reply = fmt.Appendf(nil, `{"id":%q,"ok":true,"result":%s}`, req.ID, result)
	}
	_, _ = conn.Write(append(reply, '\n'))
}

// answer works out the reply's result (JSON) or error code.
func (f *fakeHelper) answer(op string, args map[string]any) (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if codes := f.fail[op]; len(codes) > 0 {
		f.fail[op] = codes[1:]
		return "", codes[0]
	}
	if rs := f.results[op]; len(rs) > 0 {
		f.results[op] = rs[1:]
		return rs[0], ""
	}
	switch op {
	case opPlace:
		key, _ := args["ref"].(string)
		symbol, _ := args["symbol"].(string)
		if why, ok := f.refused[symbol]; ok {
			return mustJSON(map[string]any{"ref": key, "client_ref": ClientRef(key), "broker_id": nil,
				"status": "rejected", "filled": "0", "fill_price": nil, "detail": why}), ""
		}
		f.nextID++
		o := &fakeOrder{key: key, clientRef: ClientRef(key), brokerID: fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID),
			status: "accepted", filled: "0"}
		f.orders[o.clientRef] = o
		return mustJSON(o.report(true)), ""
	case opLookup, opCancel:
		key, _ := args["ref"].(string)
		o, ok := f.orders[ClientRef(key)]
		if !ok {
			return "", "order_not_found"
		}
		if op == opCancel && (o.status == "accepted" || o.status == "partially_filled") {
			o.status = "cancelled"
		}
		return mustJSON(o.report(true)), ""
	case opOpenOrders:
		var out []map[string]any
		for _, o := range f.orders {
			if o.status == "accepted" || o.status == "partially_filled" {
				out = append(out, o.report(false))
			}
		}
		out = append(out, f.foreign...)
		sort.Slice(out, func(i, j int) bool { return out[i]["broker_id"].(string) < out[j]["broker_id"].(string) })
		if out == nil {
			out = []map[string]any{}
		}
		return mustJSON(map[string]any{"as_of": "2026-03-03T15:00:00Z", "orders": out}), ""
	case opHoldings:
		return mustJSON(map[string]any{"as_of": "2026-03-03T15:00:00Z", "holdings": f.holdings}), ""
	case opCash:
		return mustJSON(map[string]any{"as_of": "2026-03-03T15:00:00Z", "cash": f.cash, "buying_power": f.cash}), ""
	case opAccount:
		return mustJSON(map[string]any{"as_of": "2026-03-03T15:00:00Z", "cash": f.cash, "buying_power": f.cash,
			"value": f.value}), ""
	}
	return "", "unknown_op"
}

func (o *fakeOrder) report(withKey bool) map[string]any {
	r := map[string]any{"ref": nil, "client_ref": o.clientRef, "broker_id": o.brokerID, "status": o.status,
		"filled": o.filled, "fill_price": o.fillPrice, "detail": ""}
	if withKey {
		r["ref"] = o.key
	}
	return r
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// keys lists the keys of every order the fake was sent.
func (f *fakeHelper) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, o := range f.orders {
		out = append(out, o.key)
	}
	return out
}

// count is how many requests for op the fake received.
func (f *fakeHelper) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r["op"] == op {
			n++
		}
	}
	return n
}

func (f *fakeHelper) last() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return nil
	}
	return f.requests[len(f.requests)-1]
}

func (f *fakeHelper) failNext(op string, codes ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[op] = append(f.fail[op], codes...)
}

func (f *fakeHelper) resultNext(op string, raw ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[op] = append(f.results[op], raw...)
}
