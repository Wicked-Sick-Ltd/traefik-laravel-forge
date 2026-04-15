# Tag Configuration

Forge tags let you control routing behaviour per-site and per-server without editing config files. Changes apply on the next poll (default: 30 seconds).

## Format

```
traefik:key=value
traefik:flag
```

Tags that don't start with `traefik:` are ignored. Tags with no `=` are treated as boolean flags (`true`).

## Site tags

Add these to a site in Forge (Sites → your site → Tags).

### `traefik:enabled`

Control whether a site gets a router. Most useful with `defaultSitesEnabled: false` for opt-in mode.

```
traefik:enabled=true    # include this site
traefik:enabled=false   # exclude this site
```

When `defaultSitesEnabled: true` (the default), all installed sites are routed unless explicitly disabled.

---

### `traefik:cert-resolver`

Override the certificate resolver for this site, independent of `defaultCertResolver`.

```
traefik:cert-resolver=letsencrypt
traefik:cert-resolver=letsencrypt-staging
traefik:cert-resolver=cloudflare
```

Setting this also enables TLS for the site (equivalent to setting `traefik:tls=true`).

---

### `traefik:tls`

Enable or disable TLS for this site, regardless of whether `defaultCertResolver` is set.

```
traefik:tls=true
traefik:tls=false
```

---

### `traefik:port`

Override the backend port for this site. Useful when a specific app listens on a non-standard port.

```
traefik:port=8080
traefik:port=3000
```

Does not affect the Reverb router port — use `traefik:reverb-port` for that.

---

### `traefik:http-redirect`

Override the global `httpRedirect` setting for this specific site.

```
traefik:http-redirect=true    # generate HTTP→HTTPS redirect router
traefik:http-redirect=false   # no redirect router even if httpRedirect is globally true
```

---

### `traefik:entrypoints`

Override which Traefik entry points this site's router listens on. Comma-separated.

```
traefik:entrypoints=websecure
traefik:entrypoints=web,websecure
```

When not set, defaults to `websecure` if TLS is enabled, `web` otherwise.

---

### `traefik:aliases`

Add extra hostnames to the router rule that aren't registered as domain records in Forge. Comma-separated.

```
traefik:aliases=app.example.com
traefik:aliases=app.example.com,www.example.com
```

These are appended as `|| Host(...)` clauses to the generated rule. For subdomains already covered by a wildcard domain (`allow_wildcard_subdomains` enabled in Forge), this tag is unnecessary.

---

### `traefik:middlewares`

Attach one or more named Traefik middlewares to this site's router. Comma-separated. The middlewares must be defined elsewhere — either in the Traefik static config or a static dynamic config file (e.g. `conf.d/middlewares.toml`).

```
traefik:middlewares=my-auth
traefik:middlewares=my-auth,rate-limit,headers
```

Applied to the main router and the Reverb router. Not applied to the HTTP redirect router (which only redirects — adding auth there would block the redirect).

Example `conf.d/middlewares.toml`:
```toml
[http.middlewares.my-auth.basicAuth]
  users = ["user:$apr1$..."]
```

Aliases: `traefik:middleware`

---

### `traefik:reverb-port`

Override the Reverb WebSocket port. The plugin auto-detects the port from Forge's Reverb integration config — use this tag only if the detected port is wrong.

```
traefik:reverb-port=8081
```

Has no effect if the site has no Reverb domain configured.

---

## Server tags

Add these to a server in Forge (Servers → your server → Tags).

### `traefik:upstream-host`

Override the IP address used to reach this server. By default the plugin uses `private_ip_address` from Forge, falling back to `ip_address`.

```
traefik:upstream-host=10.0.1.10
```

Aliases: `traefik:lb-host`, `traefik:loadbalancer-host`

---

### `traefik:upstream-port`

Override the default backend port (80) for all sites on this server. Can be overridden per-site with `traefik:port`.

```
traefik:upstream-port=8080
```

Aliases: `traefik:lb-port`, `traefik:loadbalancer-port`

---

### `traefik:traefik-id`

Assign this server to a specific Traefik instance. When the plugin is configured with `traefikID: "lb01"`, it only processes servers carrying a matching `traefik:traefik-id=lb01` tag — all others are skipped.

```
traefik:traefik-id=lb01
traefik:traefik-id=lb02
```

Has no effect when `traefikID` is not set in the plugin config (single-LB mode).

---

## Forge configuration auto-behaviours

These are not tags — they are settings you configure in the Forge UI that the plugin reads automatically when fetching domain records.

### Wildcard subdomains

If a domain record in Forge has **Allow Wildcard Subdomains** enabled, the plugin appends a `HostRegexp` rule for all single-level subdomains (Traefik v3 syntax):

```
Host(`example.com`) || HostRegexp(`^[^.]+\.example\.com$`)
```

Configured in Forge under Sites → your site → Domains → edit the primary domain.

### www redirects

If a domain record has **WWW Redirect** set to `from-www` or `to-www`, the plugin adds `www.<domain>` to the router's Host() rule:

```
Host(`example.com`) || Host(`www.example.com`)
```

Traefik must route both the apex and `www.` variant to the backend regardless of redirect direction, since the redirect itself is performed by Nginx on the Forge server. Configured in Forge under Sites → your site → Domains → edit the primary domain.

### Reverb WebSocket

If the Forge Reverb integration is enabled for a site (Sites → your site → Integrations → Reverb), the plugin auto-creates a separate router for the Reverb domain pointing to the configured port. Override the port with `traefik:reverb-port=` if needed.

---

## Configuration priority

For any given setting, the first matching source wins:

| Priority | Source |
|----------|--------|
| 1 (highest) | Site tag |
| 2 | Server tag |
| 3 | `serverMappings` in plugin config |
| 4 | Plugin config defaults |
| 5 (lowest) | Auto-detected from Forge API |

---

## Examples

### Opt-in mode: only route tagged sites

Plugin config:
```yaml
defaultSitesEnabled: false
defaultCertResolver: cloudflare
httpRedirect: true
```

Tag any site you want routed:
```
traefik:enabled=true
```

Everything else is hidden from Traefik until explicitly tagged.

---

### Staging cert resolver

Production uses `cloudflare` via `defaultCertResolver`. A staging site needs a different CA:

```
traefik:cert-resolver=letsencrypt-staging
```

---

### Override backend port for a Node.js app

App running on port 3000:

```
traefik:port=3000
```

---

### Add a subdomain alias not in Forge domain records

`app.example.com` isn't a Forge domain record but should route to the same site:

```
traefik:aliases=app.example.com
```

If `example.com` has `allow_wildcard_subdomains` enabled in Forge, the plugin handles this automatically via `HostRegexp` — no tag needed.
