# Running it on a server

Pick this when you want the mirror to stay up whether or not your machine is
on. It costs a few dollars a month and one real trade-off: a full plaintext
copy of your mail moves onto a rented disk, with the app passwords in the
same environment. Read [Security](../README.md#security) before you choose
it.

The install is the [quick start](../README.md#quick-start) plus a tunnel, on
someone else's computer. The tunnel dials out, so the box opens no inbound
port and needs no DNS record or certificate of its own.

## Install

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

# 3. A public address, exactly as on your own machine
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

Connecting a client is the same as
[from your phone](../README.md#use-it-from-your-phone).

`restart: unless-stopped` in `compose.yaml` brings the containers back after a
reboot. Check on it with the `status` tool, which reports each account's last
sync and its last error, or with `docker compose logs --tail=50`.

Now read [Hardening](#hardening). A VPS you can SSH into with a password,
holding a copy of your mail, is worse than not running this at all.

## Hardening

None of this is needed to make the server work, which is why it is not in the
install steps. It is ordered by how much it buys you.

**Pick a real passphrase.** `OAUTH_PASSPHRASE` is the whole door. A wrong
guess costs the attacker one second, and guesses are serialised so running
them in parallel does not help, but neither of those saves a short passphrase.
Use a long one you can still type on a smartphone.

**Lock down SSH.** A rented box with password login and a copy of your mail
on it is the worst combination in this document. As root, before anything
else:

```bash
adduser mail && usermod -aG sudo mail
rsync --archive --chown=mail:mail ~/.ssh /home/mail
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/; s/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
systemctl restart ssh
```

Then do the install as `mail`, not as root.

**Close the ports you are not using.** With a tunnel you need no inbound
ports at all, so:

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
does not encrypt anything at rest. See [Security](../README.md#security).

## Your own domain and certificate instead of a tunnel

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
