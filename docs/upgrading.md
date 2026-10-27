# Upgrading from 0.4.x

Nothing to do. Pull the image and restart; the accounts file, the environment
variables and the tools are the same.

Three things change by themselves after the upgrade.

The socket for local clients moves from `mcp.sock` in the index volume to
`/tmp/your-mail-mcp.sock` inside the container, because Docker Desktop cannot
bind a Unix socket in a bind-mounted host directory. A `stdio` session started
with `docker exec` finds the new path by itself. The old `mcp.sock` file in the
index volume is not used any more and can be deleted.

A folder that is deleted or renamed on the server no longer makes every later
sync of its account fail. The local copy of the folder stays, with its mail.
Only an empty local folder whose server folder is gone is removed. An account
that fails today with "far side box ... cannot be opened anymore" recovers on
the first pass after the upgrade.

HTTP sessions are stateless now, which the ChatGPT app needs to connect.

New in this release, and off unless you set them: `pass_cmd` and `auth_mechs`
on an account. See [the accounts file](reference.md#the-accounts-file) for
what they do.
