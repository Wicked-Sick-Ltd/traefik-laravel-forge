# Agent instructions

This is a Traefik v3 provider plugin. **Read `CLAUDE.md` before changing code.** That file is the source of truth for Forge API quirks, Yaegi constraints, the no-partial-config error policy, and which docs to update with a code change.

This file exists so Cursor, ChatGPT/Codex, Copilot, Gemini, and Grok discover those rules. Do not duplicate them here.

Org-wide plugin marketplaces (Claude, Cursor, Codex, Copilot, Gemini, Grok) are planned in `docs/cross-ai-marketplace-plan.md`. That plan is not Traefik runtime behaviour.

## Repository Contributor Guide

## Project Structure

A Traefik plugin to support Laravel Forge sites.

- `docs/` — design and operational documentation.

## Development and Validation

Use Go (version in the component `go.mod`). Run commands from the repository root unless the component documentation says otherwise. Configure local dependencies and test services before application tests.

- `go test ./...` — run Go tests.
- `go vet ./...` — check Go code.

## Coding and Testing

Format Go code with `gofmt`. Tests use Go testing. Keep changes focused and follow existing test filenames. Add regression coverage for behavior changes, using isolated fixtures instead of live customer data. For documentation-only edits, verify commands, local links, and `git diff --check`.

## Working Agreement

Read `CONTRIBUTING.md` and `SECURITY.md` before contributing. Read `CLAUDE.md` for additional project constraints. Honor directory-specific agent instructions. Preserve existing local changes and use a separate branch or worktree when other work is in progress. Keep credentials, private datasets, and generated artifacts out of commits. Deployment, publishing, and live service changes require authorization for that environment.

Use concise commit subjects consistent with recent history (for example, `docs: clarify setup`). Pull requests should explain the change, link relevant issues, and record validation results and any skipped checks. Include screenshots when user-visible behavior changes.

## Licensing

MIT licensed; see [LICENSE](LICENSE). Preserve third-party notices.
