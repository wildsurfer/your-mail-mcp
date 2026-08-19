# Naming a self-hosted MCP mail server

Prepared 19 August 2026.

Project: a self-hosted MCP server for email over a local maildir mirror (mbsync + notmuch), exposed to any MCP client over authenticated HTTP, multi-mailbox, with read-only mode enforced inside the server.

---

## What I checked, and what I could not

| Check | Method | Reliability |
|---|---|---|
| GitHub user/org name free | HTTP status of `github.com/<name>` (200 = taken, 404 = free) | Verified. Calibrated against a known-good and known-bad name. |
| GitHub repo name in use elsewhere | Web search, `site:github.com` | Partial. GitHub's code search API and its logged-out search HTML were not usable from this environment, so a low-star repo could have been missed. |
| npm package free | HTTP status of `registry.npmjs.org/<name>` | Verified. |
| PyPI package free | HTTP status of `pypi.org/pypi/<name>/json` | Verified. |
| Existing software / companies | Web search plus direct page fetches | Verified where a page was fetched; otherwise search-derived. |
| Trademark | Web search, Trademarkia/Justia listings, obvious commercial brand use | **Weak.** USPTO TESS and EUIPO were not reachable. Treat every "no mark found" as "none surfaced", not as clearance. |
| Domain availability | Attempted HTTPS fetch of the domain | **Weak.** No DNS, WHOIS or RDAP was reachable from this environment. All I could establish is whether a live site answers. A registered-but-parked domain reads as "no site" here. Every domain line below should be re-checked at a registrar. |

Where a finding below is weak, it says so inline.

---

## Part 1 — The naming convention

### What the official side actually does now

The `modelcontextprotocol/servers` repository (86.7k stars) no longer indexes the ecosystem. Its README now points at the registry and says it houses "just the small number of reference servers maintained by the MCP steering group" — seven of them. Their directory names carry no MCP marker at all: `filesystem`, `git`, `memory`, `time`, `fetch`, `everything`, `sequentialthinking`.

The same servers use four different names for one thing:

```
repo directory:  filesystem
npm package:     @modelcontextprotocol/server-filesystem
binary:          mcp-server-filesystem
registry name:   io.github.modelcontextprotocol/server-filesystem
client config:   "filesystem"
```

That divergence is the single most important fact for this decision. There is no requirement that these agree, and in practice they routinely do not.

### The registry imposes exactly one rule, and it is not about "mcp"

From the live schema at `static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json`:

```json
"name": {
  "description": "Server name in reverse-DNS format. Must contain exactly one forward slash separating namespace from server name.",
  "example": "io.github.user/weather",
  "pattern": "^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$",
  "minLength": 3, "maxLength": 200
}
```

The namespace half must match your authenticated GitHub owner (`io.github.<your-username>/...`) or a DNS-verified domain. Nothing in the schema, the official registry requirements document, or the moderation policy mentions the string "mcp".

The official publishing quickstart demonstrates dropping the marker on purpose: repo `my-username/mcp-weather-server`, npm `@my-username/mcp-weather-server`, registry name `io.github.my-username/weather`. A real published server does the same — `pipeworx-io/mcp-flood` is registered as `io.github.pipeworx-io/flood`.

Registry search matches on both `name` and `description`. Since essentially every server's description contains "MCP", putting it in the name buys nothing there.

### What the community does

A star-ranked sample of 34 genuine community servers:

| Pattern | Count | Share |
|---|---|---|
| `x-mcp` | 17 | 50% |
| `x-mcp-server` | 7 | 21% |
| no MCP marker at all | 5 | 15% |
| other (CamelCase, bare `mcp`) | 3 | 9% |
| `mcp-server-x` (the official pattern) | 1 | 3% |
| `mcp-x` | 1 | 3% |

The official `mcp-server-x` prefix has collapsed. Its highest-star instance anywhere is `antvis/mcp-server-chart` at 4.3k, against `microsoft/playwright-mcp` at 36.3k and `upstash/context7` at 61.0k. The two largest servers in the ecosystem, `microsoft/markitdown` (173k) and `context7`, carry no marker in the repo name.

### Does any client or directory reward the suffix

No. GitHub's MCP Registry at `github.com/mcp` ranks by stars and actively *strips* "MCP" from display names: `microsoft/playwright-mcp` shows as "Playwright", `github/github-mcp-server` as "GitHub", `hashicorp/terraform-mcp-server` as "Terraform". VS Code's `mcp.json` keys are arbitrary. Claude Code's only naming rule is a charset restriction on the local config key plus five reserved names.

Claude Code is in fact an argument *against* the suffix: it exposes tools as `mcp__<server-name>__<tool-name>`, and users type that string into permission rules and hook matchers. `mcp__mailrune__search` beats `mcp__mailrune-mcp-server__search`, and `mcp__foo-mcp__bar` is a literal stutter.

Smithery, Glama, PulseMCP and mcp.so were not fetched directly. I found no claim that any of them rank by name content, but that is unverified.

The one place the marker plausibly still earns its keep is raw npm and PyPI text search, where there is no MCP-specific channel and the token has to live in the package name. That is exactly the layer where projects retain it: `upstash/context7` publishes `@upstash/context7-mcp`, `firecrawl/firecrawl-mcp-server` publishes `firecrawl-mcp`.

### The argument against naming after a protocol

You raised it and it is correct. MCP may not be the only transport this project ever speaks. A local CLI, a plain REST endpoint, or a successor protocol would all make a name like `mailrune-mcp` a permanent apology. Projects that hit this have handled it by demoting the marker rather than removing it: `wonderwhy-er/DesktopCommanderMCP` now publishes `@wonderwhy-er/desktop-commander` and brands itself desktopcommander.app.

### Recommendation

Use the three-slot structure the ecosystem has already converged on, and put the marker only where it is cheap and where it demonstrably pays.

| Slot | Value | Why |
|---|---|---|
| Product name, binary, client config key, MCP registry server segment | `mailrune` | Protocol-free, so it survives a second transport. `io.github.<you>/mailrune`. Shortest thing a user types. |
| GitHub repository | `mailrune-mcp` | The dominant community pattern (50%), and repos can be renamed with automatic redirects if MCP stops being the point. |
| npm / PyPI package | `mailrune-mcp` | The only layer where free-text search rewards the token. |

Suffix, not prefix — `mailrune-mcp`, never `mcp-mailrune` or `mcp-server-mailrune`. Then set the registry `title` field carefully, because that is what directories display, and entries without one get ugly auto-derived names like "Basicmachines Co Basic Memory".

### On "mail" in the name, and the trade-off you cannot escape

Your steer toward a legible name is right for this project, but it costs something and it is worth being explicit about what.

A name containing "mail" tells a reader the domain immediately. That matters more than usual here, because the MCP registry caps `description` at 100 characters and most directories show a name and a one-liner. The cost is that `mail-*` is the most heavily occupied namespace in the space, so the distinguishing element has to do all the work, and a generic one leaves you invisible.

The checks bore this out. `mail-` first with a common second word is where the collisions are: `maildex` is an existing commercial email indexing product, `mailvault` is three separate email-archiving products, `mailview` is pre-owned by Rails developers, `mailscope` and `mailseal` both have live occupants. Names where `mail` is the second half fared better — `wardmail`, `havenmail`, `scopemail` were all empty — which matches your intuition, though the resulting words read slightly more awkwardly.

A distinctive non-mail name (`restante`, `angaria`) is more memorable and much safer on trademark, but needs the description to carry the meaning. You cannot fully have both. My judgement is that legibility is worth more than distinctiveness here, provided the second element is unusual enough to be searchable on its own.

On length: `mailrune-mcp` is twelve characters and two hyphen-free content words, which is fine as a repo and package name. A three-part name would not be. `mailkeep-notmuch-mcp` or `mailward-mcp-server` are mouthfuls, and the binary should never carry `-mcp` at all — `mailrune` is what gets typed. Keep it to `mail` + one distinguishing word, with `-mcp` appearing only at the repo and package layers.

---

## Part 2 — Availability of your four candidates

Registry results are HTTP status codes fetched live on 19 Aug 2026.

### poste — **do not use**

| Check | Result |
|---|---|
| GitHub org/user | **Taken.** `github.com/poste` is org id 7690469, contact postechat@gmail.com, no public repos. |
| npm | **Taken.** `poste@0.0.5`, published 2015, described as *"A NodeJS IMAP server"*, repo `mailstache/poste`. Dormant but occupied, and it is a mail server. |
| PyPI | Free (404). |
| Existing software | **Direct collision.** Poste.io is a live all-in-one self-hosted mail server (SMTP/IMAP/POP3, Roundcube, rspamd, ClamAV) by Analogic s.r.o., trading since 2014. I fetched the site. It is the same shelf as mailcow and Mail-in-a-Box — your exact audience. |
| Trademark | **High.** Poste Italiane is a €10B+ listed company and one of the EU's largest trademark filers, with a POSTE- brand family (Postepay, PosteMobile, BancoPosta). |
| Domain | poste.io is in active commercial use. |

Your suspicion was right on both counts, and it is worse than you thought: the npm name is also occupied by an IMAP server. This one is dead.

### cursus — poor

| Check | Result |
|---|---|
| GitHub org/user | **Taken** (200). |
| npm | **Taken** (200). |
| PyPI | **Taken** (200). |
| Existing software | Only a "Cursus" LMS HTML template on code.market. Trivial. |
| Trademark | Nothing significant surfaced. |
| Searchability | **Bad.** It is the ordinary Dutch and French word for "course", a Neolithic monument type, and a high-frequency word inside lorem ipsum filler, so it appears across scraped junk everywhere. |

All three registries occupied, and unsearchable even if they were not.

### restante — the best of the four, but the GitHub name is gone

| Check | Result |
|---|---|
| GitHub org/user | **Taken** (200). You would need `restante-mcp` (free, 404) or a personal namespace. |
| npm | Free (404). |
| PyPI | Free (404). |
| Existing software | None found. |
| Companies | Poste Restante, a Swedish performance-art company; a dormant US "POSTE RESTANTE, INC." shell. Neither is a conflict. |
| Trademark | Nothing surfaced. The phrase is a generic postal term, which weakens anyone's claim to it. |
| Domain | restante.dev and restante.io returned no live site. **Registration status unverified.** |
| Fit | Good. *Poste restante* means mail held at an office for collection, which is close to a local mirror you read from. |

The strongest non-mail option. It fails your new steer though: nobody reads "restante" and thinks email.

### muniment — risky

| Check | Result |
|---|---|
| GitHub org/user | **Taken** (200). |
| npm | Free (404). |
| PyPI | Free (404). |
| Existing software | **muniment.ai** is live — "One app for your whole org", a multi-model AI governance and harness platform, currently launching. Exact string, and competing for the same developer attention. Also Munimentum (.eu, .tech) — a software agency and a defence consultancy. |
| Trademark | An actively launching startup using it as a brand is meaningful risk. |

Nice word, wrong moment. An AI-tooling startup with the exact name is a bad neighbour for an AI-tooling open source project.

### The `-mcp` variants

`poste-mcp`, `cursus-mcp`, `restante-mcp` and `muniment-mcp` are all free on GitHub, npm and PyPI (404 across the board). That does not rescue `poste` or `cursus` — the suffix does not cure a trademark problem or a product collision, it just moves you one hyphen away from the thing you are being confused with.

---

## Part 3 — Further names, checked

I generated and checked 50 candidates across both styles. The full registry sweep is summarised here; names that failed early are listed at the end rather than given a table each.

### Cleared, mail-bearing

| Name | GitHub | npm | PyPI | `-mcp` variant | Collisions found |
|---|---|---|---|---|---|
| **mailrune** | Free | Free | Free | all free | **None.** No product, no company, no repo, no trademark surfaced. mailrune.dev and mailrune.io returned no live site. |
| **mailwright** | Free | Free | Free | all free | Mailwright Inc., a small business-services company in Springfield/Willard, Missouri (D&B listing only, no software, no web presence found). Not a software collision; a minor trademark footnote. mailwright.dev/.io/.org/.com returned no live site. |
| **mailkeep** | Free | Free | Free | all free | mailkeep.com is live but ancient and dormant — "Mailkeep — The SMTP and ODMR Mail Server", no community footprint. Same domain as you, which is the concern, but the name is not defended. No GitHub repo found. |
| **mailhold** | Free | Free | Free | all free | No software found; mailhold.com resolves with nothing behind it. Two drags: USPS "hold mail" owns those search terms, and **MailHold is one character from MailHog**, a widely known dev mail-catcher. That near-miss is disqualifying on its own. |
| **wardmail** | Free | Free | Free | all free | Nothing. Only wardmail.org.uk (an unrelated UK organisation's mail domain) and a person on X. |
| **havenmail** | Free | Free | Free | all free | Nothing found. Nearest is MailHaven (below), a different string. |
| **scopemail** | Free | Free | Free | all free | Nothing with this exact string, but MailScope is taken, so you would live in permanent confusion with it. |

### Failed, mail-bearing

| Name | Why |
|---|---|
| **maildex** | **Blocked.** MailDex® by Encryptomatic LLC indexes, searches and converts email across PST, OST, MSG, MBOX, EML with all processing local. That is a near-verbatim description of your project. Registered mark asserted. The closest functional collision in the entire exercise. |
| **mailglass** | **Blocked.** The GitHub *username* is free, which is why my first sweep passed it — but `szTheory/mailglass` is a Phoenix transactional-email framework with a preview/admin UI. Same domain, exact name. |
| **mailward** | **Risky.** GitHub, npm and PyPI are all free, and web search finds nothing — but I fetched **mailward.io** and it is a live pre-launch product: "Mailward · on-device phishing protection for Gmail", © 2026, waitlist open, positioned on local-only processing and privacy. Same domain, same year, same privacy pitch. Also one letter from Mailwarm. |
| **mailvault** | **Blocked.** Three occupants, all adjacent: MailVault (enterprise email archiving with eDiscovery), mailvaultapp.com ("Archive IMAP emails locally, Mac & Linux" — very close to you), MailVault Pro. GitHub org `mailvault` taken, created Aug 2025. |
| **mailery** | **Blocked.** `github.com/maileryio` is Mailery, an open-source self-hostable email marketing platform. The plain username `mailery` is separately taken. |
| **mailview** | Risky. `basecamp/mail_view` became ActionMailer previews in Rails 4.1; every Rails developer knows the term. npm and PyPI both taken. |
| **mailseal** | Risky. GitHub org `MailSeal` taken (France, mailseal.io, no public repos — someone is building something unreleased under this name now). SealedMedia Limited holds registered marks for SEALED MAIL and SEALED EMAIL. |
| **mailscope** | Risky. mailscope.io is a live email-enrichment SaaS; the GitHub account `mailscope` holds a fork of KumoMTA, so the holder works in mail infrastructure. |
| **sealmail** | Risky. GitHub org `sealmail` has a Rust repo `seal-mail` **updated July 2026** — actively being built. Plus sealmail.net and a SourceForge project. |
| **stillmail** | Risky. stillmail.app launched in 2026 — a minimalist email app, on Product Hunt with a Show HN. |
| **vaultmail** | Risky. npm taken; vaultmail.shop is a live temp-email service; `Mikej81/vaultmail` extracts email from PST/OST/MBOX archives. |
| **coldmail** | Risky. "Cold email" is a saturated outbound-SaaS category, so the name is both unsearchable and semantically brands a privacy-respecting local tool as spam-adjacent. |
| **readmail** | Fails on search. GitHub, npm and PyPI are free, but there are at least four existing `readmail` repos, including `samdmarshall/readmail`, a CLI client for mail retrieved via getmail — which is almost exactly your neighbourhood. Also a generic phrase, so it is impossible to search for. |
| **keepmail**, **quietmail**, **mailhaven** | GitHub names taken (placeholders or a dormant acquired IoT brand), and each carries a small live-domain or brand footprint. Not blocked, just worse than the cleared list for no gain. |

### Non-mail names, checked for completeness

| Name | Verdict |
|---|---|
| **angaria** | GitHub taken, npm and PyPI free. Only collisions are an Italian RFID seal app and a defunct delivery startup. Etymologically excellent — *angaria* was the Roman compulsory courier service. Fails the legibility steer. |
| **nuncio**, **billet**, **carrel**, **plica** | All three registries occupied for most; each has a heavy unrelated search shadow (papal diplomacy, billet aluminium, library desks, knee surgery). |
| **missive** | **Blocked.** Missive is an established commercial team-email and shared-inbox client shipping on every platform. Direct category collision. |
| **wicket** | **Blocked.** Apache Wicket, an ASF top-level project since 2007, and the ASF enforces project marks. Plus two other software companies. |
| **signet** | Risky, close to blocked. withsignet.com is a BIMI/inbox-branding service (your space); Signet Jewelers is NYSE-listed; registered US mark SIGNET Reg. 6113823. |
| **verso**, **quire**, **corvid**, **estafette** | Each occupied by something substantial: Verso Books and Verso Corporation; Quire.io project management; Corvid Technologies, a US defence engineering firm (and Wix abandoned the name for sounding like Covid); Estafette CI/CD, an existing open-source infrastructure project. |

---

## Shortlist

### 1. mailrune — recommended

Repo `mailrune-mcp`, binary and registry name `mailrune`, packages `mailrune-mcp`.

**For:** the only finalist that came back empty at every single layer checked — GitHub username, GitHub repo search, npm, PyPI, web search, company search, trademark search, and no live site on .dev or .io. Eight characters, unambiguous to spell from hearing and to pronounce from reading, no shift keys, no doubled letters. "mail" gives the domain instantly, and "rune" is unusual enough that a search for it will find you rather than the mail-tooling crowd, which is the specific failure mode that kills mail-prefixed names. A rune is a mark you read, and the older sense of the word is a secret, which is a reasonable fit for a private local mail index.

**Against:** it does not signal the read-only or custody idea at all. It is mildly fantasy-flavoured, which some people will find twee in infrastructure software. The domain checks are the weak kind described above.

### 2. mailkeep

**For:** semantically the best of the three. "Keep" carries both the local-custody idea (you hold a mirror of the mail) and, loosely, the read-only posture. Free on GitHub, npm and PyPI, and on the `-mcp` variants. Very easy to type.

**Against:** mailkeep.com is live, dormant, and describes an SMTP/ODMR mail server — same domain as you, which is the one kind of collision that hurts most even when the occupant is inactive, because it is exactly what someone searching for your project will hit. "Keep" also skews toward archiving and backup, and there is a real cluster of email-archiving products (the MailVault group) you would be adjacent to.

### 3. mailwright

**For:** free on GitHub, npm and PyPI including the `-mcp` variants, and no software collision anywhere. The `-wright` suffix (craftsman: wheelwright, playwright, shipwright) reads well for a carefully built tool and is unusual in this space. Distinctive enough to be searchable.

**Against:** ten characters is the longest of the three, and `mailwright-mcp` at fourteen is at the edge of comfortable for a repo name. Mailwright Inc. exists as a Missouri business-services company — no software, no web presence I could find, but it is a live corporate name, and my trademark checking was weak. There is also a faint pull toward Playwright, which is one of the most-installed MCP servers in the ecosystem.

---

## Recommendation

**mailrune**, with the repository at `<owner>/mailrune-mcp`, packages published as `mailrune-mcp` on npm and PyPI, the binary as `mailrune`, and the MCP registry entry as `io.github.<owner>/mailrune` with a hand-written `title` of "Mailrune".

It is the only candidate that survived every check with nothing behind it, it satisfies your legibility requirement without stepping into the crowded part of the `mail-` namespace, and the protocol marker sits only in the two slots you can change later without renaming the product.

Two things to do before committing, both of which I could not do from here: run the domain names through a registrar to get real WHOIS answers rather than my "no site responds" proxy, and run a proper USPTO and EUIPO search on the final choice.

---

## Sources

- [modelcontextprotocol/servers](https://github.com/modelcontextprotocol/servers)
- [MCP server.schema.json](https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json)
- [MCP registry publishing quickstart](https://modelcontextprotocol.io/registry/quickstart)
- [MCP registry: about](https://modelcontextprotocol.io/registry/about)
- [Official registry requirements](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/official-registry-requirements.md)
- [MCP registry moderation policy](https://modelcontextprotocol.io/registry/moderation-policy.md)
- [GitHub MCP Registry](https://github.com/mcp)
- [VS Code MCP documentation](https://code.visualstudio.com/docs/agent-customization/mcp-servers)
- [Claude Code MCP documentation](https://docs.claude.com/en/docs/claude-code/mcp)
- [GitHub topic: mcp-server](https://github.com/topics/mcp-server)
- [Poste.io](https://poste.io/)
- [Poste Italiane](https://en.wikipedia.org/wiki/Poste_Italiane)
- [npm: poste](https://registry.npmjs.org/poste)
- [Mailward (mailward.io)](https://mailward.io/)
- [MailDex by Encryptomatic](https://www.encryptomatic.com/maildex/)
- [szTheory/mailglass](https://github.com/szTheory/mailglass)
- [Mailery](https://github.com/maileryio)
- [MailVault](https://www.thobson.com/mailvault)
- [Missive](https://missiveapp.com/)
- [Apache Wicket](https://wicket.apache.org/)
- [Estafette CI/CD](https://estafette.io/)
- [Corvid Technologies](https://www.corvidtec.com/)
- [Quire.io](https://quire.io/)
- [muniment.ai](https://muniment.ai/)
- [samdmarshall/readmail](https://github.com/samdmarshall/readmail)
- [MAILWRIGHT INC (Dun & Bradstreet)](https://www.dnb.com/business-directory/company-profiles.mailwright_inc.56dbd7e035505d26a74f315316a12d00.html)
- [basecamp/mail_view](https://github.com/basecamp/mail_view)
- [stillmail on Product Hunt](https://www.producthunt.com/products/stillmail)
