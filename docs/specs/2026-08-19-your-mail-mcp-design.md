# your-mail-mcp — v1 design

Date: 2026-08-19. Status: approved, ready for an implementation plan.

A self-hosted MCP server that exposes one or more mail accounts to any MCP client,
read-only, over authenticated HTTP. It mirrors each account to a local maildir,
indexes them into a single notmuch database, and answers queries from that index.
It cannot send, delete, move, tag or modify anything on any account.

This document is the v1 contract. Decisions already recorded in `CLAUDE.md`
(Go, notmuch over maildir, read-only, container-first) are assumed and not
re-argued here; where v1 changes one of them, it says so explicitly.

## Scope

In scope for v1:

- Remote streamable HTTP transport with OAuth 2.0 authentication.
- Multiple IMAP accounts from mixed providers, served from one index.
- Nine read tools.
- Sync and indexing inside the same process, on a timer and on demand.
- A container image and a compose file as the shipped artifact.

Out of scope for v1, listed so it does not creep in:

- Sending mail. Composing drafts. Any write to any account.
- Attachment export.
- XOAUTH2 against Gmail or Microsoft as mail providers.

## Host and provider agnosticism

The container is the unit of deployment. Where it runs is a user decision the
program knows nothing about: a laptop, a home server, a VPS, a NAS. Nothing in the
code refers to a particular machine, filesystem layout or operating system.

The program makes exactly two assumptions about an account: it speaks IMAP, and it
has an `INBOX`. RFC 3501 mandates the second, so it is the one folder name that is
safe to hardcode. Folder hierarchy, delimiter convention, display language, TLS
mode and the names of the junk and trash folders all differ by server, and none of
them appear as constants in the code. There is no provider table and no
per-provider branch.

Provider-specific knowledge lives in README notes instead: that iCloud logs in
with a short name rather than a full address and throttles concurrent connections,
that Gmail needs an App Password with 2-step verification enabled and keeps a copy
of everything in `[Gmail]/All Mail`, that Dovecot servers commonly use `INBOX.` as
a prefix. These are facts a user needs while writing the config, not behaviour in
the program.

A tunnel in front of the server (Cloudflare Tunnel, Tailscale Funnel) is one
documented deployment example. It is not the architecture.

## Configuration

Process-level settings are environment variables:

| Variable | Meaning |
|---|---|
| `CONFIG` | path to the accounts file |
| `MAILDIR` | maildir root; each account gets a directory under it |
| `INDEX` | notmuch/Xapian index directory |
| `SYNC_INTERVAL` | full-sync period, default 5m |
| `SYNC_TIMEOUT` | per-account deadline for one mbsync run, default 1h |
| `OAUTH_PASSPHRASE` | the single passphrase gating consent |
| `PUBLIC_URL` | external URL, used in OAuth metadata documents |
| `LISTEN_ADDR` | address to bind, default `:8080` |
| `INIT_MIRROR` | opt-in for syncing into an empty directory that is not a mount point |

Accounts live in a JSON file, parsed with `encoding/json` so no dependency is
added. Secrets stay in the environment and are referenced by `${VAR}`, expanded at
load:

```json
{ "accounts": [
  { "name": "work",     "host": "imap.gmail.com",   "user": "…", "password": "${WORK_PASS}" },
  { "name": "personal", "host": "imap.mail.me.com", "user": "…", "password": "${PERSONAL_PASS}" }
]}
```

Optional per-account keys: `port`, `tls` (`imaps` default, `starttls`, `none`),
`patterns` (mbsync folder patterns, default `*`), `exclude_folders`.

Deliberately **not** configurable, with the reasoning recorded so it is not
reintroduced by habit:

- **Pipeline depth is pinned to 1.** It caps IMAP commands in flight; iCloud
  returns `Server Busy` above that. The conservative value is correct on every
  server and costs only first-sync speed, which happens once per account. A knob
  whose safe setting is always the same is a default, not a knob.
- **`SubFolders` is pinned to `Verbatim`.** It controls the *local* hierarchy
  layout, and mbsync discovers the server's delimiter itself. The setting only
  matters when another mail client reads the same maildir, and nothing does.
- **`AuthMechs` is left unset**, so mbsync negotiates. The reference implementation
  pinned `LOGIN` for iCloud without evidence it was required. If a real server
  fails during testing, it comes back as a per-account key.

## 1. Shape

One container, one process, two long-lived things inside it:

```
your-mail-mcp (Go, static binary)
├── HTTP        streamable MCP + OAuth endpoints
├── ticker      per account: mbsync → then notmuch new once
└── exec        notmuch --format=json, per tool call
```

No cron, no supervisor, no second container.

The image is Debian-slim carrying `isync`, `notmuch`, `w3m`, `ca-certificates` and
the binary, built multi-arch because deployment hosts vary.

**Correction to a figure in `CLAUDE.md`:** the ~20MB image estimate assumed a
server-only container. With mbsync, notmuch, Xapian and w3m included, expect
roughly 150–250MB. The static binary still means there is no runtime to install,
which was the point of choosing Go.

notmuch is invoked by executing it with JSON output, never through cgo. This keeps
the build static and decouples the binary from the installed notmuch version.
`bin/mailq` in the reference implementation is a working spec for this.

### Generated configuration

The mbsync and notmuch configuration files are generated at startup from the
accounts file into a private temporary directory the process owns and removes
on exit, rather than mounted from the host. That directory is ordinary
container filesystem, not a tmpfs: the compose file mounts none, so the file's
protection is its 0600 mode and the container boundary, nothing more. One mbsync channel
per account. The four directives that constitute the read-only guarantee —
`Sync Pull`, `Create Near`, `Remove None`, `Expunge None` — are therefore written
by the program, per channel, and cannot be edited into something that pushes.

This also removes a documented failure mode: a blank line inside a Channel block
silently turns those four directives into inert global options, and nothing
reports it.

## 2. Sync

```go
// one mutex guards every caller
func (s *Server) sync(ctx context.Context, account, folder string) (added int, err error)
```

- **Ticker.** Every `SYNC_INTERVAL`, a full pass over every account and folder,
  then one `notmuch new` for the whole database.
- **`refresh` tool.** `INBOX` only, for one account if named and otherwise for all
  of them, then `notmuch new`. An incremental INBOX pass fits inside a tool call;
  a full pass over every folder of every account does not, and the client would
  time out. `INBOX` is the only folder name the program hardcodes.
- **Failure isolation.** A failing account is logged and skipped; the pass
  continues. With six accounts, one expired password must not stop the other five.
  The error is retained and surfaced through `folders`.
- **Mutex.** Two overlapping mbsync runs produce the `near side box cannot be
  opened anymore` failure recorded in the reference implementation's README. One
  at a time, accounts synced sequentially. `refresh` returns "sync already
  running" rather than queueing. An account that fails twice in a row is
  backed off exponentially from the sync interval, capped at an hour, so a
  provider outage or a Gmail quota lockout is retried tens of times a day
  rather than hundreds; a manual `refresh` of that account bypasses the
  backoff, because a person asking is not a ticker looping. Sequential syncing is a deliberate ceiling: per
  account locks and parallel passes are the upgrade if a full pass gets slow.
- **Empty-volume guard.** The question worth asking is the one the reference
  implementation asked of `/Volumes/2TB`: is the storage actually there? An empty
  directory answers it only together with two other signals.

  The first is whether the directory is a mount point, which the program
  determines by comparing its device number with its parent's. A mounted volume
  that is empty is a genuine first run and syncs with no opt-in, so the shipped
  compose path has no initialisation step. That check alone is not reliable
  inside a container, though: a bind-mounted or named volume does not always
  change device number from the container's point of view, so an actually
  missing mail volume can still look mounted.

  The second signal is a sentinel file (`mirror-exists`) written into the
  `INDEX` directory once a sync pass leaves the maildir non-empty. It lives in
  `INDEX`, a separate volume from the maildir, specifically so it survives the
  maildir vanishing: an empty maildir next to a sentinel that remembers a
  previous mirror means the mail volume went missing, not that this is day one,
  and the guard refuses regardless of what the mount-point check says. It is
  checked first, being the stronger evidence of the two.

  Only once both signals come back negative — no sentinel, and not a mount
  point — does the remaining case get refused: a plain empty directory, equally
  a fresh maildir and a path whose volume was never mounted, since syncing into
  it re-downloads every account into a directory that vanishes the moment the
  mount appears. `INIT_MIRROR=1` overrides all of this for an operator who
  really does want to start over in an ordinary directory. A maildir that
  already holds anything is an existing mirror and is never questioned.

  Residual gap: if the maildir and the index vanish together — the whole data
  volume gone, not just the mail one — the sentinel disappears with it and this
  still reads as a first run. There is no signal left inside the container to
  catch that case.

  An earlier version of this spec used a marker file inside the maildir itself
  plus a mandatory flag on every first run. That threw away the signal: a
  marker inside the same volume it is meant to detect the loss of cannot
  distinguish "never initialised" from "volume missing", so the flag existed
  only to paper over the ambiguity, and every operator paid a two-step first
  run for it. The sentinel above avoids that by living on the other side of the
  volume boundary, in `INDEX` rather than in the maildir, and by being a second
  signal alongside the mount-point check rather than a replacement for it, so a
  genuine first run still needs no flag.

- **Timeouts and throttling.** Every sync has a deadline. Provider throttling is
  logged and left for the next tick, never retried in a tight loop.

### SPECIAL-USE discovery

Folder names are not portable across servers or languages — Junk is Spam,
Papierkorb, Corbeille, Papelera, Корзина, and Gmail localizes inside its `[Gmail]/`
prefix too. A name list cannot be correct for a set of accounts in several
locales.

At startup, and per account, the server issues one IMAP `LIST` and reads the
RFC 6154 attributes, caching which mailbox is `\Junk` and which is `\Trash`
whatever its display name. The request retries as a plain `LIST` when the
extended `RETURN (SPECIAL-USE)` form is rejected — iCloud advertises the
capability and then refuses that syntax with a parse error, while its plain
`LIST` still carries the attributes.

A configured `exclude_folders` wins outright. Otherwise the exclusion set is
the union of the advertised attributes and the built-in list of common
English folder names, deduplicated. The tiers were originally exclusive, and
live iCloud showed why that loses mail it should not: its `LIST` marks only
`\Trash`, so a one-entry attribute list won its tier and the folder named
Junk stayed searchable.

Each mailbox name is translated from the server's own hierarchy delimiter —
which the `LIST` response for that mailbox carries, and which is server- and
namespace-specific (Dovecot's `.`, as in `INBOX.Trash`, is the common
non-`/` case) — to the `/` a maildir path and a `folder:` query both use. A
name returned verbatim never matches what `SubFolders Verbatim` actually
wrote to disk on such a server, so the translation runs before the name is
cached or excluded.

This is the one place an IMAP client exists in the process. Its consequences are
stated in section 5.

## 3. Tool surface

Nine tools: the eight from the reference `mailq`, mapped one to one, plus
`refresh`.

| Tool | Arguments | Returns |
|---|---|---|
| `search` | query, account?, limit, offset | thread summaries (JSON) |
| `ids` | query, account? | matching message ids |
| `files` | query, account? | maildir paths, container-internal |
| `show` | id | one message, headers and body |
| `thread` | id | the whole thread |
| `text` | id | plain-text body, HTML converted through w3m |
| `count` | query, account? | integer |
| `folders` | — | accounts, their folders, tags, last sync, last error |
| `refresh` | account? | number of new messages |
| `status` | — | sync health per account: first-sync completion, last sync, messages indexed, errors, backoff |

Four rules apply to all of them, each implemented in exactly one place so no
individual tool can forget it:

- **Untrusted-content markers.** Every body, subject and sender line that reaches
  the model passes through one `render()` chokepoint that wraps it. The server
  reads attacker-chosen text as its normal operation; this is the most effective
  structural defence available.
- **Query validation before notmuch sees the query.** Unknown prefixes and
  nonexistent tags are rejected with an explanation. Without this, a typo and an
  empty mailbox are indistinguishable, and the model confidently reports that no
  such mail exists.
- **Size caps.** Bodies are paginated with an explicit `truncated` flag and a byte
  offset. `search` has a modest default limit. Context window is the real
  constraint on mail tools.
- **Exclusions.** The junk and trash folders identified per account are kept out of
  search by default and can be included per call. The most adversarial mail should
  not reach the model by accident.

### Accounts in queries

One notmuch database covers every account. `mail_root` is `$MAILDIR` and each
account occupies a top-level directory under it, so folders read `work/INBOX` and
`personal/Archive`. The optional `account` argument becomes a `path:<name>/**`
clause; omitting it searches everything. One helper function, no extra tools.

This is the arrangement the landscape research recommends: multi-account belongs
to the sync layer, and the index handles the rest without a connection pool, a
credential broker or a per-provider quirk matrix inside the server.

A consequence worth knowing: notmuch keys messages by Message-ID, so mail
addressed to two of your accounts is one message with two files. A search scoped
to `work` can therefore return a message that also lives in `personal`, and
`files` returns both paths. This is correct, and it looks surprising the first
time.

### Folders and status

`folders` enumerates the maildir on disk — every directory holding `cur`, `new`
and `tmp` — and returns the names in the exact form a `folder:` query needs,
grouped by account, alongside the tags in the index, each account's last
successful sync and its last error. A broken account is therefore visible without
reading logs. The reference `mailq` returned tags alone under this name, which
answers nothing when the layout is unknown.

Attachments are listed by filename, media type and size in `show` and `thread`,
and served one part at a time by the `attachment` tool. Serving was excluded
from the first release; it was added after real use showed the listing alone
answers no question a reader actually has. The risks the exclusion guarded are
handled structurally instead: a 5MB cap bounds the context cost, and binary
parts, which cannot carry the text markers, return as typed image or blob
content whose tool schema names them attacker-authored data. Text parts pass
through `render` like all other mail text.

Parts over the cap cannot travel through model context at all, so the server
also serves raw attachment bytes at `GET /attachment/{id}/{part}`, outside
the MCP content path. The endpoint accepts a bearer token, or an HMAC-signed
link scoped to exactly one part and valid for 15 minutes, which the
`attachment` tool returns in place of an oversized part. The signing key is
generated per process; a restart invalidates outstanding links.

## 4. Authentication

Claude's connector documentation makes OAuth 2.0 with Dynamic Client Registration
the supported path across every surface, and rules out a machine-to-machine
`client_credentials` grant: every connection requires user consent. A fixed bearer
token via `static_headers` exists but is beta and administrator-scoped, so it is
not the basis for v1.

The resource-server half is implemented directly rather than through the Go SDK's
`auth` package: bearer verification, the 401 shape and both metadata documents are
about twenty lines of standard library (`requireBearer` and `handlePRMetadata` in
`main.go`/`oauth.go`). The 401 shape and the `resource` field are the two things
Claude's connector flow is most sensitive to, and writing them directly keeps them
visible and free of version coupling to the SDK's `auth` package. The SDK is still
used for MCP itself. The authorization-server half is ours, as before.

```
GET  /.well-known/oauth-protected-resource     handlePRMetadata, RFC 9728
GET  /.well-known/oauth-protected-resource/mcp handlePRMetadata, path variant Claude probes first
GET  /.well-known/oauth-authorization-server   RFC 8414 metadata
POST /register                                 DCR, RFC 7591, JSON body
GET  /authorize                                passphrase form and consent
POST /authorize                                verify, issue authorization code
POST /token                                    code and refresh grants, form-urlencoded
POST /mcp                                      protected by requireBearer
```

Requirements taken directly from the documentation, all mandatory:

- Metadata advertises `code_challenge_methods_supported: ["S256"]` and a
  `registration_endpoint`.
- `/token` parses `application/x-www-form-urlencoded`; `/register` parses JSON.
  They need different body parsers.
- Refresh tokens rotate, because DCR registers Claude as a public client. The new
  refresh token is returned in the same response that invalidates the old one.
- A dead refresh token returns `invalid_grant`, not a custom code.
- Redirect URIs accepted: `https://claude.ai/api/mcp/auth_callback` for the hosted
  surfaces, and loopback (`localhost` and `127.0.0.1`) with the port component
  ignored, for Claude Code.
- The protected-resource metadata `resource` field must match the server URL
  exactly as the user enters it, which is what `PUBLIC_URL` is for.
- Every endpoint answers well inside 10 seconds.

**Client ID Metadata Documents** are accepted alongside DCR: a URL-shaped
`client_id` is resolved by fetching the document it names, checking the
document's own `client_id` equals that URL, and using its redirect URIs, with
nothing persisted. The fetch happens before any authentication, so it runs
behind an SSRF guard that resolves the host itself, refuses loopback, private,
link-local and CGNAT addresses, and dials the checked IP directly. Claude
prefers CIMD over DCR when the metadata advertises it, which keeps the
registered-client store from growing with every connection.

**Consent** is a single page: one passphrase field checked against
`OAUTH_PASSPHRASE` in constant time, with a delay after a failed attempt. No
session, no user table, no cookie. The form posts and the authorization code is
issued in the same request. One passphrase gates the whole server; accounts are
not separate identities.

**Storage** is one JSON file, mutex-guarded and fsynced, holding registered clients
and refresh tokens — a handful of records for a single user. Access tokens are
random, in memory, and expire in an hour. Authorization codes are in memory with a
sixty-second TTL. A restart drops access tokens, the client receives a 401 and
refreshes, which is the documented client behaviour anyway. No SQLite, no JWT
library, no key management.

## 5. What is absent, and why that is the security model

Nothing in the process can write to any account. mbsync is configured pull-only by
a file the program generates, and there is no other component that sends, deletes,
moves, flags or appends.

**The one piece of IMAP code is the SPECIAL-USE discovery in section 2.** It opens
a connection, issues `LIST`, reads folder attributes and closes. It never fetches a
message, and the client object does not escape the function that creates it — the
call returns folder names and nothing else. This is a real reduction from "no IMAP
client exists" to "a LIST-only client exists behind one function", and it is the
price of correct junk and trash detection in every language. It is worth stating
plainly rather than discovering later.

That makes `CLAUDE.md`'s principle load-bearing rather than automatic. When
drafting arrives in v2: all mutation behind a single interface, a constructor
returning a refusing implementation when the send flag is unset, the sending type
unexported and obtainable only from that factory.

Against prompt injection the structural defences are: no send path, no delete, no
move, no tag, no attachment export, untrusted-content markers applied at one
chokepoint, and each account's junk and trash kept out of search. An injected
instruction has nothing in the process to reach for.

On the network, the server binds `LISTEN_ADDR`, which defaults to all interfaces
inside the container. Exposure is decided by how the container is published and by
whatever the operator puts in front of it, not by the program. Deployments fronted
by a tunnel can additionally restrict ingress to Anthropic's published egress
range, `160.79.104.0/21`.

On credentials, account passwords are supplied in the process environment and
referenced from the accounts file. The README states this plainly, along with the
consequence: they are visible to anything that can read the container's
environment, and protection at rest is the host's responsibility. No claim of
encryption at rest is made.

## 6. Repository, tests, build order

Five Go files, flat, no internal tree:

```
main.go       environment, accounts file, wiring, ticker
mcp.go        the nine tools, the render() chokepoint
notmuch.go    exec wrapper, JSON passthrough, query validation, account clauses
sync.go       generated configs, mbsync, notmuch new, mutex, guards, SPECIAL-USE
oauth.go      authorization-server endpoints, JSON store
Dockerfile
compose.yaml
README.md
```

Tests use `testing` from the standard library and nothing else. Five that matter:

1. Query validation rejects unknown prefixes and nonexistent tags.
2. `render()` wraps every path by which content reaches the model.
3. PKCE S256 verification, and refresh-token rotation invalidating the old token.
4. The sync mutex prevents overlapping runs, and one failing account does not stop
   the others in the same pass.
5. End to end over `httptest`: register, authorize, token, `tools/call`, against
   two fixture accounts indexed into a temporary directory — which also covers
   account addressing and the shared-Message-ID case.

The fixture maildirs are created by the test setup, so a fresh clone builds and
tests with no mail account and no credentials. SPECIAL-USE discovery is not unit
tested, because a fake IMAP server costs more than it returns; it is verified
against real Gmail and iCloud accounts during phase 2.

Build order, each phase independently useful:

1. notmuch wrapper and the eight read tools, multi-account from the start, against
   the fixture maildirs. This phase runs over stdio as a development harness only;
   stdio is not a shipped transport.
2. Sync: generated configs, ticker, `refresh`, mutex, failure isolation,
   empty-volume guard, SPECIAL-USE discovery. Verified against real Gmail and
   iCloud accounts.
3. Container image and compose file, multi-arch.
4. OAuth authorization server and streamable HTTP.
5. Deployment documentation, including a tunnel as one worked example.

The largest risk, the authorization server, lands after the server already works,
so it never blocks a usable build.
