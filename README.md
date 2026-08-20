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

First run only, to populate an empty maildir:

```bash
INIT_MIRROR=1 docker compose up
```

The maildir has an initialization marker file. Without `INIT_MIRROR=1` set,
the server refuses to sync into a maildir that doesn't have one — this is
deliberate, see [Troubleshooting](#troubleshooting). Once the marker exists,
drop the flag for normal runs:

```bash
docker compose up -d
```

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
| `INIT_MIRROR` | no | unset | Set to `1` to allow the first sync into an empty maildir. |

`CONFIG`, `MAILDIR` and `INDEX` are required; the process refuses to start
without them. `PUBLIC_URL` and `OAUTH_PASSPHRASE` are required by the OAuth
layer and the process also fails to start without them.

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

## Troubleshooting

**"maildir ... has no .your-mail-mcp-initialised marker: refusing to
sync"** — this is the empty-volume guard. It exists because an unmounted or
mistyped volume looks exactly like an empty mailbox, and syncing into it
would trigger a full re-download of every account the moment you noticed
and fixed the mount. Run once with `INIT_MIRROR=1` to opt in; the server
writes the marker and every later sync proceeds normally without the flag.

**Check per-account sync status with the `folders` tool.** It lists every
configured account, its last successful sync time, its last error if any,
its folders, and the tags in the index. A single account with a bad
password or an expired app-specific password does not stop the others —
sync failures are isolated per account — but it will show up here as a
`last error` line, not as silence.

**"special-use discovery: account NAME: ..." in the container logs** means
the startup `LIST` against that account's server failed or the server
didn't respond usefully — a network problem, bad credentials, or (for
servers that don't support RFC 6154 SPECIAL-USE at all) simply no
attributes to read. This is not fatal: the account still syncs. What it
means is that junk/trash exclusion falls back to a built-in list of common
English folder names (`junk`, `spam`, `trash`, `deleted messages`, `deleted
items`, `bulk mail`), matched case-insensitively against the last path
component. If your junk folder has a different name — a non-English
locale, or something the server just calls something else — set
`exclude_folders` for that account explicitly in `accounts.json`, e.g.
`"exclude_folders": ["Papierkorb"]`, and it takes priority over both
SPECIAL-USE and the built-in list.
