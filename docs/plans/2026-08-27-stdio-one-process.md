# stdio, one process, fresh mail first: Implementation Plan

**Goal:** Ship section 7 of the spec: a `stdio` attach point over a Unix socket, HTTP only when `PUBLIC_URL` is set, `refresh` as the full scheduled pass with a bounded wait, a `recent` mbsync channel so today's mail is searchable during a weeks-long initial mirror, and mirror-progress metadata on results.

**Architecture:** One process. `serve` syncs on a ticker, listens on `INDEX/mcp.sock` for local sessions, and optionally on HTTP. `stdio` bridges stdin/stdout to that socket, or runs the daemon in-process when no socket exists. Every session is one `mcp.ServerSession` on the same `*mcp.Server`, so both attach points share one `Syncer`. mbsync gets a second `recent` channel per account with `MaxMessages`; notmuch merges the overlap by Message-ID.

**Tech Stack:** Go 1.27, `modelcontextprotocol/go-sdk` v1.7.0 (`mcp.IOTransport`, `mcp.StdioTransport`, `Server.Connect`), standard library `net`, `os/exec`, `regexp`. mbsync (isync), notmuch. `testing` only.

**Spec:** `docs/specs/2026-08-19-your-mail-mcp-design.md`, section 7.

## Global Constraints

- Exactly two dependencies: `modelcontextprotocol/go-sdk` and `emersion/go-imap/v2`. No new module.
- The only IMAP operation in Go code is `LIST`. No `STATUS`, no `SELECT`.
- The four read-only mbsync directives (`Sync Pull`, `Create Near`, `Remove None`, `Expunge None`) appear in every generated channel. `TestGenMbsyncrcIsPullOnlyForEveryAccount` must keep passing.
- Every byte of mail text reaches the model through `page()` / `render()`. Server-generated notes are the one exception and must not contain mail text.
- Tests use `testing` only. No framework, no mocking library.
- `go vet ./...`, `go build ./...`, `go test ./...`, `go test -race ./...` clean before every commit.
- Commit messages describe the change plainly. No AI or agent attribution anywhere.
- No em dashes in new comments or messages.

---

## File map

| File | Responsibility after this plan |
|---|---|
| `main.go` | env, config, subcommand dispatch (`serve`, `stdio`), daemon wiring, socket listener, HTTP handler |
| `bridge.go` (new) | `stdio` bridge: copy stdin/stdout to the socket |
| `sync.go` | generated configs incl. `recent` channel, `Sync` with completion channel and progress parsing, backoff |
| `mcp.go` | tools; `refresh` bounded wait; incomplete-mirror note; socket-session attachment path |
| `Dockerfile`, `compose.yaml` | `CMD ["stdio"]`, `command: serve` |
| `README.md` | case 1 becomes `docker exec`, HTTP optional |
| `*_test.go` | one test per behaviour, table-driven where the existing file is |
| `live_test.go` | bare `docker run -i` answers `tools/list` |

---

### Task 1: Zero accounts is legal; `PUBLIC_URL` makes HTTP optional

**Files:**
- Modify: `main.go:71-82` (`loadConfig`), `main.go:277-395` (`run`)
- Modify: `mcp.go:660-695` (`statusTool`)
- Test: `main_test.go`, `mcp_test.go`

**Interfaces:**
- Produces: `loadConfig(path string) (*Config, error)` returns `&Config{}` with zero accounts when the file is missing or lists none. `env.PublicURL == ""` means no listener.

- [ ] **Step 1: Write the failing tests**

In `main_test.go`, add:

```go
func TestLoadConfigAllowsMissingFileAndNoAccounts(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || len(cfg.Accounts) != 0 {
		t.Fatalf("missing file: cfg=%v err=%v", cfg, err)
	}
	p := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(p, []byte(`{"accounts":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(p)
	if err != nil || len(cfg.Accounts) != 0 {
		t.Fatalf("empty list: cfg=%v err=%v", cfg, err)
	}
}
```

Remove the `"no accounts"` case from `TestLoadConfigRejectsBadInput` (`main_test.go:90-120`): it now asserts the old behaviour.

In `mcp_test.go`, add:

```go
func TestStatusExplainsNoAccounts(t *testing.T) {
	s := newServer(&Config{}, nil, t.TempDir())
	s.status = func() map[string]AccountStatus { return nil }
	res, _, err := s.statusTool(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(got, "no accounts configured") || !strings.Contains(got, "accounts.json") {
		t.Fatalf("status without accounts should say how to configure, got:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestLoadConfigAllowsMissingFileAndNoAccounts|TestStatusExplainsNoAccounts' ./...`
Expected: FAIL. `loadConfig` returns "accounts file: open ... no such file" and "no accounts defined"; `statusTool` prints nothing about configuration.

- [ ] **Step 3: Implement**

In `main.go` `loadConfig`, replace the read and the empty check:

```go
func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// No accounts file is a legal way to start: the server answers every
		// tool and status explains what to configure.
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("accounts file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("accounts file: %w", err)
	}
	seen := map[string]bool{}
```

Delete the `if len(cfg.Accounts) == 0 { return nil, fmt.Errorf("accounts file: no accounts defined") }` block.

In `mcp.go` `statusTool`, before the account loop:

```go
	if len(s.cfg.Accounts) == 0 {
		b.WriteString("no accounts configured: mount an accounts.json at CONFIG (see README, \"The accounts file\") and restart\n")
		return page(b.String(), 0, 0), nil, nil
	}
```

In `main.go` `run`, make OAuth and the listener conditional. Replace the `newOAuth` call through the end of the function with:

```go
	m := mcp.NewServer(&mcp.Implementation{Name: "your-mail-mcp", Version: "0.3.0"}, nil)
	srv.registerTools(m)

	if e.PublicURL == "" {
		fmt.Fprintln(os.Stderr, "PUBLIC_URL unset: no HTTP listener, local sessions only")
		<-ctx.Done()
		return nil
	}
	o, err := newOAuth(filepath.Join(e.Index, "oauth.json"), e.PublicURL, e.Passphrase)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Addr:              e.ListenAddr,
		Handler:           newHTTPHandler(o, m, srv),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	fmt.Fprintf(os.Stderr, "listening on %s\n", e.ListenAddr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
```

Move the existing `newOAuth` call (and its "built before the discovery loop" comment) down to this block; the early-exit rationale no longer applies when the listener is optional. The exclusion refresh, startup sync and ticker goroutines stay where they are; with zero accounts they iterate an empty slice.

- [ ] **Step 4: Run the full suite**

Run: `go vet ./... && go test ./... && go test -race ./...`
Expected: PASS. `TestLoadEnvRequiresTheEssentials` still passes (env requirements unchanged).

- [ ] **Step 5: Commit**

```bash
git add main.go mcp.go main_test.go mcp_test.go
git commit -m "Start with no accounts, and open the HTTP listener only when PUBLIC_URL is set"
```

---

### Task 2: Unix socket sessions in `serve`

**Files:**
- Modify: `main.go` (`run`, new `serveSocket`)
- Test: `main_test.go`

**Interfaces:**
- Produces: `serveSocket(ctx context.Context, path string, m *mcp.Server) error`. Listens on `path`, one `m.Connect` per accepted connection via `mcp.IOTransport`, removes the socket file on return. `run` calls it in a goroutine before the `PublicURL` check, with `path = filepath.Join(e.Index, "mcp.sock")`.

- [ ] **Step 1: Write the failing test**

In `main_test.go`:

```go
func TestServeSocketAnswersToolsList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := filepath.Join(t.TempDir(), "mcp.sock")
	m := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	newServer(&Config{}, nil, t.TempDir()).registerTools(m)
	go func() { _ = serveSocket(ctx, sock, m) }()
	var conn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		if conn, err = net.Dial("unix", sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("socket never came up: %v", err)
	}
	defer conn.Close()
	c := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil)
	sess, err := c.Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 11 {
		t.Fatalf("want 11 tools over the socket, got %d", len(res.Tools))
	}
}
```

Add `"net"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestServeSocketAnswersToolsList ./...`
Expected: FAIL, `undefined: serveSocket`.

- [ ] **Step 3: Implement**

In `main.go`:

```go
// serveSocket accepts local MCP sessions on a Unix socket. Each connection is
// one session on the shared server, so it sees the same tools and the same
// syncer as HTTP. There is no auth on the socket: reaching it means running
// a process inside the container, which is the boundary.
func serveSocket(ctx context.Context, path string, m *mcp.Server) error {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func() {
			defer conn.Close()
			sess, err := m.Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)
			if err != nil {
				return
			}
			sess.Wait()
		}()
	}
}
```

Add `"net"` to `main.go` imports. In `run`, after `srv.registerTools(m)` and before the `PublicURL` check:

```go
	sock := filepath.Join(e.Index, "mcp.sock")
	go func() {
		if err := serveSocket(ctx, sock, m); err != nil {
			fmt.Fprintln(os.Stderr, "socket:", err)
		}
	}()
```

- [ ] **Step 4: Run the full suite**

Run: `go vet ./... && go test ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add main.go main_test.go
git commit -m "Accept local MCP sessions on a Unix socket in the index directory"
```

---

### Task 3: `serve` and `stdio` subcommands, and the bridge

**Files:**
- Create: `bridge.go`
- Modify: `main.go` (`main`, `run` signature)
- Modify: `Dockerfile`, `compose.yaml`
- Test: `main_test.go`

**Interfaces:**
- Consumes: `serveSocket` (Task 2), `run` (Task 1).
- Produces: `run(ctx context.Context, stdio bool) error`. `bridge(sock string) error` in `bridge.go`. `main` dispatches: `serve` → `run(ctx, false)`; `stdio` → `bridge` if the socket exists, else `run(ctx, true)`; no argument → `stdio`.

- [ ] **Step 1: Write the failing tests**

In `main_test.go`:

```go
func TestBridgeCopiesBothWays(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "mcp.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 5)
		_, _ = io.ReadFull(c, buf)
		_, _ = c.Write([]byte("echo:" + string(buf)))
	}()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- bridgeIO(sock, inR, outW) }()
	_, _ = inW.Write([]byte("hello"))
	got := make([]byte, 10)
	if _, err := io.ReadFull(outR, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "echo:hello" {
		t.Fatalf("got %q", got)
	}
	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDispatchPicksModeFromArgs(t *testing.T) {
	cases := map[string]struct {
		args     []string
		sockUp   bool
		wantMode string
	}{
		"serve":                {[]string{"serve"}, false, "serve"},
		"stdio, no socket":     {[]string{"stdio"}, false, "stdio-daemon"},
		"stdio, socket exists": {[]string{"stdio"}, true, "bridge"},
		"no args, no socket":   {nil, false, "stdio-daemon"},
		"no args, socket":      {nil, true, "bridge"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := pickMode(c.args, c.sockUp); got != c.wantMode {
				t.Fatalf("want %s, got %s", c.wantMode, got)
			}
		})
	}
}
```

Add `"io"` to the test imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestBridgeCopiesBothWays|TestDispatchPicksModeFromArgs' ./...`
Expected: FAIL, `undefined: bridgeIO`, `undefined: pickMode`.

- [ ] **Step 3: Implement**

Create `bridge.go`:

```go
package main

import (
	"io"
	"net"
	"os"
)

// bridge attaches the calling client to a running daemon: stdin goes to the
// socket, the socket comes back on stdout. It is a pipe, not a server, so a
// docker exec'd stdio session shares the daemon's syncer and index instead
// of starting its own. Returns when stdin closes.
func bridge(sock string) error {
	return bridgeIO(sock, os.Stdin, os.Stdout)
}

func bridgeIO(sock string, in io.Reader, out io.Writer) error {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		_, _ = io.Copy(conn, in)
		if c, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
	}()
	_, err = io.Copy(out, conn)
	return err
}
```

In `main.go`, replace `main` and the `run` signature:

```go
// pickMode maps the command line onto the three ways the binary runs. With
// no argument it behaves as stdio, so a bare "docker run -i image" speaks
// MCP on stdin, which is what every client and directory expects.
func pickMode(args []string, socketExists bool) string {
	if len(args) > 0 && args[0] == "serve" {
		return "serve"
	}
	if socketExists {
		return "bridge"
	}
	return "stdio-daemon"
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sock := filepath.Join(os.Getenv("INDEX"), "mcp.sock")
	_, statErr := os.Stat(sock)
	var err error
	switch pickMode(os.Args[1:], statErr == nil) {
	case "serve":
		err = run(ctx, false)
	case "bridge":
		err = bridge(sock)
	default:
		err = run(ctx, true)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "your-mail-mcp:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdio bool) error {
```

Inside `run`, delete the existing `ctx, stop := signal.NotifyContext(...)` / `defer stop()` pair (the context now comes in). Wrap the rest so a stdio daemon exits on EOF: immediately after `srv.registerTools(m)` and the socket goroutine, add:

```go
	if stdio {
		// stdin is one more session. When the client closes it, the whole
		// process goes: a daemon that outlives its client is the orphan bug
		// every stdio server ships.
		ctx, cancel := context.WithCancel(ctx)
		go func() {
			defer cancel()
			_ = m.Run(ctx, &mcp.StdioTransport{})
		}()
		if e.PublicURL == "" {
			<-ctx.Done()
			return nil
		}
	}
```

and then continue into the existing `PublicURL` check (which for stdio with `PUBLIC_URL` set also starts HTTP; the HTTP server's `ctx.Done()` shutdown goroutine already covers the EOF cancel).

Update `Dockerfile`: replace the last line with

```dockerfile
ENTRYPOINT ["/usr/local/bin/your-mail-mcp"]
CMD ["stdio"]
```

Update `compose.yaml`: under the service, add `command: serve` after `image:`.

- [ ] **Step 4: Run the full suite and build the image**

Run: `go vet ./... && go test ./... && go test -race ./... && docker build -t ymm-dev .`
Expected: PASS, image builds.

- [ ] **Step 5: Smoke the bare run**

Run:
```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | docker run -i --rm ymm-dev | grep -c '"name":"'
```
Expected: `11` (one line of output containing eleven tool names, or a count of 1 line with eleven names; either way tools/list answered).

- [ ] **Step 6: Commit**

```bash
git add bridge.go main.go main_test.go Dockerfile compose.yaml
git commit -m "Add serve and stdio subcommands; stdio bridges to the daemon socket or runs one in-process"
```

---

### Task 4: `refresh` is the full pass, bounded wait, joins a running pass

**Files:**
- Modify: `sync.go` (`Syncer` struct, `newSyncer`, `Sync`, `syncAccount`, `record`, `AccountStatus`)
- Modify: `mcp.go` (`refreshTool`, `statusTool`, `Server.sync` field type), `main.go` (wiring, ticker reset)
- Test: `sync_test.go`, `mcp_test.go`

**Interfaces:**
- Produces:
  - `AccountStatus` gains `Running bool`, `StartedAt time.Time`, `LastDuration time.Duration`.
  - `Syncer.Sync(ctx, account string) (int, error)`: the `folder` parameter is removed; every pass is all folders.
  - `Syncer.Wait(ctx context.Context, d time.Duration) (done bool)`: blocks up to `d` for the in-flight pass, if any.
  - `Syncer.Kick()`: resets the ticker so the next scheduled pass is one interval from now.
  - `Server.sync func(ctx context.Context, account string) (int, error)`.
  - `Server.syncWait func(ctx context.Context, d time.Duration) bool`.
  - `refreshWait = 20 * time.Second`.

- [ ] **Step 1: Write the failing tests**

In `sync_test.go`, change `TestSyncOneAccountAndFolder` to:

```go
func TestSyncOneAccountIsAllFolders(t *testing.T) {
	s, calls := testSyncer(t)
	if _, err := s.Sync(context.Background(), "home"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != "home" {
		t.Fatalf("want one mbsync run for the whole account, got %v", *calls)
	}
}
```

Add:

```go
func TestSyncWaitJoinsRunningPass(t *testing.T) {
	s, _ := testSyncer(t)
	release := make(chan struct{})
	s.runCmd = func(context.Context, string, ...string) error { <-release; return nil }
	go func() { _, _ = s.Sync(context.Background(), "home") }()
	time.Sleep(20 * time.Millisecond)
	if s.Wait(context.Background(), 30*time.Millisecond) {
		t.Fatal("Wait returned done while the pass was still running")
	}
	st := s.Status()["home"]
	if !st.Running || st.StartedAt.IsZero() {
		t.Fatalf("running pass not reflected in status: %+v", st)
	}
	close(release)
	if !s.Wait(context.Background(), time.Second) {
		t.Fatal("Wait did not return done after the pass finished")
	}
	st = s.Status()["home"]
	if st.Running || st.LastDuration <= 0 {
		t.Fatalf("finished pass not reflected in status: %+v", st)
	}
}

func TestDeadlineIsNotAFailure(t *testing.T) {
	s, _ := testSyncer(t)
	s.runCmd = func(ctx context.Context, _ string, _ ...string) error { return context.DeadlineExceeded }
	for i := 0; i < 3; i++ {
		_, _ = s.Sync(context.Background(), "home")
	}
	if st := s.Status()["home"]; st.Failures != 0 || !st.NextRetry.IsZero() {
		t.Fatalf("a SYNC_TIMEOUT expiry must not back off: %+v", st)
	}
}
```

In `mcp_test.go`:

```go
func TestRefreshReportsInProgressAfterBoundedWait(t *testing.T) {
	s := newServer(&Config{Accounts: []Account{{Name: "home"}}}, nil, t.TempDir())
	started := time.Now().Add(-12 * time.Second)
	s.sync = func(context.Context, string) (int, error) { return 0, errSyncBusy }
	s.syncWait = func(context.Context, time.Duration) bool { return false }
	s.status = func() map[string]AccountStatus {
		return map[string]AccountStatus{"home": {Running: true, StartedAt: started, LastDuration: 45 * time.Second}}
	}
	res, _, err := s.refreshTool(context.Background(), nil, refreshArgs{})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{"in progress", "12s", "45s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in refresh reply, got:\n%s", want, got)
		}
	}
}

func TestRefreshNamesSkippedBackedOffAccount(t *testing.T) {
	s := newServer(&Config{Accounts: []Account{{Name: "home"}, {Name: "gmail"}}}, nil, t.TempDir())
	retry := time.Now().Add(40 * time.Minute)
	s.sync = func(context.Context, string) (int, error) { return 2, nil }
	s.syncWait = func(context.Context, time.Duration) bool { return true }
	s.status = func() map[string]AccountStatus {
		return map[string]AccountStatus{"gmail": {NextRetry: retry, LastError: "quota"}}
	}
	res, _, _ := s.refreshTool(context.Background(), nil, refreshArgs{})
	got := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(got, "gmail") || !strings.Contains(got, "skipped") || !strings.Contains(got, "backing off") {
		t.Fatalf("refresh must name a skipped account, got:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestSyncOneAccountIsAllFolders|TestSyncWaitJoinsRunningPass|TestDeadlineIsNotAFailure|TestRefresh' ./...`
Expected: FAIL to compile (`Sync` arity, `Wait`, `Running`, `syncWait`).

- [ ] **Step 3: Implement in `sync.go`**

Extend `AccountStatus`:

```go
type AccountStatus struct {
	LastSync  time.Time
	LastError string
	Failures  int
	NextRetry time.Time
	// Running, StartedAt and LastDuration let a client that asked for a
	// refresh and got "in progress" decide how long to wait.
	Running      bool
	StartedAt    time.Time
	LastDuration time.Duration
}
```

Add to `Syncer`:

```go
	// done is closed when the pass in flight finishes; nil when none is.
	// Wait uses it so a refresh that arrives mid-pass joins rather than
	// starts another. Guarded by mu.
	done chan struct{}
	// kick is read by the ticker loop in run() to reset its schedule after
	// a manual refresh. Buffered so Kick never blocks.
	kick chan struct{}
```

In `newSyncer`, add `kick: make(chan struct{}, 1),`.

Change `Sync`'s signature and body. Remove the `folder` parameter; call `s.syncAccount(ctx, a.Name)`; record start and finish:

```go
func (s *Syncer) Sync(ctx context.Context, account string) (int, error) {
	if err := s.checkInitialised(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	if s.done == nil {
		s.done = make(chan struct{})
	}
	done := s.done
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.done == done {
			close(done)
			s.done = nil
		}
		s.mu.Unlock()
	}()
```

then the existing loop, with two changes inside the per-account goroutine:

```go
		go func(a Account) {
			defer wg.Done()
			defer lock.Unlock()
			s.mu.Lock()
			st := s.status[a.Name]
			st.Running, st.StartedAt = true, time.Now()
			s.status[a.Name] = st
			s.mu.Unlock()
			err := s.syncAccount(ctx, a.Name)
			s.record(a.Name, err)
```

Everything after `wg.Wait()` is unchanged. Note the `defer` above closes the channel only if this call owns it; a second concurrent `Sync` (different account, since `TryLock` serialises the same one) shares the same `done` and the first to finish closes it, which is the join semantics the spec asks for.

`syncAccount` loses `folder`:

```go
func (s *Syncer) syncAccount(ctx context.Context, name string) error {
	if err := os.MkdirAll(filepath.Join(s.maildir, name), 0o700); err != nil {
		return err
	}
	accountCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.runCmd(accountCtx, "mbsync", "-c", s.mbsyncConfig, name)
}
```

`record` learns about duration and deadlines:

```go
func (s *Syncer) record(account string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status[account]
	st.Running = false
	st.LastDuration = time.Since(st.StartedAt)
	if errors.Is(err, context.DeadlineExceeded) {
		// SYNC_TIMEOUT expired mid-download. That is progress interrupted,
		// not a refusal: mbsync journals per message, and the next pass
		// resumes. Backing off here would halve a multi-day first mirror.
		st.LastError = "sync deadline reached; will resume next pass"
		s.status[account] = st
		return
	}
	if err != nil {
```

and the rest as before. Add:

```go
// Wait blocks up to d for the pass in flight. Returns true when no pass is
// running by the time it returns, false when one still is.
func (s *Syncer) Wait(ctx context.Context, d time.Duration) bool {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	case <-ctx.Done():
		return false
	}
}

// Kick asks the ticker loop to restart its interval from now.
func (s *Syncer) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}
```

Add `"errors"` to `sync.go` imports if absent.

- [ ] **Step 4: Implement in `mcp.go` and `main.go`**

`Server` fields:

```go
	sync     func(ctx context.Context, account string) (int, error)
	syncWait func(ctx context.Context, d time.Duration) bool
	syncKick func()
```

Add `const refreshWait = 20 * time.Second` near `attachmentLinkTTL`.

Replace `refreshTool`:

```go
func (s *Server) refreshTool(ctx context.Context, _ *mcp.CallToolRequest, a refreshArgs) (*mcp.CallToolResult, any, error) {
	if a.Account != "" {
		if err := s.knownAccount(a.Account); err != nil {
			return nil, nil, err
		}
	}
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := s.sync(ctx, a.Account)
		ch <- result{n, err}
	}()
	var b strings.Builder
	select {
	case r := <-ch:
		if r.err != nil && !errors.Is(r.err, errSyncBusy) {
			fmt.Fprintln(os.Stderr, "refresh:", r.err)
			b.WriteString("sync failed; call status for detail\n")
			break
		}
		if errors.Is(r.err, errSyncBusy) && !s.syncWait(ctx, refreshWait) {
			b.WriteString(s.inProgress())
			break
		}
		if s.syncKick != nil {
			s.syncKick()
		}
		fmt.Fprintf(&b, "%d new message(s)\n", r.n)
	case <-time.After(refreshWait):
		b.WriteString(s.inProgress())
	}
	if a.Account == "" {
		for _, acct := range s.cfg.Accounts {
			st := s.status()[acct.Name]
			if time.Now().Before(st.NextRetry) {
				fmt.Fprintf(&b, "%s: skipped, backing off until %s (%s)\n",
					acct.Name, st.NextRetry.UTC().Format(time.RFC3339), st.LastError)
			}
		}
	}
	return page(b.String(), 0, 0), nil, nil
}

// inProgress is the refresh reply when the pass outlives the bounded wait:
// enough for a client to decide whether to wait or answer from what is
// indexed now.
func (s *Server) inProgress() string {
	var b strings.Builder
	b.WriteString("sync in progress")
	for _, a := range s.cfg.Accounts {
		st := s.status()[a.Name]
		if st.Running {
			fmt.Fprintf(&b, "; %s started %s ago", a.Name, time.Since(st.StartedAt).Round(time.Second))
			if st.LastDuration > 0 {
				fmt.Fprintf(&b, ", last pass took %s", st.LastDuration.Round(time.Second))
			}
		}
	}
	b.WriteString(". Call refresh again to wait, or search now.\n")
	return b.String()
}
```

The goroutine's `sync` call keeps running after a timeout; that is the point. Add `"os"` and `"errors"` to `mcp.go` imports if absent.

In `statusTool`, inside the account loop after the `last successful sync` branch:

```go
		if v.Running {
			b.WriteString("  sync running since " + v.StartedAt.UTC().Format(time.RFC3339) + "\n")
		}
		if v.LastDuration > 0 {
			b.WriteString("  last pass took " + v.LastDuration.Round(time.Second).String() + "\n")
		}
```

In `main.go` `run`, wire the new fields and make the ticker resettable:

```go
	srv.sync = syncer.Sync
	srv.syncWait = syncer.Wait
	srv.syncKick = syncer.Kick
```

and replace `go runTicker(ctx, e.SyncInterval, sync)` with:

```go
	go func() {
		t := time.NewTicker(e.SyncInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-syncer.kick:
				t.Reset(e.SyncInterval)
			case <-t.C:
				sync(ctx)
			}
		}
	}()
```

The startup `sync` closure changes to `syncer.Sync(ctx, "")`. Update the `refresh` tool description at `mcp.go:769` to: `"Sync every folder of one account or all accounts now, then reindex. Waits up to 20 seconds; if the pass is still running it says so and you can call again or search what is indexed."`.

Fix remaining callers of the old three-argument `Sync` (grep `Sync(ctx` and `s.sync(`); `e2e_test.go` and `live_test.go` may call it.

- [ ] **Step 5: Run the full suite**

Run: `go vet ./... && go test ./... && go test -race ./...`
Expected: PASS. `TestSyncIsSerialisedPerAccount` and `TestSyncContinuesAfterOneAccountFails` unchanged and green.

- [ ] **Step 6: Commit**

```bash
git add sync.go mcp.go main.go sync_test.go mcp_test.go
git commit -m "Make refresh the scheduled pass: every folder, bounded wait, joins a running pass, resets the ticker"
```

---

### Task 5: `recent` channel, completion flag, and mbsync progress

**Files:**
- Modify: `sync.go` (`genMbsyncrc`, `Syncer.runCmd` default, `syncAccount`, `record`, `AccountStatus`)
- Test: `sync_test.go`

**Interfaces:**
- Produces:
  - `genMbsyncrc` emits, per account, a second `Channel <name>-recent` with `Patterns "INBOX"`, `MaxMessages 1000`, a `MaildirStore <name>-recent-local` at `<maildir>/<name>-recent/`, the same four read-only directives, `SyncState *`, `CopyArrivalDate yes`, and a `Group <name>` listing `<name>-recent` then `<name>`.
  - `AccountStatus` gains `Complete bool`, `Pulled int`, `Total int`.
  - `Syncer.runCmd` signature becomes `func(ctx context.Context, name string, args ...string) (string, error)` returning combined output.
  - `parseProgress(out string) (pulled, total int, ok bool)`.

- [ ] **Step 1: Write the failing tests**

In `sync_test.go`:

```go
func TestGenMbsyncrcAddsRecentChannelPerAccount(t *testing.T) {
	cfg := &Config{Accounts: []Account{{Name: "work", Host: "h", User: "u", Password: "p"}}}
	got := genMbsyncrc(cfg, "/mail")
	for _, want := range []string{
		"Channel work-recent\n", "Patterns \"INBOX\"\n", "MaxMessages 1000\n",
		"MaildirStore work-recent-local\n", "Path /mail/work-recent/\n",
		"Group work\nChannels work-recent work\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// The read-only guarantee applies to the recent channel too.
	recent := got[strings.Index(got, "Channel work-recent"):]
	for _, d := range []string{"Sync Pull\n", "Create Near\n", "Remove None\n", "Expunge None\n"} {
		if !strings.Contains(recent, d) {
			t.Fatalf("recent channel lacks %q", d)
		}
	}
}

func TestParseProgress(t *testing.T) {
	cases := map[string]struct {
		out       string
		p, tot    int
		ok        bool
	}{
		"final line":   {"C: 1/1  B: 14/14  F: +0/0 *0/0 #0/0  N: +123/45000 *0/0 #0/0\n", 123, 45000, true},
		"multi line":   {"C: 0/1\nC: 1/1  B: 3/3  F: +0/0 *0/0 #0/0  N: +7/7 *0/0 #0/0\n", 7, 7, true},
		"no counter":   {"mbsync: error\n", 0, 0, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p, tot, ok := parseProgress(c.out)
			if p != c.p || tot != c.tot || ok != c.ok {
				t.Fatalf("got %d/%d %v, want %d/%d %v", p, tot, ok, c.p, c.tot, c.ok)
			}
		})
	}
}

func TestCompleteSetOnceFullPassSucceeds(t *testing.T) {
	s, _ := testSyncer(t)
	s.runCmd = func(_ context.Context, _ string, args ...string) (string, error) {
		if args[len(args)-1] == "home" {
			return "N: +10/10 *0/0 #0/0\n", nil
		}
		return "", errors.New("down")
	}
	_, _ = s.Sync(context.Background(), "")
	st := s.Status()
	if !st["home"].Complete || st["home"].Pulled != 10 || st["home"].Total != 10 {
		t.Fatalf("home should be complete with progress: %+v", st["home"])
	}
	if st["work"].Complete {
		t.Fatalf("work failed and must not be complete: %+v", st["work"])
	}
	s.runCmd = func(context.Context, string, ...string) (string, error) { return "", context.DeadlineExceeded }
	_, _ = s.Sync(context.Background(), "home")
	if !s.Status()["home"].Complete {
		t.Fatal("Complete is set once and never unset")
	}
}
```

Update `testSyncer` so its `runCmd` matches the new signature: return `("", errors.New("AUTHENTICATIONFAILED"))` for work and `("", nil)` otherwise. Update `TestSyncWaitJoinsRunningPass` and `TestDeadlineIsNotAFailure` (Task 4) to the new signature likewise.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestGenMbsyncrcAddsRecentChannelPerAccount|TestParseProgress|TestCompleteSetOnceFullPassSucceeds' ./...`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

In `genMbsyncrc`, after the existing `Channel <name>` block for each account, append:

```go
		// A second, small channel so today's mail is searchable within
		// minutes of a first run, while the full mirror takes as long as
		// the provider's quota allows. MaxMessages fetches only the newest
		// UIDs and ignores the rest; notmuch merges the overlap by
		// Message-ID once the full channel catches up. Expiry under
		// MaxMessages is near-side only and Expunge None keeps even that
		// from deleting a file.
		recentLocal := filepath.Join(maildir, a.Name+"-recent") + string(filepath.Separator)
		b.WriteString("\nMaildirStore " + a.Name + "-recent-local\n")
		b.WriteString("Path " + recentLocal + "\n")
		b.WriteString("Inbox " + filepath.Join(recentLocal, "INBOX") + "\n")
		b.WriteString("SubFolders Verbatim\n")
		b.WriteString("\nChannel " + a.Name + "-recent\n")
		b.WriteString("Far :" + a.Name + "-remote:\n")
		b.WriteString("Near :" + a.Name + "-recent-local:\n")
		b.WriteString("Patterns \"INBOX\"\n")
		b.WriteString("MaxMessages 1000\n")
		b.WriteString("Sync Pull\n")
		b.WriteString("Create Near\n")
		b.WriteString("Remove None\n")
		b.WriteString("Expunge None\n")
		b.WriteString("SyncState *\n")
		b.WriteString("CopyArrivalDate yes\n")
		// The group runs recent first, then full, on one connection at a
		// time: mbsync processes group members sequentially.
		b.WriteString("\nGroup " + a.Name + "\n")
		b.WriteString("Channels " + a.Name + "-recent " + a.Name + "\n")
```

`TestGenMbsyncrcIsPullOnlyForEveryAccount` (`sync_test.go:18`) counts directives per `Channel` block; confirm it still passes, and if it asserts exactly one channel per account, change its expectation to two.

Change `runCmd` to return output. In `newSyncer`:

```go
		runCmd: func(ctx context.Context, name string, args ...string) (string, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
			}
			return string(out), nil
		},
```

and its field type to `runCmd func(ctx context.Context, name string, args ...string) (string, error)`.

`syncAccount` returns the output too:

```go
func (s *Syncer) syncAccount(ctx context.Context, name string) (string, error) {
	if err := os.MkdirAll(filepath.Join(s.maildir, name), 0o700); err != nil {
		return "", err
	}
	accountCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.runCmd(accountCtx, "mbsync", "-c", s.mbsyncConfig, name)
}
```

The caller in `Sync` becomes `out, err := s.syncAccount(ctx, a.Name); s.record(a.Name, out, err)`.

Add the parser:

```go
// progressLine matches mbsync's per-pass counter: N: +pulled/total is the
// new-message tally for the near side. It is the only progress figure that
// needs no IMAP command of our own.
var progressLine = regexp.MustCompile(`N: \+(\d+)/(\d+)`)

func parseProgress(out string) (pulled, total int, ok bool) {
	m := progressLine.FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return 0, 0, false
	}
	last := m[len(m)-1]
	pulled, _ = strconv.Atoi(last[1])
	total, _ = strconv.Atoi(last[2])
	return pulled, total, true
}
```

Extend `AccountStatus`:

```go
	// Complete is set the first time the full channel exits 0 within
	// SYNC_TIMEOUT, and never unset. Pulled and Total are mbsync's own
	// counter from the last pass, so a client can see how far a first
	// mirror has got without the server issuing any IMAP command.
	Complete bool
	Pulled   int
	Total    int
```

`record` takes the output:

```go
func (s *Syncer) record(account, out string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status[account]
	st.Running = false
	st.LastDuration = time.Since(st.StartedAt)
	if p, t, ok := parseProgress(out); ok {
		st.Pulled, st.Total = p, t
	}
	if errors.Is(err, context.DeadlineExceeded) {
		st.LastError = "sync deadline reached; will resume next pass"
		s.status[account] = st
		return
	}
	if err != nil {
		// unchanged failure path
	} else {
		st.LastError = ""
		st.LastSync = time.Now()
		st.Failures = 0
		st.NextRetry = time.Time{}
		st.Complete = true
	}
	s.status[account] = st
}
```

Add `"regexp"` and `"strconv"` to `sync.go` imports. The mbsync group name equals the account name, so `mbsync -c <cfg> <name>` now runs both channels; no change to the argument.

- [ ] **Step 4: Run the full suite**

Run: `go vet ./... && go test ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sync.go sync_test.go
git commit -m "Add a recent INBOX channel per account, track mirror completion, read progress from mbsync's counter"
```

---

### Task 6: Incomplete-mirror note on query results, and `status` progress

**Files:**
- Modify: `mcp.go` (`runQuery`, `countTool`, `statusTool`)
- Test: `mcp_test.go`

**Interfaces:**
- Consumes: `AccountStatus.Complete/Pulled/Total` (Task 5).
- Produces: `(s *Server) mirrorNote(account string) string`, empty when every account in scope is complete.

- [ ] **Step 1: Write the failing test**

In `mcp_test.go`:

```go
func TestQueryResultsCarryIncompleteMirrorNote(t *testing.T) {
	s := newServer(&Config{Accounts: []Account{{Name: "home"}, {Name: "gmail"}}}, nil, t.TempDir())
	s.status = func() map[string]AccountStatus {
		return map[string]AccountStatus{
			"home":  {Complete: true},
			"gmail": {Complete: false, Pulled: 12340, Total: 45000},
		}
	}
	if got := s.mirrorNote("home"); got != "" {
		t.Fatalf("complete account must add no note, got %q", got)
	}
	got := s.mirrorNote("")
	for _, want := range []string{"gmail", "12340", "45000", "older mail may be missing"} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in note, got %q", want, got)
		}
	}
	if strings.Contains(got, "home") {
		t.Fatalf("complete account must not be named, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestQueryResultsCarryIncompleteMirrorNote ./...`
Expected: FAIL, `undefined: mirrorNote`.

- [ ] **Step 3: Implement**

In `mcp.go`:

```go
// mirrorNote names every account in scope whose full mirror has not yet
// completed, with mbsync's pulled/total, so a model reading results knows
// older mail may be absent. Server text, not mail text: it never passes
// through render and must never include anything from a message.
func (s *Server) mirrorNote(account string) string {
	st := s.status()
	var parts []string
	for _, a := range s.cfg.Accounts {
		if account != "" && a.Name != account {
			continue
		}
		v := st[a.Name]
		if v.Complete {
			continue
		}
		if v.Total > 0 {
			parts = append(parts, fmt.Sprintf("%s mirror incomplete, %d of %d pulled", a.Name, v.Pulled, v.Total))
		} else {
			parts = append(parts, a.Name+" mirror incomplete")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "note: " + strings.Join(parts, "; ") + "; older mail may be missing\n"
}
```

In `runQuery`, change the return to `return page(s.mirrorNote(a.Account)+string(out), 0, 0), nil, nil`. In `countTool`, prefix the count output the same way. `filesTool` and the rest are unchanged.

In `statusTool`, inside the account loop:

```go
		if v.Complete {
			b.WriteString("  full mirror: complete\n")
		} else if v.Total > 0 {
			b.WriteString(fmt.Sprintf("  full mirror: %d of %d pulled\n", v.Pulled, v.Total))
		}
```

and drop the older `first full sync: not completed yet` line, which `Complete` now covers.

- [ ] **Step 4: Run the full suite**

Run: `go vet ./... && go test ./... && go test -race ./...`
Expected: PASS. If a `page()` test asserts exact output for a query tool, update it to allow the prefix when status reports incomplete; with `s.status` unset in those tests the note is empty.

- [ ] **Step 5: Commit**

```bash
git add mcp.go mcp_test.go
git commit -m "Say on query results and in status when an account's mirror is still filling"
```

---

### Task 7: Attachments over a socket session

**Files:**
- Modify: `mcp.go` (`attachmentTool` binary branch, `Server` struct)
- Test: `mcp_test.go`

**Interfaces:**
- Consumes: `s.publicURL` (empty when HTTP is off).
- Produces: `(s *Server) saveAttachment(id string, part int, raw []byte) (string, error)` writing to `<index>/attachments/<safe-id>-<part>` at 0600. `Server` gains an `index string` field set in `run`.

- [ ] **Step 1: Write the failing test**

```go
func TestBinaryAttachmentWithoutHTTPIsSavedToIndex(t *testing.T) {
	s := newServer(&Config{}, nil, t.TempDir())
	s.index = t.TempDir()
	path, err := s.saveAttachment("<a@b>", 3, []byte{0, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, filepath.Join(s.index, "attachments")) {
		t.Fatalf("saved outside attachments dir: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("want a 0600 file, got %v %v", info, err)
	}
	if strings.ContainsAny(filepath.Base(path), "<>/") {
		t.Fatalf("id must be sanitised in the filename: %s", path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestBinaryAttachmentWithoutHTTPIsSavedToIndex ./...`
Expected: FAIL, `undefined: saveAttachment` / no `index` field.

- [ ] **Step 3: Implement**

Add `index string` to `Server`, set `srv.index = e.Index` in `run`. In `mcp.go`:

```go
// saveAttachment writes one binary part where a local client can fetch it
// with docker cp. The bytes reach a shell, never the model: the tool returns
// only the path. Used when there is no HTTP endpoint to sign a link for.
func (s *Server) saveAttachment(id string, part int, raw []byte) (string, error) {
	dir := filepath.Join(s.index, "attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
	path := filepath.Join(dir, fmt.Sprintf("%s-%d", safe, part))
	return path, os.WriteFile(path, raw, 0o600)
}
```

Replace the binary branch at the end of `attachmentTool`:

```go
	if s.publicURL == "" {
		path, err := s.saveAttachment(a.ID, a.Part, raw)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
			"Part %d (%s, %s, %d bytes) is binary content, saved for you to fetch rather than returned inline:\n%s\nFrom the host: docker cp your-mail-mcp:%s .",
			a.Part, ctype, filename, len(raw), path, path)}}}, nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
		"Part %d (%s, %s, %d bytes) is binary content, served by link rather than inline. Download it (link valid %d minutes):\n%s",
		a.Part, ctype, filename, len(raw), int(attachmentLinkTTL.Minutes()), s.attachmentURL(a.ID, a.Part))}}}, nil, nil
```

Update the `attachment` tool description at `mcp.go:768`: replace "other binaries as a short-lived signed download link" with "other binaries as a short-lived signed download link, or as a file path to fetch with docker cp when the server has no HTTP listener".

- [ ] **Step 4: Run the full suite**

Run: `go vet ./... && go test ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add mcp.go mcp_test.go main.go
git commit -m "Save oversized binary attachments to the index for docker cp when there is no HTTP listener"
```

---

### Task 8: Live test: bare image answers `tools/list`; recent channel against a real server

**Files:**
- Modify: `live_test.go`, `testdata/live/compose.live.yaml`

**Interfaces:**
- Consumes: the image built by the live harness.
- Produces: `TestLiveBareImageAnswersToolsList`, and an assertion in the existing live flow that `<account>-recent/INBOX` exists after the first pass.

- [ ] **Step 1: Write the failing tests**

In `live_test.go`, add:

```go
// TestLiveBareImageAnswersToolsList is the property every directory checks:
// the shipped image, run with no environment and no volumes, speaks MCP on
// stdin and lists its tools.
func TestLiveBareImageAnswersToolsList(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	img := buildLiveImage(t)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"live","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n"
	cmd := exec.Command("docker", "run", "-i", "--rm", img)
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	if got := strings.Count(string(out), `"name":"`); got < 11 {
		t.Fatalf("want at least 11 tool names in tools/list, got %d:\n%s", got, out)
	}
}
```

`buildLiveImage` is whatever helper `live_helpers_test.go` already uses to build the image for `TestLive`; if it is inlined there, extract it into a helper returning the tag.

In `TestLive`, after the first successful sync assertion, add:

```go
	if out := composeOutput(t, "exec", "-T", "your-mail-mcp", "ls", "/mail"); !strings.Contains(out, "live-recent") {
		t.Fatalf("recent channel did not create its maildir; ls /mail:\n%s", out)
	}
```

using the account name the live fixture configures (check `testdata/live/accounts.json`) and the existing compose-exec helper name in `live_test.go:38`.

Add `command: serve` to the `your-mail-mcp` service in `testdata/live/compose.live.yaml`.

- [ ] **Step 2: Run to verify the new test fails against the old image**

Run: `git stash && go test -tags live -run TestLiveBareImageAnswersToolsList -timeout 5m ./... ; git stash pop`
Expected: FAIL on the pre-change image (process exits on missing config).

- [ ] **Step 3: Run the live suite on the new code**

Run: `go test -tags live -run 'TestLive' -timeout 15m ./...`
Expected: PASS. If `MaxMessages` behaves differently from the research notes against GreenMail (for example the recent maildir is created but empty), stop and report; the spec flags this as the one unverified assumption.

- [ ] **Step 4: Commit**

```bash
git add live_test.go live_helpers_test.go testdata/live/compose.live.yaml
git commit -m "Live test: bare image answers tools/list; recent channel creates its maildir"
```

---

### Task 9: README and docs

**Files:**
- Modify: `README.md` (case 1, environment table, accounts section), `CLAUDE.md` (Layout table, Invariants wording on attachments)

- [ ] **Step 1: Rewrite README case 1**

Replace the "Case 1" section (`README.md:183-235`) with:

````markdown
### Case 1 — on your machine, for your machine only

Start the stack without `PUBLIC_URL`. There is no listener, no OAuth and no
port; local clients attach through Docker.

```bash
docker compose up -d
docker compose logs -f          # watch the first sync
```

The first sync populates the maildir. Today's INBOX mail is searchable within
minutes; the full history follows at whatever pace the provider allows, and
`status` reports how far it has got.

**Claude Code**

```bash
claude mcp add your-mail -- docker exec -i your-mail-mcp your-mail-mcp stdio
```

**Claude Desktop, Cursor, Codex, any stdio client**

```json
{ "command": "docker", "args": ["exec", "-i", "your-mail-mcp", "your-mail-mcp", "stdio"] }
```

Each session is a bridge into the running container, so every client sees the
same index and the same sync. Close the client and the session goes with it.

**Without a running stack**

`docker run -i --rm -v index:/index -v mail:/mail -v ./accounts.json:/config/accounts.json:ro ghcr.io/wildsurfer/your-mail-mcp` starts a daemon for the life of one session. Fine for a look; use compose for anything you want kept fresh.
````

Confirm `compose.yaml` sets `container_name: your-mail-mcp` so the `docker exec` target is stable; add it if absent.

- [ ] **Step 2: Environment table and attachments**

In the `PUBLIC_URL` row of the environment table, change "required" to "optional; unset means no HTTP listener and no OAuth". In `OAUTH_PASSPHRASE`, "required when PUBLIC_URL is set". In "What it cannot do", after the attachment sentence, add: "Without an HTTP listener, an oversized binary is saved under `/index/attachments/` and the tool returns the path to `docker cp`."

In `CLAUDE.md` Layout table, add a row: `bridge.go` | `stdio` bridge to the daemon socket. In the Invariants attachment bullet, after "signed link to `GET /attachment/{id}/{part}`", add "(or, with no listener, a file under the index for `docker cp`)".

- [ ] **Step 3: Commit**

```bash
git add README.md CLAUDE.md compose.yaml
git commit -m "Document stdio attach, optional HTTP, and attachment paths without a listener"
```

---

### Task 10: Version and release check

**Files:**
- Modify: `main.go` (`Version: "0.3.0"` if not done in Task 1), `server.json` (`version`, add a `stdio` transport entry)

- [ ] **Step 1: Bump and register**

In `server.json`, set `"version": "0.3.0"` and add to `packages` a second entry:

```json
{
  "registryType": "oci",
  "identifier": "ghcr.io/wildsurfer/your-mail-mcp:0.3.0",
  "transport": { "type": "stdio" },
  "runtimeArguments": [
    { "type": "positional", "value": "stdio" }
  ]
}
```

Mark `PUBLIC_URL` and `OAUTH_PASSPHRASE` as `"isRequired": false` in the existing streamable-http entry.

- [ ] **Step 2: Full verification**

Run: `go vet ./... && go build ./... && go test ./... && go test -race ./... && go test -tags live -run TestLive -timeout 15m ./...`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add main.go server.json
git commit -m "Bump version to 0.3.0 and register the stdio transport"
```

---

## Self-review

**Spec coverage, section 7 against tasks:**

| Spec requirement | Task |
|---|---|
| `serve` daemon, Unix socket at `INDEX/mcp.sock` | 2, 3 |
| HTTP only with `PUBLIC_URL`; passphrase required with it | 1 |
| `stdio` bridges if socket exists, else daemon in-process, exit on EOF | 3 |
| `CMD ["stdio"]`, compose `command: serve` | 3 |
| Same tools and syncer on both attach points | 2 |
| Missing or empty accounts file legal; `status` explains | 1 |
| Local HTTP case retired in docs | 9 |
| `refresh` full pass, 20s wait, joins running pass, resets ticker | 4 |
| INBOX-only branch removed | 4 |
| Backed-off account named in refresh reply; named refresh bypasses | 4 |
| Deadline expiry not counted as failure | 4 |
| `status` gains running / started / last duration | 4 |
| `recent` channel, `MaxMessages 1000`, INBOX, before `full` | 5 |
| Read-only directives on both channels | 5 (test asserts) |
| `Complete` set once, never unset | 5 |
| Progress from mbsync's `N: +a/b` | 5 |
| Note on `search`, `count`, `ids` when incomplete | 6 |
| Attachments by path over the socket; link when HTTP is up | 7 |
| Live test: bare `docker run -i` answers `tools/list` | 8 |
| `MaxMessages` verified against a real server before release | 8 |

**Placeholder scan:** none.

**Type consistency:** `Sync(ctx, account string) (int, error)` in Tasks 4 and 5; `runCmd` returns `(string, error)` from Task 5 onward, and Task 5 updates the Task 4 tests; `record(account, out string, err error)` from Task 5; `AccountStatus` fields added in 4 (`Running`, `StartedAt`, `LastDuration`) and 5 (`Complete`, `Pulled`, `Total`) are the names used in 6. `pickMode` / `bridge` / `bridgeIO` / `serveSocket` / `saveAttachment` / `mirrorNote` / `inProgress` match between definition and use.
