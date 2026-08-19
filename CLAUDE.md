# your-mail-mcp

Self-hosted MCP server that exposes Ivan's mail to any MCP client. Repo: `wildsurfer/your-mail-mcp`.

This file is a briefing on decisions already made. Detail lives in `docs/research/`; do not restate it here, point at it.

## Status

Nothing is built yet. The directory holds research and this briefing. The working system it replaces is at `/Volumes/2TB/Mail` (see "Reference implementation" below).

## Decisions

### Build, do not adopt

The landscape was surveyed and no existing project fits. The candidates are all live-IMAP servers with a send path, which is the opposite of both architectural choices below. Adopting one would mean rewriting its data layer and removing its default capability, so it is cheaper to build. See `docs/research/email-mcp-landscape.md`.

### Data layer is notmuch over maildir, not live IMAP

Mail is mirrored to a local maildir by mbsync and indexed by notmuch. The server queries notmuch. It does not open an IMAP connection to read.

Live IMAP was rejected on mailbox size. The account holds roughly 36,000 messages across 14 folders. Search over IMAP means either round-tripping SEARCH per query against a provider that is slow and rate-limited, or maintaining a local index anyway. notmuch already is that index, with full-text search, threading, and stable message IDs. iCloud IMAP specifics are in `docs/research/icloud-imap-research.md`.

### Read-only is enforced inside the process, at every write path

Read-only is enforced on the write paths themselves. It is not implemented by leaving tools out of the registered tool list.

The cautionary example is `codefuturist/email-mcp`. It gated tool registration on a read-only flag, so the mutating tools were not exposed to the client. A scheduler timer inside the process sent mail anyway, because it called the send path directly and never passed through the gate. The tool list is a description of the interface, not a security boundary. Anything inside the process that is not an MCP tool handler (timers, retries, background jobs, cleanup) bypasses it entirely.

For the concrete mechanism, see "The send gate is a type, not a guard function" under Implementation language. The enforcement is a constructor returning a refusing implementation, so the check cannot be omitted at a call site.

### Composing a draft is the default; the server does not send

This is a product requirement, not a preference, and it is the project's identity. The default behaviour of the server is to compose a draft and hand it to a human for review. There is no send.

Sending is opt-in behind an explicit startup flag and is off unless the flag is set. The flag must gate every send path in the process, and gating tool registration is not sufficient. This is the same lesson as read-only above, with the same failure mode if it is done at the registration layer.

The flag is read once, at construction. See "The send gate is a type, not a guard function" under Implementation language.

### Implementation language is Go

This is a long-running network service that holds mail credentials and parses untrusted email, so a small dependency tree matters more than it usually would. A single static binary gives a container of roughly 20MB with no runtime to install, which is what makes the container-first distribution decision below practical.

Raw performance was not the deciding factor. notmuch and Xapian do the heavy lifting; the server parses a query, calls notmuch, formats results, and occasionally appends over IMAP.

The accepted trade-off: `go install` is not how people expect to install MCP servers, where `npx` and `uvx` dominate. Mitigate by shipping release binaries and the container image, so nobody is expected to build from source.

#### Design consequences

These are the point of choosing Go. Write them into the architecture rather than treating them as style notes.

**The send gate is a type, not a guard function.** Do not write a `canSend()` check called at each write site. That is exactly the mistake made twice already: the Rust project we studied repeated its check at around ten call sites, and codefuturist's scheduler sent from outside the gate entirely. A check that must be remembered at every site will eventually be forgotten at one.

Instead, put all mutation behind a single interface. The constructor returns a refusing implementation when the send flag is unset, so no code path capable of sending exists in the process at all. Keep the sending type unexported and obtainable only from that factory. The guarantee is then structural: there is no object to call. The same shape applies to the read-only enforcement above.

**Query notmuch by executing it with JSON output.** Do not bind to the C library through cgo. Shelling out keeps the build fully static, avoids putting a cgo toolchain in the container, and sidesteps version coupling between the Go binary and whatever notmuch is installed. `bin/mailq` in the reference implementation already does exactly this and can be read as a working spec.

#### Starting-point libraries

Starting points, not settled dependencies.

- `github.com/modelcontextprotocol/go-sdk` — the official Go SDK, maintained with Google. Supports stdio, SSE, and streamable HTTP. Its `auth` and `oauthex` packages are useful later for the OAuth work.
- `github.com/emersion/go-imap` and `github.com/emersion/go-message` — the established choices for IMAP and MIME.

### Fallback and reference code

If the maildir approach is ever abandoned for IMAP-direct, fork `bradsjm/mail-imap-mcp-rs`. Do not fork `tecnologicachile/mail-mcp`, the Chilean fork.

Two things in `mail-mcp` are worth copying regardless of which base is used:

- Provider-aware Sent folder logic. The Sent folder name varies by provider and cannot be guessed from a single constant.
- The UIDVALIDITY recheck between search and mutation. A UID obtained from a search is only meaningful under the UIDVALIDITY that was current when the search ran. Re-verify before acting on it, or the mutation lands on a different message.

Comparison and repo-layout notes: `docs/research/mcp-mail-server-repo-shape.md`.

### OAuth is real work but off the critical path

iCloud has no OAuth, so the first working version needs none. Google and Microsoft do, and implementing both is a meaningful amount of work to schedule later.

One constraint on the sequencing: Microsoft 365 has required OAuth for IMAP since October 2022, basic authentication having been retired. Any claim that the server works with all providers depends on OAuth existing. Do not make that claim before it does.

### Credential storage

The current setup keeps app-specific passwords in a dedicated macOS keychain. Moving off the Mac mini means the keychain is gone and the server needs its own encrypted credential store. Use `mailbox-mcp` as the reference: AES-256-GCM with a passphrase-derived key.

### Naming

Naming research is in `docs/research/mcp-mail-server-naming.md` and `docs/research/mcp-naming-family-research-archive.md`.

## Deployment

Decided. The reasoning is recorded so a future session understands the constraints; treat the conclusions as settled.

### Serverless and edge are ruled out

The data layer is a maildir plus a Xapian index. It needs a persistent filesystem, a long-running sync process, and native binaries. Cloudflare Workers cannot run notmuch. Lambda cannot hold a mail spool. Anything edge-shaped is the wrong shape for this workload, so do not evaluate it again.

### Preferred: index stays on the Mac mini, tunnel in front

Keep the maildir and the index where they are and expose the server through Cloudflare Tunnel or Tailscale Funnel. Either one is acceptable.

Both give a public HTTPS hostname over an outbound connection from the machine, with no open ports and no public IP. That matters because of a constraint established earlier: custom connectors are reached from Anthropic's cloud rather than from the user's device. A tailnet-only address is therefore unreachable, while a Funnel or Tunnel hostname is reachable. The mail never leaves the house.

### Fallback: a small VPS

Hetzner or DigitalOcean, both of which Ivan already has accounts with. About five dollars a month covers it, since the mirror is roughly 2.5GB. AWS would also work and adds complexity for nothing gained.

The real cost of this option is the threat model rather than the money. A full plaintext copy of Ivan's mail moves onto a rented disk reachable from the internet, with the app-specific password sitting beside it. Taking this path requires an encrypted volume and an encrypted credential store, and it should be chosen deliberately.

Two triggers that would push towards the VPS, both already observed on the mini:

- It is headless, so a reboot without auto-login stops the scheduled sync.
- An unmounted `/Volumes/2TB` would silently break the mirror.

### Local Claude Code needs none of this

Claude Code running on the same machine reaches the server over stdio or localhost. Public reachability exists only for phone-initiated sessions and non-Anthropic clients.

### Ship a container image and a compose file

Product requirement, not a preference. The image and compose file are part of what ships. The maildir and the index are mounted volumes; everything else is configured by environment variable.

The research found this is where the field consistently fails. The most popular IMAP MCP server had its Dockerfile removed. The one project that shipped a proper multi-arch image sits at zero stars and went unnoticed. Doing this well is a differentiator.

A good image also makes the hosting choice reversible, since the same image runs on the mini, on a VPS, or on a NAS.

## Reference implementation: /Volumes/2TB/Mail

A working system, in daily use, being refactored into this project. Read it before designing anything; the hard-won details are in its comments.

| Path | What it does |
| --- | --- |
| `config/mbsyncrc` | mbsync config. One-way read-only mirror of iCloud into `Maildir/icloud/`. The read-only guarantee is four directives: `Sync Pull`, `Create Near`, `Remove None`, `Expunge None`. Password comes from the dedicated keychain via `PassCmd`. |
| `config/notmuch-config` | notmuch config. Index at `notmuch/`, mail root at `Maildir/icloud`, deliberately the account directory and not its parent. Maildir flags are the single source of truth for read/flagged/draft state. |
| `bin/sync-icloud.sh` | Sync wrapper run by a LaunchAgent every 5 minutes. Checks that `/Volumes/2TB` is actually mounted before writing, unlocks the mail keychain, runs mbsync, then reindexes. Rotates its own log. |
| `bin/mailq` | Read-only notmuch front end for the agent, returns JSON. Subcommands: search, ids, files, show, thread, count, folders, text. Cannot send, delete, move, tag, or sync. |
| `bin/mail-draft` | Composes a `.eml` under `drafts/` and, as a separate step, APPENDs that exact file to the iCloud Drafts mailbox over IMAP. Its entire write surface against the account is one IMAP APPEND into one hardcoded mailbox. No STORE, EXPUNGE, delete, move, or rename. Splitting compose from push means a failed upload never loses the text and every pushed draft leaves a reviewable artifact. |
| `bin/install-keychain.sh` | Creates the dedicated `icloud-mail` keychain and stores the two app-specific passwords. Run by hand. Every `security` call names its keychain by full path, because the default keychain on this Mac is not `login`. |
| `.claude/skills/email/` | The `email` skill. Tells the agent to read the mirror and compose drafts, never to connect to IMAP for reading. |
| `.claude/settings.json` | Permission allow and deny rules. Allows the two wrappers and read-only `notmuch search/show/count`. Denies network tools, sendmail/msmtp/mail, `security`, `mbsync`, `notmuch tag/dump/restore`, and `rm`. |

The shape to carry forward: the mirror is read-only by configuration, reads go through a wrapper that structurally cannot write, and the one write path is a single narrow operation into a single mailbox.

## Research files

`docs/research/`

- `icloud-imap-research.md` — iCloud IMAP behaviour, quirks, limits.
- `email-mcp-landscape.md` — survey of existing email MCP servers. **Not yet copied in.**
- `mcp-mail-server-naming.md` — naming options and reasoning. **Not yet copied in.**
- `mcp-mail-server-repo-shape.md` — repo layout, comparison of candidate bases. **Not yet copied in.**
- `mcp-naming-family-research-archive.md` — naming research archive. **Not yet copied in.**

The four marked files exist in earlier Cowork session output folders but could not be read across sessions. Copy them in from those sessions, or ask Ivan to place them in `docs/research/`. Until then the claims in this briefing that cite them are recorded from Ivan's own summary and have not been checked against the source.
