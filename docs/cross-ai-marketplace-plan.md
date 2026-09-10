# Cross-AI plugin marketplaces (plan)

How Wicked Sick should ship agent plugins across Claude, Cursor, ChatGPT/Codex, GitHub Copilot, Gemini, and Grok — using GitHub as the storefront, with [token-usage](https://github.com/Wicked-Sick-Ltd/token-usage) as the reference plugin.

This is a plan, not an implementation. Traefik routing in this repo stays a Traefik plugin. What we add here is the *agent* layer: portable instructions so any frontline AI can work the code, plus an org-wide marketplace model for the plugins we actually install into those AIs.

## Two layers (do not mix them)

| Layer | What it is | Lives in | Example |
| --- | --- | --- | --- |
| **Product plugins** | Installable skills, MCP, hooks that change how an AI works | Plugin source repos + marketplace catalogs | `token-usage` |
| **Repo agent instructions** | How an AI should change *this* codebase | Each product repo | `CLAUDE.md` / `AGENTS.md` in `traefik-laravel-forge` |

Marketplaces distribute **product plugins**. `AGENTS.md` / `CLAUDE.md` make a **repo** usable by whichever horse we put on that course. Both are needed; they solve different problems.

## Why not one plugin package for every AI

Each client has its own catalog file at a **fixed path on the marketplace repo root**. Skills and MCP *can* be shared (Agent Plugins 1.0 + Agent Skills). Marketplaces, hooks, commands, Copilot agents, Gemini `GEMINI.md`, and ChatGPT Apps UI cannot.

So the rule is:

1. **One source repo per product plugin** (like `token-usage` today).
2. **One GitHub catalog repo for the org**, containing *every* platform's marketplace index, pointing at those source repos.
3. **Platform shims in the source repo**, not a fork per AI.
4. **Do not ship every plugin to every marketplace.** Horses for courses: list a plugin only where it has a real runtime.

Grok already reads Claude Code marketplaces, plugins, skills, MCP, hooks, and `CLAUDE.md` with no extra packaging. A dedicated Grok catalog is optional, not required.

## Reference: what `token-usage` already got right

Treat this as the quality bar for every plugin we publish.

- **Manifest:** `.claude-plugin/plugin.json` with name, semver, author, repo, license, keywords, and bundled MCP.
- **Agent Skill:** `skills/report/SKILL.md` with a trigger-rich `description`, `argument-hint`, and MCP-first / CLI-fallback.
- **Hooks that must not block:** Stop / SubagentStop, always exit 0, local-only, no network, no telemetry.
- **Runnable without the host:** `python3 scripts/token_usage.py …` (stdlib only).
- **MCP as the preferred interface** when the host has it; CLI when it does not (Cowork, other AIs).
- **Submission pack:** `SUBMISSION.md` with copy-paste directory form answers.
- **Engineering hygiene:** tests on 3.9 + 3.12, ruff, CONTRIBUTING, CHANGELOG, SECURITY, CODE_OF_CONDUCT.

Gaps versus a cross-AI rollout (this is the improvement list, not criticism):

- No `.claude-plugin/marketplace.json` — it is a plugin, not a catalog. Correct. The *org* catalog should live elsewhere.
- No root Agent Plugins `plugin.json` / `mcp.json` (portable floor used by Cursor, Copilot, Codex, and the ChatGPT plugin directory).
- Claude-only paths (`${CLAUDE_PLUGIN_ROOT}`, `~/.claude/projects/`). Other hosts need host-neutral env vars plus documented transcript locations.
- Hooks are Claude (and Grok-via-Claude-compat) only. Cursor/Copilot/Codex hooks are separate files, not a port of `hooks.json`.
- No Cursor / Codex / Copilot / Gemini shims.

## Recommended GitHub layout

### Catalog repo (new): `Wicked-Sick-Ltd/ai-marketplace`

One repo, several indexes at the paths each client already looks for. Plugins are **not** vendored here; entries use GitHub sources that point at the plugin repos.

```
ai-marketplace/
├── README.md                          # how to add each marketplace + horses-for-courses
├── .claude-plugin/marketplace.json    # Claude Code + Grok (Grok reads Claude catalogs)
├── .cursor-plugin/marketplace.json    # Cursor team / public marketplace
├── .agents/plugins/marketplace.json   # ChatGPT Work + Codex CLI
├── .github/plugin/marketplace.json    # Copilot CLI / VS Code Agent Plugins
├── .grok-plugin/marketplace.json      # optional; only if we want Grok-native listing
└── gemini/README.md                   # install URLs; Gemini has no catalog file
```

Add once per machine / team:

```bash
# Claude Code (Grok picks this up too)
claude plugin marketplace add Wicked-Sick-Ltd/ai-marketplace

# ChatGPT native CLI (Codex)
codex plugin marketplace add Wicked-Sick-Ltd/ai-marketplace --sparse .agents/plugins

# Grok Build (only if we maintain a Grok-native index)
grok plugin marketplace add Wicked-Sick-Ltd/ai-marketplace

# Cursor: Team marketplace → connect this GitHub repo
# Copilot: chat.plugins.marketplaces = ["Wicked-Sick-Ltd/ai-marketplace"]
# Gemini: gemini extensions install https://github.com/Wicked-Sick-Ltd/<plugin>
```

Do **not** put marketplace indexes inside `traefik-laravel-forge` or inside `token-usage`. Catalogs are org infrastructure; plugins are products.

### Plugin source repos (existing pattern)

Keep `token-usage` as its own repo. Same for future plugins (`shift-ai` fork, domain tools, Forge helpers). Each plugin repo grows toward:

```
token-usage/
├── plugin.json                         # Agent Plugins 1.0 (portable)
├── mcp.json                            # portable MCP (stdio + typed transport)
├── skills/<name>/SKILL.md              # Agent Skills spec — one copy
├── scripts/                            # host-neutral CLI + MCP
├── .claude-plugin/plugin.json          # Claude (keep; already works)
├── hooks/hooks.json                    # Claude / Grok-compat
├── .cursor-plugin/plugin.json          # Cursor extras (rules, hooks, variables)
├── .codex-plugin/plugin.json           # ChatGPT/Codex fallback until they read root plugin.json
├── gemini-extension.json               # Gemini CLI (must sit at *that* repo root)
├── com.github.copilot/                 # Copilot-only agents/commands/hooks
└── SUBMISSION.md                       # per-directory submission answers
```

Portable core is **skills + MCP + scripts**. Everything else is a shim. If a shim would be a lie (e.g. token-usage hooks against Claude transcripts on a host that has no Claude transcripts), **do not list that plugin on that marketplace**.

`awesome-copilot` stays a fork of the community catalog, not our storefront. We may *submit* a plugin there later; we do not use the fork as Wicked Sick's source of truth.

## How each platform defines a marketplace

| Platform | Catalog file (repo root) | Install | Public directory | What a plugin can contain |
| --- | --- | --- | --- | --- |
| **Claude Code** | `.claude-plugin/marketplace.json` | `/plugin marketplace add owner/repo` then `/plugin install` | [anthropics/claude-plugins-official](https://github.com/anthropics/claude-plugins-official) + clau.de form | Skills, agents, hooks, MCP, LSP |
| **Grok Build** | `.grok-plugin/marketplace.json` **or** Claude's catalog | `/marketplace` / `grok plugin marketplace add` | [xai-org/plugin-marketplace](https://github.com/xai-org/plugin-marketplace) | Same family as Claude; **reads Claude catalogs natively** |
| **Cursor** | `.cursor-plugin/marketplace.json` | Customize → marketplace; Teams connect a Git repo | cursor.com/marketplace (review); cursor.directory (community) | Agent Plugins skills+MCP; Cursor extras: rules, agents, commands, hooks, variables |
| **ChatGPT app + Codex CLI** | `.agents/plugins/marketplace.json` | `codex plugin marketplace add owner/repo`; Plugins Directory in the desktop app (Work / Codex) | Universal plugin directory (ChatGPT + Codex). **Not** in Chat, IDE extension, or mobile | Skills, MCP/connectors, optional Apps SDK UI, hooks |
| **GitHub Copilot** (VS Code, Copilot CLI, Copilot app) | `.github/plugin/marketplace.json` (awesome-copilot style) or Agent Plugins marketplace | `chat.plugins.marketplaces`; Awesome Copilot is default | Awesome Copilot; enterprise `extraKnownMarketplaces` | Agent Plugins skills+MCP; Copilot-only under `com.github.copilot/` |
| **Gemini CLI** | **None.** `gemini-extension.json` must be at the **plugin repo** root | `gemini extensions install <github-url>` | [geminicli.com/extensions](https://geminicli.com/extensions/) gallery | MCP, `GEMINI.md`, commands, hooks, sub-agents, `skills/*/SKILL.md` |

Authoritative docs used for this table:

- Claude: https://code.claude.com/docs/en/plugin-marketplaces
- Cursor: https://cursor.com/docs/reference/plugins
- ChatGPT/Codex: https://developers.openai.com/plugins/build/plugins
- Agent Plugins 1.0: https://agent-plugins.org
- Copilot Agent Plugins: https://github.blog/changelog/2026-08-12-agent-plugins-1-0-in-vs-code-copilot-cli-and-the-copilot-app/
- Gemini: https://github.com/google-gemini/gemini-cli/blob/main/docs/extensions/index.md
- Grok: https://docs.x.ai/build/features/skills-plugins-marketplaces

## Portable floor: Agent Plugins 1.0 + Agent Skills

Cursor, Copilot, Codex/ChatGPT, and the Agent Plugins TSC (Amazon, Cursor, Microsoft, OpenAI, Vercel; Google as core maintainer) share one package shape:

```
plugin.json          # $schema https://agent-plugins.org/schemas/1.0.0/plugin.schema.json
skills/<id>/SKILL.md # agentskills.io
mcp.json             # typed stdio / streamable-http
com.<vendor>.<product>/   # ignored by other clients
```

Claude and Grok still want `.claude-plugin/plugin.json` and Claude-style MCP keys. Gemini wants `gemini-extension.json` at the source-repo root. We keep those **in addition to** the portable floor, not instead of it.

Closed root `plugin.json`: do not put Claude `mcpServers`, Cursor `hooks`, or Copilot agents at the top level of the Agent Plugins manifest. Those go in client namespaces or the client-specific hidden dirs.

## Horses for courses

We already pick tools by job. Encode that in the catalog README so a plugin is listed only where it earns its keep.

| Job | Default horse | Why | Marketplace listing |
| --- | --- | --- | --- |
| Day-to-day coding in this Traefik/Go repo, Cloud Agents | **Cursor** | Native here; team marketplace; Agent Plugins | Cursor catalog |
| Deep plugin authoring, Claude-native hooks, Cowork | **Claude Code** | `token-usage` is a first-class Claude plugin | Claude catalog (+ official directory when ready) |
| ChatGPT app / native CLI, research, connector/Apps UI | **ChatGPT Work + Codex CLI** | One directory for app and CLI; not in Chat/mobile | Codex/ChatGPT catalog |
| VS Code, PR review, GitHub-native agents | **Copilot** | Already forked `awesome-copilot`; Agent Plugins 1.0 | Copilot catalog; optional later PR to Awesome Copilot |
| Huge-context / Google-stack / Gemini CLI | **Gemini** | Extension gallery + GitHub URL install; no org catalog file | Gemini README + `gemini-extension.json` on the plugin repo |
| Terminal agent on Grok, reuse Claude work | **Grok** | Zero-config Claude compatibility | Usually **no extra catalog**; add Grok-native only for Grok-only plugins |

`token-usage` as a first example:

| Surface | Ship? | Notes |
| --- | --- | --- |
| Claude Code + Cowork | Yes (already) | Hooks + skill + MCP |
| Grok | Yes, via Claude marketplace | Transcripts are Claude-shaped; Grok reads Claude plugins |
| Cursor / Copilot / Codex | MCP + skill **if** we add parsers for those hosts' logs, or document "Claude transcripts only" | Listing a Claude-only profiler on Cursor without a Cursor transcript parser is misleading |
| ChatGPT app | MCP remote only if we ever host the server; local stdio is CLI-shaped | Apps SDK UI is unnecessary |
| Gemini | Same caveat as Cursor | Only if Gemini session logs are worth parsing |

Most Wicked Sick plugins will be **workflow skills** (review, Forge, Shift, solar-system ops). Those belong on every marketplace that speaks Agent Skills. Host-specific observability plugins stay on the host that produces the logs.

## Catalog entry shape (Claude index as the template)

`token-usage` already drafted this in `SUBMISSION.md`. The org Claude catalog should look like:

```json
{
  "name": "wickedsick",
  "description": "Wicked Sick plugins for the agents we actually use",
  "owner": {
    "name": "Wicked Sick Ltd",
    "email": "craig@wickedsick.com"
  },
  "plugins": [
    {
      "name": "token-usage",
      "description": "Attribute token usage to the work that consumed it",
      "version": "0.6.1",
      "source": {
        "source": "github",
        "repo": "Wicked-Sick-Ltd/token-usage",
        "ref": "v0.6.1"
      },
      "homepage": "https://discovery.wickedsick.com/token-usage-claude-code-plugin-documentation",
      "keywords": ["tokens", "cost", "observability"]
    }
  ]
}
```

Pin **`ref` (tag) or `sha`** on every remote entry. Floating `main` in a marketplace is how a bad commit becomes everyone's plugin. Grok's official catalog requires a full SHA; we should do the same everywhere.

Cursor / Codex / Copilot indexes repeat the same plugin list with that platform's source object. Gemini's `gemini/README.md` is a table of `gemini extensions install` URLs plus gallery-submission status.

## What to improve versus the Claude plugin as we roll out

1. **Host-neutral scripts.** Prefer `PLUGIN_ROOT` / argv over `${CLAUDE_PLUGIN_ROOT}` in shared Python. Keep Claude env vars as aliases.
2. **One skill description, many allow-lists.** Skill body stays portable; `allowed-tools` is host-specific — either omit it (Gemini/Grok ignore-or-don't-enforce) or maintain a short per-host overlay, not five SKILL.md copies.
3. **MCP is the cross-AI API.** Anything we want on ChatGPT, Cursor, Copilot, and Gemini should be a stdio or remote MCP tool. Skills tell the model when to call it.
4. **Hooks are not portable.** Rebuild per host or skip. Never pretend a Claude Stop hook runs in Codex.
5. **Submission files per directory.** `SUBMISSION.md` today is Claude-directory-shaped. Add sections (or sibling files) for Cursor publish, ChatGPT universal directory, Gemini gallery, Awesome Copilot, xAI marketplace PR.
6. **CI that validates catalogs.** Fail the marketplace PR if a `source.repo` 404s, a pin is missing, or `plugin.json` name ≠ catalog name.

## Repo agent instructions (this Traefik plugin)

Making *this* Traefik plugin suitable for every frontline AI is instruction files, not Traefik features.

| File | Who loads it |
| --- | --- |
| `CLAUDE.md` | Claude Code (already the source of truth) |
| `AGENTS.md` | Cursor, Codex, Copilot, Grok, many others |
| `.github/copilot-instructions.md` | Copilot in VS Code / PRs |
| `GEMINI.md` | Gemini CLI (optional; only if we use Gemini on this repo) |

Do **not** copy the Forge/Yaegi rules into five files. `AGENTS.md` and Copilot instructions point at `CLAUDE.md`. That file stays the only place hard-won API knowledge lives.

A later optional product plugin — "Forge/Traefik routing skill" — could live in its own repo and be listed on the org marketplaces for agents that work *on* Forge sites, not inside this module.

## Rollout order

1. **Stand up `Wicked-Sick-Ltd/ai-marketplace`** with README + empty-but-valid indexes for Claude, Cursor, Codex, Copilot. Optional Grok index. Gemini README stub.
2. **Add `token-usage` to the Claude index only**, pinned to a release tag. `/plugin marketplace add` on a real Claude Code machine; install; run `/token-usage:report`.
3. **Confirm Grok** sees the same marketplace via Claude compatibility (`grok inspect`). Skip a Grok-native index unless inspect is incomplete.
4. **Add Agent Plugins `plugin.json` + `mcp.json` to `token-usage`** without breaking Claude (keep `.claude-plugin/`).
5. **Cursor team marketplace** on the same GitHub repo. Install a *portable* skill plugin first (Shift-style workflow), not token-usage, unless Cursor session parsing exists.
6. **Codex:** `codex plugin marketplace add Wicked-Sick-Ltd/ai-marketplace --sparse .agents/plugins`. Test in ChatGPT desktop Work/Codex, not in Chat/mobile.
7. **Copilot:** point `chat.plugins.marketplaces` at the catalog; keep the `awesome-copilot` fork as upstream tracking only.
8. **Gemini:** add `gemini-extension.json` to plugin repos that are actually used from Gemini CLI; submit to the gallery when stable.
9. **Public directories last** (Claude official, Cursor marketplace review, ChatGPT universal directory, Awesome Copilot, xAI catalog). Internal GitHub catalog is the daily driver.

## Out of scope

- Changing Traefik poll/routing behaviour.
- A new top-level "Claude vs Cursor" markdown series in every product repo.
- Vendoring plugin sources into the catalog repo.
- One mega-plugin that claims to be native on every host.

## Decision checklist (when we implement)

- [ ] Create `Wicked-Sick-Ltd/ai-marketplace` (empty indexes + README).
- [ ] Pin `token-usage` into the Claude catalog; document the one-liner in the catalog README.
- [ ] Add `AGENTS.md` (and Copilot pointer) to product repos, including this one, without duplicating `CLAUDE.md`.
- [ ] For each new plugin: portable floor first, then only the shims for marketplaces we will list on.
- [ ] Update `token-usage` SUBMISSION.md with the other directories when we actually submit.
