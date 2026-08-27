# your-mail-mcp

[![MCP registry](https://img.shields.io/badge/MCP_registry-io.github.wildsurfer%2Fyour--mail--mcp-blue)](https://registry.modelcontextprotocol.io/v0.1/servers?search=io.github.wildsurfer)
[![Glama score](https://glama.ai/mcp/servers/wildsurfer/your-mail-mcp/badges/score.svg)](https://glama.ai/mcp/servers/wildsurfer/your-mail-mcp)

Your mail already holds the answers: booking references, gate codes,
invoices, warranty periods, promises people made in writing. This server lets
your AI assistant find them.

**Ask it things like:**

- "Find the booking reference for the June ferry."
- "What was the wifi password the hotel sent last summer?"
- "What did the accountant answer about VAT, and when?"
- "Collect everything between me and the builder about the roof, in order,
  and summarize who promised what."
- "What arrived this morning, across all my accounts, that actually needs me?"

**Use it for:**

- **Search that understands questions.** Full-text search over your entire
  history, every account in one index, phrased the way you think instead of
  the way search syntax works.
- **Triage from your phone.** A morning summary of what came in overnight,
  with junk already filtered out, from wherever you are.
- **Mail as context for other work.** Pull the client's requirements out of
  the thread and into your coding or writing session, instead of retyping
  them.
- **Agents you can leave running.** The server can only read. A malicious
  email that reaches your assistant gets read and nothing more, because
  sending, deleting and moving do not exist here. That makes scheduled
  digests and always-on agents a calm thing to run.

Setup is two files and `docker compose up -d` — see [Running it](#running-it).

A self-hosted MCP server that gives an MCP client (Claude, or any other client
that speaks streamable HTTP MCP with OAuth) read access to your mail. It
mirrors one or more IMAP accounts into a local maildir with
[mbsync](https://isync.sourceforge.io/), indexes them with
[notmuch](https://notmuchmail.org/), and answers tool calls from that index.

![How your-mail-mcp works: mail is pulled from IMAP providers into a local mirror, indexed by notmuch, and served to an MCP client through an OAuth gate, with no write path back to the providers](docs/diagrams/how-it-works.png)

Mail only ever moves left to right in that picture. The one arrow the server
makes back toward a provider is an IMAP `LIST`, issued once per account at
startup and repeated hourly, to find out what that server calls its junk and
trash folders; it never selects a mailbox and never fetches a message. The
diagram source is
[`docs/diagrams/how-it-works.html`](docs/diagrams/how-it-works.html).

## What it cannot do

The read-only property is built into the architecture.

The mirror is pull-only. The generated mbsync configuration for every account
carries `Sync Pull`, `Create Near`, `Remove None`, `Expunge None` — nothing in
that configuration can push a change back to the server, delete a message, or
expunge one.

The only IMAP operation anywhere in the Go code is `LIST`, issued once per
account at startup and repeated hourly to find each account's junk and trash
folders (see [Provider notes](#provider-notes) and
[Troubleshooting](#troubleshooting)). An account whose `exclude_folders` is
set by hand skips that call entirely. That connection logs in, lists
mailboxes, and logs out. It never selects a mailbox and never fetches a
message.

There is no send, no delete, no move, and no tag. Attachments are listed in
`show` and `thread` and served read-only by the `attachment` tool, one part
at a time: images inline up to 5MB, textual parts as marked text, and other
binaries as a short-lived signed link to `GET /attachment/{id}/{part}`
(a bearer token works there too). Without an HTTP listener, an oversized
binary is saved under `/index/attachments/` and the tool returns the path to
`docker cp`. Nothing in the process holds write access to any account.

Eleven tools, all read-only:

| Tool | What it does |
|---|---|
| `search` | Search mail. Returns thread summaries as JSON. |
| `ids` | Return the message ids matching a query. |
| `files` | Return the maildir file paths matching a query. |
| `count` | Count the messages matching a query. |
| `show` | Show one message: headers and decoded body, as JSON. |
| `thread` | Show the whole thread containing a message. Excludes junk/trash replies by default; set `include_excluded` to include them. |
| `text` | Return the plain-text body of one message, converting HTML. |
| `folders` | List accounts, their folders, index tags, and each account's last sync and last error. |
| `refresh` | Sync every folder of one account or all accounts now, then reindex. Waits up to 20 seconds; if the pass is still running it says so. |
| `status` | Sync health per account: first-sync completion, last sync, messages indexed, errors and backoff. |
| `attachment` | One attachment or MIME part of a message, by part number from `show`. Images inline, text (JSON and XML included) as a marked block, other binaries as a signed download link. |

`search`, `ids`, `files` and `count` take a notmuch query (`from:`, `to:`,
`subject:`, `tag:`, `folder:`, `date:2026-01-01..2026-06-30`, combined with
and/or/not), an optional `account` to scope to one account, and can include
junk/trash with `include_excluded`. While an account's mirror is still
filling, these four tools prepend a note naming the account and how many
messages are indexed so far.

## Why not one of the others

There are around forty email MCP servers on GitHub. The full survey, including
which claims were read in source and which were taken from a README, is in
[`docs/research/email-mcp-landscape.md`](docs/research/email-mcp-landscape.md).
Two of them are close enough to this one to be worth naming here.

**[igor47/notmuchproxy](https://github.com/igor47/notmuchproxy)** is the nearest
thing to this server that already existed, and a large part of why this one has
the shape it does. It reads a notmuch archive, has no write path to disable
rather than a flag that turns one off, ships a ghcr image, and takes either a
static bearer token or full OIDC with dynamic client registration. Its query
validation, which rejects an unknown prefix with an explanation instead of
returning an empty result that looks like an empty mailbox, is a better idea
than anything here started with, and `validateQuery` in `notmuch.go` is that
idea reimplemented.

Two things differ. It reads an archive you keep up to date yourself, so it
assumes you already run mbsync and notmuch; this server generates the mbsync
configuration, syncs every account in parallel, and discovers each account's
junk and trash over IMAP `LIST`. And it has no `account` parameter, so several
mailboxes in one index are addressed through tags or folder queries, where here
`account` is a parameter on every query tool. If you already run a notmuch
setup you are happy with, notmuchproxy is the smaller thing to deploy and you
should use it instead of this.

**[hgn/mcp-server-notmuch](https://github.com/hgn/mcp-server-notmuch)** is stdio
only, so one client on one machine, but its handling of untrusted content is the
best in the survey. The single `render()` chokepoint here, which marks every
byte of mail text in one place so that no individual tool can forget to, comes
from its `render.py`.

Everything else surveyed talks live IMAP and ships a send path, which is the
opposite of both choices this server rests on.

### How proven this is

One author, one operator, three real accounts: one iCloud and two Gmail. No
third-party security review, and nobody else has deployed it. notmuchproxy has
zero stars and a more convincing production story than this does. Read
[Security](#security) and [What it cannot do](#what-it-cannot-do) before you
point this at a mailbox you care about.

## Upgrading from 0.2.x

The image now starts in stdio mode when it is given no command, so a compose
file without a `command:` line gets a container that reads end of input and
exits at once, over and over. Add `command: serve` to the service before you
pull the new image. The `compose.yaml` in this repository already has it.

Two directories appear by themselves after the upgrade. The mail volume gains
a `<name>-recent/` per account, an INBOX-only mirror capped at 1000 messages
that fills within minutes while the full history catches up, so the first
sync after the upgrade pulls those messages again. The index volume gains
`mcp.sock`, the socket local clients attach to, and `attachments/`, where
binary parts are written when there is no HTTP listener to link them from.

Nothing else changes. The accounts file, the environment variables and the
tools are the same.

## Running it

Three ways to run this. They differ in one thing: who can reach the server.
Start at case 1 and move up only when you need to. None of them is hardened
beyond the defaults — that is [Hardening](#hardening), further down, and it is
deliberately separate so you can get the thing working first.

| | Where it runs | Who can reach it | Your mail is stored on |
|---|---|---|---|
| **1** | your machine | that machine only | your machine |
| **2** | your machine | you, from anywhere | your machine |
| **3** | a VPS | you, from anywhere | a rented disk |

The server ships as a container image at
`ghcr.io/wildsurfer/your-mail-mcp`, built and published by CI for amd64 and
arm64. Nothing needs compiling, and every case starts the same way — two
files in an empty directory:

```bash
mkdir your-mail && cd your-mail
curl -fsSLO https://raw.githubusercontent.com/wildsurfer/your-mail-mcp/main/compose.yaml
curl -fsSL https://raw.githubusercontent.com/wildsurfer/your-mail-mcp/main/accounts.example.json -o accounts.json
```

Edit `accounts.json` with your accounts (see
[The accounts file](#the-accounts-file)), then put the secrets it references
in a `.env` file next to `compose.yaml`:

```bash
# .env
OAUTH_PASSPHRASE=pick-a-long-one-you-can-type-on-a-smartphone
WORK_PASS=your-gmail-app-password
PERSONAL_PASS=your-icloud-app-specific-password
```

`OAUTH_PASSPHRASE` is the only credential between the internet and your mail
in cases 2 and 3. Treat it accordingly.

These two files hold your mail passwords. If you ever put this directory
under version control or into a backup that leaves the machine, treat them
accordingly.

---

### Case 1 — on your machine, for your machine only

Start the stack without `PUBLIC_URL`. There is no listener, no OAuth and no
port; local clients attach through Docker.

```bash
docker compose up -d
docker compose logs -f          # watch the first sync
```

The first sync populates the maildir, into two directories per account:
`<name>/` for the full history and `<name>-recent/` for a fast INBOX-only
pass. Today's INBOX mail is searchable within minutes; the full history
follows at whatever pace the provider allows, and `status` reports how far it
has got.

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

---

### Case 2 — on your machine, reachable from anywhere

Same server, plus something that gives it a public HTTPS address. Your mail
stays on your machine, and nothing listens on your home network, because the
tunnel dials out. You need this for the smartphone and desktop apps: a custom
connector is fetched by the vendor's servers, so it cannot reach a private
address.

#### With Tailscale (no domain needed)

One command, same on macOS and Linux, and you get an HTTPS hostname without
owning a domain.

```bash
tailscale funnel --bg 8080
```

`--bg` keeps it running across reboots. It prints the public URL, which looks
like `https://your-machine.your-tailnet.ts.net`. That is the hostname to use:

```bash
# .env
PUBLIC_URL=https://your-machine.your-tailnet.ts.net
```

```bash
docker compose up -d
```

Funnel needs HTTPS certificates and the Funnel node attribute enabled for
your tailnet; the CLI offers to add the policy line the first time, and the
rest is in your admin console. `tailscale funnel status` shows what is
exposed, and `tailscale funnel --https=443 off` takes it down.

#### With Cloudflare (you own a domain, and it is on Cloudflare)

Use this if you want a hostname on your own domain rather than a `.ts.net`
one. `mail.example.com` below is **your** domain, already added to your
Cloudflare account — Cloudflare does not hand you a hostname for a named
tunnel.

```bash
cloudflared tunnel login
cloudflared tunnel create your-mail
```

`create` prints the tunnel's UUID and the credentials file it just wrote:

```
Tunnel credentials written to /Users/you/.cloudflared/f9e2…-… .json
Created tunnel your-mail with id f9e2…-…
```

Use that exact path below; `cloudflared tunnel list` prints the UUID again if
you lose it. Route the hostname, then write `~/.cloudflared/config.yml`:

```bash
cloudflared tunnel route dns your-mail mail.example.com
```

```yaml
tunnel: your-mail
credentials-file: /Users/you/.cloudflared/f9e2….json   # the path create printed
url: http://localhost:8080
```

```bash
cloudflared tunnel run your-mail
```

To keep it running: on Linux, `sudo cloudflared service install`. On macOS,
install it through Homebrew and use `brew services start cloudflared`, because
the `sudo` install path looks for its certificate under the root user's home
and will not find the one `cloudflared tunnel login` wrote to yours.

Then set `PUBLIC_URL=https://mail.example.com` in `.env` and
`docker compose up -d`.

#### Either way

`PUBLIC_URL` has to match what you type into the client exactly. The server
publishes `PUBLIC_URL + /mcp` as the `resource` in its OAuth metadata, and a
mismatch there is the most common reason a connector refuses to add.

One thing to know before you start on a smartphone: **neither Claude nor
ChatGPT lets you add a connector from the smartphone app.** You add it once on
the web (or in Claude's desktop app), and it then shows up on your smartphone.
Trying to do the setup on the smartphone itself will waste your time.

**Claude — add on web or desktop, then use on your smartphone**

1. On [claude.ai](https://claude.ai) or in Claude Desktop, go to
   **Settings → Connectors**, and click **+** next to Connectors, or
   **Add custom connector**.
2. Give it a name and the URL `<PUBLIC_URL>/mcp`. Leave the advanced OAuth
   fields empty: this server registers clients dynamically.
3. Claude opens the consent page. Enter your `OAUTH_PASSPHRASE`.
4. Open the Claude app on your smartphone. The connector is already there, and
   the tools are available in a chat. Turn it on for a conversation from the
   tools or connectors menu in the composer.

**ChatGPT — add on web, then use on your smartphone**

Custom MCP connectors live behind developer mode, which needs a Pro, Plus,
Business, Enterprise or Education account and is only available on the web.

1. In ChatGPT on the web, open **Settings → Security and login** and turn on
   **Developer mode**. On Business and Enterprise workspaces an admin may have
   to allow it first.
2. Add a connector for a remote MCP server and give it the URL
   `<PUBLIC_URL>/mcp`, with OAuth as the authentication. ChatGPT supports
   dynamic client registration, so there is nothing to paste.
3. Approve the consent page with your `OAUTH_PASSPHRASE`.
4. Open ChatGPT on your smartphone and enable the connector in a chat.

These menus move. If the names above do not match what you see, look for
developer mode in settings, then for the place that adds a connector by URL.

ChatGPT disables some MCP write actions on mobile. That has no effect here,
because this server has no write actions at all.

**Claude Code**

```bash
claude mcp add --transport http your-mail https://your-host/mcp
```

**Codex**

```bash
codex mcp add your-mail --url https://your-host/mcp
codex mcp login your-mail
```

---

### Case 3 — on a VPS, reachable from anywhere

Pick this when you want the mirror to stay up whether or not your machine is
on. It costs a few dollars a month and one real trade-off: a full plaintext
copy of your mail moves onto a rented disk, with the app passwords in the same
environment. Read [Security](#security) before you choose it.

The install is case 1 plus a tunnel, on someone else's computer. No ports to
open, no DNS to configure, no certificates to manage.

On a fresh Debian or Ubuntu box:

```bash
# 1. Docker
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER && newgrp docker

# 2. The two files, and your accounts
mkdir your-mail && cd your-mail
curl -fsSLO https://raw.githubusercontent.com/wildsurfer/your-mail-mcp/main/compose.yaml
curl -fsSL https://raw.githubusercontent.com/wildsurfer/your-mail-mcp/main/accounts.example.json -o accounts.json
$EDITOR accounts.json             # your accounts
$EDITOR .env                      # OAUTH_PASSPHRASE and the account passwords

# 3. A public address, exactly as in case 2
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
tailscale funnel --bg 8080        # prints your https://….ts.net hostname

# 4. Put that hostname in .env, then start
echo "PUBLIC_URL=https://your-machine.your-tailnet.ts.net" >> .env
docker compose up -d
docker compose logs -f
```

`PUBLIC_URL` comes last because you do not know the hostname until step 3
prints it.

Connecting a client is identical to case 2.

`restart: unless-stopped` in `compose.yaml` brings the containers back after a
reboot. Check on it with the `folders` tool, which reports each account's last
sync and its last error, or with `docker compose logs --tail=50`.

Now go and read [Hardening](#hardening). A VPS you can SSH into with a
password, holding a copy of your mail, is worse than not running this at all.

---

## Hardening

None of this is needed to make the server work, which is why it is not in the
install steps. It is ordered by how much it buys you. Case 1 needs none of it.

**Pick a real passphrase.** `OAUTH_PASSPHRASE` is the whole door. A wrong
guess costs the attacker one second, and guesses are serialised so running
them in parallel does not help, but neither of those saves a short passphrase.
Use a long one you can still type on a smartphone.

**Lock down SSH** (case 3). A rented box with password login and a copy of
your mail on it is the worst combination in this document. As root, before
anything else:

```bash
adduser mail && usermod -aG sudo mail
rsync --archive --chown=mail:mail ~/.ssh /home/mail
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/; s/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
systemctl restart ssh
```

Then do the install as `mail`, not as root.

**Close the ports you are not using** (case 3). With a tunnel you need no
inbound ports at all, so:

```bash
sudo ufw allow OpenSSH && sudo ufw --force enable
```

**Restrict who can reach the connector.** If the only thing that talks to your
server is a custom connector in a Claude app, that traffic arrives from
Anthropic's published egress range, `160.79.104.0/21`, and you can refuse
everything else at the tunnel or firewall. Do not do this if you also use
Claude Code or Codex from a laptop, since those connect from wherever you are.

**Back up the volumes, or accept a re-sync.** `compose.yaml` keeps the maildir
and the index in named volumes. Nothing in them is unique — it is all still on
your mail server — but re-downloading a large mailbox takes a while and annoys
providers that throttle.

**Know what the passphrase does not protect.** It gates the MCP surface. It
does not encrypt anything at rest. See [Security](#security).

<details>
<summary>Your own domain and certificate instead of a tunnel</summary>

If you would rather terminate TLS yourself on a domain you own, point an `A`
record at the box and put Caddy in front. Add `compose.override.yaml`:

```yaml
services:
  caddy:
    image: caddy:2
    restart: unless-stopped
    ports: ["80:80", "443:443"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
volumes:
  caddy_data:
```

```
# Caddyfile
mail.example.com {
    reverse_proxy your-mail-mcp:8080
}
```

Open both ports — 80 is not optional, Caddy uses it for the certificate
challenge and the HTTPS redirect:

```bash
sudo ufw allow 80/tcp && sudo ufw allow 443/tcp
```

Caddy obtains and renews the certificate itself. Set `PUBLIC_URL` to the
hostname and `docker compose up -d`.
</details>

## The accounts file

Mounted read-only at `/config/accounts.json` (see `compose.yaml`). JSON,
parsed with `encoding/json`, expanded against the process environment before
parsing so `${VAR}` in any string value is replaced with the environment
variable of that name. This is how secrets stay out of the file:

```json
{
  "accounts": [
    {
      "name": "work",
      "host": "imap.gmail.com",
      "user": "you@example.com",
      "password": "${WORK_PASS}"
    }
  ]
}
```

Per-account keys:

| Key | Default | Notes |
|---|---|---|
| `name` | — | Required. No spaces, quotes, or slashes (forward or back). Becomes the top-level maildir directory for the account and the `account` argument in tool calls. |
| `host` | — | Required. IMAP server hostname. |
| `port` | `993` (`imaps`) or `143` (otherwise) | |
| `user` | — | Required. See [Provider notes](#provider-notes): iCloud wants the short name, not the full email address. |
| `password` | — | Required. `${VAR}` expands from the environment; a literal password also works but is not recommended. |
| `tls` | `imaps` | `imaps`, `starttls`, or `none`. |
| `patterns` | `["*"]` | mbsync folder patterns — which folders to mirror. |
| `exclude_folders` | discovered automatically | Folder names to exclude from search by default (see [SPECIAL-USE discovery](#troubleshooting)). Setting this overrides discovery entirely for that account. |

An account name must be unique. At least one account is required; an empty
`accounts` array is a startup error.

## Environment variables

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `CONFIG` | yes | — | Path to the accounts file. |
| `MAILDIR` | yes | — | Maildir root; each account gets a subdirectory. |
| `INDEX` | yes | — | notmuch/Xapian index directory. |
| `PUBLIC_URL` | no | — | The external URL the server is reached at, exactly as a client will use it (a trailing slash, if any, is stripped). Used in OAuth metadata and must match what you type into the client. Unset means no HTTP listener and no OAuth: local sessions only, over `stdio`. |
| `OAUTH_PASSPHRASE` | when `PUBLIC_URL` is set | — | The one passphrase that gates the consent screen. |
| `SYNC_INTERVAL` | no | `10m` | Full-sync period, as a Go duration (`5m`, `1h`). The default follows Google's recommended IMAP client cadence of 10 minutes. An account that keeps failing is retried at twice this interval, then four times, capped at an hour, so a provider outage or quota lockout is not hammered. |
| `SYNC_TIMEOUT` | no | `1h` | Per-account deadline for one mbsync run, as a Go duration. A run cut off by the deadline resumes where it stopped on the next pass, so a large first mirror completes in chunks. Think before raising it on a multi-account setup: accounts sync one at a time, so one account stalled on a throttled connection blocks the others for the whole deadline. |
| `LISTEN_ADDR` | no | `:8080` | Address the HTTP server binds. |
| `INIT_MIRROR` | no | unset | Set to `1` to sync into an empty directory that is not a mount point. Not needed with compose, where `/mail` is a volume. |

`CONFIG`, `MAILDIR` and `INDEX` are required; the process refuses to start
without them. `PUBLIC_URL` is optional: leave it unset and the process runs
with no HTTP listener and no OAuth, serving only the `stdio` bridge. Set it,
and `OAUTH_PASSPHRASE` becomes required too; the OAuth layer fails to start
without it in that case.

The container image already sets four of these (`Dockerfile`):
`MAILDIR=/mail`, `INDEX=/index`, `CONFIG=/config/accounts.json`,
`LISTEN_ADDR=:8080`. `compose.yaml` doesn't override any of them. Leave them
alone unless you're also changing the matching volume mount or config mount
in `compose.yaml` — an override that doesn't move the mount with it points
the server at an empty or missing path.

## Without Docker

Release binaries for Linux and macOS, amd64 and arm64, are on the
[releases page](https://github.com/wildsurfer/your-mail-mcp/releases), with
checksums. The binary shells out to `mbsync`, `notmuch` and `w3m`, so install
those first — `brew install isync notmuch w3m` on macOS,
`apt install isync notmuch w3m` on Debian and Ubuntu. isync 1.4.4 or newer
works.

Then the same configuration as the container, with paths of your choosing.
The container's volumes start out as mount points, which the empty-maildir
guard reads as a genuine first run; a plain directory you create yourself
looks exactly like a missing volume to that same guard, so it needs
`INIT_MIRROR=1` to say it really is meant to be a first run here:

```bash
mkdir -p mail index
CONFIG=./accounts.json MAILDIR=./mail INDEX=./index INIT_MIRROR=1 \
PUBLIC_URL=http://127.0.0.1:8080 OAUTH_PASSPHRASE=... \
WORK_PASS=... ./your-mail-mcp
```

Windows is not supported: the maildir handling leans on Unix filesystem
semantics, and there is no mbsync to shell out to.

## Building it yourself

CI builds, tests and publishes every image, so nobody has to — but it is one
command if you want to: `docker build -t your-mail-mcp .` for the container,
or `go build` for the binary (Go 1.27, with the three tools above on PATH for
the tests).

## Provider notes

The iCloud notes come from long-running operation of a real iCloud mirror
that predates this server. The Gmail and Dovecot notes come from provider
documentation and the project's research, and have not all been re-verified
through this server yet.

- **iCloud** (`imap.mail.me.com`): the IMAP `user` is the short name — the
  part before `@icloud.com` — not the full email address. iCloud throttles
  concurrent IMAP connections; this is why the generated mbsync
  configuration pins `PipelineDepth 1` for every account, and it is not
  configurable.
- **Gmail** (`imap.gmail.com`): requires an App Password, which requires
  2-step verification to be enabled on the account first — Gmail does not
  accept the account password directly over IMAP. Gmail also keeps a copy of
  essentially everything in `[Gmail]/All Mail`, so a Gmail account's mirror
  is roughly double the size of what the folder list suggests, since most
  messages exist both under their folder and under All Mail. The first
  mirror of a large Gmail account takes hours, and Google also enforces a
  daily IMAP download quota (about 2.5GB per day), so a multi-gigabyte
  mailbox spreads its first mirror over several days. This is normal: the
  server keeps retrying on its schedule and mbsync resumes where it
  stopped. Set `SYNC_TIMEOUT` to something like `8h` for the first mirror
  so a long run is not cut off by the default one-hour deadline.
- **Dovecot** servers (many self-hosted and smaller providers) commonly
  prefix folder names with `INBOX.` (e.g. `INBOX.Sent`). If `folders` shows
  folder names you didn't expect, this is usually why.

## Security

Account passwords are supplied through the process environment (`${VAR}` in
`accounts.json`, or literal values). At startup, the server writes them into
a generated mbsync configuration file on disk inside the container, at file
mode `0600`. That file is not encrypted. Anything that can read the
container's environment, or that file, can read the passwords in plain
text.

Protection at rest — disk encryption, restricting who can exec into the
container, access to the host — is the operator's responsibility. This
server makes no claim of encrypting credentials at rest, and does not
attempt to.

The OAuth passphrase is checked in constant time and gates the whole server
with a single shared secret; it is not a per-user credential system. Treat
`OAUTH_PASSPHRASE` and the mail account passwords with the same care.

A mailbox is a secret store. Password resets, sign-in codes and magic links
all arrive by mail, so read access alone is enough to take over accounts if
it lands in the wrong hands or the wrong AI session. The read-only design
and the untrusted-content markers remove the write path and the instruction
channel; they do not make mail contents harmless. Connect clients you trust,
and remember that everyone holding the passphrase sees the whole mailbox —
there is one consent, not per-user accounts. Agent workflows that need their
own inboxes need their own addresses, which is a different tool.

`search`'s thread summaries include a display name for every message in a
matching thread, which a sender controls. A message in a folder excluded by
default (junk, trash) can still put its own attacker-chosen name in front of
you this way, even though its body never does — `search` does not fetch or
show the body of an excluded message. `thread` and `show` are read paths, not
subject to this: `thread` excludes junk/trash replies by default (see the
tools table above), and `show` reads a single message you already have the
id for. This display-name leak in `search` is not fixed in this release.

## Troubleshooting

**"maildir ... is an empty plain directory, not a mount point: refusing to
sync"** — the server checks whether your maildir is a mounted filesystem. A
mounted volume that happens to be empty is a first run and syncs without any
opt-in, which is why compose needs no extra step. An empty *plain* directory
is ambiguous: a fresh maildir looks exactly like a path whose volume was
never mounted, and syncing into the second one re-downloads every account
into a directory that disappears the moment you fix the mount. Either mount
the storage where `MAILDIR` points, or set `INIT_MIRROR=1` if it really is
meant to be an ordinary directory on this filesystem.

**"maildir ...: no such file or directory"** — the path does not exist at
all. With compose that means the volume or bind mount is missing from
`compose.yaml`; running the binary directly, it means `MAILDIR` is wrong.

**Check per-account sync status with the `folders` tool.** It lists every
configured account, its last successful sync time, its last error if any,
its folders, and the tags in the index. A single account with a bad
password or an expired app-specific password does not stop the others —
sync failures are isolated per account — but it will show up here as a
`last error` line, not as silence.

**Junk/trash exclusion, two different failure shapes:**

- **"special-use discovery: account NAME: ..." in the container logs**
  means the startup connect, login, or `LIST` for that account failed
  outright. On that failure there are no folder names to fall back to
  matching against, so that account gets **nothing excluded at all** — not
  even by the built-in English name list — until the connection problem is
  fixed or `exclude_folders` is set for it by hand.
- **No error line, but `folders` still shows nothing excluded** means the
  `LIST` succeeded — the server just doesn't advertise `\Junk`/`\Trash`
  attributes (no RFC 6154 SPECIAL-USE support) *and* its folder names don't
  match the built-in English list (`junk`, `spam`, `trash`, `deleted
  messages`, `deleted items`, `bulk mail`). This is the localised-mailbox
  case — a German or French mailbox, for instance — and the fix is the same:
  set `exclude_folders` by hand.

`exclude_folders` in `accounts.json`, e.g. `"exclude_folders":
["Papierkorb"]`, takes priority over both SPECIAL-USE and the built-in list
in every case.
