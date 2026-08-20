# your-mail-mcp

A self-hosted MCP server that gives an MCP client (Claude, or any other client
that speaks streamable HTTP MCP with OAuth) read access to your mail. It
mirrors one or more IMAP accounts into a local maildir with
[mbsync](https://isync.sourceforge.io/), indexes them with
[notmuch](https://notmuchmail.org/), and answers tool calls from that index.

## What it cannot do

This is read-only, by construction, not by convention.

The mirror is pull-only. The generated mbsync configuration for every account
carries `Sync Pull`, `Create Near`, `Remove None`, `Expunge None` — nothing in
that configuration can push a change back to the server, delete a message, or
expunge one.

The only IMAP operation anywhere in the Go code is `LIST`, issued once per
account at startup to find each account's junk and trash folders (see
[Provider notes](#provider-notes) and [Troubleshooting](#troubleshooting)).
That connection logs in, lists mailboxes, and logs out. It never selects a
mailbox and never fetches a message.

There is no send, no delete, no move, and no tag. There is no attachment
export; attachments are listed by filename, media type and size in `show` and
`thread`, but never served. Nothing in the process holds write access to any
account.

Nine tools, all read-only:

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
| `refresh` | Sync INBOX now and report how many messages arrived. |

`search`, `ids`, `files` and `count` take a notmuch query (`from:`, `to:`,
`subject:`, `tag:`, `folder:`, `date:2026-01-01..2026-06-30`, combined with
and/or/not), an optional `account` to scope to one account, and can include
junk/trash with `include_excluded`.

## Quick start

```bash
cp accounts.example.json accounts.json
```

Edit `accounts.json` with your accounts (see [The accounts
file](#the-accounts-file) below), then set the environment variables the file
references — `WORK_PASS` and `PERSONAL_PASS` for the example — plus
`PUBLIC_URL` and `OAUTH_PASSPHRASE`, in a `.env` file next to `compose.yaml`
or in your shell:

```bash
export PUBLIC_URL=https://mail.example.com
export OAUTH_PASSPHRASE=some-long-passphrase
export WORK_PASS=...
export PERSONAL_PASS=...
```

Then start it:

```bash
docker compose up -d
```

The first run populates the maildir, which takes a while for a large mailbox
and is slower than later runs on purpose — one IMAP command at a time is
easier on providers that throttle. There is no separate initialization step.

Call the `folders` tool to confirm both accounts synced with no errors.

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
| `PUBLIC_URL` | yes | — | The external URL the server is reached at, exactly as a client will use it (a trailing slash, if any, is stripped). Used in OAuth metadata and must match what you type into the client. |
| `OAUTH_PASSPHRASE` | yes | — | The one passphrase that gates the consent screen. |
| `SYNC_INTERVAL` | no | `5m` | Full-sync period, as a Go duration (`5m`, `1h`). |
| `LISTEN_ADDR` | no | `:8080` | Address the HTTP server binds. |
| `INIT_MIRROR` | no | unset | Set to `1` to sync into an empty directory that is not a mount point. Not needed with compose, where `/mail` is a volume. |

`CONFIG`, `MAILDIR` and `INDEX` are required; the process refuses to start
without them. `PUBLIC_URL` and `OAUTH_PASSPHRASE` are required by the OAuth
layer and the process also fails to start without them.

The container image already sets four of these (`Dockerfile`):
`MAILDIR=/mail`, `INDEX=/index`, `CONFIG=/config/accounts.json`,
`LISTEN_ADDR=:8080`. `compose.yaml` doesn't override any of them. Leave them
alone unless you're also changing the matching volume mount or config mount
in `compose.yaml` — an override that doesn't move the mount with it points
the server at an empty or missing path.

## Connecting a client

In Claude, add a custom connector with the URL:

```
https://mail.example.com/mcp
```

— your `PUBLIC_URL` with `/mcp` appended. The client performs Dynamic Client
Registration, then opens the consent screen. The consent screen asks for one
thing: the passphrase you set as `OAUTH_PASSPHRASE`. There is no username and
no per-account login; one passphrase authorizes the whole server, all
accounts together. A wrong passphrase is rejected with a one-second delay, to
slow down guessing.

Once authorized, the client has an access token valid for one hour and a
refresh token to renew it. Restarting the server drops all issued access
tokens (they are in memory only); the client gets a 401 and refreshes
automatically, which is standard OAuth client behavior and needs no action
from you.

## Deployment examples

`docker compose up -d` runs the container with `compose.yaml`, which binds
the server to `127.0.0.1:8080` only — it is not reachable from outside the
host by itself. Something has to sit in front of it and terminate TLS on
`PUBLIC_URL`.

### Cloudflare Tunnel

Run `cloudflared` on the same host, pointed at `http://localhost:8080`, with
a public hostname that matches `PUBLIC_URL`. This gives you a public HTTPS
endpoint over an outbound connection, with no inbound port opened on your
network. Tailscale Funnel is an equivalent alternative for the same purpose.

Either way, once traffic is flowing through the tunnel you can restrict
ingress to Anthropic's published egress range, `160.79.104.0/21`, at the
tunnel or firewall level, since a custom connector is reached from Anthropic's
infrastructure rather than directly from your device.

### Volumes

`compose.yaml` mounts `accounts.json` read-only and two named volumes, `mail`
and `index`, for the maildir and the notmuch database. Back these up if you
don't want to re-sync from scratch after losing the host; there's nothing in
them that isn't also on the mail server, but a full mirror re-download of a
large mailbox takes a while.

### Building a multi-arch image

The image needs to run on both an arm64 Mac mini and an amd64 VPS from the
same tag. Build and push both platforms with `buildx`:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/wildsurfer/your-mail-mcp:latest --push .
```

## Provider notes

These were confirmed against real accounts while building this server.

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
  messages exist both under their folder and under All Mail.
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
