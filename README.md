# traefik-laravel-forge

A [Traefik](https://traefik.io) provider plugin that automatically generates HTTP routers from your [Laravel Forge](https://forge.laravel.com) sites. Add a site to Forge and it appears in Traefik within 30 seconds — no config file edits, no restarts.

**Docs:** [Tag Configuration](TAG_CONFIGURATION.md) · [Migrating from static config](MIGRATION_FROM_STATIC.md)

## Contents

- [Requirements](#requirements)
- [How it works](#how-it-works)
- [Installation](#installation)
- [Minimal configuration](#minimal-configuration)
- [Plugin configuration reference](#plugin-configuration-reference)
- [Multiple load balancers](#multiple-load-balancers)
- [Forge tags](#forge-tags)
  - [Site tags](#site-tags)
  - [Server tags](#server-tags)
  - [Configuration priority](#configuration-priority)
- [Auto-discovered routing](#auto-discovered-routing)
  - [Domains](#domains)
  - [www redirects](#www-redirects)
  - [Reverb WebSocket](#reverb-websocket)
- [What to keep in static config](#what-to-keep-in-static-config)
- [Verification tool](#verification-tool)
- [Troubleshooting](#troubleshooting)
- [API rate limits](#api-rate-limits)
- [Development](#development)

## Requirements

- Traefik v3
- Laravel Forge account with API access
- Go 1.19+ (development only)

## How it works

On each poll the plugin:

1. Fetches all servers in your Forge organisation
2. For each server, fetches its sites and their domain records (`/domains`)
3. Fetches the Reverb WebSocket integration config per site (`/integrations/reverb`)
4. Auto-detects the server's private IP (falls back to public IP)
5. Generates Traefik routers and services for all installed sites

Each site produces:
- A **main router** covering all its primary and alias domains (with `HostRegexp` for wildcard-enabled domains)
- An **HTTP redirect router** (if `httpRedirect` is enabled)
- A **Reverb router** for the Reverb domain (if Reverb is configured in Forge), routing through Nginx

No `serverMappings` config is required — the plugin discovers everything from Forge automatically.

## Installation

### From the Traefik Plugin Catalog

```yaml
# traefik.yml
experimental:
  plugins:
    forge:
      moduleName: github.com/Wicked-Sick-Ltd/traefik-laravel-forge
      version: v1.0.0
```

### Local / development mode

```bash
mkdir -p ./plugins-local/src/github.com/wickedsick
git clone https://github.com/Wicked-Sick-Ltd/traefik-laravel-forge \
  ./plugins-local/src/github.com/Wicked-Sick-Ltd/traefik-laravel-forge
```

```yaml
# traefik.yml
experimental:
  localPlugins:
    forge:
      moduleName: github.com/Wicked-Sick-Ltd/traefik-laravel-forge
```

## Minimal configuration

```yaml
# traefik.yml
entryPoints:
  web:
    address: ":80"
  websecure:
    address: ":443"

certificatesResolvers:
  cloudflare:
    acme:
      email: you@example.com
      storage: /etc/traefik/acme/acme.json
      dnsChallenge:
        provider: cloudflare

experimental:
  plugins:
    forge:
      moduleName: github.com/Wicked-Sick-Ltd/traefik-laravel-forge
      version: v1.0.0

providers:
  plugin:
    forge:
      apiToken: "${FORGE_API_TOKEN}"
      organization: "${FORGE_ORG_SLUG}"
      defaultCertResolver: "cloudflare"
      httpRedirect: true
```

Traefik expands `$VAR` / `${VAR}` references in the static config file before passing values to the plugin, so sensitive values never need to be stored in plain text. Add them to your `.env` file (or systemd `EnvironmentFile`, Docker secrets, etc.).

That's all that's needed. The plugin discovers your servers and sites automatically.

## Plugin configuration reference

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `apiToken` | string | **required** | Forge API token — generate at forge.laravel.com/user/profile#/api. Use `${ENV_VAR}` to avoid storing in plain text |
| `organization` | string | **required** | Forge organisation slug — from the URL: `forge.laravel.com/orgs/{slug}`. Can also use `${ENV_VAR}` |
| `pollInterval` | string | `"30s"` | How often to poll Forge. Minimum `"10s"`. See [API rate limits](#api-rate-limits) before reducing this |
| `defaultCertResolver` | string | `""` | Cert resolver name to use for all sites. Enables TLS when set |
| `defaultSitesEnabled` | bool | `true` | Set to `false` for opt-in mode: only sites with `traefik:enabled` tag are routed |
| `httpRedirect` | bool | `false` | Generate HTTP→HTTPS redirect routers for all sites |
| `redirectMiddleware` | string | `""` | Name of an externally-defined redirect middleware to use. If empty and `httpRedirect` is `true`, the plugin creates `forge-https-redirect` automatically |
| `traefikID` | string | `""` | When set, only process servers tagged `traefik:traefik-id=<value>`. Use this in multi-LB setups so each Traefik instance only routes its own servers. Servers with no matching tag are skipped |
| `serverMappings` | array | `[]` | Optional: explicitly set the upstream host/port for specific servers (tags take priority over this) |

### serverMappings

Only needed if you want to override the auto-detected IP or port for a specific server.

```yaml
serverMappings:
  - forgeServerName: "app01"   # must match name in Forge exactly
    upstreamHost: "10.0.1.10"  # override auto-detected IP
    upstreamPort: 8080          # override default port 80
```

Both override fields are optional. Omit `upstreamHost` to override only the port
and keep the IP address auto-detected from Forge:

```yaml
serverMappings:
  - forgeServerName: "app01"
    upstreamPort: 8080
```

## Multiple load balancers

When running more than one Traefik instance, set `traefikID` so each instance only routes the servers assigned to it.

**On each Traefik instance** (`traefik.yml`):
```yaml
providers:
  plugin:
    forge:
      traefikID: "lb01"   # this instance only processes servers tagged traefik:traefik-id=lb01
```

**In Forge**, tag each server with which LB owns it:
```
traefik:traefik-id=lb01
```

Servers with no `traefik:traefik-id` tag (or a non-matching value) are skipped entirely when `traefikID` is configured — so every server should be explicitly assigned in multi-LB setups.

## Forge tags

Tags on Forge servers and sites configure routing behaviour. Format: `traefik:key=value`.

### Site tags

Add these to any site in Forge to control how it's routed:

| Tag | Example | Description |
|-----|---------|-------------|
| `traefik:enabled` | `traefik:enabled=false` | Enable or disable routing for this site. Useful with `defaultSitesEnabled: false` |
| `traefik:cert-resolver` | `traefik:cert-resolver=letsencrypt` | Override the cert resolver for this site |
| `traefik:tls` | `traefik:tls=true` | Force TLS on or off regardless of `defaultCertResolver` |
| `traefik:port` | `traefik:port=8080` | Override the backend port for this site |
| `traefik:http-redirect` | `traefik:http-redirect=false` | Override the global `httpRedirect` setting for this site |
| `traefik:entrypoints` | `traefik:entrypoints=websecure,web` | Override entry points (comma-separated) |
| `traefik:aliases` | `traefik:aliases=app.example.com,www.example.com` | Add extra hostnames to the router rule (comma-separated) |
| `traefik:middlewares` | `traefik:middlewares=my-auth,rate-limit` | Attach named Traefik middlewares to this site's router (comma-separated). Middlewares must be defined in static config. Applied to the main and Reverb routers; not the HTTP redirect router |
| `traefik:forge-domain` | `traefik:forge-domain=true` | Opt `.on-forge.com` domains into routing. Without this, `.on-forge.com`-only sites are skipped entirely; mixed-domain sites have the `.on-forge.com` entry dropped. When opted in, TLS is disabled for `.on-forge.com` domains |

### Server tags

Add these to a server in Forge to override how it's addressed:

| Tag | Example | Description |
|-----|---------|-------------|
| `traefik:upstream-host` | `traefik:upstream-host=10.0.1.10` | Override the auto-detected server IP |
| `traefik:upstream-port` | `traefik:upstream-port=8080` | Override the default backend port (80) |
| `traefik:traefik-id` | `traefik:traefik-id=lb01` | Informational: which Traefik instance handles this server |

Aliases accepted for backwards compatibility: `lb-host`, `lb-port`, `loadbalancer-host`, `loadbalancer-port`.

### Configuration priority

For any given setting, the resolution order is:

1. **Site tag** (highest — per-site override)
2. **Server tag** (server-level override)
3. **Server mapping** (config file)
4. **Plugin config default**
5. **Auto-detected from Forge** (lowest — IP from server record)

## Auto-discovered routing

### Domains

The plugin fetches all domain records for each site. Domain types:

- `primary` — the site's main domain, always included in the Host() rule
- `alias` — additional domains, included in the Host() rule
- `reverb` — Laravel Reverb WebSocket domain, gets its own router (see below)

**`.on-forge.com` domains are excluded by default.** Forge assigns every site an `.on-forge.com` subdomain, but Forge controls DNS and TLS for these — the plugin can't obtain certs for them and their DNS may not point to your load balancer. Sites where `.on-forge.com` is the only domain are skipped entirely. Use `traefik:forge-domain=true` to opt in (routes HTTP only, no TLS).

If a primary domain has **wildcard subdomains enabled** in Forge, a `HostRegexp` clause is automatically appended:

```
Host(`example.com`) || HostRegexp(`^[^.]+\.example\.com$`)
```

This covers `app.example.com`, `www.example.com`, etc. without any additional configuration.

### www redirects

If a domain has a **www redirect** configured in Forge (`from-www` or `to-www`), `www.<domain>` is automatically added to the Host() rule:

```
Host(`example.com`) || Host(`www.example.com`)
```

This applies in both redirect directions — Traefik must route `www.example.com` to the backend regardless of which way the redirect goes, since the redirect itself is handled by Nginx on the Forge server.

### Reverb WebSocket

If a site has Reverb configured in Forge, the plugin automatically:

1. Fetches the Reverb host from `/integrations/reverb`
2. Creates a **separate router** for the Reverb domain, pointing to Nginx on port 80
3. Applies the same TLS settings as the main router
4. **No HTTP→HTTPS redirect** for the Reverb router — WebSocket clients don't follow redirects

Nginx handles the WebSocket proxy to the Reverb process internally, using the location block Forge creates when you enable Reverb. Traefik only needs to route the Reverb domain to Nginx.

## What to keep in static config

The plugin handles app routing. Some things are outside its scope and belong in static TOML/YAML files alongside the plugin:

| Config | Reason |
|--------|--------|
| Vanity domain redirects (e.g. `old-brand.com` → `new-brand.com`) | Not a Forge site — purely a routing policy |
| Traefik API dashboard | Not related to Forge |
| Custom middleware definitions (rate limiting, auth, etc.) | Middleware is referenced by name; define it once in static config |

Example static file that can coexist with the plugin:

```toml
# /etc/traefik/conf.d/redirects.toml
[http.routers.old-brand]
  rule = "Host(`old-brand.com`)"
  entryPoints = ["websecure"]
  middlewares = ["old-brand-redirect"]
  service = "noop@internal"
  [http.routers.old-brand.tls]
    certResolver = "cloudflare"

[http.middlewares.old-brand-redirect.redirectRegex]
  regex = "^https://old-brand\\.com/(.*)"
  replacement = "https://new-brand.com/${1}"
  permanent = true
```

## Verification tool

The `cmd/verify` tool lets you preview what the plugin would generate against your live Forge account — without running Traefik:

```bash
# Preview generated configuration
FORGE_TOKEN=xxx FORGE_ORG=my-org go run ./cmd/verify \
  --cert-resolver cloudflare \
  --http-redirect \
  --redirect-middleware https-redirect

# Compare against an existing Traefik dynamic config file
go run ./cmd/verify ... --compare /etc/traefik/conf.d/mysite.toml

# Dump raw Forge API responses (useful for debugging)
go run ./cmd/verify ... --dump

# Output the full generated config as JSON
go run ./cmd/verify ... --json
```

Flags mirror the plugin config: `--cert-resolver`, `--default-sites-enabled`, `--http-redirect`, `--redirect-middleware`.

## Troubleshooting

**No routes created**
- Check `apiToken` and `organization` are correct
- Ensure sites have status `installed` in Forge
- Check Traefik logs — the plugin logs every discovery decision to stdout

**Routes created but traffic not flowing**
- Verify the server's private IP is reachable from your Traefik host
- Check that the backend is listening on the expected port

**Wrong IP being used**
- The plugin prefers `private_ip_address` over `ip_address` from Forge
- Override with a `traefik:upstream-host=` tag on the server, or a `serverMappings` entry

**Wildcard subdomains not matching**
- Requires Traefik v3 — `HostRegexp` syntax changed between v2 and v3
- Check that `allow_wildcard_subdomains` is enabled on the domain in Forge

**Reverb WebSocket not working**
- Confirm Reverb is enabled in Forge under the site's Integrations tab
- The Reverb domain routes to Nginx (port 80), which proxies to Reverb internally — check Nginx logs on the server if connections are failing

**Poll interval error**
- Minimum is `10s`. Recommended `30s`–`60s` in production.

**"Too many attempts" / HTTP 429 from Forge**
- This plugin only calls the current `/api/orgs/...` JSON:API endpoints. Forge sunset
  `/api/v1/*` on 1 September 2026, and requests against those removed routes can
  trigger "Too many attempts" for the whole token — so check other tools sharing the
  same API token first.
- The plugin quotes Forge's `X-RateLimit-Limit` / `-Remaining` / `-Reset` headers in
  the error when they are present. A 429 *without* those headers is not ordinary
  quota exhaustion and increasing `pollInterval` will not fix it.
- `pollInterval` lives in Traefik's **static** config, so changing it needs a Traefik
  restart — and a restart while Forge is refusing every request brings Traefik up
  with no routes at all until the first poll succeeds. Confirm the API is answering
  before restarting.

**Forge API errors in the logs, but routing still works**
- This is intended. If any Forge call needed to determine routing fails, the plugin
  abandons that poll entirely and sends nothing, so Traefik keeps the configuration
  it already has. A partial config would be applied as a deletion and would take
  live sites offline.
- The log line to look for is `forge: error generating configuration (keeping previous config)`.
- Routing only changes once a poll completes successfully end to end.

**A `traefik:port` or `traefik:upstream-port` tag is being ignored**
- Values must be whole numbers in the range 1–65535. Anything else (`-1`, `80x`,
  `999999`) is rejected and the default is used, with a line in the Traefik log
  naming the offending value.

## API rate limits

Forge's default API rate limit is **60 requests per minute**.

Each poll consumes approximately `1 + (N_sites × 2)` requests:

| Request | Count |
|---------|-------|
| List servers | 1 |
| List sites (per server) | 1 per server |
| Fetch domains (per site) | 1 per site |
| Fetch Reverb integration (per site) | 1 per site |

With 4 sites on 1 server that's **10 requests per poll**. At the default 30s interval that's 20 requests/minute — well within the limit.

As a rough guide for choosing `pollInterval`:

| Sites | Requests/poll | Safe minimum interval |
|-------|--------------|----------------------|
| 5 | 12 | 15s |
| 10 | 22 | 30s |
| 20 | 42 | 45s |
| 25 | 52 | 60s |

**The limit is per token, not per Traefik instance.** If several load balancers
poll with the same `apiToken`, they share one budget — two instances at a 30s
interval cost the same as one instance at 15s. Either give each instance its own
Forge token, or size `pollInterval` against the combined rate.

If you hit rate limits, Traefik logs will show Forge API errors. Increase `pollInterval` until they stop.

If you have a large number of sites and need a higher limit, Forge allows you to request an adjustment — see the [Forge API rate limiting docs](https://forge.laravel.com/docs/api-reference/rate-limiting) for details.

## Development

```bash
# Run tests
go test ./...

# Build
go build ./...

# Preview against live Forge
FORGE_TOKEN=xxx FORGE_ORG=my-org go run ./cmd/verify
```

## License

See [LICENSE](LICENSE).
