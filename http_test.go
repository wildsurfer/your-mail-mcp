package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A modern client must be able to use the version advertised by discovery
// without falling back to an initialize handshake.
func TestHTTPModernDiscoveryAndCalls(t *testing.T) {
	const version = "2026-07-28"
	const token = "http-test-access-token"
	o := testOAuth(t)
	o.access[token] = time.Now().Add(time.Minute)
	srv := newServer(&Config{}, nil, t.TempDir())
	srv.publicURL = o.publicURL
	m := mcp.NewServer(&mcp.Implementation{Name: "your-mail-mcp", Version: "test"}, nil)
	srv.registerTools(m)
	ts := httptest.NewServer(newHTTPHandler(o, m, srv))
	defer ts.Close()
	client := ts.Client()
	client.Timeout = 5 * time.Second

	call := func(method string, params map[string]any, authorized bool) *http.Response {
		t.Helper()
		params["_meta"] = map[string]any{
			"io.modelcontextprotocol/protocolVersion":    version,
			"io.modelcontextprotocol/clientInfo":         map[string]string{"name": "http-test", "version": "1"},
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		}
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Protocol-Version", version)
		req.Header.Set("Mcp-Method", method)
		if method == "tools/call" {
			req.Header.Set("Mcp-Name", params["name"].(string))
		}
		if authorized {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	for _, method := range []string{"server/discover", "tools/call"} {
		resp := call(method, map[string]any{"name": "status", "arguments": map[string]any{}}, false)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s status = %d, want 401", method, resp.StatusCode)
		}
	}

	var discovery mcp.DiscoverResult
	decodeHTTPRPCResult(t, call("server/discover", map[string]any{}, true), &discovery)
	if !containsString(discovery.SupportedVersions, version) {
		t.Fatalf("discovery supports %v, want %s", discovery.SupportedVersions, version)
	}
	var tools mcp.ListToolsResult
	decodeHTTPRPCResult(t, call("tools/list", map[string]any{}, true), &tools)
	if len(tools.Tools) != 11 {
		t.Fatalf("got %d tools without initialization, want 11", len(tools.Tools))
	}
	var status mcp.CallToolResult
	decodeHTTPRPCResult(t, call("tools/call", map[string]any{"name": "status", "arguments": map[string]any{}}, true), &status)
	if status.IsError || len(status.Content) == 0 {
		t.Fatalf("status without initialization failed: %+v", status)
	}
}

func decodeHTTPRPCResult(t *testing.T, resp *http.Response, result any) {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %d, body = %s", resp.StatusCode, body)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(bytes.NewReader(body))
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data:"); ok {
				body = []byte(strings.TrimSpace(data))
				break
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatalf("decode RPC response: %v, body = %s", err, body)
	}
	if len(reply.Error) != 0 {
		t.Fatalf("RPC error: %s", reply.Error)
	}
	if err := json.Unmarshal(reply.Result, result); err != nil {
		t.Fatalf("decode RPC result: %v", err)
	}
}
