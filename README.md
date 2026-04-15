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
  - [Reverb WebSocket](#reverb-websocket)
- [What to keep in static config](#what-to-keep-in-static-config)
- [Verification tool](#verification-tool)
- [Troubleshooting](#troubleshooting)
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
- A **Reverb router** on the Reverb port (if Reverb is configured in Forge)

No `serverMappings` config is required — the plugin discovers everything from Forge automatically.

## Installation

### From the Traefik Plugin Catalog

```yaml
# traefik.yml
experimental:
  plugins:
    forge:
      moduleName: github.com/wickedsick/traefik-laravel-forge
      version: v1.0.0
```

### Local / development mode

```bash
mkdir -p ./plugins-local/src/github.com/wickedsick
git clone https://github.com/wickedsick/traefik-laravel-forge \
  ./plugins-local/src/github.com/wickedsick/traefik-laravel-forge
```

```yaml
# traefik.yml
experimental:
  localPlugins:
    forge:
      moduleName: github.com/wickedsick/traefik-laravel-forge
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
      moduleName: github.com/wickedsick/traefik-laravel-forge
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
| `pollInterval` | string | `"30s"` | How often to poll Forge. Minimum `"10s"` |
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
| `traefik:reverb-port` | `traefik:reverb-port=8081` | Override the auto-detected Reverb WebSocket port |

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

If a primary domain has **wildcard subdomains enabled** in Forge, a `HostRegexp` clause is automatically appended:

```
Host(`example.com`) || HostRegexp(`^[^.]+\.example\.com$`)
```

This covers `app.example.com`, `www.example.com`, etc. without any additional configuration.

### Reverb WebSocket

If a site has Reverb configured in Forge, the plugin automatically:

1. Fetches the Reverb host and port from `/integrations/reverb`
2. Creates a **separate router** for the Reverb domain pointing to that port
3. Applies the same TLS and HTTP redirect settings as the main router

Use `traefik:reverb-port=NNNN` to override the detected port if needed.

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

**Reverb router on wrong port**
- Use `traefik:reverb-port=NNNN` tag on the site to override
- Or verify the port in Forge under the site's Reverb integration settings

**Poll interval error**
- Minimum is `10s`. Recommended `30s`–`60s` in production.

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
