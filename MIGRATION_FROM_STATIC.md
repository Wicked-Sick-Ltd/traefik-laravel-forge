# Migrating from static config

If you're currently managing Traefik routers with static TOML or YAML files, this guide walks through replacing them with the plugin.

## What the plugin replaces

The plugin auto-generates routers for sites managed in Forge. A typical static config file like this:

```toml
# /etc/traefik/conf.d/myapp.com.toml
[http.routers.myapp]
  rule = "Host(`myapp.com`)"
  service = "myapp-service"
  entryPoints = ["websecure"]
  [http.routers.myapp.tls]
    certResolver = "cloudflare"

[http.routers.myapp-http]
  rule = "Host(`myapp.com`)"
  entryPoints = ["web"]
  middlewares = ["https-redirect"]
  service = "myapp-service"

[http.services.myapp-service]
  [http.services.myapp-service.loadBalancer]
    [[http.services.myapp-service.loadBalancer.servers]]
      url = "http://192.168.1.10:80"
```

…becomes zero config. The plugin generates an equivalent router automatically as long as `myapp.com` is a site on a Forge-managed server.

## What stays in static config

Not everything belongs in the plugin. Keep static files for:

| Use case | Why |
|----------|-----|
| Vanity domain redirects | These aren't Forge sites — they're pure routing policy |
| Traefik API dashboard | Unrelated to Forge |
| Shared middleware definitions | Define once, reference by name from multiple routers |
| WebSocket routes with non-standard ports not in Forge | If the port isn't in Forge's Reverb integration, use static config |

## Migration steps

### 1. Audit your static files

For each router in your static config, ask: **is this domain a site in Forge?**

- Yes → the plugin will generate it; delete the static file after verifying
- No → keep it as a static file

### 2. Install the plugin

Add to `traefik.yml`:

```yaml
experimental:
  plugins:
    forge:
      moduleName: github.com/Wicked-Sick-Ltd/traefik-laravel-forge
      version: v1.0.0

providers:
  plugin:
    forge:
      apiToken: "your-forge-api-token"
      organization: "your-org-slug"
      defaultCertResolver: "cloudflare"   # match your existing cert resolver name
      httpRedirect: true
```

If your static config references an `https-redirect` middleware defined in a shared file, either:
- Set `redirectMiddleware: "https-redirect"` and keep the shared file, **or**
- Omit `redirectMiddleware` — the plugin creates `forge-https-redirect` automatically

### 3. Verify before switching

Use the verify tool to preview what the plugin would generate and compare it against your live config:

```bash
FORGE_TOKEN=xxx FORGE_ORG=my-org go run ./cmd/verify \
  --cert-resolver cloudflare \
  --http-redirect \
  --compare /etc/traefik/conf.d/myapp.com.toml
```

The output shows three sections:
- **MISSING** — hosts in your static file not covered by the plugin (check if the site exists in Forge)
- **NEW** — Forge sites not yet in your static config (new routes that will appear)
- **MATCHED** — hosts covered by both

### 4. Handle domain aliases

If your static config has multi-host rules like:

```toml
rule = "Host(`myapp.com`) || Host(`app.myapp.com`)"
```

Check whether these are already registered as domain aliases in Forge (under Sites → your site → Domains). If so, the plugin picks them up automatically.

If `myapp.com` has `allow_wildcard_subdomains` enabled in Forge, subdomains are covered automatically via a `HostRegexp` rule — no extra config needed.

For any alias that isn't in Forge and you don't want to add there, use the `traefik:aliases=` tag on the site.

### 5. Handle Reverb WebSocket routes

If you have static entries for Reverb/WebSocket domains (e.g. `ws.myapp.com`), check whether the domain is registered in Forge's Reverb integration (Sites → your site → Integrations → Reverb).

If it is, the plugin creates the Reverb router automatically with the correct port. Delete the static file.

If the port in Forge is wrong, use the `traefik:reverb-port=` tag on the site to override it.

### 6. Remove static files gradually

Once you've verified coverage, remove static config files one at a time and confirm routing still works after each removal. Traefik's file provider and the plugin coexist — there's no need to cut over all at once.

## Example: before and after

**Before** (4 static TOML files):

```
conf.d/
  myapp.com.toml          # main router + HTTP redirect
  ws.myapp.com.toml       # Reverb WebSocket router
  staging.myapp.com.toml  # staging site
  vanity-old-brand.toml   # old domain redirect (keep this one)
```

**After** (1 static file, everything else via plugin):

```
conf.d/
  vanity-old-brand.toml   # stays — pure redirect, not a Forge site

traefik.yml               # plugin config replaces the other three files
```

`myapp.com` and `ws.myapp.com` are generated from Forge. `staging.myapp.com` is generated if it's a Forge site. `vanity-old-brand.toml` stays because it's a redirect, not an app.
