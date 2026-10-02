package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Bridge is the stdio side of the module: `kopusha mcp`. It reads
// newline-delimited JSON-RPC messages from an agent on standard input,
// forwards each one to the running viewer's /api/mcp endpoint, and
// writes the reply to standard output, one message per line.
//
// It is deliberately thin. It holds no engine, loads no files and keeps
// no state beyond the negotiated protocol revision, so the agent and
// the UI keep sharing one session in the one process that owns DuckDB.
// The cost is that the viewer must already be running with [mcp]
// enabled; when it is not, every request is answered with a JSON-RPC
// error that says so, rather than the bridge exiting and leaving the
// agent with a dead pipe.
//
// Messages are forwarded one at a time, in order. Standard output
// carries protocol messages only; diagnostics go to Log.
type Bridge struct {
	// URL is the MCP endpoint, e.g. http://127.0.0.1:9200/api/mcp.
	URL string
	// Token, when set, is sent as "Authorization: Bearer <token>".
	Token string
	// Client performs the requests. Nil means http.DefaultClient.
	Client *http.Client
	// Log receives diagnostics. Nil discards them.
	Log io.Writer

	protocolVersion string
}

// maxLine bounds one inbound message. The endpoint refuses bodies over
// maxBody; reading far past that only to have it refused would let a
// runaway client grow this process without limit.
const maxLine = maxBody + 1024

// codeUnreachable is the JSON-RPC error the bridge returns when the
// endpoint cannot answer: the implementation-defined server-error range.
const codeUnreachable = -32000

// Run forwards messages until in reaches EOF or ctx is cancelled.
// It returns nil on a clean EOF.
func (b *Bridge) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	r := bufio.NewReaderSize(in, 64*1024)
	w := bufio.NewWriter(out)
	for {
		line, err := readLine(r)
		if len(bytes.TrimSpace(line)) > 0 {
			if reply := b.forward(ctx, line); reply != nil {
				if _, werr := w.Write(append(reply, '\n')); werr != nil {
					return werr
				}
				if werr := w.Flush(); werr != nil {
					return werr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if errors.Is(err, errLineTooLong) {
				// The id is lost with the message, so answer with a null
				// id rather than leave a request waiting forever.
				b.logf("dropped a message over %d bytes", maxLine)
				reply := errorReply(nil, fmt.Sprintf("message over %d bytes dropped", maxLine))
				if _, werr := w.Write(append(reply, '\n')); werr != nil {
					return werr
				}
				if werr := w.Flush(); werr != nil {
					return werr
				}
				continue
			}
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

var errLineTooLong = errors.New("line too long")

// readLine returns the next line without its terminator. A line over
// maxLine is consumed and discarded, and reported as errLineTooLong.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) > maxLine {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			return nil, errLineTooLong
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

// forward sends one message and returns the line to write back, or nil
// when the message expects no reply.
func (b *Bridge) forward(ctx context.Context, msg []byte) []byte {
	id, isRequest, method := peek(msg)

	body, err := b.post(ctx, msg)
	if err != nil {
		b.logf("%v", err)
		if !isRequest {
			return nil
		}
		return errorReply(id, err.Error())
	}
	if body == nil {
		return nil
	}
	if method == "initialize" {
		b.rememberVersion(body)
	}
	return body
}

// post sends msg and returns the compacted JSON reply, or nil for 202.
func (b *Bridge) post(ctx context.Context, msg []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL, bytes.NewReader(msg))
	if err != nil {
		return nil, fmt.Errorf("kopusha MCP endpoint %s: %v", b.URL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if b.protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", b.protocolVersion)
	}
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}

	client := b.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kopusha is not reachable at %s. Start kopusha with the mcp module enabled (docs/MCP.md): %v", b.URL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("read reply from %s: %v", b.URL, err)
	}

	switch {
	case resp.StatusCode == http.StatusAccepted:
		return nil, nil
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("kopusha MCP endpoint %s answered %s: %s",
			b.URL, resp.Status, strings.TrimSpace(string(data)))
	}
	// A viewer without the module enabled has no /api/mcp route, and the
	// SPA fallback answers it with index.html and 200. Say so instead of
	// handing the agent a page of HTML.
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "application/json" {
		return nil, fmt.Errorf("kopusha at %s answered with %q, not JSON: the mcp module is not enabled. Rename kopusha_mcp.conf.example to kopusha_mcp.conf and restart kopusha",
			b.URL, mt)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return nil, fmt.Errorf("kopusha MCP endpoint %s returned invalid JSON: %v", b.URL, err)
	}
	return compact.Bytes(), nil
}

// rememberVersion records the revision the server chose, so later
// requests carry the MCP-Protocol-Version header the spec requires.
func (b *Bridge) rememberVersion(reply []byte) {
	var r struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if json.Unmarshal(reply, &r) == nil && r.Result.ProtocolVersion != "" {
		b.protocolVersion = r.Result.ProtocolVersion
	}
}

// peek extracts what forward needs without validating the message; the
// endpoint does the validation and answers with the proper error.
func peek(msg []byte) (id json.RawMessage, isRequest bool, method string) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(msg, &m) != nil {
		// Unparseable: let the endpoint answer with a parse error, and
		// if it cannot be reached, answer for it with a null id.
		return json.RawMessage("null"), true, ""
	}
	if len(m.ID) == 0 || string(m.ID) == "null" || m.Method == "" {
		return nil, false, m.Method
	}
	return m.ID, true, m.Method
}

func errorReply(id json.RawMessage, msg string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	out, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id,
		Error: &rpcError{Code: codeUnreachable, Message: msg}})
	return out
}

func (b *Bridge) logf(format string, args ...any) {
	if b.Log != nil {
		fmt.Fprintf(b.Log, "kopusha mcp: "+format+"\n", args...)
	}
}
