# Family-suffix naming research — archived, incomplete

Work stopped 19 August 2026 at the user's request, after the decision to keep a single repository under a personal account. This file preserves what was actually verified. It is a record, not a recommendation — the collision research for the conversation-flavoured words was cancelled before it ran, so the second half is availability data only.

Third of three files. See also `mcp-mail-server-naming.md` (naming convention and the mail-name shortlist) and `mcp-mail-server-repo-shape.md` (org vs personal account, rename/transfer mechanics).

---

## What still applies after the single-repo decision

From the earlier two files, and unaffected by the pivot:

- The `mcp-server-x` prefix has collapsed (1 of 34 in a star-ranked sample, ceiling ~4.3k stars). `x-mcp` is the dominant community form at 50%.
- The MCP registry enforces only a reverse-DNS regex tied to your authenticated GitHub owner. Nothing anywhere requires or rewards "mcp" in a name.
- Three independent name slots — repo, package, registry — and successful projects routinely make all three differ. Marker at the repo and package layers, bare word for the binary and registry name.
- Renaming a repository preserves stars, issues, wikis and followers, and redirects git operations. Transferring preserves the same and redirects links. So starting on the personal account costs nothing later.

Obsolete: everything about organisation names, umbrella words, and per-service repos.

---

## Method and limits

Same as before. GitHub, npm and PyPI results are HTTP status codes fetched live (200 = taken, 404 = free) and are reliable — the method was calibrated against known-good and known-bad names. GitHub users and organisations share one namespace, so a 404 means free for either.

Domain results are weak. No DNS, WHOIS or RDAP was reachable from this environment; all I could establish is whether a live HTTPS site answers. A registered-but-parked domain reads as "no site" here.

Trademark results are weak. USPTO TSDR/TESS, EUIPO and IP Australia were not reachable. Every "nothing found" means "nothing surfaced in search", not "cleared".

---

## Phase 1 — archive and index suffixes

Screened as `mailX` / `slackX` with an org named for the bare word.

### Registry availability (verified)

| Suffix | bare word: gh / npm / PyPI | `mailX` | `slackX` |
|---|---|---|---|
| trove | taken / taken / taken | all free | all free |
| stash | taken / taken / taken | gh **taken**, npm+PyPI free | all free |
| rune | taken / taken / taken | all free | all free |
| hoard | taken / taken / taken | all free | all free |
| cairn | taken / taken / taken | all free | all free |
| holt | taken / **npm free** / taken | all free | all free |
| corpus | taken / taken / taken | all free | all free |
| cove | taken / taken / taken | all free | all free |
| quarry | taken / taken / taken | all free | all free |
| fold | **gh free** / taken / taken | gh **taken**, rest free | all free |
| loft | taken / taken / taken | all free | all free |
| crib | taken / taken / taken | all free | all free |
| warren | taken / taken / taken | all free | all free |

The headline: **every bare word except `fold` was already taken as a GitHub org**, and every one was taken on npm. The `github.com/<word>` shape did not survive contact with reality for any candidate.

### Qualified organisation forms that were free (verified)

Ten patterns were tested per word (`Xhq`, `getX`, `X-project`, `X-labs`, `withX`, `X-oss`, `X-sh`, `theX`, `X-dev`, `useX`). Free forms:

| Word | Free qualified org names |
|---|---|
| trove | `trove-project`, `withtrove`, `trove-oss`, `trove-sh`, `thetrove`, `usetrove` |
| stash | `stash-project`, `withstash`, `stash-sh` |
| rune | `getrune`, `withrune`, `userune` |
| hoard | `gethoard`, `hoard-labs`, `withhoard`, `hoard-oss`, `hoard-sh`, `hoard-dev`, `usehoard` |
| cairn | `withcairn`, `cairn-sh` |
| corpus | `corpushq`, `corpus-project`, `corpus-oss`, `corpus-sh` |
| cove | `cove-project`, `cove-oss`, `cove-sh`, `cove-dev`, `usecove` |
| holt | `holthq`, `holt-project`, `holt-labs`, `withholt`, `holt-oss`, `holt-sh`, `theholt` |
| quarry | `quarryhq`, `getquarry`, `quarry-project`, `withquarry`, `quarry-oss`, `quarry-sh`, `thequarry` |
| loft | `lofthq`, `withloft`, `loft-oss`, `useloft` |

Judgement on which read as a real project rather than a squat: `X-project` and `X-labs` read as real (precedent: `rust-lang`, `loft-sh`); `getX` and `useX` read as SaaS marketing; `theX` and `X-oss` read as consolation prizes. `X-sh` reads well only if the domain matches.

### Domain probes (weak — live-site check only)

| Domain | Result |
|---|---|
| `stash.dev` | **In use.** Redirects to Stash Financial's blog (stashinvest.com). |
| `cove.dev` | **In use.** Live company — "white-label financial products", AI software for brokers, realtors and lenders. |
| `holt.dev` | **In use.** Redirects to `github.com/bpholt`, a personal GitHub profile. |
| `quarry.dev` | **Registered, parked, for sale** on GoDaddy at $3,800. |
| `trove.dev`, `rune.dev`, `hoard.dev`, `cairn.dev` | No live site. Registration status unverified. |

### Collision findings

Search-derived unless marked. Ranked worst-first by same-domain collision, which was the disqualifying test.

| Word | Finding | Verdict |
|---|---|---|
| **crib** | **Cribl Search** — federated search over your own data, ~$300M ARR. One letter away, same category. | BLOCKED |
| **corpus** | Term of art for the thing being indexed (Vertex `RagCorpus`, NLTK, "corpus search"). Unbrandable, likely unregistrable as descriptive, unsearchable. A live pending Class-42 application filed April 2026 for document-management SaaS. | BLOCKED |
| **hoard** | `crates.io/crates/hoard` is literally a file-backup tool — same word, same binary name, same space, owns hoard.rs. Plus a second git-backup `hoard`, a command-organiser `hoard` on Homebrew, and the Hoard memory allocator. | BLOCKED |
| **loft** | Loft Labs filed a live 2024 US application for LOFT in dev-tool SaaS. The name was never vacated — `loft-sh` and `slack.loft.sh` still host vCluster. npm taken by a 2025 squat. | BLOCKED |
| **stash** | Three separate problems. `stashapp/stash` (12.8k stars, verified) is a self-hosted Go app that scans and indexes your local media directories and serves the files back — that is the product sentence, already occupied. `stash.run` is a live commercial Kubernetes **backup** product, supported to Dec 2027. And `git stash` poisons every discovery query for exactly this audience. A live US registration, Reg. 4861696, covers "Electronic storage of files and documents". Atlassian turned out to be a non-issue — Stash is not on their current word-mark list and the application went abandoned around 2014. | BLOCKED |
| **holt** | `NoKV-Lab/holt` — a path-keyed metadata **storage engine**, released the day before the check. crates.io taken. | RISKY |
| **warren** | No hard collision found; warren.io gone; npm taken by Trainline's RabbitMQ library. | RISKY |
| **cove** | No collision in category, but cove.dev is a live funded fintech and the GitHub name belongs to an active Epic engineer. | RISKY |
| **rune** | No collision in category, which was rare. But three separately funded software companies use RUNE as a primary mark (Rune Technologies, $24M Series A; Rune AI Inc.; Rune Labs, the Parkinson's data company, which launched an AI product in April 2026), with two pending Class-9 filings from 2025. RuneScape owns the search token permanently. npm taken by Rune AI, published 18 Aug 2026. | RISKY |
| **quarry** | Wikimedia Quarry is a public SQL query service. npm `quarry` is a ClickHouse query builder published 19 Aug 2026. | RISKY |
| **trove** | The least-bad of the obvious words, but not clean. PyPI `trove` is **OpenStack Trove (DBaaS), v25.0.0 released April 2026** — actively maintained, so `pip install trove` is gone. "Trove classifiers" is core Python packaging vocabulary (PEP 301), so any Python-facing docs would use the word in two unrelated senses. NLA Trove (27M digitised pages, faceted full-text search) describes the product better than the product does and will outrank it forever, though it is a government library unlikely to pursue an OSS tool. Good news, verified: the two closest US registrations are both dead — Reg. 4646074 cancelled 2021, Serial 77732005 abandoned 2010. trove.com belongs to Trove Recommerce, which is extending into "Trove AI". | RISKY |
| **cairn** | The standout of phase 1. No collision found in search, indexing, storage, backup, archiving or personal-data tooling. Justia Class-9 CAIRN marks cover a wilderness-safety app, a video game, sinks, jackets and e-bikes — nothing in the relevant goods. Cairn Energy no longer exists (renamed Capricorn Energy, 2021). Search is fragmented with no dominant owner, which is workable. Costs: GitHub org held by a small "Cairn Software" studio, npm dormant since 2017, and a few tiny 2026 crates including `cairn-memory` ("local-first memory for AI agents") — three weeks old, 15 downloads. Cairn.info is a French academic archive, conceptually adjacent, different market. | MINOR — best of the twelve |

---

## Phase 2 — conversation suffixes

Screened after the pivot to correspondence. **Availability was checked. Collision research was cancelled before it ran.** Treat this table as availability only.

### Registry availability (verified), across all three members

| Suffix | bare gh | `mailX` gh/npm/PyPI | `slackX` gh/npm/PyPI | `telegramX` gh/npm/PyPI |
|---|---|---|---|---|
| parley | taken | all free | all free | all free |
| natter | taken | all free | all free | all free |
| confab | taken | all free | all free | all free |
| chatter | taken | all free | all free | all free |
| colloquy | taken | all free | all free | all free |
| scroll | taken | all free | all free | all free |
| palaver | taken | all free | all free | all free |
| banter | taken | all free | gh **taken**, rest free | all free |
| lore | taken | all free | all free | all free |
| glyph | taken | **all three taken** | all free | all free |
| spiel | taken | all free | all free | all free |
| moot | taken | all free | all free | all free |
| rapport | taken | all free | all free | all free |
| trove | taken | all free | all free | all free |
| stash | taken | gh **taken**, rest free | all free | all free |
| rune | taken | all free | all free | all free |
| cairn | taken | all free | all free | all free |

Again, every bare word was taken as a GitHub org.

### The one collision that was verified

**Colloquy is a live project and would have been disqualifying.** Fetched `github.com/colloquy/colloquy`: an advanced IRC, SILC and ICB client for macOS and iOS, 261 stars, 48 forks, 7,097 commits, 536 open issues, site colloquy.app, org name held. Small by star count, but it is a chat client, and a conversation-history indexer sharing its name is exactly the unsearchable failure mode.

### Suspected problems, flagged but NOT verified

These were the hypotheses the cancelled research was going to test. None of them were checked. Do not rely on them.

- **chatter** — Salesforce Chatter is an enterprise social/messaging product, and Salesforce also owns Slack. Suspected double collision, possibly fatal.
- **parley** — believed to be a Rust text-layout crate in the linebender/Xilem ecosystem.
- **scroll** — the Scroll zkEVM Ethereum L2 is large and well funded; there is also a Rust `scroll` crate for binary parsing; and the word is generic UI jargon.
- **lore** — `lore.kernel.org` is the Linux kernel's public-inbox **mail archive**. If that holds, it is a direct same-domain collision and would rule the word out.
- **rapport** — IBM Trusteer Rapport, the banking security browser plugin.
- **glyph** — typography term, plus Trion Worlds' Glyph launcher. Note that all three registries for `mailglyph` were already taken, which is corroborating.
- **moot** — the US-English sense of "irrelevant" is a naming problem independent of any collision.
- **spiel** — the sales-pitch connotation.
- **natter, confab, palaver, banter** — unchecked. `banter` in particular seemed likely to have been used by a chat app, and `slackbanter` was already taken on GitHub.

### The three-syllable prefix question, unanswered

The test was whether a suffix that reads well as `mailX` survives `telegramX`. It was never run properly, but the shape of the problem is visible from the character counts: `telegram` is eight characters, so `telegramcolloquy` is sixteen and `telegrampalaver` fifteen, against `telegramrune` at twelve. Short suffixes survive the three-syllable prefix; three-syllable suffixes do not.

---

## The Slack trademark finding

This was verified and is worth keeping regardless of the naming decision, because a Slack server is still on the roadmap.

**The written rules are strict.** The Slack Brand ToS says: do not use Slack as a noun; "Don't register a domain containing the word 'slack' or any variation thereof"; "Do not apply for a trademark that includes the word 'slack'." The Salesforce Trademark Guidelines, which the Slack ToS incorporates, say you may not incorporate any recognisable portion of a Salesforce mark into a product, service, website or domain name. There is also a "trademark family" clause forbidding names that look like an extension of a Salesforce family — which is precisely what a family of `slackX` names would have looked like.

**The observed enforcement record is a null result.** `rusq/slackdump` is a portmanteau with 2.6k stars, in Homebrew core, no disclaimer, five years unchallenged. The bare npm package `slack` is held by an unaffiliated developer who publicly offered to hand it over and Slack never took it. PyPI `slackbot` has sat under a third party for eleven years. Anthropic's own `@modelcontextprotocol/server-slack` ships with no disclaimer. TTABVUE shows four proceedings ever naming Slack Technologies, none litigated. The one real action found was Salesforce killing German company Slace's EU mark around November 2024 — a corporate registration, not an OSS project.

**Practical read, by surface** (not legal advice):

- GitHub repo name — lowest risk. `slack-<yourname>` is fine.
- npm/PyPI package — low. Avoid `@slack/*`, which is genuinely Slack's. Unscoped `slack*` is wide open.
- Domain — high. Explicitly prohibited, and UDRP is cheap for a complainant. Do not register a `slack*` domain.
- Trademark application containing "slack" — don't. It is the one act with a demonstrated enforcement record.

**Preferred form:** brand first, Slack as descriptor — `<yourname>-slack`, "X for Slack". This matches Slack's own naming for its own product and the CNCF "XYZ for Kubernetes" pattern, and it is better brand architecture anyway. Add the disclaimer in Slack's own words: *"Slack is a trademark of Salesforce, Inc. This project is not created by, affiliated with, or supported by Slack Technologies, LLC."*

**The bigger exposure is probably not trademark, it is the Slack API Terms.** The Developer Policy prohibits "Using Data to train an LLM under any circumstances" and using the API to "replicate or compete with core products or services offered by Slack". Slack shipped its own MCP server and Enterprise Search API in February 2026, so the competition clause is now live rather than theoretical. Two mitigations worth building in deliberately: work from user-owned exports (slackdump) rather than live API scraping, and state explicitly in the docs that retrieval over a local index is not LLM training.

---

## If the naming question reopens

The state of play when work stopped:

- **cairn** was the only word to clear the same-domain test in phase 1, on a metaphor that suited an archive better than correspondence.
- **rune** was the best fit for the correspondence reading — a rune is an inscription, a message rather than a container — and had no collision in the messaging category. It was blocked on company marks and on RuneScape owning the search token, neither of which is a same-domain collision.
- **trove** and **stash** were both weakened by the pivot. Both are storage words, and neither survives the addition of Telegram as a member.
- The conversation words were never screened for collisions. `lore` and `chatter` are the two most likely to be ruled out, and `colloquy` already is.

Nothing here is a recommendation. The single-repo decision makes most of it moot in any case — under a personal account there is no org to name, and the `mcp-mail-server-naming.md` conclusion still stands for the repo itself.
