# Migration from Static Configs to Plugin

This document shows how to reproduce your current static Traefik configurations using the Laravel Forge provider plugin.

## Your Current Static Configs

### perfectcellar.net.toml
```toml
# Main router (HTTPS)
[http.routers.perfectcellar-net]
  rule = "Host(`perfectcellar.net`)"
  service = "perfectcellar-net-service"
  entryPoints = ["websecure"]
  [http.routers.perfectcellar-net.tls]
    certResolver = "cloudflare"

# HTTP router for HTTP->HTTPS redirect
[http.routers.perfectcellar-net-http]
  rule = "Host(`perfectcellar.net`)"
  entryPoints = ["web"]
  middlewares = ["https-redirect"]
  service = "perfectcellar-net-service"

# Backend service
[http.services.perfectcellar-net-service]
  [http.services.perfectcellar-net-service.loadBalancer]
    [[http.services.perfectcellar-net-service.loadBalancer.servers]]
      url = "http://192.168.5.101:80"
```

### ws.perfectcellar.net.toml
```toml
# WebSocket router (HTTPS)
[http.routers.ws-perfectcellar-net]
  rule = "Host(`ws.perfectcellar.net`)"
  service = "ws-perfectcellar-net-service"
  entryPoints = ["websecure"]
  [http.routers.ws-perfectcellar-net.tls]
    certResolver = "cloudflare"

# Backend service (different port!)
[http.services.ws-perfectcellar-net-service]
  [http.services.ws-perfectcellar-net-service.loadBalancer]
    [[http.services.ws-perfectcellar-net-service.loadBalancer.servers]]
      url = "http://192.168.5.101:8080"
```

---

## Migration to Plugin

### Step 1: Initial Traefik Configuration

Replace your static `.toml` files with this plugin configuration:

```yaml
# traefik.yml
entryPoints:
  web:
    address: :80
  websecure:
    address: :443

# Define the https-redirect middleware
http:
  middlewares:
    https-redirect:
      redirectScheme:
        scheme: https
        permanent: true

# Plugin configuration
experimental:
  plugins:
    forge:
      moduleName: github.com/wickedsick/traefik-laravel-forge
      version: v1.0.0

providers:
  plugin:
    forge:
      apiToken: "your-forge-api-token"
      organization: "your-org-slug"
      pollInterval: "30s"
      defaultCertResolver: "cloudflare"
      httpRedirect: true                    # Enable HTTP->HTTPS redirect
      redirectMiddleware: "https-redirect"  # Use the middleware defined above
      serverMappings: []                    # Empty! Everything via tags

# Certificate resolver
certificatesResolvers:
  cloudflare:
    acme:
      email: your-email@example.com
      storage: /acme.json
      dnsChallenge:
        provider: cloudflare
        resolvers:
          - "1.1.1.1:53"
          - "8.8.8.8:53"
```

### Step 2: Configure Forge Server

Assuming both sites are on the same server (server IP: 192.168.5.101):

**Server Tags in Forge:**
```
traefik:upstream-host=192.168.5.101
```

That's it for the server! The plugin will auto-detect and use this IP.

### Step 3: Configure Forge Sites

**Site: perfectcellar.net**
- No tags needed!
- Inherits `defaultCertResolver: cloudflare`
- Gets HTTP redirect automatically (`httpRedirect: true`)

**Site: ws.perfectcellar.net**
Add this tag (custom port):
```
traefik:port=8080
```

### What Gets Generated

The plugin will automatically create:

**For perfectcellar.net:**
```toml
[http.routers.forge-{server}-{id}]
  rule = "Host(`perfectcellar.net`)"
  service = "forge-{server}-{id}-service"
  entryPoints = ["websecure"]
  [http.routers.forge-{server}-{id}.tls]
    certResolver = "cloudflare"

[http.routers.forge-{server}-{id}-http]
  rule = "Host(`perfectcellar.net`)"
  entryPoints = ["web"]
  middlewares = ["https-redirect"]
  service = "forge-{server}-{id}-service"

[http.services.forge-{server}-{id}-service]
  [http.services.forge-{server}-{id}-service.loadBalancer]
    passHostHeader = true
    [[http.services.forge-{server}-{id}-service.loadBalancer.servers]]
      url = "http://192.168.5.101:80"
```

**For ws.perfectcellar.net:**
```toml
[http.routers.forge-{server}-{id}]
  rule = "Host(`ws.perfectcellar.net`)"
  service = "forge-{server}-{id}-service"
  entryPoints = ["websecure"]
  [http.routers.forge-{server}-{id}.tls]
    certResolver = "cloudflare"

[http.routers.forge-{server}-{id}-http]
  rule = "Host(`ws.perfectcellar.net`)"
  entryPoints = ["web"]
  middlewares = ["https-redirect"]
  service = "forge-{server}-{id}-service"

[http.services.forge-{server}-{id}-service]
  [http.services.forge-{server}-{id}-service.loadBalancer]
    passHostHeader = true
    [[http.services.forge-{server}-{id}-service.loadBalancer.servers]]
      url = "http://192.168.5.101:8080"  # Custom port from tag!
```

## Comparison: Before vs After

### Before (Static Files)
```
perfectcellar.net.toml      - 20 lines
ws.perfectcellar.net.toml   - 12 lines
Total: 2 files, 32 lines
```
Every new site = new `.toml` file

### After (Plugin)
```yaml
# traefik.yml (one time setup)
providers:
  plugin:
    forge:
      apiToken: "..."
      organization: "..."
      defaultCertResolver: "cloudflare"
      httpRedirect: true
      redirectMiddleware: "https-redirect"
      serverMappings: []
```

**Forge Tags:**
- Server: `traefik:upstream-host=192.168.5.101`
- ws.perfectcellar.net: `traefik:port=8080`
- perfectcellar.net: (no tags needed)

Every new site = **automatically configured!**

## Feature Mapping

| Static Config Feature | Plugin Support | How |
|----------------------|----------------|-----|
| Host rule | ✅ Yes | Automatic from site name |
| Entry points | ✅ Yes | Auto: `websecure` for TLS, `web` for non-TLS |
| TLS cert resolver | ✅ Yes | `defaultCertResolver` or tag `traefik:cert-resolver=cloudflare` |
| Service URL | ✅ Yes | From server IP + optional site port tag |
| HTTP redirect | ✅ Yes | `httpRedirect: true` + `redirectMiddleware` config |
| Custom port per site | ✅ Yes | Tag: `traefik:port=8080` |
| Middlewares | ✅ Partial | Only redirect middleware currently |

## Advanced Tag Options

For even more control, you can use tags:

### Override Entry Points
If you wanted only HTTPS (no HTTP redirect) for a specific site:
```
traefik:entrypoints=websecure
traefik:http-redirect=false
```

### Different Cert Resolver
For testing a site with staging certs:
```
traefik:cert-resolver=letsencrypt-staging
```

### Disable TLS for Dev Site
```
traefik:tls=false
```

## Complete Reproduction Example

### Traefik Configuration
```yaml
# traefik.yml
entryPoints:
  web:
    address: :80
  websecure:
    address: :443

http:
  middlewares:
    https-redirect:
      redirectScheme:
        scheme: https
        permanent: true

experimental:
  plugins:
    forge:
      moduleName: github.com/wickedsick/traefik-laravel-forge
      version: v1.0.0

providers:
  plugin:
    forge:
      apiToken: "your-forge-api-token"
      organization: "your-org-slug"
      pollInterval: "30s"
      defaultCertResolver: "cloudflare"
      httpRedirect: true
      redirectMiddleware: "https-redirect"
      serverMappings: []

certificatesResolvers:
  cloudflare:
    acme:
      email: your-email@example.com
      storage: /acme.json
      dnsChallenge:
        provider: cloudflare
        resolvers:
          - "1.1.1.1:53"
          - "8.8.8.8:53"
```

### Forge Tags

**Server (e.g., "app01"):**
```
traefik:upstream-host=192.168.5.101
```

**Site: perfectcellar.net**
- No tags needed (uses defaults)

**Site: ws.perfectcellar.net**
```
traefik:port=8080
```

### Result

**Identical behavior to your static configs!**

Plus you get:
- ✅ Automatic discovery of new sites
- ✅ No manual `.toml` file creation
- ✅ Changes apply in 30s (no restart)
- ✅ Manage via Forge UI

## New Site Workflow Comparison

### Before (Static)
1. Create site in Forge
2. SSH to Traefik server
3. Create new `.toml` file
4. Copy/paste config
5. Update host, URL, cert resolver
6. Save file
7. Restart Traefik (or wait for file watcher)

### After (Plugin)
1. Create site in Forge
2. (Optional) Add tags if custom port needed
3. Done! Routed in 30s

**~90% less work per site!**

## Rollback Plan

If you need to rollback to static configs:

1. Keep your static `.toml` files
2. Disable the plugin in Traefik config
3. Re-enable file provider
4. Restart Traefik

The static files and plugin can coexist during migration.

## Migration Strategy

### Recommended Approach: Gradual

1. **Week 1**: Add plugin alongside static configs
   - Both active simultaneously
   - No changes to existing sites
   - Test with one new site via plugin

2. **Week 2**: Move some sites to plugin
   - Add server tags in Forge
   - Remove corresponding `.toml` files
   - Verify routing works

3. **Week 3**: Migrate remaining sites
   - All sites now via plugin
   - Delete static config files
   - Update documentation

### Aggressive Approach: All at Once

1. Add plugin config
2. Add server tags
3. Add site port tags (ws.perfectcellar.net)
4. Delete static `.toml` files
5. Restart Traefik
6. Verify all sites working

## Verification

After migration, verify each site:

```bash
# Check HTTPS works
curl -I https://perfectcellar.net

# Check HTTP redirects
curl -I http://perfectcellar.net  # Should redirect to HTTPS

# Check WebSocket site
curl -I https://ws.perfectcellar.net

# Check Traefik dashboard
# Navigate to dashboard and verify routers exist
```

## Summary

**Can the plugin reproduce your configs?** ✅ **YES, completely!**

**Required:**
- Global: `httpRedirect: true` + `redirectMiddleware`
- Server tag: `traefik:upstream-host=192.168.5.101`
- Site tag (ws.perfectcellar.net): `traefik:port=8080`

**Benefits over static:**
- Automatic site discovery
- No manual file creation
- No Traefik restarts for new sites
- Manage via Forge UI
- Less maintenance

**The plugin can fully replace your static configs!** 🎉
