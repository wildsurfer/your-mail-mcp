# iCloud IMAP → Claude Code: Research Source Material

Retrieved from claude.ai chat "Connecting Claude Code to iCloud Mail via IMAP"
(https://claude.ai/chat/22bb9ee1-552f-4c5e-ae9b-134c45def729) on 2026-07-31.

Two research reports are reproduced below.

- **Report 1** — "Connecting Claude Code to iCloud Mail over IMAP: A Reliability-Focused
  Guide" (226 sources). Architecture comparison, MCP server survey, security model.
- **Report 2** — "Building a Local, Read-Only Mirror of iCloud Mail for Claude Code on a
  Headless Mac Mini M2" (274 sources). **This is the build guide to follow.**

---
---

# REPORT 1 — Connecting Claude Code to iCloud Mail over IMAP: A Reliability-Focused Guide

## TL;DR

- The most reliable architecture is a local mirror rather than live IMAP inside an MCP
  server: use `mbsync` (isync) to sync iCloud into a local Maildir, index it with
  `notmuch`, and let Claude Code search it through its ordinary Bash tool. Authentication
  uses an app-specific password, because Apple offers no OAuth path for self-built
  third-party IMAP clients, and the password is stored in the macOS login Keychain.
- If you want a live MCP server instead, the strongest maintained option is
  `ai-zerolab/mcp-email-server` (Python, uvx, 251 stars, v0.10.0 released May 29, 2026);
  `nikolausm/imap-mcp-server` and `codefuturist/email-mcp` both ship explicit iCloud
  presets. Avoid the AppleScript-based Apple Mail MCP servers on a headless Mac, because
  Mail.app automation needs a logged-in GUI session.
- The dominant risk is prompt injection: email is attacker-controlled content, and Claude
  Code has shell access. Give the agent read-only email access, sync one-way, keep
  credentials out of the model's reach, and use Claude Code deny rules plus the OS sandbox
  to shrink the blast radius.

## Key Findings

### iCloud IMAP and authentication

iCloud Mail exposes standard IMAP and SMTP. The incoming server is `imap.mail.me.com` on
port 993 with SSL/TLS. The outgoing server is `smtp.mail.me.com` on port 587 with
STARTTLS. iCloud does not support POP. These values are confirmed by Apple Support article
HT1625/102525 ("iCloud Mail server settings for other email client apps"), which states:
"Server name: imap.mail.me.com … Port: 993 … Server name: smtp.mail.me.com … Port: 587 …
iCloud Mail does not support POP."

One iCloud-specific quirk matters for setup: **the IMAP username is normally the name
portion of the address, not the full address.** Apple's article 102525 states verbatim:
"Username: This is usually the name of your iCloud Mail email address (for example,
johnappleseed, not johnappleseed@icloud.com). If your email client app can't connect… try
using the full address." The SMTP server, by contrast, expects the full address.

Apple requires an **app-specific password** for any third-party IMAP/SMTP client. Your
normal Apple Account password will be rejected. To generate one, two-factor authentication
must already be enabled on the account. You create the password at account.apple.com under
"Sign-In and Security" then "App-Specific Passwords." The password is displayed only once,
in the format `xxxx-xxxx-xxxx-xxxx`. Per Apple Support article 102654, "You can have up to
25 active app-specific passwords," and "Any time you change or reset your primary Apple
Account password, all of your app-specific passwords are revoked automatically to protect
the security of your account." You can also revoke them individually or all at once from
the same page.

Apple does not offer OAuth/XOAUTH2 to arbitrary third-party IMAP clients. For its own
supported partner apps Apple can authorize access through the Apple Account directly, but
for a self-built IMAP client the app-specific password is the only route. This is the same
situation faced by Apple Mail, Thunderbird, and Outlook, none of which use OAuth against
iCloud. This means you cannot avoid storing a long-lived credential; the mitigation is to
store it in the Keychain and scope its use.

iCloud enforces IMAP connection limits, but Apple does not publish exact numbers.
Third-party empirical testing describes iCloud as the strictest of the major providers.
The Mailbox Taxi troubleshooting guide reports: "Gmail caps each account at roughly 15
simultaneous IMAP connections. Yahoo sits around 10. iCloud is the strictest at about 5,"
and the limit appears to vary by account age. The practical implication for an agent is to
use a single long-lived connection, or better, a local mirror, rather than opening many
parallel sessions that would trip "Too many simultaneous connections" refusals.

### MCP servers for IMAP email

Several genuinely maintained MCP servers exist as of mid-2026. Maintenance figures below
were verified directly against the GitHub repositories.

- **ai-zerolab/mcp-email-server** (Python, `uvx mcp-email-server`): 251 stars, 104 forks,
  latest release v0.10.0 on May 29, 2026, 4 open issues. Generic IMAP/SMTP configured by
  environment variables or a TOML config, with read-only mode (omit SMTP), STARTTLS,
  self-signed cert support (for ProtonMail Bridge), custom Sent-folder detection, and
  multiple accounts. This is the most mature pure-IMAP MCP server. It has no iCloud-specific
  example, but app-specific passwords work by design. Production-usable.
- **nikolausm/imap-mcp-server** (TypeScript, `npx -y imap-mcp-server`): 38 stars, latest
  release v1.2.3 on June 4, 2026, zero open issues. Explicitly lists "Apple iCloud Mail"
  among 15+ provider presets in its setup wizard, with AES-256 encrypted credential
  storage, connection pooling, multi-account support, and tools for search, read, move,
  mark, delete, bulk delete, plus SMTP send/reply/forward. First-class Claude Code
  instructions. Production-usable, and the most iCloud-aware full server.
- **codefuturist/email-mcp** (Node ≥22, `npx @codefuturist/email-mcp`): 48 stars, latest
  release v0.2.1 on Feb 20, 2026, 5 open issues, 11 open PRs. Explicit iCloud domain
  auto-detection (icloud.com, me.com, mac.com), 47 tools including email scheduling, an
  IMAP IDLE watcher, and AI triage. Feature-heavy but early-stage; OAuth2 is explicitly
  labeled experimental. Usable but least battle-tested version number; the broadest feature
  surface also means the largest attack surface.
- **yunfeizhu/mcp-mail-server** (TypeScript, `npx mcp-mail-server`): 37 stars, latest
  v1.2.1 on March 18, 2026, 1 open issue. Recommends app-specific passwords and gives
  `claude mcp add` instructions. Its v1.2.1 changelog documents a large batch of fixes for
  previously broken TO/compound searches, silent failures in delete on read-only mailboxes,
  and false "success" reports on send, so treat it as newly stabilized. Use only if pinned
  to a version you have tested.
- **florianbuetow/imap-mini-mcp** (TypeScript): 6 stars, no published releases.
  Read-oriented by design (reads, searches, moves, stars, and drafts, but cannot send or
  delete), built primarily for ProtonMail Bridge, and its env var description notes
  "Password or app-specific password." The inability to send or delete is a genuine safety
  feature, but the low adoption is a concern for reliability. A toy/young project, though a
  safe one.

Among full servers, `ai-zerolab/mcp-email-server` run in read-only mode is the best balance
of maturity and safety; `nikolausm/imap-mcp-server` is the best if you specifically want an
iCloud preset and encrypted credential storage.

The two Apple Mail MCP servers (`patrickfreyer/apple-mail-mcp`, 155 stars, v3.1.7 June 13,
2026; `sweetrb/apple-mail-mcp`, 29 stars, v1.5.0 April 20, 2026) are AppleScript bridges to
Mail.app, not IMAP clients. They do not take IMAP credentials directly; iCloud works only
if the account is already configured in Mail.app. On a headless Mac they are problematic
because Mail.app automation requires a logged-in GUI session. The sweetrb project also
documents a fixed bug (v1.4.0) where reply/forward sent empty message bodies when spawned
as a background process, exactly the way Claude Code would invoke it, which is a useful
warning about AppleScript reliability under automation.

The Python library `ikvk/imap_tools` (830 stars, v1.13.0 May 12, 2026, 1 open issue, no
external dependencies) is not an MCP server but is an excellent building block if you write
your own thin wrapper. It exposes search, fetch with `mark_seen=False` (peek without
marking read), folder listing, copy, move, flag, and delete.

### Native macOS approaches

**Reading Mail.app's data directly.** Apple Mail keeps a SQLite metadata index at
`~/Library/Mail/V10/MailData/Envelope Index` on recent macOS (V10 on Sonoma, Sequoia, and
Tahoe; V9 on Ventura). It holds message metadata plus short body snippets and can be read
while Mail is running because SQLite WAL mode permits concurrent reads. Full message bodies
live as `.emlx` files under `~/Library/Mail/V10/<UUID>/.../Messages/`. Reading the index
directly is roughly a thousand times faster than iterating messages via AppleScript. There
are two problems on a headless Mac Mini. First, Mail.app must actually be running and
syncing, which needs a GUI login session. Second, reading `~/Library/Mail` requires Full
Disk Access, a TCC permission that cannot be granted over a plain SSH session (Apple
developer forums and Jamf community threads confirm this explicitly) and normally needs the
GUI System Settings pane or an MDM configuration profile. This makes the Envelope Index
approach a poor fit for a headless box unless someone grants Full Disk Access to the
terminal/SSH binary (`/usr/libexec/sshd-keygen-wrapper`) while present at the machine or
over screen sharing.

**AppleScript/JXA scripting of Mail.app.** AppleScript automation of Mail.app is unreliable
without a GUI login. Apple's own developer documentation (Technote 2083 on execution
contexts) and multiple forum threads confirm that GUI scripting and NSWorkspace-based
launching fail or crash outside an Aqua session. On a Mac Mini accessed only over
SSH/mosh/tmux, this route should be avoided.

**Local sync into a Maildir (recommended).** `mbsync` (isync) pulls iCloud IMAP into a
local Maildir on disk. This is more robust for an agent than live IMAP for several concrete
reasons: the agent never touches the network, so it cannot trip iCloud's connection limits;
it works offline and survives network hiccups; and it gives deterministic, static
filesystem access with no session state to manage. A launchd job runs mbsync on a schedule,
and the agent reads static files. `notmuch` or `mu` indexes the Maildir for fast search
(notmuch handles millions of messages easily), or plain ripgrep works for simple needs.
iCloud has folder-naming quirks (spaces in "Sent Messages" and "Deleted Messages"), which
the sample config below handles with `SubFolders Verbatim` and explicit patterns.

**CLI mail client as a tool.** `himalaya` is a Rust CLI email client (6.6k stars, v1.2.0
stable Feb 19, 2026) with an explicit iCloud configuration section, JSON output via
`--output json`, and IMAP/Maildir/notmuch backends. Its documentation records the same
iCloud username quirk Apple documents ("IMAP login = name of your iCloud Mail email
address, for example johnappleseed, not johnappleseed@icloud.com; SMTP login = full iCloud
Mail email address"), uses `imaps://imap.mail.me.com:993` and STARTTLS SMTP, and notes that
an app-specific password is required. For many setups, giving Claude Code a small wrapper
around himalaya or the imap-tools library through its Bash tool is simpler and more
reliable than running a full MCP server, because there is less moving machinery and the
tool surface is small and reviewable. Note two himalaya caveats: its README currently tracks
an unreleased v2 while the released binary is v1.2.0, and v2 removes the built-in keyring in
favor of external tools like `pass`.

### Claude Code integration mechanics

Claude Code registers MCP servers with `claude mcp add`. For a local stdio server the form
is `claude mcp add <name> -- <command> [args]`, where the `--` separator divides Claude
Code's own flags from the command being run. Environment variables are passed with `--env
KEY=value` before the `--`. Scopes are `local` (the default, private to you and scoped to
one project's path in `~/.claude.json`), `project` (`.mcp.json` at the repo root,
committable and shared with a team), and `user` (available across all your projects).
Transports are stdio (local subprocess), HTTP (the recommended remote transport and the only
one supporting OAuth), and SSE (deprecated). A stdio server runs as a subprocess with your
full user privileges, so anything that process can read, the model can read, and Claude Code
will not automatically restart it if it crashes.

Alternatives to a full MCP server, in increasing simplicity: a **Claude Code skill** (a
`.claude/skills/<name>/SKILL.md` folder with YAML frontmatter for name and description plus
markdown instructions, loaded on demand when the prompt matches, and able to restrict tools
via `allowed-tools` in frontmatter); a **slash command** (custom commands have merged into
skills, so `.claude/commands/x.md` and `.claude/skills/x/SKILL.md` both create `/x`); or
simply a **shell script** the agent calls through Bash. For a read-only email reader, a
skill that documents the notmuch/himalaya commands plus an allow-list is the lightest and
most auditable option.

### Security

Store the app-specific password in the macOS login Keychain and retrieve it at runtime with
the `security` CLI, so it never sits in plaintext in a config file or shell history.
mbsync's `PassCmd` and himalaya's `auth.cmd` both support pulling a secret from a command,
and `security add-generic-password -T /usr/bin/security` pre-authorizes the retrieval to
avoid interactive prompts.

**The central risk is prompt injection.** Email is content controlled by anyone who can send
you a message. If Claude Code reads that content and also has shell access, a malicious
email can attempt to steer the agent into running commands or exfiltrating data. This is
Simon Willison's "lethal trifecta," coined June 16, 2025: "Access to your private data…
Exposure to untrusted content… The ability to externally communicate in a way that could be
used to steal your data… If your agent combines these three features, an attacker can easily
trick it into accessing your private data and sending it to that attacker." An
email-reading coding agent has all three unless you deliberately remove one, and the
practical way to remove one is to cut off external communication (network egress) via
sandboxing.

Documented real-world precedent underscores this. The EchoLeak class of attacks delivered
hidden instructions inside an email that an assistant then acted on. Cymulate disclosed
Claude Code CVEs in 2025 (CVE-2025-54794 path bypass, CVE-2025-54795 command injection via
crafted whitelisted commands), and researchers have shown agents disabling their own sandbox
to complete a task. The lesson is that permission rules alone are semantic and bypassable;
OS-level enforcement is the real boundary.

Mitigations, in order of importance: grant read-only email access and sync one-way so the
agent cannot send, reply, or delete; keep the credential out of the agent's reach (Keychain,
fetched by the sync job, not by the model); enable the Claude Code OS sandbox and deny rules
so that even a successful injection cannot reach the network or sensitive files; and treat
every message body as adversarial data rather than instructions. Read/Edit deny rules apply
to Claude's built-in file tools and to recognized file commands like `cat`, `head`, and
`sed`, but **not** to arbitrary Python or Node scripts that open files themselves, so the
sandbox (not the permission rules) is what actually stops a script from exfiltrating.

## Details

### Recommended setup: mbsync + notmuch + Keychain (step by step)

1. Enable 2FA on the Apple Account, then generate an app-specific password at
   account.apple.com under Sign-In and Security → App-Specific Passwords. Label it something
   like "macmini-mbsync." Copy it immediately, since it is shown only once.

2. Install tools with Homebrew:

   ```
   brew install isync notmuch
   ```

3. Store the password in the login Keychain (this prompts for the password without echoing
   it):

   ```
   security add-generic-password -a "youraccount" -s "mbsync-icloud-password" \
     -T /usr/bin/security -w
   ```

   Retrieve it later with:

   ```
   security find-generic-password -s mbsync-icloud-password -w
   ```

4. Create `~/.mbsyncrc`. The iCloud IMAP `User` is the name portion of the address, not the
   full address:

   ```
   IMAPAccount icloud
   Host imap.mail.me.com
   User yourusername
   PassCmd "security find-generic-password -s mbsync-icloud-password -w"
   Port 993
   SSLType IMAPS
   SSLVersions TLSv1.2
   AuthMechs PLAIN

   IMAPStore icloud-remote
   Account icloud

   MaildirStore icloud-local
   Path ~/Maildir/icloud/
   Inbox ~/Maildir/icloud/INBOX
   SubFolders Verbatim

   Channel icloud
   Far :icloud-remote:
   Near :icloud-local:
   Patterns "INBOX" "Sent Messages" "Drafts" "Archive" "Deleted Messages" "Junk"
   Create Near
   Expunge None
   SyncState *
   ```

   `Create Near` and `Expunge None` keep this a one-way, read-only mirror: nothing on the
   local side propagates back to iCloud, and mbsync will never expunge mail on the server.
   This is the safety property that makes the whole setup low-risk. (On very old isync
   versions the keywords are Master/Slave instead of Far/Near.)

5. First sync and index:

   ```
   mkdir -p ~/Maildir/icloud
   mbsync icloud
   notmuch setup      # point the database at ~/Maildir when prompted
   notmuch new
   ```

6. Schedule periodic sync with a launchd agent (more headless-friendly than cron, though
   note a LaunchAgent needs the user context to be loaded). A minimal
   `~/Library/LaunchAgents/com.user.mbsync.plist` runs a script containing `mbsync icloud &&
   notmuch new` on a StartInterval of, say, 300 seconds. Because the credential comes from
   the Keychain via PassCmd, the job needs the login Keychain unlocked, which is why a
   logged-in (even if headless) user session is preferable to a pure daemon.

7. Give Claude Code read access. The agent now runs, through its Bash tool:

   ```
   notmuch search from:bank and date:this_week
   notmuch show thread:0000000000000abc
   ```

   or ripgrep directly over `~/Maildir/icloud`. Wrap these in a skill so the agent knows the
   vocabulary.

### Fallback setup: read-only MCP server

If you need live, real-time reads, use `ai-zerolab/mcp-email-server` in read-only mode (omit
SMTP config so no send capability exists). Register it with Claude Code:

```
claude mcp add email-icloud \
  --env MCP_EMAIL_SERVER_IMAP_HOST=imap.mail.me.com \
  --env MCP_EMAIL_SERVER_IMAP_PORT=993 \
  --env MCP_EMAIL_SERVER_IMAP_USER=yourusername \
  --env MCP_EMAIL_SERVER_IMAP_PASSWORD="$(security find-generic-password -s mbsync-icloud-password -w)" \
  -- uvx mcp-email-server@0.10.0 stdio
```

Passing the password through command substitution keeps it out of the config file, though it
can appear briefly in the process list. Pin the version (`@0.10.0`) rather than `@latest` to
avoid a supply-chain rug-pull, since npx/uvx fetch-at-run is a known risk vector. If you
specifically want an iCloud provider preset and encrypted at-rest credential storage,
`nikolausm/imap-mcp-server` is the alternative.

### Claude Code hardening for either setup

In `~/.claude/settings.json` (user scope) or `.claude/settings.json` (project scope):

```json
{
  "permissions": {
    "allow": ["Read", "Bash(notmuch:*)", "Bash(rg:*)"],
    "deny": ["Read(.env*)", "Bash(curl:*)", "Bash(wget:*)", "Bash(rm -rf:*)", "Bash(sudo:*)"]
  },
  "sandbox": {
    "enabled": true
  }
}
```

Rules are evaluated deny-first, and deny always beats allow. The sandbox is the real
enforcement layer: it restricts the Bash tool's filesystem and network access at the OS level
even if a prompt injection bypasses Claude's own decision-making. Combine
`network.allowedDomains` restrictions (or a fully closed network) with the deny rules so a
malicious email cannot cause data to leave the machine. Do not use
`--dangerously-skip-permissions` for this workflow.

## Recommendations

1. **Start with the local mirror (mbsync + notmuch).** It sidesteps iCloud's undocumented
   connection limits, works on a headless Mac Mini over SSH/mosh/tmux, survives network
   interruptions, and hands the agent read-only static files.
2. **Keep it strictly one-way and read-only** using `Create Near` and `Expunge None`. If you
   later want to send mail, add a separate, explicitly-invoked himalaya or msmtp path with
   its own SMTP app-specific password, and require human confirmation before any send.
   Sending should never be a capability the agent has silently.
3. **Store the app-specific password in the Keychain**, never in a dotfile, and fetch it at
   runtime via PassCmd/command substitution. Generate a dedicated password labeled for this
   use so you can revoke it in isolation.
4. **Harden Claude Code** with deny rules for network-egress commands (curl, wget) and the OS
   sandbox enabled, and treat email bodies as untrusted input. This directly breaks the
   lethal trifecta by removing external communication.
5. **Use the MCP server route only if you genuinely need live reads.** Prefer
   `ai-zerolab/mcp-email-server` (v0.10.0) in read-only mode, version-pinned.

Thresholds that would change these recommendations: if Apple ever ships XOAUTH2 for
third-party IMAP, a live token-based MCP server with revocable, scoped tokens becomes more
attractive than a stored app-specific password. If you need sub-minute freshness, a
single-connection live IMAP client beats a polling mirror. If you can keep a GUI session
logged in on the Mac Mini and grant Full Disk Access, the Envelope Index direct-read approach
becomes viable and is extremely fast, but it adds a GUI dependency that undercuts the
headless design.

## Caveats

- iCloud's exact IMAP connection and rate limits are undocumented by Apple; the "about 5
  connections" figure comes from third-party empirical testing (Mailbox Taxi, Nylas), not
  from Apple, and reportedly varies by account age.
- Several MCP servers are young and were buggy until recently: `yunfeizhu/mcp-mail-server`
  shipped a large bug-fix batch in v1.2.1 (March 2026), and `codefuturist/email-mcp` is at
  v0.2.x with OAuth2 marked experimental. Verify and pin the exact version before trusting
  any of them.
- AppleScript and Mail.app-based approaches, including the two Apple Mail MCP servers, are
  unreliable without a GUI login and are not recommended for a headless Mac.
- Full Disk Access to read `~/Library/Mail` cannot be granted over a plain SSH session; it
  requires GUI access to System Settings or an MDM configuration profile.
- Prompt injection defenses reduce but do not eliminate risk. No current technique fully
  solves prompt injection; OWASP ranks it the top LLM application risk, and least privilege
  plus removing network egress is the primary practical defense.
- himalaya's public README currently documents an unreleased v2, while the shipping stable
  binary is v1.2.0; confirm which version you have installed before copying config syntax,
  since v2 changes credential handling.

---
---

# REPORT 2 — Building a Local, Read-Only Mirror of iCloud Mail for Claude Code on a Headless Mac Mini M2

**This is the implementation guide to follow.**

## TL;DR

- Use **isync/mbsync 1.5.1** (installed via Homebrew at `/opt/homebrew/bin/mbsync`) to pull
  iCloud Mail into a local **Maildir**, index it with **notmuch**, and expose it to Claude
  Code through a Skill plus a small JSON wrapper script; this is the fastest, most
  maintained, and lowest-memory stack for a read-only agent mirror in 2026.
- Enforce a one-way, read-only mirror with **Sync Pull**, **Create Near**, **Remove None**,
  and **Expunge None**, store the iCloud app-specific password so a background job can read
  it, and pull on a timer of a few minutes; the biggest headache on a headless Mac is not
  mbsync but the macOS login-Keychain being unreadable to background jobs when nobody is
  logged in at the GUI.
- The two failure modes you will actually hit are the iCloud app-specific password being
  auto-revoked whenever the Apple Account password changes, and a UIDVALIDITY change forcing
  a re-download of one folder; both are recoverable and covered below.

## Key Findings

- **mbsync is the right tool.** isync 1.5.1 (released 11 March 2025) is current, actively
  maintained by Oswald Buddenhagen, written in C, ships as a Homebrew bottle for Apple
  Silicon (Tahoe/Sequoia/Sonoma), and depends only on `openssl@3` and `berkeley-db@5`. It is
  faster and far lighter on memory than OfflineIMAP (Python), and unlike imapsync it is
  designed for IMAP-to-Maildir mirroring rather than server-to-server migration.
- **Maildir is the correct storage format** for concurrent agent reads: one file per message,
  atomic delivery via tmp→new→cur, and flags encoded in the filename. mbsync keeps its own
  state in `.mbsyncstate` and `.uidvalidity` files per folder.
- **iCloud specifics:** host `imap.mail.me.com`, port 993, `SSLType IMAPS`; username is the
  short name (not the full address) in most cases but occasionally the full address; folder
  names include spaces ("Sent Messages", "Deleted Messages"); an app-specific password is
  mandatory because of 2FA.
- **The Keychain-in-background problem is real** and is the single most important design
  decision for a headless box.
- **notmuch is the best index for an agent** because of its JSON output and query vocabulary.

## Details

### 1. Tool selection (2026)

**isync / mbsync — recommended.** The project name is isync; the binary is mbsync. Current
stable is 1.5.1, released 11 March 2025 (Fossies NEWS: "1.5.1 (2025-03-11)"), maintained by
Oswald Buddenhagen. Its changelog notably adds that "UIDVALIDITY change recovery is now
attempted even if both sides of the Channel are affected." The prior 1.5.0 release (2 August
2024) moved config/state to the XDG base-dir spec, added non-ASCII mailbox name support, and
made MaxMessages/MaxSize combinable. mbsync is written in C, has a single dependency chain
(openssl@3, berkeley-db@5), and the Homebrew bottle is available for Apple Silicon on Tahoe,
Sequoia, and Sonoma. Synchronization is UID-based, so it is robust against identification
conflicts. Memory usage is bounded (per-channel, per-direction soft limit defaults to 10
MiB). It stores state in a plain text file per mailbox pair.

**OfflineIMAP — usually not preferred now.** Written in Python, historically prone to
instability reports, and slower. Charl Botha's vxlabs benchmark (2019-07-05, ~11,000 emails /
2 GB) found that "in their default full synchronization modes, mbsync is indeed substantially
faster than offlineimap"; the one exception is that OfflineIMAP's quick folder-change mode is
"slightly faster than mbsync in full synchronization mode" but may miss some flag changes.
OfflineIMAP's remaining advantages are complex folder-name mapping and unusual server
configurations. For a straightforward iCloud mirror it adds a Python runtime dependency and
offers no benefit.

**imapsync — wrong tool for this job.** It is a server-to-server migration utility. It does
not write a local Maildir mirror.

**getmail** is a mail retriever and can write Maildir, but it is oriented at fetch-and-delete
or fetch-and-archive delivery rather than maintaining a synchronized mirror with flag/state
tracking; mbsync's UID-based state model is a better fit.

**Newer Rust/Go tools.** Himalaya (Rust, Pimalaya) reached v1.2.0 on 19 February 2026; a
v2.0.0 has since shipped that removed native keyring support and built-in OAuth flows.
Himalaya is a CLI email client with JSON output and Maildir/IMAP/Notmuch backends, useful for
scripting individual queries but not a scheduled mirroring daemon in the mbsync sense. aerc
(Go) and meli / NeoMutt are interactive clients. None of these displaces mbsync for the
specific task of an unattended one-way Maildir mirror.

*Note on OAuth2:* the Homebrew isync bottle is not compiled with OAuth2/XOAUTH2 support. This
does not matter for iCloud because iCloud uses an app-specific password over AuthMechs
LOGIN/PLAIN, not OAuth.

### 2. Maildir vs other formats

A Maildir folder contains three subdirectories:

- `tmp/` — messages mid-delivery; a writer creates the file here first, then renames into
  `new/`, which makes delivery atomic and safe over NFS.
- `new/` — delivered but not yet seen by any client.
- `cur/` — messages a client has seen; the filename carries flags.

**Filename and flag encoding.** A message in `cur/` looks like
`1464003587.M12345P95754.hostname:2,S`. The part after `:2,` is the info/flags field, with
zero or more flags in ASCII order: `D` draft, `F` flagged, `P` passed, `R` replied, `S` seen,
`T` trashed. New messages in `new/` have no `:2,` suffix. Because each message is a separate
file, a reader can open, index, or grep messages while new mail is being written, with no
locking of a monolithic file. This is the key advantage over mbox.

**mbsync UID storage and state.** Because Maildir has no standard UID store, mbsync uses one
of two schemes:

- `native` (default): UID validity in a `.uidvalidity` file; UIDs encoded in message
  filenames. Faster, space-efficient, human-readable. Can be disrupted if a message is copied
  into the folder without a new filename.
- `alternative` (`AltMap yes`): a Berkeley DB `.isyncuidmap.db` per folder.

Per-channel sync state lives in `.mbsyncstate` (set with `SyncState *` to keep it inside the
near-side Maildir folder). Use `mdconvert` to switch schemes. For a read-only mirror the
native scheme is fine and preferred.

**Disk space.** Expect the local Maildir to consume roughly the same space as the server
mailbox, dominated by attachments. A notmuch Xapian index adds a fraction on top. As a
concrete datapoint, Antoine Beaupré (anarc.at, 2021-11-21) reported a spool of 372,758
messages taking 13 GB, with his notmuch index of 437,261 files processing at about 208
files/sec. Budget accordingly: tens of thousands of messages with attachments can easily be
several GB.

### 3. Full, commented .mbsyncrc for iCloud

*Terminology note on Far/Near vs Master/Slave:* isync renamed Master→Far and Slave→Near in
the 1.4 series. Version 1.5.1 (what Homebrew installs) uses Far/Near. Old configs and blog
posts use `Master :store:` / `Slave :store:`; those keywords still parse, but write new
configs with Far/Near.

```ini
# ~/.mbsyncrc — one-way, READ-ONLY mirror of iCloud Mail into ~/Maildir/icloud
# isync 1.5.x (Far/Near terminology). Binary: /opt/homebrew/bin/mbsync

# ---------- IMAP account (the iCloud server side) ----------
IMAPAccount icloud
Host imap.mail.me.com
Port 993
# iCloud username is usually the SHORT name (e.g. "emilyparker"),
# NOT the full address. If LOGIN fails with the short name, use the
# full address (emilyparker@icloud.com or @me.com).
User SHORTNAME
# App-specific password fetched from the login Keychain (see section 5
# for the headless-background caveat and alternatives):
PassCmd "/usr/bin/security find-generic-password -a SHORTNAME -s mbsync-icloud -w"
# iCloud accepts LOGIN over the TLS channel:
AuthMechs LOGIN
# Implicit TLS on 993:
SSLType IMAPS
# Leave TLS versions at the mbsync default (1.2+). Do not pin to TLSv1.2 only
# unless you hit a negotiation bug; modern iCloud negotiates 1.3.
# System cert store is used by default (SystemCertificates yes); no
# CertificateFile is needed on macOS because the system trust store
# validates Apple's chain.
# iCloud can be flaky under load; limit in-flight commands on the one
# connection to spare the server:
PipelineDepth 1
# 0 = unlimited connect/data timeout; helps on slow first syncs, but a
# finite value (e.g. 60) will surface hangs instead of stalling forever:
Timeout 60

IMAPStore icloud-remote
Account icloud

# ---------- Local Maildir (the near side) ----------
MaildirStore icloud-local
Path ~/Maildir/icloud/
Inbox ~/Maildir/icloud/INBOX
# Verbatim reproduces the server hierarchy on disk, including spaces:
SubFolders Verbatim

# ---------- Channel: strictly one-way, read-only ----------
Channel icloud
Far :icloud-remote:
Near :icloud-local:
# Match every server folder. Quote names with spaces if listing explicitly.
Patterns "*"
# Pull only: never push local changes to the server.
Sync Pull
# Create missing folders ONLY on the local (near) side:
Create Near
# Never create/remove folders on the server; never remove locally either
# unless you want server deletions to prune your mirror. For a pure archive
# that keeps everything, use Remove None.
Remove None
# Never expunge (permanently delete) on EITHER side:
Expunge None
# Keep the sync-state file inside each local Maildir folder:
SyncState *
# Preserve the server's arrival date on local copies:
CopyArrivalDate yes
```

**What each directive does for read-only safety:**

- `Sync Pull` — propagate changes from Far (iCloud) to Near (local) only. Nothing local is
  ever pushed. (Sync sub-flags are New, ReNew, Flags, Delete; `Pull` means all of them,
  one-directional far→near. `Pull New` would pull only new messages and ignore flag updates.)
- `Create Near` — mbsync may create folders locally to mirror the server, but will never
  create a folder on the server.
- `Remove None` — mbsync will never remove a folder on either side. (`Remove Near` would let
  server folder deletions prune local folders.)
- `Expunge None` — mbsync will never permanently delete messages on either side. **This is
  the key line that guarantees it cannot delete anything from iCloud.**
- Because `Sync` is Pull-only, even a locally deleted file cannot propagate a deletion to the
  server.

**Folder mapping.** iCloud exposes INBOX, Sent Messages, Drafts, Archive, Deleted Messages,
Junk, and any custom folders. With `SubFolders Verbatim` and `Patterns "*"`, these map to
on-disk directories of the same names (spaces preserved), e.g.
`~/Maildir/icloud/Sent Messages/`. If you enumerate folders explicitly instead of `"*"`,
quote the ones with spaces: `Patterns "INBOX" "Sent Messages" "Archive" "Deleted Messages"
"Drafts" "Junk"`. A common early error, "near side box … cannot be opened," is caused by
omitting `SubFolders Verbatim`.

**Password retrieval alternatives to `security`:**

- Create the Keychain item first: `security add-generic-password -a SHORTNAME -s
  mbsync-icloud -w 'APP-SPECIFIC-PW'` (optionally `-T /opt/homebrew/bin/mbsync` to
  pre-authorize that binary). mbsync also supports `UseKeychain yes` with an
  internet-password item created via `security add-internet-password -r imap -s
  imap.mail.me.com -a SHORTNAME -w PW`.
- `pass` (password-store): `PassCmd "/opt/homebrew/bin/pass show icloud/mbsync | head -n1"`.
- gpg-encrypted file: `PassCmd "/opt/homebrew/bin/gpg -q --for-your-eyes-only --no-tty -d
  ~/.secrets/icloud.gpg"`.
- 1Password CLI: `PassCmd "op read op://Private/iCloud-mbsync/password"`.
- direnv or a plain 0600 file: `PassCmd "cat ~/.secrets/icloud-app-pw"` (least secure; see
  section 5 for why this is sometimes the pragmatic choice on a headless box).

**Multiple channels / groups.** To sync folders independently (useful for the first sync, see
section 4), define per-folder channels and a group:

```ini
Channel icloud-inbox
Far :icloud-remote:INBOX
Near :icloud-local:INBOX
Sync Pull
Create Near
Expunge None
Remove None
SyncState *

Channel icloud-archive
Far :icloud-remote:"Archive"
Near :icloud-local:"Archive"
Sync Pull
Create Near
Expunge None
Remove None
SyncState *

Group icloud-all
Channel icloud-inbox
Channel icloud-archive
# add more channels as needed
```

Then `mbsync icloud-all` runs every channel, or `mbsync icloud-inbox` runs just one.

**Large-mailbox options:**

- `MaxMessages n` — keep only the newest n messages per folder locally (a partial mirror).
  Leave unset for a full archive.
- `ExpireUnread yes|no` — whether unread messages count toward the MaxMessages expiry.
- `MaxSize 1m` — propagate only a small placeholder for messages larger than the limit; the
  man page warns against setting a size limit on a store you never read directly, so avoid
  MaxSize on the server side for a mirror.
- `CopyArrivalDate yes` — preserve original arrival timestamps locally.

### 4. First sync and scale

**Duration and resumption.** A first full sync of tens of thousands of messages and several GB
will take a while and will likely be interrupted at least once against iCloud. mbsync saves
state incrementally per message in `.mbsyncstate`, so an interrupted first sync resumes where
it left off rather than restarting the folder; already-downloaded messages are not re-fetched.
A practitioner guide (irreal.org, "Configuring mbsync for Apple Mail") notes for iCloud
specifically that a large repository "may drop off after a while … you can just restart the
download and it will take up where it left off."

**iCloud throttling and connection limits.** iCloud runs Dovecot and enforces a
per-user+IP connection cap. Apple does not publish the number, but the exact server error
string observed against imap.mail.me.com (Apple Support Communities thread 253560326) is
`Maximum number of connections from user+IP exceeded (mail_max_userip_connections=10)`, i.e.
the default Dovecot cap of 10. Some third-party migration guidance generalizes iCloud as one
of the stricter providers at "about 5." Under overload iCloud may also return `Server Busy.
Please try again later`, or simply time out. When you exceed the limit or overload the server,
mbsync will report broken-pipe/socket errors.

**Mitigations for a robust first sync:**

- Set `PipelineDepth 1` (or a small value). This limits commands in flight on the single
  connection mbsync uses; it does not reduce the TCP connection count (mbsync uses one
  connection per run) but it does spare the server from too many pipelined commands, which
  the maintainer confirms helps with iCloud-style flakiness.
- Do not run multiple mbsync instances against the account at once, and quit Apple Mail /
  phone Mail during the initial bulk pull so you stay under the connection cap. mbsync locks
  per near-side folder, preventing overlapping runs on the same box.
- Sync folder-by-folder using per-folder channels (section 3) so a failure in one large folder
  does not force re-scanning others.
- If a run dies, wait a few minutes and re-run; it resumes.

**Polling interval.** There is no official mbsync interval. Community practice against iCloud
is on the order of every few minutes at most; overly aggressive retries can trigger further
throttling. For an agent mirror, a 5-minute timer is a reasonable balance.

**Progress line.** mbsync prints `C: 1/2 B: 3/4 F: +13/13 *23/42 #0/0 -0/0 N: +0/7 *0/0 #0/0
-0/0`. C = channels done/total, B = boxes done/total, F = far side, N = near side. Within each
side the four counters are `+` added, `*` flag-updated, `#` trashed, `-` expunged, each as
done/total. Totals grow as mbsync discovers more work. (Older isync labeled these M: master
and S: slave.)

### 5. Automation on a headless Mac

**The Keychain problem — read this first.** A macOS LaunchAgent runs in the user's context but
the login Keychain is locked until the user unlocks it, which normally happens at GUI login.
On a headless Mac accessed only over SSH, there is no GUI login, so a timer-driven LaunchAgent
calling `security find-generic-password` can fail with "User interaction is not allowed" or
return nothing, because the login Keychain is locked or the job lacks the session to unlock
it. A LaunchDaemon runs as root before any user logs in and cannot read a user's login
Keychain at all. This is a well-documented pain point.

**Reliable options on a headless box, in order of preference:**

1. **A separate, dedicated Keychain that you unlock explicitly at the start of the job.**
   Create it once: `security create-keychain -p 'KCPASS'
   ~/Library/Keychains/mbsync.keychain-db`, store the app password there, and have the sync
   wrapper run `security unlock-keychain -p 'KCPASS'
   ~/Library/Keychains/mbsync.keychain-db` before mbsync. The unlock password still has to
   live somewhere the job can read it, so this mostly moves the secret, but it isolates it
   from your login Keychain.
2. **An age- or gpg-encrypted secret file** where the private key/passphrase is supplied
   out-of-band (for a fully unattended box the passphrase still ends up on disk or in the
   plist environment, so the real gain is encryption at rest).
3. **A plain file with 0600 permissions** read by `PassCmd "cat ~/.secrets/icloud-app-pw"`.
   On a single-user headless box that you already trust with SSH keys and a Tailscale
   identity, this is often the pragmatic choice: the app-specific password is low-privilege
   (mail only, revocable, does not expose the Apple Account), and file permissions plus
   full-disk encryption (FileVault) are a reasonable boundary. This is the most reliable
   across reboots because it has no Keychain-unlock dependency.

Given the read-only, low-privilege nature of an iCloud app-specific password, **option 3 (or
option 1 if you want defense in depth) is the sane default for this headless use case.**

**Sample LaunchAgent plist.** Save as `~/Library/LaunchAgents/com.user.mbsync-icloud.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.user.mbsync-icloud</string>

  <key>ProgramArguments</key>
  <array>
    <string>/Users/USERNAME/bin/sync-icloud.sh</string>
  </array>

  <!-- Homebrew on Apple Silicon lives in /opt/homebrew/bin; launchd jobs
       do NOT inherit your shell PATH, so set it explicitly. -->
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/opt/homebrew/sbin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>

  <!-- Run every 5 minutes. launchd coalesces missed runs after wake. -->
  <key>StartInterval</key>
  <integer>300</integer>

  <key>RunAtLoad</key>
  <true/>

  <key>StandardOutPath</key>
  <string>/Users/USERNAME/Library/Logs/mbsync-icloud.log</string>
  <key>StandardErrorPath</key>
  <string>/Users/USERNAME/Library/Logs/mbsync-icloud.err.log</string>

  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
```

The wrapper `~/bin/sync-icloud.sh`:

```bash
#!/bin/bash
set -euo pipefail
export PATH=/opt/homebrew/bin:/opt/homebrew/sbin:/usr/bin:/bin:/usr/sbin:/sbin

echo "=== $(date '+%Y-%m-%d %H:%M:%S') starting mbsync ==="
# If using a dedicated keychain, unlock it here:
# security unlock-keychain -p "$(cat ~/.secrets/kcpass)" ~/Library/Keychains/mbsync.keychain-db

# Keep the machine awake for the duration of the sync + index:
caffeinate -s bash -c '
  /opt/homebrew/bin/mbsync -a
  /opt/homebrew/bin/notmuch new
'
echo "=== $(date '+%Y-%m-%d %H:%M:%S') done, exit $? ==="
```

**Loading/unloading (modern syntax, macOS 11+):**

```bash
# Load:
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.user.mbsync-icloud.plist
# Unload:
launchctl bootout gui/$(id -u)/com.user.mbsync-icloud
# Force a run now:
launchctl kickstart -k gui/$(id -u)/com.user.mbsync-icloud
# Status / last exit code:
launchctl list | grep mbsync
```

The legacy `launchctl load -w` / `unload` still work but are deprecated in favor of
bootstrap/bootout.

**Sleep/wake.** With `StartInterval`, if the Mac is asleep when an interval elapses, launchd
runs the job on wake and coalesces multiple missed intervals into one. If you need runs to
happen even overnight, either keep it awake (`sudo pmset -a sleep 0 disablesleep 1`, or run
under `caffeinate -s` as above) or schedule a wake with `pmset repeat wakeorpoweron`. Note
that launchd runs a `StartCalendarInterval` job on next wake if the machine was asleep, but
not if it was fully powered off at the scheduled time.

**cron vs launchd vs tmux loop.** launchd is the correct mechanism on macOS and handles wake
coalescing; cron still exists but skips (does not defer) jobs missed during sleep. A
tmux-resident `while true; do mbsync -a; notmuch new; sleep 300; done` loop is a legitimate,
simple alternative on a headless box you already run inside tmux: it inherits your interactive
shell's environment (so PATH and any unlocked Keychain from your SSH session are present), it
is trivial to observe and restart, and it sidesteps the launchd/Keychain interaction entirely.
Its downside is that it does not survive reboots unless you also start tmux at boot.

### 6. Indexing and search for the agent

**Why notmuch.** notmuch indexes a Maildir into a Xapian database, is very fast after the
first index, never modifies your mail except optionally to sync Maildir flags, and crucially
emits structured JSON that an agent can parse directly. `mu` (maildir-utils) is a close
alternative that treats the Maildir as canonical and also has `mu find --format=json`. Plain
ripgrep/grep over Maildir works for quick literal searches but has no notion of threads,
addresses, dates, or MIME decoding.

**notmuch setup.** Config file `~/.notmuch-config`:

```ini
[database]
path=/Users/USERNAME/Maildir

[user]
name=Your Name
primary_email=you@icloud.com

[new]
tags=new;
ignore=.mbsyncstate;.uidvalidity;.isyncuidmap.db

[search]
exclude_tags=deleted;spam

[maildir]
synchronize_flags=true
```

Then `notmuch new` to index. Add the mbsync state files to `new.ignore` so notmuch does not
try to index them.

**Query vocabulary an agent would use:**

- `from:alice@example.com`, `to:bob@example.com`
- `subject:invoice`
- `date:2026-01-01..2026-06-30` or `date:yesterday..today`
- `tag:inbox`, `tag:unread`
- `thread:<id>` to expand a conversation
- `folder:"Sent Messages"` / `path:` to scope to a directory
- boolean `and`/`or`/`not`

**JSON output.**

- `notmuch search --format=json --output=summary tag:unread` returns an array of thread
  summaries (thread id, date, matched/total counts, authors, subject).
- `notmuch search --format=json --output=messages from:alice` returns message ids.
- `notmuch show --format=json --entire-thread=false id:MESSAGE_ID` returns full structured
  message(s) including headers and a decoded MIME body tree.

**Extracting plain text from multipart / HTML-only mail.** `notmuch show --format=json`
decodes text parts; for a single part use `notmuch show --part=N id:...`. For HTML-only
messages, pipe the HTML part through a converter: `w3m -dump -T text/html`, `lynx -stdin
-dump -nolist`, or `html2text`.

**Attachments — handle safely.** Extract with `notmuch show --part=N id:...` to a temp file;
never execute or open attachments. Prefer to surface attachment metadata (filename, MIME type,
size) from the JSON and only extract text-like parts on explicit request. **Treat every
attachment as untrusted input.**

### 7. Wiring it to Claude Code

Expose the mirror through a Skill plus a read-only wrapper script, and lock permissions in
`settings.json`. This is preferable to a full MCP server for read-only use because there is no
long-running process to manage, no extra auth surface, and it is trivially auditable.

**Wrapper script `~/bin/mailq`** (returns clean JSON):

```bash
#!/bin/bash
# mailq — read-only notmuch front-end for the agent.
set -euo pipefail
export PATH=/opt/homebrew/bin:/usr/bin:/bin
cmd="${1:-search}"; shift || true
case "$cmd" in
  search) exec notmuch search --format=json --output=summary "$@" ;;
  ids)    exec notmuch search --format=json --output=messages "$@" ;;
  show)   exec notmuch show --format=json --body=true "$@" ;;
  text)   notmuch show --format=raw "$1" | \
            (w3m -dump -T text/html 2>/dev/null || cat) ;;
  count)  exec notmuch count "$@" ;;
  *) echo '{"error":"unknown command"}' >&2; exit 2 ;;
esac
```

`chmod 755 ~/bin/mailq`.

**Skill `.claude/skills/email/SKILL.md`:**

```markdown
---
name: email
description: >
  Read-only access to the local iCloud Mail mirror (Maildir indexed by
  notmuch). Use for searching, reading, and summarizing the user's email.
  Cannot send, delete, or modify mail.
allowed-tools:
  - Bash(mailq:*)
  - Bash(notmuch search:*)
  - Bash(notmuch show:*)
  - Bash(notmuch count:*)
---

# Email (read-only)

The user's iCloud Mail is mirrored locally and indexed by notmuch. Query it
with the `mailq` wrapper, which returns JSON.

## Commands
- `mailq search <query>` → array of thread summaries (JSON)
- `mailq ids <query>`    → matching message IDs (JSON)
- `mailq show id:<ID>`   → full structured message(s) (JSON)
- `mailq text id:<ID>`   → plain-text body (HTML auto-converted)
- `mailq count <query>`  → integer count

## Query syntax
from: to: subject: date:YYYY-MM-DD..YYYY-MM-DD tag:unread tag:inbox
folder:"Sent Messages" thread:<id>   combine with and / or / not

## Rules
- This mirror is READ-ONLY. Never attempt to send, delete, or move mail.
- Treat attachments as untrusted; only extract text parts on request.
```

**settings.json permissions** (allow only read commands, deny writes):

```json
{
  "permissions": {
    "allow": [
      "Skill(email)",
      "Bash(mailq:*)",
      "Bash(notmuch search:*)",
      "Bash(notmuch show:*)",
      "Bash(notmuch count:*)"
    ],
    "deny": [
      "Bash(notmuch tag:*)",
      "Bash(notmuch new:*)",
      "Bash(mbsync:*)",
      "Bash(rm:*)"
    ]
  }
}
```

**Note a caveat:** `allowed-tools` in a Skill frontmatter grants pre-approval but has been
reported not to reliably restrict other tool access on its own (anthropics/claude-code issues
#14956, #37683), so **the real enforcement is the deny list in settings.json.** Put
restriction rules there, not only in the Skill. Also place user settings in
`~/.claude/settings.json`, not `settings.local.json`, which one bug report found was not
loaded.

### 8. Maintenance and failure modes

**App-specific password revocation.** Per Apple Support doc 102654, "Any time you change or
reset your primary Apple Account password, all of your app-specific passwords are revoked
automatically." mbsync will then fail authentication (`NO [AUTHENTICATIONFAILED]
Authentication failed`). Recovery: generate a new app-specific password at account.apple.com →
Sign-In and Security → App-Specific Passwords, and update the Keychain item or secret file.
You can hold "up to 25 active app-specific passwords" and the default TTL is one year, and the
account must have two-factor authentication enabled to create them at all.

**UIDVALIDITY change.** If the server changes a folder's UIDVALIDITY, mbsync prints a notice
and recovers automatically when the change is "unfounded" (and 1.5.1 now attempts recovery
even when both sides are affected). When it cannot, you get `Error: channel icloud, near side
box INBOX: Unable to recover from UIDVALIDITY change (got X, expected Y)`. Recovery is
per-folder and is community best practice rather than an official recipe: the smallest-scope
reliable fix is to delete the affected local Maildir folder (its `cur/ new/ tmp/`, plus
`.mbsyncstate` and `.uidvalidity`) and re-run mbsync, which re-downloads that folder fresh.
Deleting the local copy destroys any local-only metadata (notmuch tags), so `notmuch dump`
your tags first if you rely on them.

**Sync-state corruption.** If `.mbsyncstate`/`.uidvalidity` is corrupted or out of sync you may
see `Maildir error: UID N is beyond highest assigned UID M`, meaning a local message carries a
UID higher than the recorded maximum (usually because a file was moved in without a new name).
Per the maintainer, "you can just adjust the folder's .uidvalidity … you can reset it in the
.mbsyncstate," but note "the incorrectly moved messages which have a uid below the maximal seen
one will never be synced over" until state is reset.

**"lost track of N pulled message(s)"** is a warning, not an error, "typically [caused by]
interruptions/connection breakdowns" per the maintainer; mbsync re-detects and re-attempts
those messages on the next run. It is generally safe to ignore.

**Duplicate messages** can appear if a message is copied between folders without a new filename
(native scheme), or across a UIDVALIDITY reset. notmuch de-duplicates by message-id in search
results; on disk, AltMap/mdconvert or cleaning stale `,U=` infixes resolves persistent cases.

**Verbose debugging.** Run `mbsync -V icloud` for verbose progress, `mbsync -D icloud` for full
debug, or targeted categories, e.g. `mbsync -Dn` (network protocol only) or `mbsync -Ds` (sync
logic). Use `mbsync -l icloud` to list mailboxes a channel would touch without syncing, `mbsync
--dry-run` (`-y`) to compute operations without changing anything, and `mbsync --list-stores`
to verify each store's raw contents.

**Verify the mirror matches the server.** Compare counts: `notmuch count folder:INBOX` locally
against the message count iCloud reports for INBOX. `mbsync -V` prints per-folder message counts
during sync. A `--dry-run` that shows zero pending far→near operations indicates the mirror is
current.

**Backups / Time Machine.** The Maildir plus notmuch Xapian DB can be many GB of many small
files, which is slow for Time Machine and redundant since it is a reproducible mirror of iCloud.
Exclude both from Time Machine and cloud backup. If you do want a backup, back up the plain
Maildir (not the Xapian index, which is regenerable with `notmuch new`).

## Recommendations

1. **Install and pin the stack:** `brew install isync notmuch w3m` (add lynx/html2text/pdftotext
   as needed). Confirm binaries at `/opt/homebrew/bin`. Create the app-specific password at
   account.apple.com.
2. **Prove read-only safety before automating:** write the `.mbsyncrc` from section 3, run
   `mbsync --dry-run -V icloud`, confirm `Expunge None` / `Sync Pull` / `Remove None`, then do
   the first real sync folder-by-folder with `PipelineDepth 1`. Benchmark: when `mbsync
   --dry-run` reports no pending far→near operations, the mirror is complete.
3. **Index and expose:** write `~/.notmuch-config`, run `notmuch new`, install the `mailq`
   wrapper, the Skill, and the `settings.json` deny rules. Test with `mailq count '*'` and one
   `mailq search` before letting the agent use it.
4. **Automate with the least-surprising scheduler for your setup:** if you live in tmux over
   mosh, start with the tmux while loop; move to the LaunchAgent once you want reboot-survival.
   Decide the secret-storage approach up front (dedicated Keychain vs 0600 file); on a
   single-user FileVault-encrypted headless box the 0600 file is the pragmatic default.
5. **Set a 5-minute pull interval** and back off if you see connection-limit or "Server Busy"
   errors. Threshold to change: if you see repeated broken-pipe or
   `mail_max_userip_connections` errors, lengthen the interval and ensure no other IMAP client
   is signed into the account.

**Thresholds that change the plan:**

- If you ever need to send mail or write tags back to the server, revisit the read-only design
  (add msmtp for sending, which is out of current scope) — but keep the mirror itself Pull-only.
- If the mailbox is very large and disk is tight, switch from a full mirror to `MaxMessages` per
  folder.
- If notmuch's tag-in-DB model bothers you, use `mu` instead; the wrapper and Skill change only
  in the query commands.

## Caveats

- Connection limit is unpublished and sources conflict (a directly observed iCloud error shows
  `mail_max_userip_connections=10`; a third-party guide generalizes iCloud to ~5). Treat 5–10 as
  the practical ceiling and avoid concurrent clients.
- The `[LIMIT]` / "Too many simultaneous connections" wording cited for other providers
  (Gmail/Yahoo) is not necessarily iCloud's literal string; iCloud's observed strings are the
  Dovecot connection-limit message, "Server Busy," and generic timeouts.
- `PipelineDepth` limits pipelined commands on one connection, not the TCP connection count. It
  mitigates server overload/throttling, not a raw connection cap.
- UIDVALIDITY recovery is community best practice, not an official man-page procedure, and
  deleting a local folder to recover destroys local-only metadata such as notmuch tags. Dump
  tags first if you rely on them.
- `allowed-tools` in a Skill has been reported not to reliably restrict tool access on its own;
  rely on the `settings.json` deny list for enforcement.
- launchd wake behavior differs between sleep and power-off: `StartCalendarInterval` jobs run on
  next wake from sleep but are skipped entirely if the machine was powered off at the scheduled
  time.
- Homebrew isync is not built with OAuth2; irrelevant for iCloud app-specific passwords but
  relevant if you later add a Gmail account that requires XOAUTH2.
- Figures for indexing speed and Maildir size are drawn from third-party user reports on
  non-iCloud mailboxes and will vary with your message count, attachment mix, and disk speed.
