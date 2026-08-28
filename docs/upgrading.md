# Upgrading from 0.2.x

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
