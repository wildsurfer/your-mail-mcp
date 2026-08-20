# Email MCP servers: build or adopt?

Research date: **19 August 2026**. All figures verified against GitHub's API, `raw.githubusercontent.com` and vendors' own documentation on that date. Directory sites (glama, playbooks, mcp.so, smithery) were deliberately not used as sources — they are stale and auto-generated.

Evaluated against four requirements:

1. **Self-hostable for real** — Docker image, compose file, published releases, config via env or files, runs as a long-lived service.
2. **Multiple mailboxes, mixed providers** — several accounts served at once, iCloud alongside Gmail alongside a self-hosted IMAP box, with sane addressing in the tool interface.
3. **Security** — credential storage, OAuth vs app passwords, transport authentication, encryption at rest, any security review.
4. **Read-only mode enforced at the MCP layer** — the server refuses writes regardless of what the client asks.

---

## Summary and recommendation

**Nothing scores well on all four.** Fifteen months into the MCP ecosystem there are roughly forty email servers on GitHub and not one is a mature, multi-account, authenticated, read-only-capable, self-hosted service. The closest candidates each nail two or three requirements and miss badly on the rest.

The three that matter:

| Project | Verdict |
|---|---|
| **igor47/notmuchproxy** | Architecturally read-only, ships a ghcr image, streamable HTTP with bearer token *or* full OIDC/OAuth 2.1 including dynamic client registration. Reads a notmuch archive, which is exactly your data layer. Zero stars, one author, 11 open issues, last push 1 July 2026. The right design, at prototype maturity. |
| **Wh1isper/mcp-email-server** | The only project with serious engineering discipline — 313 stars, OS-keyring credential storage, GreenMail and Playwright E2E tests, real docs. Multi-account with mixed providers. But its own documentation states plainly that it has no read-only mode, and its HTTP transport ships with no authentication. |
| **codefuturist/email-mcp** | The only IMAP server with genuine server-side read-only enforcement, verified in source, plus multi-account, a ghcr image and a Streamable HTTP mode. Also LGPL-3.0, three months idle, 40 open issues, and the read-only flag has a hole in it (below). |

**Recommendation: build, but fork the design rather than the code — and keep mbsync + notmuch as the data layer.**

Your current setup is not the weak part. Local sync plus a local index already solves the problems that wreck every IMAP-direct MCP server: search latency, threading, offline access, and per-provider IMAP idiosyncrasies. Replacing that with live IMAP inside an MCP server would be a downgrade. The refactor you actually want is to lift the machine-bound half — the notmuch query surface and the draft-append script — into a small HTTP service, and leave mbsync where it is.

Concretely: take **notmuchproxy's** deployment and auth model (FastAPI, one schema serving both REST and MCP via `FastMCP.from_fastapi()`, ghcr image, maildir mounted `:ro`, bearer token or OIDC), add **hgn/mcp-server-notmuch's** untrusted-content markers and tiered opt-in flags, and add the per-account addressing that neither has. That is a few weekends of work, not a rewrite, and it is the only path that satisfies all four requirements at once.

Multi-provider support is worth reframing. Requirement 2 belongs to the sync layer, not the MCP layer. notmuch already indexes several maildirs in one database, so adding Gmail and a self-hosted IMAP box is an mbsync channel and a tag convention, not new server code. Every project in this survey that implements multi-account inside the MCP server pays for it with a connection pool, a credential store, and a per-provider quirk matrix. You can skip all three.

---

## Comparison

Scoring: **Y** meets the requirement, **~** partial, **N** fails.

### Self-hosted candidates

| Project | Lang | Licence | ★ | Last push | 1. Self-host | 2. Multi-account | 3. Security | 4. Read-only |
|---|---|---|---|---|---|---|---|---|
| [igor47/notmuchproxy](https://github.com/igor47/notmuchproxy) | Python | MIT | 0 | 2026-07-01 | **Y** ghcr image, compose, env config | **~** via notmuch index; no account addressing | **Y** bearer or OIDC/OAuth 2.1 + DCR | **Y** no write path exists |
| [Wh1isper/mcp-email-server](https://github.com/Wh1isper/mcp-email-server) | Python | BSD-3 | 313 | 2026-08-17 | **~** PyPI, HTTP service, Dockerfile removed | **Y** `[[emails]]` blocks, `account_name` param | **~** OS keyring; no transport auth; no OAuth | **N** docs explicitly deny it |
| [codefuturist/email-mcp](https://github.com/codefuturist/email-mcp) | TS | LGPL-3.0 | 96 | 2026-05-20 | **Y** ghcr image, compose, `http` subcommand | **Y** `[[accounts]]`, `name` field | **N** plaintext TOML; unauthenticated HTTP | **~** real, but leaks (see detail) |
| [hgn/mcp-server-notmuch](https://github.com/hgn/mcp-server-notmuch) | Python | MIT | 0 | 2026-07-31 | **N** stdio only, uvx, not published | **~** via notmuch scopes | **~** local only; best injection handling found | **Y** default; tiered opt-in flags |
| [bradsjm/mail-imap-mcp-rs](https://github.com/bradsjm/mail-imap-mcp-rs) | Rust | MIT | **0** | 2026-07-17 | **Y** GHCR multi-arch image, npm, `--transport http` | **Y** `MAIL_IMAP_<ID>_*` env, `account_id` param | **~** no transport auth; app passwords only | **Y** default-off gate, runtime refusal |
| [tecnologicachile/mail-mcp](https://github.com/tecnologicachile/mail-mcp) | Rust | MIT | 58 | 2026-07-17 | **N** stdio only (verified in `main.rs`), no image | **Y** `MAIL_*_<ID>_*` env, `account_id` param | **~** OAuth2 refresh-grant/Graph/EWS; env plaintext | **Y** default-off gate, verified in source |
| [n24q02m/better-email-mcp](https://github.com/n24q02m/better-email-mcp) | TS | MIT/Apache* | 30 | 2026-08-15 | **Y** Docker Hub image, compose, npm | **Y** `EMAIL_CREDENTIALS` list, addressed by address | **Y** OAuth 2.1 JWT on HTTP; AES-GCM at rest | **N** none |
| [jgalea/mailbox-mcp](https://github.com/jgalea/mailbox-mcp) | TS | MIT | 6 | 2026-07-13 | **N** stdio only, no Docker | **Y** Gmail + IMAP + JMAP, `account` alias | **~** AES-256-GCM at rest; Gmail OAuth | **N** tool groups, but `core` includes send |
| [nikolausm/imap-mcp-server](https://github.com/nikolausm/imap-mcp-server) | TS | MIT | 75 | 2026-08-17 | **N** stdio only, no Docker | **Y** `accountId` on every tool | **~** AES-256-CBC, key beside ciphertext | **N** none |
| [wyattjoh/jmap-mcp](https://github.com/wyattjoh/jmap-mcp) | TS/Deno | MIT | 175 | 2026-08-19 | **N** stdio only, JSR package | **N** one JMAP account per process | **~** bearer token in env; no OAuth | **~** capability-gated by the JMAP token |
| [marlinjai/email-mcp](https://github.com/marlinjai/email-mcp) | TS | MIT | 17 | 2026-06-14 | **N** stdio only, no Docker | **Y** Gmail API + Graph + iCloud + IMAP | **Y** AES-256-GCM + OAuth2 PKCE | **N** none, and has `email_batch_delete` |
| [cldt-fr/imap-mcp](https://github.com/cldt-fr/imap-mcp) | TS | MIT | 2 | 2026-04-30 | **~** compose, build-your-own, needs Clerk | **Y** unlimited per user, Postgres | **Y** AES-256-GCM + OAuth 2.1 RS + DCR | **N** none |

\* The two independent checks of `better-email-mcp` returned MIT and Apache-2.0 respectively. Verify before relying on it.

### Not worth your time

| Project | Why |
|---|---|
| [GongRzhe/Gmail-MCP-Server](https://github.com/GongRzhe/Gmail-MCP-Server) | **Archived.** 1,165 ★, 411 forks, 70 open issues, last push 2025-08-06. The most-installed Gmail MCP server is dead. Forks exist; none has consolidated the userbase. |
| [gabigabogabu/email-mcp-server](https://github.com/gabigabogabu/email-mcp-server) | 6 commits, dead since 2025-04-08, 14 ★. |
| [david-strejc/gmail-mcp-server](https://github.com/david-strejc/gmail-mcp-server) | 4 commits over one day in April 2025. 146 MB repo of committed junk. README suggests enabling "less secure app access". |
| [non-dirty/imap-mcp](https://github.com/non-dirty/imap-mcp) | 59 ★, dead since 2025-04-03, 26 open issues. |
| [yunfeizhu/mcp-mail-server](https://github.com/yunfeizhu/mcp-mail-server) | Active, but single-account only and password in plaintext client JSON. |
| [gomcpgo/email](https://github.com/gomcpgo/email) | **No licence file** — legally unusable. 1 ★. Caches message bodies at mode 0644. |
| [samihalawa/email-smtp-imap-mcp](https://github.com/samihalawa/email-smtp-imap-mcp) | Repo id changed between checks, meaning it was deleted and recreated. Credentials as a JSON blob in one env var. |
| [daemonp/rummage](https://github.com/daemonp/rummage), [queelius/mail-memex](https://github.com/queelius/mail-memex), [stubbedev/notmuch-mcp](https://github.com/stubbedev/notmuch-mcp), [jkp/protonmail-mcp](https://github.com/jkp/protonmail-mcp) | 0–2 ★ each, two days to two months of commits, no licence in two cases. Worth reading, not adopting. |

---

## Detail

### The notmuch family — closest to your architecture

#### igor47/notmuchproxy

Python, MIT, **0 ★**, 11 open issues, created 2026-06-10, last push **2026-07-01**. Read-only OpenAPI + MCP server over a notmuch archive.

Four tools: `search_email`, `get_thread`, `get_message`, `list_tags`. The README's framing is the whole point: *"There is no UI and no write path: the server only ever reads the archive, so the worst an over-eager LLM can do is search your email too enthusiastically."*

- **Self-hosting (Y).** `ghcr.io/igor47/notmuchproxy:latest`, a documented compose file, maildir mounted `:ro`, runs as non-root uid 1000 with an override documented for other uids. All config via environment variables. Indexing stays wherever mail is delivered; the container picks up index updates because xapian readers do not block writers. This is the only project in the survey whose deployment story reads like someone has actually run it in production.
- **Multi-account (~).** Inherits whatever notmuch indexes. There is no `account` parameter, so you would address accounts through tags or folder queries. This is the gap to close if you fork it.
- **Security (Y).** Two auth modes, and the server refuses to start with both or neither. Static bearer token, or OIDC against any external IdP while presenting a spec-compliant MCP authorization server to clients — including the dynamic client registration claude.ai requires. `NOTMUCHPROXY_EXCLUDE_TAGS` drops messages tagged `spam` or `deleted` from every result, including explicit `tag:spam` queries, which is a deliberate move to keep adversarial content away from the model.
- **Read-only (Y).** Enforced by construction rather than by flag. There is no write code path to disable, and the `:ro` mount enforces it a second time at the kernel.

One nice detail worth stealing outright: search queries are validated before reaching xapian. Unknown prefixes (`status:unread`), capitalised prefixes (`From:alice`) and nonexistent tags are rejected with a 400 explaining the fix, because to xapian they are simply terms no message contains — so a mistyped query is indistinguishable from an empty mailbox. Every LLM-facing search API needs this and almost none have it.

**Maturity: honest prototype.** One author, no stars, 11 open issues, six weeks idle. CI runs the suite inside the docker image against Debian's notmuch. Adopt the design; expect to maintain the code yourself.

#### hgn/mcp-server-notmuch

Python, MIT, **0 ★**, created 2026-07-23, last push **2026-07-31**. "Read-first" server over a local notmuch database.

The tiering model is the best answer to requirement 4 in the entire survey. Four tiers: read (always on), `--allow-drafts`, `--allow-tags`, `--allow-export DIR`. The README is explicit that there is no "registered but refused" state — an unauthorised tool is simply absent from the tool list. And: *"It never sends mail. There is no send capability anywhere in this codebase, in any mode, with any flag."* Drafts are written to a local maildir for you to send yourself from a real client.

Its security section is the only one in the survey that treats prompt injection as a first-class design constraint rather than a disclaimer. Message bodies, attachment text, calendar summaries and thread overview lines are all wrapped by a single `render.py` function in `-----BEGIN/END UNTRUSTED EMAIL CONTENT-----` markers, *"so no individual tool can forget to do this"*. Path confinement resolves targets and re-checks containment afterwards, catching both `..` and symlinks out of the root. Every subprocess call is `shell=False` with an argv list. Diagnostics are content-free.

Also worth copying: `mail_thread_overview` gives one line per message before you read a long thread in full, and `mail_pending` answers "who owes me a reply". Both are token-economics features that IMAP-direct servers cannot cheaply offer.

Fails requirement 1 outright — stdio only, uvx, not published to PyPI, and it pins an MCP SDK pre-release (`mcp==2.0.0b2`, spec 2026-07-28) so every install needs `--prerelease=allow`. Fails requirement 2 except through notmuch scopes.

**Maturity: three weeks of commits by one author, zero stars.** The thinking is excellent, the project is brand new. Read the source, take the ideas.

#### The rest

`runekaagaard/mcp-notmuch-sendmail` (Python, MPL-2.0, 7 ★, dead since 2025-07-10), `stubbedev/notmuch-mcp` (Go, MIT, 0 ★, created 2026-08-13 — six days old), `daemonp/rummage` (Rust, no licence, 1 ★, two days of commits), `queelius/mail-memex` (Python, no licence, 2 ★), `jkp/protonmail-mcp` (default branch is an unmerged feature branch). No mbsync/isync or offlineimap-specific MCP server exists.

### General IMAP servers

#### Wh1isper/mcp-email-server (formerly ai-zerolab)

Python, BSD-3-Clause, **313 ★**, 114 forks, created 2025-02-24, last push **2026-08-17**, latest release 1.4.1 on 2026-08-08, 54 versions on PyPI. The de facto leader by adoption and by a wide margin the most engineered.

- **Self-hosting (~).** `uvx mcp-email-server@latest`. Runs as a long-lived service: `streamable-http` on `/mcp` at `localhost:9557` by default, plus SSE. But the root `Dockerfile` returns 404 as of today — it appears to have been removed during the 1.x rewrite, so the ghcr image referenced in older docs is stale. Container deployment is currently your problem.
- **Multi-account (Y).** Unlimited `[[emails]]` blocks in `~/.config/mcp-email-server/config.toml`, each with its own IMAP/SMTP host, so iCloud + Gmail + self-hosted in one server works. Tools select by `account_name`. Note the trap: *"The environment variable interface describes one account. Use TOML or the UI when persistent configuration requires multiple accounts."*
- **Security (~).** The best credential story of any IMAP server here. `credential_storage = auto | keyring | plaintext`; `auto` probes the OS keyring with a live usability check and falls back to TOML with a warning; `keyring` refuses to fall back. Keyring entries are `mcp-email-server / <account>:<incoming|outgoing|api_key>` with `__KEYRING__` sentinels in the file, TOML written atomically at 0600. A newer managed mode uses a private SQLite `managed_secret` table and *"never falls back to TOML plaintext"*. Against that: **no OAuth at all** (app passwords only), and **no transport authentication**. The docs are candid: *"Network exposure still requires appropriate authentication, authorization, TLS termination, and firewall policy around the server."* Only DNS-rebinding protection is built in. No third-party audit.
- **Read-only (N).** The documentation states it directly: *"IMAP-only does not mean read-only. These tools can still change mailbox state: `save_to_mailbox`, `set_email_flags`, `mark_emails_as_read`, `move_emails`, `archive_emails`, `delete_emails`."* What it offers instead is allowlists — `allowed_recipients` (empty by default, which disables sending), `allowed_senders` globs that fail closed on malformed `From` headers, `enable_attachment_download = false` by default, `set_email_flags` refusing `\Deleted`, and scoped `UID EXPUNGE` only.

**Maturity: genuinely maintained.** CI runs pytest across Python 3.11–3.14, pyright, strict docs build, Playwright browser E2E and GreenMail IMAP/SMTP E2E. Bounded stdio framing at 2 MiB, MCP cancellation propagation, a log-redaction spec. Single maintainer, so low bus factor, and 1.0.0 (25 July 2026) removed the `add_email_account` tool and changed the config store — pin your version.

#### codefuturist/email-mcp

TypeScript, **LGPL-3.0**, **96 ★**, 48 forks, **40 open issues**, created 2026-02-18, last push **2026-05-20** (three months idle). 47 tools, 7 prompts, 6 resources, an IMAP IDLE watcher, a scheduler, AI triage presets.

This is the only IMAP server with a read-only mode I could verify in source rather than in a README, so it deserves the detail.

`src/config/schema.ts` defines `read_only: z.boolean().default(false)` in `SettingsSchema`. `src/tools/register.ts` then does the enforcement, and the file header says exactly what it does — *"In read-only mode, write tools are not registered at all."*

```ts
const { readOnly } = config.settings;
// Read tools — always registered
registerAccountsTools(...); registerMailboxesTools(...); registerEmailsTools(...); /* … */
// Write tools — skipped in read-only mode
if (!readOnly) {
  registerSendTools(server, smtpService);
  registerManageTools(server, imapService);
  registerLabelTools(server, imapService);
  registerBulkTools(server, imapService);
  registerDraftTools(server, imapService, smtpService);
  registerFolderTools(server, imapService);
  registerTemplateWriteTools(server, templateService, imapService, smtpService);
  registerSchedulerTools(server, schedulerService);
}
```

That is real server-side enforcement — a client cannot call what was never registered, and the flag lives in server config, not in the client's hands.

**But the guarantee has two holes, both visible in `src/main.ts`.** The scheduler's background sender runs on a 60-second interval, and on startup, in both the stdio and HTTP paths, entirely outside the `readOnly` gate:

```ts
schedulerInterval = setInterval(async () => {
  try { await schedulerService.checkAndSend(); } catch { /* Silent */ }
}, 60_000);
```

So a server configured read-only still sends any email already sitting in the schedule store. Separately, `registerWatcherTools` sits in the always-registered block, and `HooksService` carries `auto_label` and `auto_flag` settings that mutate mailbox state on new mail. Read-only gates the tool surface; it does not gate the background services. This is precisely the failure mode worth designing against.

Other notes: multi-account via `[[accounts]]` with provider auto-detection for Gmail/Outlook/Yahoo/iCloud/Fastmail/ProtonMail/Zoho/GMX. Credentials are **plaintext TOML** at `~/.config/email-mcp/config.toml`. OAuth2 for Google and Microsoft exists but the README labels it experimental: *"Token refresh and provider-specific flows may require additional testing."* Contrary to its own README, it does have a Streamable HTTP mode (`email-mcp http [port]`, endpoint `/mcp`, health at `/health`) — with **no authentication whatsoever**, binding 0.0.0.0. Ships `ghcr.io/codefuturist/email-mcp` plus a compose file, though the compose file is configured for stdio with the config mounted read-only.

**Maturity: heavy churn, currently idle.** 40 open issues against 48 forks on a six-month-old repo, no `test` script in the documented dev workflow, single maintainer. LGPL-3.0 also matters if you intend to link against it.

#### tecnologicachile/mail-mcp

Rust, MIT, **57 ★**, 16 forks, **11 open issues**, latest release v0.4.9 on **2026-07-17**. A fork of `bradsjm/mail-imap-mcp-rs`.

The only IMAP server with a **default-on** write gate: `MAIL_IMAP_WRITE_ENABLED` and `MAIL_SMTP_WRITE_ENABLED` both default to `false`, so an unconfigured server is read-only until you opt in, and deletes additionally require `confirm: true`. (Verified in the README's config table; I did not read the Rust enforcement code.)

Best protocol coverage in the survey: IMAP, SMTP, EWS and Microsoft Graph, with real XOAUTH2 for Google and Microsoft. Multi-account via `MAIL_IMAP_<ID>_*` env vars with `account_id` on every tool and cross-account `imap_copy_message`.

Against it: **stdio only**, so requirement 1 fails outright — there is a Dockerfile but the roadmap still lists "Docker image" as not done. Credentials are **environment variables only**, meaning plaintext in your MCP client config, with no keychain and no encryption at rest (they are wrapped in `secrecy::SecretString` in-process). And the quality signal is mixed: 64 tests and GreenMail fixtures on one hand; three consecutive releases (0.4.3→0.4.5) spent trying to stop LLMs leaking literal `</body_text><parameter name="body_html">` markup into recipients' inboxes before 0.4.6 added server-side rejection, on the other. Shipped bugs reached real mailboxes. Its EWS flow also defaults to Microsoft Office's pre-approved client id `d3590ed6-52b3-4102-aeff-aad2292ab01c`, which is a Thunderbird-style trick you should understand before depending on it.

#### n24q02m/better-email-mcp

TypeScript, MIT or Apache-2.0 (sources conflict), **30 ★**, latest release v1.39.0 on **2026-08-15**. The most distribution-complete: Docker Hub image `n24q02m/better-email-mcp:latest`, compose file, npm package, MCP registry `server.json`, and a vendor-hosted instance.

Best transport security of the self-hostable options: OAuth 2.1 / Bearer JWT per user on the HTTP path, `CREDENTIAL_SECRET` encrypting the per-user credential store, credentials held in an in-memory map cleared on restart, and `MCP_AUTH_DISABLE=1` for when you front it with your own gateway. Device-code OAuth for Outlook/Hotmail/Live using a bundled public Azure client. Multi-account via `EMAIL_CREDENTIALS=user1@gmail.com:pass1,user2@outlook.com:pass2`, addressed by email address, with iCloud among the auto-discovered providers. Seven composite tools covering 21 actions.

No read-only mode. And the velocity is a risk, not a feature: 646 commits and 100+ releases in a few months, heavily agent-generated (`.jules/`, `.coderabbit.yaml`, `AGENTS.md`, `CLAUDE.md`), one of fifteen sibling "better-*" servers by the same author sharing `@n24q02m/mcp-core`. The author's own status note is the most useful signal in the repo: *"Past months saw significant churn around credential handling and the daemon-bridge auto-spawn pattern. This caused multi-process races, browser tab spam, and inconsistent setup UX across plugins."* Pin your version.

#### jgalea/mailbox-mcp

TypeScript, MIT, **6 ★** but 11 forks and outside bug reporters, last push **2026-07-13**, npm at 0.10.0. Note that its GitHub Releases page stopped at v0.6.1 on 20 April 2026 while development continued to 0.10.0 in July — anything scraping the releases tab will wrongly call it dead.

The only server covering Gmail API, generic IMAP/SMTP **and** JMAP (Fastmail, Stalwart, Cyrus) in one process, with a `multi_account_search` that fans one query across every account in parallel. Real Gmail OAuth with a user-supplied Google Cloud desktop client. AES-256-GCM at rest keyed from `MAILBOX_MCP_PASSPHRASE` — an env var only, because 0.9.2 removed the equivalent tool parameter with an explanation every server author should read: *"Tool arguments are serialized into MCP host logs and model context, so accepting a secret there leaked it."*

Read-only fails on a technicality that matters. 0.10.0 added `MAILBOX_MCP_TOOLS` gating by group (`core`, `organize`, `bulk`, `attachments`, `gmail-extras`), and disabled tools genuinely refuse calls server-side. But the groups are packaged by workflow rather than by read/write, and `core` includes the send path, so no combination yields a read-only server. Stdio only, no Docker.

Its CHANGELOG is the most honest engineering document in the survey — it documents root causes, including an imapflow `{ uid: true }` options-versus-query bug and sequential-fetch timeouts presenting as silent disconnects. Read it before writing your own IMAP layer.

#### nikolausm/imap-mcp-server

TypeScript, MIT, **75 ★**, last push **2026-08-17**, npm `imap-mcp-server` v1.2.2. Multi-account with `accountId` on every tool and 15+ provider presets including iCloud. Credentials encrypted AES-256-**CBC** with the key at `~/.imap-mcp/.key` beside the ciphertext at `~/.imap-mcp/accounts.json` — unauthenticated encryption with the key on the same disk is obfuscation, not protection, and the README overstates it. App passwords only. No Dockerfile, stdio only, no read-only mode, and it ships `imap_bulk_delete_by_search`. Has vitest tests. The recommended install path is a curl-pipe-to-bash script.

#### cldt-fr/imap-mcp

TypeScript/Next.js, MIT, **2 ★**, **12 commits**, last push **2026-04-30**, no releases, no `.github/` directory and therefore no CI, no tests.

Best security design on paper of anything self-hostable: AES-256-GCM at rest under `MCP_MASTER_KEY`, OAuth access tokens stored only as SHA-256 hashes, refresh token rotation, HMAC-signed attachment URLs with 15-minute expiry, and a full OAuth 2.1 resource server with PKCE and RFC 7591 dynamic client registration. The identity model is right too: *"The authenticated user's ID is always injected from the OAuth token — tools never accept it as an argument, so a client cannot impersonate another user."* Unlimited accounts per user in Postgres with presets including iCloud.

All of which is twelve unaudited commits by one author, dormant since April, requiring a Clerk SaaS account and a Postgres, with no read-only mode and a tool surface including `delete_folder` and permanent expunge. Read it for the auth design. Do not run it.

### JMAP

#### wyattjoh/jmap-mcp

TypeScript on Deno, MIT, **175 ★**, last push **2026-08-19** (today) — the best-maintained JMAP server. Published to JSR, installable as a Claude Code plugin.

Full RFC 8620/8621 coverage via jmap-jam, with the two features IMAP servers struggle to provide: state-based incremental sync (`get_email_changes` from a `sinceState`, `get_search_updates` from a `queryState`) and native thread objects. Zod validation throughout.

One account per process (`JMAP_SESSION_URL`, `JMAP_BEARER_TOKEN`, optional `JMAP_ACCOUNT_ID`), stdio only, bearer token in the client's env. Read-only is partial and interesting: the README describes "capability-based tool registration (read-only, submission)", meaning send tools are registered based on the capabilities the JMAP session advertises. That is enforcement, but it lives at the provider, not in your config — you get it by issuing a token without submission scope. That is arguably the *right* model, and worth noting: it is delegation to an authorisation system that already exists.

Irrelevant to iCloud, which offers no JMAP. Relevant if you ever add Fastmail or run Stalwart.

Also in this family: `MadLlama25/fastmail-mcp` (124 ★, JMAP plus contacts and calendar) and `radiosilence/fastmail-cli` (Rust, 66 ★, JMAP + CardDAV + masked email, CLI and MCP).

### Provider-specific

**GongRzhe/Gmail-MCP-Server is archived.** 1,165 ★, 411 forks, 70 open issues, last push 2025-08-06, MIT. The single most widely installed Gmail MCP server has been abandoned for a year and formally archived, and its userbase has scattered across unconsolidated forks. Treat any tutorial recommending it as out of date. This is the clearest evidence in the survey that adoption is not a maintenance signal in this ecosystem.

`marlinjai/email-mcp` (17 ★, last push 2026-06-14) has the best auth story of the provider-native servers — Gmail REST, Microsoft Graph, iCloud IMAP and generic IMAP behind one interface, AES-256-GCM at rest with machine-derived keys, and OAuth2 browser PKCE flows with automatic refresh. Caveat: "built-in OAuth credentials" means a shared public client id baked into the package. No Docker, stdio only, no read-only mode, and it exposes `email_batch_delete` for up to 1,000 Gmail messages.

### Commercial and hosted

All of these mean your mail passes through a third party, which for a self-hosted iCloud mirror is the thing you are trying to avoid. Included because they set the capability bar.

| Offering | Model | Read-only scoping | Self-host |
|---|---|---|---|
| **[Anthropic Gmail connector](https://claude.com/connectors/gmail)** | First-party Google Workspace connector, OAuth, mirrors your existing Workspace permissions, Pro/Max/Team/Enterprise. Available since 24 Feb 2026. | Reads and drafts; does not send. The closest thing to a shipped read-only email agent, and the model your server should imitate: search and summarise freely, write drafts, never send. | No |
| **[Zapier MCP](https://zapier.com/mcp/gmail)** | Managed server over 8,000+ apps. Gmail exposes "Send Email", "Create Draft" and "Find Email" as separate callable tools. | Effectively yes — the agent's tool list is exactly the actions you enabled, so enabling only "Find Email" yields a read-only surface enforced on Zapier's side. Note it does not respect Enterprise-account app/action restrictions. | No |
| **[Composio](https://github.com/ComposioHQ/composio)** | MIT, **29,769 ★**, pushed today. 1,000+ toolkits with managed OAuth behind one endpoint. | Per-tool selection; the auth and execution remain Composio's. | The SDK is open; the managed auth layer is the product |
| **[Pipedream](https://github.com/PipedreamHQ/pipedream/tree/master/modelcontextprotocol)** | 10,000+ tools over 2,700+ apps. A self-host reference implementation exists under `modelcontextprotocol/`, but Pipedream explicitly recommends the remote server for production and calls the code a reference. Acquired by Workday in November 2025. | Per-action | Reference only |
| **[Klavis AI](https://github.com/Klavis-AI/klavis)** | Apache-2.0, **5,791 ★**, 557 forks, but **295 open issues** and last push **2026-06-01** — two and a half months idle. 50+ servers including Gmail with enterprise OAuth; self-host via ghcr images. | Per-server tool selection | Yes, via ghcr images |

**The security incident worth knowing.** In September 2025 an npm package named `postmark-mcp` — a lookalike of the official Postmark server — ran clean across fifteen releases, mirroring the real repository's code and passing sandbox review. On 17 September 2025, version 1.0.16 added one line at `index.js:231` appending a hidden BCC to every outgoing message, routing copies to `phan@giftshop[.]club`. The package had ~1,500 weekly downloads. Reported by Koi Security and covered by Snyk, Qualys and The Hacker News.

Two lessons apply directly to you. First, an MCP server that can send is a credential holder and an exfiltration channel, and behaviour that is benign at install time can change at any version bump — pin and audit. Second, this is exactly why requirement 4 is worth insisting on: a server with no send capability compiled in cannot be turned into one by a dependency update.

---

## The build-vs-adopt argument

### What the existing projects get right — copy these

- **Read-only by construction, not by flag.** notmuchproxy has no write code path; hgn's server has no send capability *"in any mode, with any flag"*. A boolean you can flip is a boolean an attacker or a bug can flip. Absence of code is the only guarantee that survives a supply-chain compromise.
- **Tiered opt-in, with tools absent rather than refusing.** hgn's four tiers get the ergonomics right: an unauthorised tool never appears in `tools/list`, so the model does not attempt it and the user is not shown a capability that will fail.
- **Delegate authorisation upward where possible.** jmap-mcp registers send tools based on what the session's token can do. A token without submission scope produces a read-only server without any server-side flag. Where a provider offers scoped credentials, use them — it is stronger than any check you write.
- **Wrap untrusted content in explicit markers, in one place.** hgn's `render.py` does this for every body, attachment, calendar summary and overview line, deliberately so no individual tool can forget.
- **Validate search queries before the index sees them.** notmuchproxy rejects unknown prefixes and nonexistent tags with an explanation. Otherwise a typo and an empty mailbox are indistinguishable, and the model confidently reports you have no such mail.
- **Two-phase reading.** `gomcpgo/email` returns metadata plus a ~1 KB preview from `fetch_email` and paginates the body separately; hgn's `mail_thread_overview` gives one line per message before the full thread. Context windows are the real constraint on mail tools.
- **Never accept secrets as tool arguments.** mailbox-mcp removed a `passphrase` parameter for exactly this reason: *"Tool arguments are serialized into MCP host logs and model context."*
- **Fail closed on ambiguity.** Wh1isper's sender allowlist fails closed on malformed, empty or multi-address `From` headers, and returns blocked mutations as no-ops by default so the caller cannot probe for hidden messages.
- **Scoped expunge only.** Wh1isper uses `UID EXPUNGE` and never mailbox-wide EXPUNGE, and refuses to set `\Deleted` via the flags tool.

### The mistakes they repeat

- **Plaintext credentials in the client's config file.** The single most common failure. tecnologicachile, codefuturist, yunfeizhu, gomcpgo, samihalawa and david-strejc all put a password in a file the MCP client reads — which then gets copied into tutorials, screenshots and bug reports.
- **HTTP transport with no authentication.** Both Wh1isper and codefuturist expose a network listener with no auth. Wh1isper at least documents it as your problem. codefuturist binds 0.0.0.0 and says nothing.
- **Encryption theatre.** AES-256-CBC with the key file beside the ciphertext (nikolausm) is not encryption at rest in any threat model where someone can read the disk. Say what you actually protect against.
- **Read-only that only covers the tool surface.** codefuturist's flag gates registration but not the background scheduler or the auto-labelling hooks. If you ship a read-only mode, it must gate every write path in the process, including timers, watchers and startup hooks.
- **Conflating "no SMTP configured" with "read-only".** Wh1isper is the only project that says this out loud, and it is right: an IMAP-only server can still delete, move, archive and flag.
- **Tool surfaces built for feature-list length.** 47 tools (codefuturist), 49 (mailbox-mcp), 27 (cldt-fr). Every extra tool is context the model must read on every request and another write path to secure.
- **Abandoning the releases page while continuing to develop**, or the reverse. mailbox-mcp shipped to 0.10.0 on npm while GitHub Releases stopped at v0.6.1. Anything automated reading that repo concludes it is dead.
- **Weekend-project maturity presented as production readiness.** Most READMEs here promise "production-ready" and "enterprise-grade" over a dozen commits, no tests and one author.

### Hard problems that are easy to underestimate

These are the reasons to keep mbsync and notmuch rather than talking IMAP from the MCP server.

- **IMAP idiosyncrasies.** UID versus sequence numbers is where everyone gets cut — mailbox-mcp's changelog records an imapflow bug where `{ uid: true }` belonged in the options rather than the query, which silently addresses the wrong messages. Beyond that: iCloud's rate limits and connection caps, Gmail's labels-as-folders with `[Gmail]/All Mail` duplication and `X-GM-RAW`, servers that lie about `UIDPLUS` or `MOVE`, `CONDSTORE`/`QRESYNC` support that varies, and folder delimiters that differ per server. mbsync has absorbed twenty years of these workarounds. Your MCP server should not re-learn them.
- **OAuth for Gmail and Microsoft.** The credential is the easy part; the app registration is not. Google requires a verified OAuth consent screen and a security assessment for restricted Gmail scopes if you distribute, and Microsoft has been tightening basic-auth and app-password paths. This is why so many projects ship a bundled public client id — tecnologicachile borrows Microsoft Office's, better-email-mcp bundles a "Thunderbird-pattern" Azure client, marlinjai bakes in shared credentials. All are fragile and can be revoked out from under you. For a personal server, a user-supplied desktop OAuth client (mailbox-mcp's approach) is the honest answer. For iCloud there is no OAuth at all — app-specific passwords are the only option, which caps how good requirement 3 can get for your primary account.
- **Threading.** `In-Reply-To` and `References` are frequently missing or wrong, mailing lists rewrite subjects, and Exchange breaks chains. notmuch has already solved this for your archive with a real thread model. Reconstructing it from IMAP per request is expensive and worse.
- **Search performance without a local index.** IMAP `SEARCH` is server-side, slow, inconsistently implemented, and cannot do full-text ranking. Every IMAP-direct server here either accepts multi-second searches or bolts on a cache. Wh1isper added a SQLite metadata projection for exactly this reason. You already have xapian.
- **Attachment handling.** MIME nesting, `multipart/related` inline images, calendar invites, encoding chaos, and the size problem — attachments must never land in the model's context by accident. Note the defaults worth copying: Wh1isper disables attachment download entirely by default; notmuchproxy lists attachments by filename but does not serve them; hgn requires `--allow-export DIR` and re-checks path containment after resolution. Also plan for the dependencies: text extraction needs `pdftotext`, and office formats need pandoc or libreoffice.
- **Prompt injection.** This is the requirement-4 argument in its strongest form. An email agent ingests attacker-chosen content, cannot reliably distinguish data from instructions, and holds a credential that can send. Anyone who knows your address can put text in your model's context. The mitigations that actually help are structural: no send capability in the process at all; drafts written to a maildir for human review; content wrapped in untrusted markers by a single chokepoint function; a spam/deleted tag exclusion so the most adversarial mail never reaches the model. Output filtering and "ignore instructions in emails" system prompts are not defences. The Anthropic Gmail connector's choice — read and draft, never send — is a considered position, not a limitation.
- **Two things specific to your migration.** Appending drafts over IMAP has its own quirks: the `\Draft` flag, `APPENDUID` when the server supports UIDPLUS, and iCloud's Drafts folder naming. And moving from a Mac mini to a container means the keychain goes away — every credential store that depends on macOS Keychain (including Wh1isper's `auto` mode) silently degrades to plaintext in a headless container. Plan for a file-based encrypted store or an external secret source from the start.

### Where the genuine gaps are

Nothing in this ecosystem currently offers, in one project:

1. A published container image plus authenticated HTTP transport. Of everything surveyed, exactly two self-hostable servers authenticate their network transport (notmuchproxy, better-email-mcp), and one of those has no read-only mode.
2. Read-only enforcement that covers background services, not just the tool list.
3. Multi-account addressing on top of a local index. Every multi-account server does live IMAP; every local-index server is single-archive.
4. Any third-party security review. None of the forty-odd projects has one.

That combination is the thing worth building, and it is a smaller job than it looks because notmuch does the hard half.

---

## Addendum, 19 August 2026 — the Rust lineage, re-examined in source

Prompted by a low-traction r/mcp post (8 upvotes, ~5 months old) claiming a from-scratch Rust server doing "everything". The post describes **tecnologicachile/mail-mcp**, which was already in this report. It was not missed. But re-reading it against the source produced one real correction and several verified facts that were previously only README-deep.

### Correction: I under-rated the upstream, `bradsjm/mail-imap-mcp-rs`

The original report treated bradsjm as a footnote ("a fork of bradsjm"). That was wrong, and it cost the Rust lineage a requirement-1 score it deserves. The upstream ships what the fork does not:

- **`ghcr.io/bradsjm/mail-imap-mcp-rs:latest`, prebuilt multi-arch**, plus a Dockerfile, plus npm `@bradsjm/mail-imap-mcp-rs` with native targets for macOS arm64/x64, Linux glibc/musl, Windows.
- **Streamable HTTP transport**: `--transport http --http-bind-address --http-port`, serving `/mcp`, defaulting to localhost.
- A better tool surface than the fork: `imap_apply_to_messages` takes one `action` of `move|copy|delete` instead of the fork's eleven separate write tools, and long-running writes get a job model (`imap_get_operation`, `imap_cancel_operation`).
- `MAIL_IMAP_CA_CERT_PATH` to trust a private CA without disabling verification.

Its security note is admirably blunt: *"The server does not add built-in HTTP authentication or TLS termination. Do not leave this server publicly reachable unless exposure is intentional and protected by a trusted boundary."*

Limits: IMAP only, so no send path at all (the fork added SMTP/Graph/EWS), no OAuth, app passwords only, **0 stars**, and its own README acknowledges the code was AI-assisted via OpenCode/GPT-5.

**Why I missed it: my sweep was sorted by stars, and this repo has none.** In this ecosystem that bias is severe — three of the most interesting designs here (`igor47/notmuchproxy`, `hgn/mcp-server-notmuch`, `bradsjm/mail-imap-mcp-rs`) all sit at zero stars. Assume the survey under-samples good zero-star work generally.

### Confirmed: tecnologicachile/mail-mcp is stdio-only

`src/main.rs` imports `rmcp::transport::stdio` and nothing else; there is no HTTP path in the binary. `docs/tool-contract.md` states "Transport: stdio only." The fork appears to have dropped the upstream's HTTP transport. Requirement 1 fails for the fork and passes for the upstream.

### The write gate is real, and it is a per-handler guard

Read from `src/server.rs` (first 2,669 lines; see caveat below). `require_write_enabled(&self.config)?` is the **first statement** in each of eight write handlers:

`update_flags_impl` (L1848), `copy_message_impl` (L1965), `move_message_impl` (L2197), `delete_message_impl` (L2379), `create_mailbox_impl` (L2483), `delete_mailbox_impl` (L2501), `rename_mailbox_impl` (L2524), `search_and_move_impl` (L2572).

`src/config.rs` confirms the defaults are off: `write_enabled: parse_bool_env("MAIL_IMAP_WRITE_ENABLED", false)?` and `smtp_write_enabled: parse_bool_env("MAIL_SMTP_WRITE_ENABLED", false)?`.

So this is genuine server-side enforcement, not a flag read once and forgotten. Three qualifications:

1. **It is a repeated per-call-site guard, not a chokepoint.** Ten-plus hand-written calls, one per handler. This is precisely the pattern where a handler eventually ships without the guard — the same class of hole found in `codefuturist/email-mcp`'s background scheduler.
2. **Write tools stay registered and advertised.** The gate lives in the handler, so `imap_bulk_delete` and `smtp_send_message` still appear in `tools/list` and the model will attempt them. The server refuses, which satisfies requirement 4 strictly, but it is weaker than hgn's model where an unauthorised tool is simply absent.
3. **`require_smtp_write_enabled` appears once** in the portion I could read (L790, in the `smtp_send_message` path). The other four send paths — `smtp_reply_message`, `smtp_forward_message`, `graph_send_message`, `ews_send_message` — dispatch to `_impl` functions beyond the fetch limit and were **not verified**.

### Delete confirmation: a real precondition, hand-rolled twice

```rust
// delete_message_impl, L2379-2385
require_write_enabled(&self.config)?;
validate_account_id(&input.account_id)?;
if !input.confirm {
    return Err(AppError::InvalidInput("delete requires confirm=true".to_owned()));
}
```

The same block is duplicated in `delete_mailbox_impl` (L2504-2508) with different wording. It is a server-side precondition independent of the write gate — both must pass — so it is not theatre. But it is copy-pasted rather than expressed as a type or shared guard, and `imap_bulk_delete` and `imap_search_and_delete` document a `confirm` requirement whose enforcement I could not read.

### OAuth token storage, verified in `src/oauth2.rs`

- **Access tokens never touch disk.** `Arc<Mutex<HashMap<String, CachedToken>>>`, in-process only, with a 600-second refresh margin and a two-strike retry. This is the right design.
- **Refresh tokens — the long-lived credential — come from environment variables** and live in plaintext in the MCP client config or a `.env` (`dotenvy::dotenv()` runs at startup). `docs/security.md` concedes it: *"No encryption at rest: Credentials are in memory only; disk encryption is the user's responsibility."*
- **There is no device code flow in the server.** The Reddit claim of "OAuth2 device code flow for Google and Microsoft" is inaccurate as a description of the code. `oauth2.rs` implements only the `refresh_token` grant against Google's and Microsoft's token endpoints. Obtaining the refresh token is an out-of-band manual step the README delegates to the user (or to Claude Code, via a copy-paste prompt).
- **First-party client-id borrowing.** `config.rs` hardcodes `d3590ed6-52b3-4102-aeff-aad2292ab01c` — Microsoft Office's own app id — as the EWS default, and the README suggests `9e5f94bc-e8a4-4e73-b8be-63364c29d753` (Azure CLI) for Graph. This works and is a known technique, but it is borrowed first-party identity, and Microsoft has been progressively restricting it. Treat as revocable.
- Public-client handling is correct: a `client_secret` of `""`, `none` or `public` omits the parameter from the grant.

### Provenance

The post says "written from scratch in Rust". `tecnologicachile/mail-mcp` was created 2026-03-13, two weeks after `bradsjm/mail-imap-mcp-rs` (2026-02-27), and its own README explains that its npm publish job "was inherited from the upstream fork and tried to publish to `@bradsjm/mail-imap-mcp-rs`, a scope this org does not own". GitHub reports `fork: false`, so it is a detached copy rather than a tracked fork. The SMTP, Graph and EWS layers do appear to be the fork's own work; the IMAP core and tool contract are not.

### Documentation drift

`docs/tool-contract.md` and `docs/security.md` both describe only the original ten IMAP tools and list four write-gated tools. The README advertises 31 tools including eleven write tools, five send tools and three EWS tools. Anyone assessing this project from its docs will materially misjudge its surface area.

### unspam.email/mcp — different category

Deliverability testing SaaS: spam scores, inbox placement across seed mailboxes, rendering previews in 50+ real clients, eye-tracking heatmaps, scheduled tests. Around 30 tools, none of which touch your mailbox. Not a candidate for this decision.

One thing worth borrowing, though: its auth model is exactly the shape a remote MCP server should have — OAuth 2.1 with Dynamic Client Registration and PKCE (S256), a single `mcp` scope, one consent screen, token cached and renewed by the client, explicit 401 / 403 / 429 semantics, and revocation under "Connected apps". That is the same pattern `notmuchproxy` implements in OIDC mode, arrived at independently.

### Does this change the recommendation?

**No, but it narrows the gap, and it changes what to fork if the IMAP-direct route is taken.**

If this project were built against live IMAP rather than notmuch, `bradsjm/mail-imap-mcp-rs` is now the best starting point in the survey: default-off write gating with runtime refusal, a published multi-arch image, an HTTP transport, a consolidated `apply_to_messages` tool surface, and a cancellable job model for long writes. It needs transport authentication added, which is a reverse proxy or ~200 lines.

What it does not change: the data-layer argument. iCloud offers no OAuth at all — app-specific passwords are the only option — so the OAuth work that looks daunting from outside is not on the critical path for his primary account. And having now read `oauth2.rs`, the Google/Microsoft refresh-grant is roughly 150 lines of well-understood code, not the hard part it appears to be.

Where mail-mcp genuinely leads everything else is **provider-quirk knowledge**, and that is worth copying regardless of which route is taken: provider-aware Sent-folder logic (Gmail dedupes by Message-ID, Zoho saves without deduping so an APPEND doubles the folder, Office 365 and generic relays save nothing), localised Sent-folder detection across seven languages, the Graph `createReply` attachment-loss bug and its fix, and MOVE-capability fallback to COPY + STORE + EXPUNGE with a UIDVALIDITY re-check between search and mutation. That last one is the single most useful twenty lines in the repository.

On OAuth proper, mail-mcp is not the leader. `marlinjai/email-mcp` (browser PKCE with automatic refresh) and `cldt-fr/imap-mcp` (full OAuth 2.1 resource server) are further along. mail-mcp leads on provider *coverage*, not on credential handling.

## Verification notes

- Star counts, fork counts, issue counts, creation dates and `pushed_at` timestamps come from GitHub's search API, which returned live data on 19 August 2026. GitHub's `/repos/` endpoint and rendered HTML pages were both serving stale cache during this research and were not relied on.
- Read-only claims for `codefuturist/email-mcp` were verified by reading `src/config/schema.ts`, `src/tools/register.ts` and `src/main.ts` directly. Every other read-only claim rests on README and configuration documentation, not on enforcement code.
- The licence of `n24q02m/better-email-mcp` is disputed between two independent checks (MIT vs Apache-2.0). Verify before relying on it.
- `tecnologicachile/mail-mcp`'s write gate, delete confirmation and OAuth token handling were verified in `src/config.rs`, `src/oauth2.rs`, `src/main.rs` and the first 2,669 lines of `src/server.rs` on 19 August 2026 (see Addendum). **`src/server.rs` was truncated by the fetch** — it ends mid-token at line 2,669 — so `bulk_delete_impl`, `bulk_move_impl`, `bulk_update_flags_impl`, `append_message_impl`, `search_and_delete_impl` and the four non-`smtp_send` send handlers were **not** verified. Their gating is documented but unread.
- `bradsjm/mail-imap-mcp-rs` was assessed from its README and the shared `config.rs` lineage only; its own source was not read, and its GHCR image was not pulled to confirm it resolves.
- The original sweep ranked candidates by star count, which systematically under-samples zero-star repositories. Three of the strongest designs found have zero stars.
- Exact last-commit timestamps could not be obtained for every repo; `pushed_at` is used as the proxy and labelled as such.
- Whether `ghcr.io/ai-zerolab/mcp-email-server` still resolves could not be confirmed. The root `Dockerfile` in `Wh1isper/mcp-email-server` returns 404 as of 19 August 2026.

## Sources

- [igor47/notmuchproxy](https://github.com/igor47/notmuchproxy) · [hgn/mcp-server-notmuch](https://github.com/hgn/mcp-server-notmuch) · [Wh1isper/mcp-email-server](https://github.com/Wh1isper/mcp-email-server) and its [security docs](https://github.com/Wh1isper/mcp-email-server/blob/main/docs/security.md)
- [codefuturist/email-mcp](https://github.com/codefuturist/email-mcp) — [src/tools/register.ts](https://github.com/codefuturist/email-mcp/blob/main/src/tools/register.ts), [src/main.ts](https://github.com/codefuturist/email-mcp/blob/main/src/main.ts), [src/config/schema.ts](https://github.com/codefuturist/email-mcp/blob/main/src/config/schema.ts)
- [tecnologicachile/mail-mcp](https://github.com/tecnologicachile/mail-mcp) · [n24q02m/better-email-mcp](https://github.com/n24q02m/better-email-mcp) · [jgalea/mailbox-mcp](https://github.com/jgalea/mailbox-mcp) · [nikolausm/imap-mcp-server](https://github.com/nikolausm/imap-mcp-server) · [cldt-fr/imap-mcp](https://github.com/cldt-fr/imap-mcp) · [marlinjai/email-mcp](https://github.com/marlinjai/email-mcp)
- [wyattjoh/jmap-mcp](https://github.com/wyattjoh/jmap-mcp) · [MadLlama25/fastmail-mcp](https://github.com/MadLlama25/fastmail-mcp) · [radiosilence/fastmail-cli](https://github.com/radiosilence/fastmail-cli)
- [GongRzhe/Gmail-MCP-Server (archived)](https://github.com/GongRzhe/Gmail-MCP-Server)
- [MCP specification — Tools (2025-06-18)](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
- [Anthropic Gmail connector](https://claude.com/connectors/gmail) · [Google Workspace connectors help](https://support.claude.com/en/articles/10166901-use-google-workspace-connectors)
- [Zapier MCP — Gmail](https://zapier.com/mcp/gmail) · [ComposioHQ/composio](https://github.com/ComposioHQ/composio) · [Pipedream MCP reference implementation](https://github.com/PipedreamHQ/pipedream/tree/master/modelcontextprotocol) · [Klavis-AI/klavis](https://github.com/Klavis-AI/klavis)
- Postmark incident: [Koi Security](https://www.koi.ai/blog/postmark-mcp-npm-malicious-backdoor-email-theft) · [Snyk](https://snyk.io/blog/malicious-mcp-server-on-npm-postmark-mcp-harvests-emails/) · [The Hacker News](https://thehackernews.com/2025/09/first-malicious-mcp-server-found.html)
- [Simon Willison on MCP prompt injection](https://simonwillison.net/2025/Apr/9/mcp-prompt-injection/)
