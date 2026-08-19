# Repository shape: `mail-ai-tools/mcp` and the alternatives

Addendum to `mcp-mail-server-naming.md`. Checked 19 August 2026.

## Availability

GitHub users and organisations share one namespace, so a 404 means the name is free for either.

| Name | Status |
|---|---|
| `mail-ai-tools` | **Free** (404) |
| `mailaitools` | Free (404) |
| `ai-mail-tools` | Free (404) |
| `mailai` | Free (404) |
| `mail-tools` | Free (404) |
| `mail-ai` | **Taken** (200) |
| `mailtools` | **Taken** (200) |
| `aimail` | **Taken** (200) |
| `ai-mail` | **Taken** (200) |
| `mailrune` | **Free** (404) — free as an organisation, not merely as a user |

So the shape he wants is available. The four closest variants are gone, which is worth noting: it means `mail-ai-tools` will be routinely confused with `mail-ai` and `aimail`, both of which already exist and neither of which is his.

## The bare `mcp` repo name

**On clone.** `git clone` derives the directory from the repo name, so this lands as `mcp/`. Anyone with two MCP servers checked out gets `mcp` and a manual rename; editor window titles, shell prompts, tmux sessions and `cd` history all say `mcp`. Contributors will each solve this differently, so bug reports and paths in issues stop being comparable. Small, constant friction.

**In search.** The repository name is a heavily weighted match field on GitHub, and `mcp` spends all of that weight on a token shared with the ~25,000 repos in the `mcp-server` topic while carrying no mail keyword at all. A search for "email mcp server" then has only the description and topics to match on, whereas `mailrune-mcp` matches on the name for both halves of that query. There is also a display problem: GitHub's MCP Registry auto-derives a name from `owner/repo` when no `title` is set, which is how it ends up showing entries like "Basicmachines Co Basic Memory" — `mail-ai-tools/mcp` would render along those lines.

The precedents for a bare `mcp` repo are `awslabs/mcp` (9.5k stars) and `BrowserMCP/mcp` (7.0k). Both work because the *organisation* is the brand and carries all the meaning. `mail-ai-tools` is a category label, not a brand, so there is nothing for the repo name to lean on.

**At the package layer.** `mcp` on PyPI is the official SDK — I fetched it, `mcp 2.0.0`, "Model Context Protocol SDK", owned by `modelcontextprotocol/python-sdk`. `mcp` on npm is also taken. So the published package can never match the repo name under this shape, and there is no product name to fall back on either. He would end up inventing a package name at publish time anyway, which is the work this shape was supposed to avoid.

## Does `ai` in an organisation name age well

The honest answer is that era-markers in names have a consistent track record and it is not good: `e-` prefixes, `i-` prefixes, `2000`, `.com` in company names, `Web 2.0`, `cloud`. In each case the marker signalled currency at the time and then dated the project precisely because it was chosen to signal currency.

The relevant evidence here is closer to hand, though, and it is from the research already done for the naming file. Within roughly eighteen months of MCP existing, the official `mcp-server-x` prefix went from being the reference convention to 1 of 34 in a star-ranked sample, and the successful projects systematically demoted the marker from the product layer to the package layer — `DesktopCommanderMCP` now ships `@wonderwhy-er/desktop-commander` and brands itself desktopcommander.app; `firecrawl-mcp-server` publishes `firecrawl-mcp`; the official registry quickstart turns `mcp-weather-server` into `io.github.you/weather`. That is a measured decay curve for a technology marker in this exact ecosystem, over a very short period.

There is a second, more specific problem with `ai` here. The project is not AI. It is a mail index with an authenticated HTTP interface, and the AI part lives in whatever client connects to it. An `ai` marker in the org name makes a claim about the software that the software does not support, and that misdescription will still be there when "AI client" stops being a remarkable thing to be.

## The three shapes

| | Product-named org | Generic tools org, protocol-named repo | Personal account, one good repo |
|---|---|---|---|
| Example | `mailrune/mailrune-mcp` | `mail-ai-tools/mcp` | `<you>/mailrune-mcp` |
| Registry name | `io.github.mailrune/mailrune` | `io.github.mail-ai-tools/mcp` | `io.github.<you>/mailrune` |
| Search weight | Both keywords in the path | Neither | Both |
| Clone directory | `mailrune-mcp` | `mcp` | `mailrune-mcp` |
| Signals | An intentional project | A grab-bag | One person's project |
| Room for siblings | Yes | Yes | Cluttered |

**Cost to change later** (verified from GitHub docs):

- **Renaming a repository** preserves issues, wikis, **stars** and followers, and redirects `git clone`, `fetch` and `push`. Two exceptions: GitHub Pages project site URLs are not redirected, and GitHub explicitly does *not* redirect calls to an Action hosted by a renamed repo — those workflows fail with `repository not found`.
- **Transferring a repository** to a different owner preserves issues, PRs, wiki, **stars** and watchers, keeps fork relationships, and redirects all links and git operations. Two traps: creating anything new at the old location permanently deletes the redirects, and if the repo had more than 100 clones or more than 100 Actions runs in the week before the transfer, GitHub **permanently retires** the old `owner/repo` pair so it can never be reused.
- **Renaming an organisation** I did not verify. Treat it as the risky operation and prefer transferring repos into a freshly created org, which has documented redirect behaviour.

The practical upshot: moving from a personal account into an org later is cheap and keeps the stars, so shape 3 is not a trap. Committing to `mail-ai-tools/mcp` is the expensive one, because fixing it later means changing *both* the org and the repo name at once — and by then the `-mcp`-in-the-repo decision has been baked into every install instruction anyone has written down.

## Read

`mail-ai-tools/mcp` is available but it is the weakest of the three. It spends both halves of the path on things that carry no information — a category label that is not a brand, and a protocol token shared with 25,000 repos — it forces a package name that matches nothing, it hands contributors a directory called `mcp`, and it puts a dating marker in the one part of the path that is hardest to change. The shape only works for organisations whose name is already a brand, which is why AWS and BrowserMCP can do it and this cannot.

Recommendation: keep `github.com/<you>/mailrune-mcp` on the personal account for now. Register the `mailrune` org to hold the name — it is free — but do not move into it until there is a second repo that needs to live beside the first. The transfer preserves stars and sets up redirects, so there is no cost to deferring, and there is real cost to committing early to a shape built around a token that the ecosystem is already demoting.

Sources: [GitHub docs — renaming a repository](https://docs.github.com/en/repositories/creating-and-managing-repositories/renaming-a-repository) · [GitHub docs — transferring a repository](https://docs.github.com/en/repositories/creating-and-managing-repositories/transferring-a-repository) · [PyPI `mcp`](https://pypi.org/project/mcp/)
