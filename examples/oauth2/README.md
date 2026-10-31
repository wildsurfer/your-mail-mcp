# IMAP over XOAUTH2

Some providers accept IMAP only with an OAuth2 access token, which expires
after about an hour. Microsoft 365 is the common case: once basic
authentication is off for a tenant, a password no longer logs in. This
directory is a compose project for one such account. mbsync gets the token
through `pass_cmd` from mutt's `mutt_oauth2.py`, which refreshes it on every
connection and writes the new token back to disk.

The server never speaks the provider's OAuth2 itself. The published image
carries the Cyrus SASL XOAUTH2 plugin; the `Dockerfile` here adds python3
for the helper. The helper is GPL-licensed and not part of this repository,
so you download it yourself.

## Files

| File | Commit? | Purpose |
|---|---|---|
| `Dockerfile` | yes | The published image plus python3. |
| `compose.yaml` | yes | The service, with `./oauth` mounted writable. |
| `accounts.example.json` | yes | Template. Copy to `accounts.json`. |
| `.env.example` | yes | Template. Copy to `.env`. |
| `renew-token.sh` | yes | One-command token renewal, see below. |
| `accounts.json` | never | Your address and folder names. |
| `.env` | never | `PUBLIC_URL`, passphrase. |
| `renew.env` | never | Where `renew-token.sh` copies the token. |
| `oauth/mutt_oauth2.py` | never | Token helper, see below. |
| `oauth/token.txt` | never | Refresh token and current access token. |

The `.gitignore` at the repository root keeps the last five out.

## Client id (Microsoft 365)

The helper needs an OAuth client id that your tenant admits for IMAP: your
own app registration, or one your administrator approved. To register one
in the Microsoft Entra admin center:

1. App registrations → New registration. Any name. Under Redirect URI,
   choose "Public client/native" and enter
   `https://login.microsoftonline.com/common/oauth2/nativeclient`.
2. Authentication → "Allow public client flows": Yes. The device-code flow
   below needs it.
3. API permissions → Add a permission → APIs my organization uses →
   Office 365 Exchange Online → Delegated → `IMAP.AccessAsUser.All`. Add
   `offline_access` from Microsoft Graph too. Many tenants require an
   administrator to grant consent here.
4. Copy the Application (client) ID from the overview.

IMAP must also be enabled for the mailbox, which an Exchange administrator
can turn off per user.

## Token helper

Download `contrib/mutt_oauth2.py` from https://github.com/muttmua/mutt to
`oauth/` and change three things:

1. `ENCRYPTION_PIPE = ['cat']` and `DECRYPTION_PIPE = ['cat']`. The
   container has no GPG, so the token file is stored as plain JSON; the
   file mode protects it instead.
2. In the `microsoft` registration, set `client_id` to the id from above.
   Leave `client_secret` empty. For a single-tenant registration, replace
   `common` in the three endpoint URLs with your tenant id.
3. Narrow the `microsoft` scope to
   `'offline_access https://outlook.office.com/IMAP.AccessAsUser.All'`. The
   default also asks for POP and for sending mail as you, which a read-only
   mirror has no use for.

Create the token once, on a machine with a browser:

```sh
TZ=UTC python3 oauth/mutt_oauth2.py oauth/token.txt --authorize --authflow devicecode
chmod 600 oauth/token.txt
```

`TZ=UTC` matters: the helper stores the access token's expiry as a naive
local time, and the container runs in UTC. A token file written on a
machine an hour ahead of UTC makes the container keep using an expired
token for that hour, and every sync in it fails to log in.

Answer `microsoft`, `devicecode` and your address; then open the URL it
prints and enter the code. Afterwards every call without `--authorize`
prints a current access token, refreshing it when needed. That is what
`pass_cmd` runs. The helper refuses a token file whose mode is not `600`.

## Account file

`accounts.example.json` shows the shape. Two settings are not optional:

- `"auth_mechs": ["XOAUTH2"]` so mbsync offers only that mechanism.
- `exclude_folders` set explicitly. SPECIAL-USE discovery logs in with a
  plain IMAP `LOGIN`, which an OAuth2-only account refuses; a configured
  list skips discovery. The names depend on the mailbox language: an
  English mailbox has `Junk Email` and `Deleted Items`, a German one
  `Junk-E-Mail` and `Gelöschte Elemente`. The `folders` tool shows the real
  ones after the first sync.

## Run

The container runs as uid 1000 and must be able to write `oauth/`:

```sh
cp accounts.example.json accounts.json   # then edit
cp .env.example .env                     # then edit
chmod 700 oauth && sudo chown -R 1000:1000 oauth
docker compose build
docker compose run --rm --entrypoint python3 your-mail-mcp \
  /oauth/mutt_oauth2.py /oauth/token.txt >/dev/null && echo token ok
docker compose up -d
docker compose exec your-mail-mcp sh -c 'cat /tmp/your-mail-mcp*/mbsyncrc'
```

The generated mbsyncrc must show `PassCmd`, `AuthMechs XOAUTH2`, and for
each channel `Sync Pull` and `Create Near`.

## Renewing the token

The refresh token stops working after a period of disuse, or sooner when the
tenant enforces a sign-in frequency, for example every 30 days however often
the token is used. The symptom is an `invalid_grant` error from the helper
and an authentication failure on every sync. Renewal needs an interactive
sign-in, so it cannot be automated away.

`renew-token.sh` reduces it to one command:

```sh
./renew-token.sh
```

It runs the device-code flow against the existing `oauth/token.txt`, so no
questions are asked: open the URL, enter the code, confirm. It then checks
the new token, copies it to the server when `renew.env` names one, and
prints the date by which to renew again. `renew.env` (never committed):

```sh
REMOTE_HOST=mailhost
REMOTE_DIR=/srv/your-mail-mcp
REMIND_DAYS=27
```

The server needs no restart: mbsync runs `pass_cmd` on every connection and
reads the new file on the next sync.
