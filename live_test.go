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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"os/exec"
	"regexp"
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
	full := append([]string{"compose", "-p", "ymm-live", "-f", "testdata/live/compose.live.yaml"}, args...)
	cmd := exec.Command("docker", full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(full, " "), err, out)
	}
}

// liveComposeOutput is liveCompose for the calls whose output is the assertion.
func liveComposeOutput(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{"compose", "-p", "ymm-live", "-f", "testdata/live/compose.live.yaml"}, args...)
	out, err := exec.Command("docker", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(full, " "), err, out)
	}
	return string(out)
}

func seed(t *testing.T, id, from, subject, body string, html bool) {
	t.Helper()
	mime := ""
	if html {
		mime = "MIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n"
	}
	msg := fmt.Sprintf("From: %s\r\nTo: tester\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: %s\r\n%s\r\n%s\r\n",
		from, subject, id, time.Now().UTC().Format(time.RFC1123Z), mime, body)
	seedRaw(t, from, msg)
}

// seedRaw delivers a complete message, headers included, to the tester's
// inbox over SMTP.
func seedRaw(t *testing.T, from, msg string) {
	t.Helper()
	if err := smtp.SendMail(liveSMTP, nil, from, []string{"tester"}, []byte(msg)); err != nil {
		t.Fatalf("seeding from %s: %v", from, err)
	}
}

// mcpOverStdin starts cmd, sends the MCP handshake and a tools/list on its
// stdin, holds the pipe open long enough for Docker to attach it, and
// returns everything the process wrote to stdout.
func mcpOverStdin(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	var out bytes.Buffer
	cmd.Stdout = &out
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(in, strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"live","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n")+"\n")
	// Docker attaches stdin asynchronously, so a pipe that closes the instant
	// the lines are written can take the reply with it. Hold it open.
	time.Sleep(time.Second)
	in.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(cmd.Args, " "), err, out.String())
	}
	return out.String()
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
	t.Cleanup(func() {
		if t.Failed() {
			// Teardown destroys the evidence, so capture it first.
			out, _ := exec.Command("docker", "compose", "-p", "ymm-live",
				"-f", "testdata/live/compose.live.yaml",
				"logs", "--tail", "40").CombinedOutput()
			t.Logf("stack logs before teardown:\n%s", out)
		}
		liveCompose(t, "down", "-v")
	})

	// Two services have to come up: ours is ready when its metadata answers,
	// and GreenMail — a JVM, much slower to boot — when SMTP answers its
	// banner. Seeding before the second is ready fails with a bare EOF.
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
	for {
		c, err := smtp.Dial(liveSMTP)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("GreenMail SMTP never became ready")
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
	if len(tools.Tools) != 11 {
		t.Errorf("tool count = %d, want 11", len(tools.Tools))
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

	// The recent channel is its own mbsync invocation into its own store, and
	// MaxMessages there has only ever been read about, never run against a
	// server. Its maildir holds messages only if that invocation worked.
	ls := liveComposeOutput(t, "exec", "-T", "your-mail-mcp", "ls", "-R", "/mail")
	// ls -R prints a "dir:" header per directory and a blank line after each
	// listing, so a directory with nothing in it is a header and no entries.
	if _, after, found := strings.Cut(ls, "/mail/testbox-recent/INBOX/new:\n"); !found || strings.HasPrefix(after, "\n") {
		t.Fatalf("the recent channel pulled no mail into its own maildir; ls -R /mail:\n%s", ls)
	}

	// attachment, end to end and past the inline cap: the part comes back
	// as a signed link, and the link serves the bytes intact.
	pdf := bytes.Repeat([]byte("%PDF-1.4\n"), attachmentCap/9+1)
	seedRaw(t, "erin@example.com", "From: erin@example.com\r\nTo: tester\r\nSubject: big attachment\r\n"+
		"Message-ID: <big-live@example.com>\r\nDate: "+time.Now().UTC().Format(time.RFC1123Z)+"\r\n"+
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n"+
		"--B\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=big.pdf\r\n"+
		"Content-Transfer-Encoding: base64\r\n\r\n"+base64.StdEncoding.EncodeToString(pdf)+"\r\n--B--\r\n")
	call("refresh", map[string]any{"account": "testbox"})
	for {
		if strings.Contains(call("search", map[string]any{"query": "from:erin@example.com"}), "big attachment") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the attachment message never appeared")
		}
		time.Sleep(2 * time.Second)
	}
	reply := call("attachment", map[string]any{"id": "big-live@example.com", "part": 2})
	link := regexp.MustCompile(`https?://\S+/attachment/\S+`).FindString(reply)
	if link == "" {
		t.Fatalf("attachment reply carries no link:\n%s", reply)
	}
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d %v", link, resp.StatusCode, err)
	}
	if sha256.Sum256(body) != sha256.Sum256(pdf) {
		t.Fatalf("downloaded %d bytes, want the %d-byte part intact", len(body), len(pdf))
	}

	if st := call("status", map[string]any{}); !strings.Contains(st, "full mirror") {
		t.Fatalf("status does not report the mirror state:\n%s", st)
	}

	// README quick start: a local client attaches to the running daemon through
	// docker exec, and the bridge answers tools/list over the socket.
	bridged := mcpOverStdin(t, exec.Command("docker", "compose", "-p", "ymm-live", "-f", "testdata/live/compose.live.yaml",
		"exec", "-T", "your-mail-mcp", "your-mail-mcp", "stdio"))
	if got := strings.Count(bridged, `"name":"`); got < 11 {
		t.Fatalf("want at least 11 tool names over the docker exec bridge, got %d:\n%s", got, bridged)
	}
}

// TestLiveBareImageAnswersToolsList is the property every directory checks:
// the shipped image, run with no environment and no volumes, speaks MCP on
// stdin and lists its tools.
func TestLiveBareImageAnswersToolsList(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	if out, err := exec.Command("docker", "build", "-t", "ymm-live-bare", ".").CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}

	out := mcpOverStdin(t, exec.Command("docker", "run", "-i", "--rm", "ymm-live-bare"))
	if got := strings.Count(out, `"name":"`); got < 11 {
		t.Fatalf("want at least 11 tool names in tools/list, got %d:\n%s", got, out)
	}
}
