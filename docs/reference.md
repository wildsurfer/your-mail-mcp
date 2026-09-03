# Reference

What an operator looks up once the server is running. The install itself is
in the [README](../README.md).

- [The accounts file](#the-accounts-file)
- [Environment variables](#environment-variables)
- [Provider notes](#provider-notes)
- [Troubleshooting](#troubleshooting)
- [Without Docker](#without-docker)
- [Building it yourself](#building-it-yourself)

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
| `name` | — | Required. No spaces, quotes, or slashes (forward or back), and it may not end in `-recent`. Becomes the top-level maildir directory for the account and the `account` argument in tool calls. |
| `host` | — | Required. IMAP server hostname. |
| `port` | `993` (`imaps`) or `143` (otherwise) | |
| `user` | — | Required. See [Provider notes](#provider-notes): iCloud wants the short name, not the full email address. |
| `password` | — | Required. `${VAR}` expands from the environment; a literal password also works but is not recommended. |
| `tls` | `imaps` | `imaps`, `starttls`, or `none`. |
| `patterns` | `["*"]` | mbsync folder patterns — which folders to mirror. |
| `exclude_folders` | discovered automatically | Folder names to exclude from search by default (see [SPECIAL-USE discovery](#troubleshooting)). Setting this overrides discovery entirely for that account. |
| `expunge_local` | `false` | Set to `true` to physically remove near-side Maildir copies after a message disappears remotely. This generates `Expunge Near`; the IMAP side remains protected by `Sync Pull`, `Create Near`, and `Remove None`. Whole remote folder deletion is not propagated. With this set the mirror stops being a backup: mail deleted remotely, including by a compromised account being emptied, is removed locally on the next pass. With the default `false`, deleted mail stays on disk and is only hidden from search by the `deleted` tag. |

An account name must be unique. With no accounts file, or an empty
`accounts` array, the server still starts and the `status` tool says so;
add the file and restart.

## Environment variables

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `CONFIG` | yes | — | Path to the accounts file. |
| `MAILDIR` | yes | — | Maildir root; each account gets a subdirectory. |
| `INDEX` | yes | — | notmuch/Xapian index directory. |
| `PUBLIC_URL` | no | — | The external URL the server is reached at, exactly as a client will use it (a trailing slash, if any, is stripped). Used in OAuth metadata and must match what you type into the client. Unset means no HTTP listener and no OAuth: local sessions only, over `stdio`. |
| `OAUTH_PASSPHRASE` | when `PUBLIC_URL` is set | — | The one passphrase that gates the consent screen. |
| `SYNC_INTERVAL` | no | `10m` | Full-sync period, as a Go duration (`5m`, `1h`). The default follows Google's recommended IMAP client cadence of 10 minutes. An account that keeps failing is retried at twice this interval, then four times, capped at an hour, so a provider outage or quota lockout is not hammered. |
| `SYNC_TIMEOUT` | no | `1h` | Per-account deadline for one mbsync run, as a Go duration. A run cut off by the deadline resumes where it stopped on the next pass, so a large first mirror completes in chunks. Accounts sync in parallel, so a long deadline on one account does not hold up the others. |
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

**The container exits at once, over and over.** The image starts in `stdio`
mode when it is given no command, reads end of input and exits. A compose
file needs `command: serve`; the one in this repository has it. See
[Upgrading from 0.2.x](upgrading.md).

**Check per-account sync status with the `status` and `folders` tools.**
`status` reports, per account, whether the full mirror is complete, the last
sync, how many messages are indexed, the last error and any backoff.
`folders` lists every configured account, its folders, and the tags in the
index. A single account with a bad password or an expired app-specific
password does not stop the others — sync failures are isolated per account —
but it will show up here as a `last error` line, not as silence.

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
WORK_PASS=... ./your-mail-mcp serve
```

Leave `PUBLIC_URL` and `OAUTH_PASSPHRASE` out for a socket-only daemon, and
attach a local client with `./your-mail-mcp stdio` in the same environment.

Windows is not supported: the maildir handling leans on Unix filesystem
semantics, and there is no mbsync to shell out to.

## Building it yourself

CI builds, tests and publishes every image, so nobody has to — but it is one
command if you want to: `docker build -t your-mail-mcp .` for the container,
or `go build` for the binary (Go 1.27, with the three tools above on PATH for
the tests).
