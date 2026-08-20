//go:build live

package main

// The live test runs the shipped container against a real IMAP server and
// drives every tool through the real OAuth flow and MCP transport. It exists
// because the class of defect it catches is invisible to the rest of the
// suite: the container's mbsync is older than the host's, the sync is a real
// IMAP conversation, and the tools read mail that arrived through the whole
// pipeline rather than files a test wrote.
//
// Run with:  go test -tags live -run TestLive -v -timeout 15m
// Needs Docker. Everything is isolated under the compose project "ymm-live"
// and torn down afterwards, including volumes.

import (
	"context"
	"fmt"
	"net/http"
	"net/smtp"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	liveBase = "http://127.0.0.1:18080"
	liveSMTP = "127.0.0.1:13025"
	livePass = "live-test-passphrase"
)

func liveCompose(t *testing.T, args ...string) {
	t.Helper()
	full := append([]string{"compose", "-p", "ymm-live", "-f", "compose.yaml", "-f", "testdata/live/compose.live.yaml"}, args...)
	cmd := exec.Command("docker", full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(full, " "), err, out)
	}
}

func seed(t *testing.T, id, from, subject, body string, html bool) {
	t.Helper()
	mime := ""
	if html {
		mime = "MIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n"
	}
	msg := fmt.Sprintf("From: %s\r\nTo: tester\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: %s\r\n%s\r\n%s\r\n",
		from, subject, id, time.Now().UTC().Format(time.RFC1123Z), mime, body)
	if err := smtp.SendMail(liveSMTP, nil, from, []string{"tester"}, []byte(msg)); err != nil {
		t.Fatalf("seeding %s: %v", id, err)
	}
}

// liveToken walks the real DCR flow against the running container.
func liveToken(t *testing.T) string {
	t.Helper()
	nr := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := nr.Post(liveBase+"/register", "application/json",
		strings.NewReader(`{"client_name":"live-test","redirect_uris":["http://localhost/callback"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	decodeJSONBody(t, resp, &reg)

	form := url.Values{
		"response_type": {"code"}, "client_id": {reg.ClientID},
		"redirect_uri":          {"http://localhost:7777/callback"},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
		"passphrase":            {livePass},
	}
	resp, err = nr.PostForm(liveBase+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Query().Get("code") == "" {
		t.Fatalf("authorize did not yield a code: status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, err = nr.PostForm(liveBase+"/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"client_id":     {reg.ClientID},
		"redirect_uri":  {"http://localhost:7777/callback"},
		"code_verifier": {verifier},
	})
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	decodeJSONBody(t, resp, &tok)
	if tok.AccessToken == "" {
		t.Fatal("no access token")
	}
	return tok.AccessToken
}

func TestLive(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	liveCompose(t, "up", "-d", "--build")
	t.Cleanup(func() { liveCompose(t, "down", "-v") })

	// The server is up when its metadata answers.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		resp, err := http.Get(liveBase + "/.well-known/oauth-authorization-server")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never became ready")
		}
		time.Sleep(2 * time.Second)
	}

	seed(t, "live0@example.com", "alice@example.com", "Thursday lunch?", "Are you free Thursday around one?", false)
	seed(t, "live1@example.com", "bank@example.com", "Your statement is ready", "Your August statement is available.", false)
	seed(t, "live2@example.com", "carol@example.com", "Re: deployment window", "Let's deploy Friday morning instead.", false)
	seed(t, "live3@example.com", "news@example.com", "HTML only digest",
		"<html><body><h1>Meeting moved</h1><p>Now <b>3pm</b> on Thursday.</p></body></html>", true)

	ctx := context.Background()
	token := liveToken(t)
	hc := &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "live-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: liveBase + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	call := func(tool string, args map[string]any) string {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		out := resultText(t, res)
		if !strings.Contains(out, "UNTRUSTED EMAIL CONTENT") {
			t.Errorf("%s: response is not wrapped in the untrusted-content markers", tool)
		}
		return out
	}

	// The ticker runs every 5s in this stack; wait for the seeds to arrive.
	for {
		if strings.Contains(call("count", map[string]any{"query": "*"}), "4") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("seeded mail never arrived through sync and index")
		}
		time.Sleep(2 * time.Second)
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 9 {
		t.Errorf("tool count = %d, want 9", len(tools.Tools))
	}

	if out := call("search", map[string]any{"query": "deployment"}); !strings.Contains(out, "deployment window") {
		t.Errorf("search did not find the seeded thread: %s", out)
	}
	if out := call("search", map[string]any{"query": "*", "account": "testbox"}); !strings.Contains(out, "Thursday lunch?") {
		t.Errorf("account-scoped search missed a message: %s", out)
	}
	if out := call("ids", map[string]any{"query": "from:carol@example.com"}); !strings.Contains(out, "live2@example.com") {
		t.Errorf("ids did not return the message id: %s", out)
	}
	if out := call("files", map[string]any{"query": "from:carol@example.com"}); !strings.Contains(out, "testbox/INBOX") {
		t.Errorf("files did not return a maildir path: %s", out)
	}
	if out := call("show", map[string]any{"id": "live2@example.com"}); !strings.Contains(out, "Friday morning") {
		t.Errorf("show did not return the body: %s", out)
	}
	if out := call("thread", map[string]any{"id": "live2@example.com"}); !strings.Contains(out, "deployment window") {
		t.Errorf("thread did not return the thread: %s", out)
	}

	// The C1 regression, through the entire real pipeline: an HTML-only
	// message must come back readable, with no raw markup.
	if out := call("text", map[string]any{"id": "live3@example.com"}); !strings.Contains(out, "Meeting moved") ||
		strings.Contains(out, "<html") {
		t.Errorf("text did not render the HTML-only message: %s", out)
	}

	out := call("folders", nil)
	for _, want := range []string{"testbox/INBOX", "last sync", "excluded from search: testbox/Spam"} {
		if !strings.Contains(out, want) {
			t.Errorf("folders output is missing %q:\n%s", want, out)
		}
	}

	// refresh: seed one more message, ask for a sync now, and require the
	// message to become searchable. The count in refresh's own reply is not
	// asserted, because the 5s ticker can legitimately win the race.
	seed(t, "fresh1@example.com", "dave@example.com", "One more thing", "Fresh message for refresh.", false)
	call("refresh", map[string]any{"account": "testbox"})
	for {
		if strings.Contains(call("search", map[string]any{"query": "from:dave@example.com"}), "One more thing") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refreshed message never appeared")
		}
		time.Sleep(2 * time.Second)
	}
}
