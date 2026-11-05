#!/bin/sh
# Renews the OAuth2 refresh token in oauth/token.txt.
#
# A provider with a sign-in frequency policy (for example 30 days) rejects
# the refresh token after that period, whatever its use. Renewing needs an
# interactive sign-in with MFA, so this cannot run unattended: it starts the
# device-code flow, you open the printed URL and enter the code, and the new
# token is written back to oauth/token.txt.
#
# Optional, in renew.env next to this script (never committed):
#   REMOTE_HOST=mailhost             ssh host to copy the token to
#   REMOTE_DIR=/srv/your-mail-mcp    compose directory on that host
#   REMIND_DAYS=27                   days until the next renewal is due
#
# The server needs no restart: mbsync runs pass_cmd on every connection and
# so reads the new file on the next sync.
set -eu
cd "$(dirname "$0")"

REMOTE_HOST=""
REMOTE_DIR=""
REMIND_DAYS=27
if [ -f renew.env ]; then
	. ./renew.env
fi

if [ ! -f oauth/token.txt ]; then
	echo "oauth/token.txt is missing; see README.md for the first authorization." >&2
	exit 1
fi

# The helper stores the expiry as a naive local time, and the container runs
# in UTC. Writing it in UTC here keeps the container from trusting an expired
# token for as long as this machine's UTC offset.
export TZ=UTC

# -u: the device-code message must appear before the script starts polling.
# The last line it prints is the access token itself; drop it.
python3 -u oauth/mutt_oauth2.py oauth/token.txt --authorize --authflow devicecode |
	awk 'length($0) > 200 && $0 !~ / / { next } { print }'
echo

# Confirms the new refresh token works without printing the access token.
python3 oauth/mutt_oauth2.py oauth/token.txt >/dev/null
echo "Token renewed."

if [ -n "$REMOTE_HOST" ]; then
	scp -q oauth/token.txt "$REMOTE_HOST:$REMOTE_DIR/oauth/token.txt"
	ssh "$REMOTE_HOST" "chmod 600 '$REMOTE_DIR/oauth/token.txt'"
	echo "Copied to $REMOTE_HOST:$REMOTE_DIR/oauth/token.txt"
fi

# BSD date (macOS) takes -v, GNU date takes -d.
due=$(date -v+"${REMIND_DAYS}"d +%Y-%m-%d 2>/dev/null ||
	date -d "+${REMIND_DAYS} days" +%Y-%m-%d)
echo "Renew again by $due."
