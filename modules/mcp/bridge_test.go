package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// runBridge feeds input through a Bridge pointed at url and returns the
// decoded output lines.
func runBridge(t *testing.T, b *Bridge, input string) []rpcReply {
	t.Helper()
	var out, logs bytes.Buffer
	b.Log = &logs
	if err := b.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Run: %v (log: %s)", err, logs.String())
	}
	var replies []rpcReply
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r rpcReply
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("output line is not JSON: %q", line)
		}
		replies = append(replies, r)
	}
	return replies
}

// TestBridgeEndToEnd runs the stdio bridge against the real module over
// a real loopback listener, the way `kopusha mcp` reaches a running
// viewer.
func TestBridgeEndToEnd(t *testing.T) {
	mux := boot(t, "[mcp]\n")
	var mu sync.Mutex
	var versions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		versions = append(versions, r.Header.Get("MCP-Protocol-Version"))
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		``,
		`{"jsonrpc":"2.0","id":"two","method":"tools/list"}` + "\r",
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_files"}}`,
	}, "\n") + "\n"

	replies := runBridge(t, &Bridge{URL: srv.URL + Endpoint}, input)
	if len(replies) != 3 {
		t.Fatalf("got %d replies, want 3 (notification and blank line get none)", len(replies))
	}
	for i, want := range []string{`1`, `"two"`, `3`} {
		if string(replies[i].ID) != want {
			t.Errorf("reply %d id = %s, want %s", i, replies[i].ID, want)
		}
		if replies[i].Error != nil {
			t.Errorf("reply %d error: %+v", i, replies[i].Error)
		}
	}
	if !strings.Contains(string(replies[1].Result), `"query"`) {
		t.Errorf("tools/list did not list query: %s", replies[1].Result)
	}

	mu.Lock()
	defer mu.Unlock()
	if versions[0] != "" {
		t.Errorf("initialize sent MCP-Protocol-Version %q, want none", versions[0])
	}
	for i, v := range versions[1:] {
		if v != "2025-06-18" {
			t.Errorf("request %d after initialize sent MCP-Protocol-Version %q, want the negotiated 2025-06-18", i+1, v)
		}
	}
}

func TestBridgeUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + Endpoint
	srv.Close() // nothing listens there now

	replies := runBridge(t, &Bridge{URL: url}, ""+
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+
		`{"jsonrpc":"2.0","id":7,"method":"ping"}`+"\n")
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1: a request gets an error, a notification nothing", len(replies))
	}
	r := replies[0]
	if string(r.ID) != "7" || r.Error == nil || r.Error.Code != codeUnreachable {
		t.Fatalf("reply = %+v, want error %d for id 7", r, codeUnreachable)
	}
	if !strings.Contains(r.Error.Message, "not reachable") {
		t.Errorf("message %q does not say the viewer is unreachable", r.Error.Message)
	}
}

// A viewer without [mcp] has no /api/mcp route; the SPA fallback
// answers with HTML and 200, which must not reach the agent as a reply.
func TestBridgeModuleDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<!doctype html><title>kopusha</title>"))
	}))
	defer srv.Close()

	replies := runBridge(t, &Bridge{URL: srv.URL + Endpoint}, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n")
	if len(replies) != 1 || replies[0].Error == nil {
		t.Fatalf("replies = %+v, want one error", replies)
	}
	if !strings.Contains(replies[0].Error.Message, "not enabled") {
		t.Errorf("message %q does not say the module is disabled", replies[0].Error.Message)
	}
}

func TestBridgeToken(t *testing.T) {
	mux := boot(t, "[mcp]\ntoken = s3cret\n")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"

	if r := runBridge(t, &Bridge{URL: srv.URL + Endpoint, Token: "s3cret"}, ping); len(r) != 1 || r[0].Error != nil {
		t.Fatalf("with token: %+v", r)
	}
	r := runBridge(t, &Bridge{URL: srv.URL + Endpoint}, ping)
	if len(r) != 1 || r[0].Error == nil || !strings.Contains(r[0].Error.Message, "401") {
		t.Fatalf("without token: %+v, want a 401 error", r)
	}
}

// An oversized message is dropped with a null-id error, and the bridge
// keeps serving the messages after it.
func TestBridgeOversizedLine(t *testing.T) {
	mux := boot(t, "[mcp]\n")
	srv := httptest.NewServer(mux)
	defer srv.Close()

	huge := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", maxLine) + `"}}`
	replies := runBridge(t, &Bridge{URL: srv.URL + Endpoint}, huge+"\n"+`{"jsonrpc":"2.0","id":2,"method":"ping"}`+"\n")
	if len(replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(replies))
	}
	if string(replies[0].ID) != "null" || replies[0].Error == nil {
		t.Errorf("oversized: %+v, want a null-id error", replies[0])
	}
	if string(replies[1].ID) != "2" || replies[1].Error != nil {
		t.Errorf("next message: %+v, want a normal reply", replies[1])
	}
}

// Output is one compact JSON object per line, whatever the endpoint's
// formatting, because stdio framing is newline-delimited.
func TestBridgeCompactsReplies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\n  \"jsonrpc\": \"2.0\",\n  \"id\": 1,\n  \"result\": {}\n}\n"))
	}))
	defer srv.Close()

	var out bytes.Buffer
	b := &Bridge{URL: srv.URL + Endpoint}
	if err := b.Run(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
