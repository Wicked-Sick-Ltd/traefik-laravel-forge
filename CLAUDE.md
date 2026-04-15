# CLAUDE.md

This is a Traefik v3 provider plugin that replaces manually-maintained static TOML routing configs by auto-discovering sites from the Laravel Forge API. The owner manages several Laravel apps on a single Forge-managed server (`php01`) behind a Traefik reverse proxy, with Cloudflare DNS challenge for TLS.

## Hard-won API knowledge

These are things that are not obvious from the code and were discovered by inspecting real API responses. Don't assume them — verify if anything changes.

### Tags come from relationships, not attributes

Forge tags are **not** in `attributes.tags`. The JSON:API response has them in `relationships.tags.data` (as ID references) with the actual tag names in the top-level `included` array. The `Tags []string` field in `ForgeServerAttributes` and `ForgeSiteAttributes` is a computed field we populate after parsing — it's marked `json:"-"` and never comes from the API directly. See `mapTagsToServers` and `mapTagsToSites`.

### `links` and `meta` can be arrays

The Forge API returns `"links": []` (an empty array) at times instead of an object. Use `interface{}` for both `Links` and `Meta` fields on response structs, not `map[string]interface{}`. Getting this wrong causes a decode error.

### Domains endpoint — not `/aliases`

What Forge calls "aliases" in the UI are returned by the `/domains` endpoint:
```
GET /api/orgs/{org}/servers/{server}/sites/{site}/domains
```
Trying `/aliases` in the URL actually also works (it redirects internally) but the canonical path is `/domains`. The response type is `domainRecords`.

### Domain type `alias` ≠ alias-only

The `type` field on a domain record is unreliable for inferring routing intent. For example, `ws.bounceiq.net` is type `alias` but is actually the Reverb WebSocket endpoint (port 8081). Don't use domain type alone to decide routing behaviour — use the Reverb integration endpoint instead.

### Reverb is the source of truth for WebSocket ports

```
GET /api/orgs/{org}/servers/{server}/sites/{site}/integrations/reverb
```
Returns `enabled`, `host`, and `port`. This is authoritative. If `enabled: false` or `port: null`, the site has no Reverb. Don't use domain record type `reverb` as the sole signal — use this endpoint. The `ws.bounceiq.net` domain is type `alias` (not `reverb`) but still has a Reverb integration at port 8081.

### Wildcard subdomains require Traefik v3

`allow_wildcard_subdomains: true` on a domain record triggers a `HostRegexp` clause. The syntax used is Traefik v3:
```
HostRegexp(`^[^.]+\.example\.com$`)
```
Traefik v2 uses named groups (`{subdomain:[a-z]+}.example.com`) which is incompatible. The plugin is v3-only.

### API is JSON:API

All responses use the [JSON:API](https://jsonapi.org) envelope format with `data`, `included`, `links`, `meta`. Related resources (like tags) come in `included` and are referenced by ID from `relationships`. The `fetchForgeServersRaw` and `fetchForgeSitesRaw` methods handle this.

## What the plugin owns vs. what stays in static config

**Plugin owns:** Everything that is a Forge-managed site — HTTP routers, HTTPS routers, HTTP redirect routers, Reverb WebSocket routers.

**Static config owns:**
- Vanity domain redirects (e.g. `bounceiq.net` → `bounceiq.com`) — these aren't Forge sites
- Traefik dashboard config
- Shared middleware definitions (the plugin references them by name via `traefik:middlewares=` tag)

The owner's live static config lives in `etc-traefik/` in this repo for reference.

## Verify tool (`cmd/verify`)

The verify tool is essential for development — it lets you preview plugin output against the real Forge API without running Traefik. Always use it to confirm changes before deploying.

```bash
FORGE_TOKEN=xxx FORGE_ORG=wickedsick go run ./cmd/verify \
  --cert-resolver cloudflare \
  --http-redirect
```

Use `--dump` to see raw Forge API responses when debugging struct mapping issues. Use `--json` to see the full generated Traefik config. Use `--compare path/to/file.toml` to diff against live static config.

Dump output files (`dump*.json`, `verify.out`) are gitignored.

## Development workflow

1. Make code changes
2. `go build ./... && go test ./...`
3. Run verify against real Forge API to confirm correct output
4. Commit

There are no integration tests that hit the real API — all tests are unit tests against the tag parsing and config validation logic. The verify tool is the integration test substitute.

## Configuration the owner uses

```yaml
apiToken: <from env>
organization: wickedsick
defaultCertResolver: cloudflare
httpRedirect: true
pollInterval: 30s
```

No `serverMappings` — everything auto-detected. No `redirectMiddleware` — plugin auto-creates `forge-https-redirect`.

## Documentation maintenance

**Keep docs in sync with the code. Documentation updates must be in the same commit as the code change — never defer them.**

When adding or changing features, update these files:

- **README.md** — if adding a new plugin config option, changing how the plugin works overall, or adding a new auto-discovered behaviour from Forge
- **TAG_CONFIGURATION.md** — for ANY of the following:
  - Adding, removing, or changing a tag (site or server)
  - Adding or changing a behaviour that is driven by a Forge configuration option rather than a tag (e.g. wildcard subdomains, www redirects, Reverb integration). These go in the "Forge configuration auto-behaviours" section.
  - Changing what the plugin reads from the Forge API and how it affects routing
- **MIGRATION_FROM_STATIC.md** — if the migration story changes (new categories of what the plugin handles vs. what stays static)
- **traefik.example.yml** — if adding config options or changing the recommended setup

The rule of thumb: if a user reading only the docs would get the wrong behaviour, the docs are wrong. Fix them in the same commit as the code change.

Do not add new top-level markdown files for features or investigations — that's how the repo ended up with 11 planning docs that had to be deleted. Put feature documentation in the existing files.

## Repo layout

```
forge.go              # plugin implementation
forge_test.go         # unit tests (tag parsing, config validation)
cmd/verify/main.go    # CLI tool for previewing plugin output against real Forge API
etc-traefik/          # owner's live Traefik static config (reference only, not deployed from here)
traefik.example.yml   # example static config for new users
README.md             # main docs
TAG_CONFIGURATION.md  # complete tag reference
MIGRATION_FROM_STATIC.md  # migration guide
```
