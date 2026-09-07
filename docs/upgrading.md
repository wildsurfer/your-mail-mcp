# Upgrading from 0.3.x

Nothing to do. Pull the image and restart; the accounts file, the environment
variables and the tools are the same.

Two things change by themselves on the first sync after the upgrade. The
`<name>-recent/` directory in the mail volume is deleted once an account's
full mirror has completed, because everything in it is a duplicate of the
full mirror by then. The index volume gains a `complete-<name>` marker file
per account, which is how completion now survives a restart: before this
release it lived only in memory, so every restart ran the INBOX-only recent
channel again against a finished mirror.

An account whose full mirror has not completed yet keeps its recent store and
its extra login per pass, exactly as before, until the first full pass
succeeds.

New in this release, and off unless you ask for it: `expunge_local` on an
account. See
[the accounts file](reference.md#the-accounts-file) for what it does and what
it costs.
