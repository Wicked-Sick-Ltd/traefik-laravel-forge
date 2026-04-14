# Complete Tag-Based Configuration Guide

Configure your entire Traefik setup using only Laravel Forge tags - no config file changes needed after initial setup!

## Table of Contents

1. [Overview](#overview)
2. [Initial Traefik Setup](#initial-traefik-setup)
3. [Server Tags](#server-tags)
4. [Site Tags](#site-tags)
5. [Complete Examples](#complete-examples)
6. [Tag Reference](#tag-reference)

---

## Overview

This plugin supports **complete tag-based configuration**. After the initial Traefik setup, you can manage everything through Forge tags:

- Which servers to expose
- Load balancer IPs and ports
- TLS/certificate settings
- Per-site overrides

**Benefits:**
- Zero config file changes after initial setup
- Manage configuration directly in Forge UI
- Changes apply on next poll (default: 30s)
- No Traefik restarts needed

---

## Initial Traefik Setup

### Minimal Configuration

You only need this once in your `traefik.yml`:

```yaml
entryPoints:
  web:
    address: :80
  websecure:
    address: :443

experimental:
  localPlugins:
    forge:
      moduleName: github.com/wickedsick/traefik-laravel-forge

providers:
  plugin:
    forge:
      apiToken: "your-forge-api-token"
      organization: "your-org-slug"
      pollInterval: "30s"                 # Minimum: 10s
      defaultCertResolver: "letsencrypt"  # Optional: default cert resolver
      defaultSitesEnabled: true           # Optional: default true, set false for opt-in
      serverMappings: []  # Empty! Everything via tags

certificatesResolvers:
  letsencrypt:
    acme:
      email: your-email@example.com
      storage: /acme.json
      httpChallenge:
        entryPoint: web
```

That's it! The `serverMappings` array can be empty.

---

## Server Tags

Add tags to your servers in Forge to configure load balancing.

### Server Tags (Optional)

Servers are automatically enabled if they have any enabled sites. You only need server tags if you want to override the IP or port:

```
traefik:upstream-host=10.0.1.10  # Override upstream IP
traefik:upstream-port=8080       # Override port (default: 80)
traefik:traefik-id=lb01          # Which Traefik instance (informational)
```

**If no server tags are present:**
- The plugin automatically uses the server's `private_ip_address` from Forge
- Falls back to public `ip_address` if no private IP exists
- Uses port 80 by default

### Server Tag Examples

#### No Tags Needed (Auto-Detection)
Most common case - no server tags needed at all:
- Plugin uses `private_ip_address` from Forge
- Uses port 80
- Server enabled if it has any enabled sites

#### Override IP Address
```
traefik:upstream-host=10.0.1.10
```

#### Custom Port
```
traefik:upstream-port=8080
```

#### Both IP and Port
```
traefik:lb-host=192.168.1.50
traefik:upstream-port=8080
```

---

## Site Tags

Add tags to your sites in Forge for per-site configuration.

### Available Tags

```
traefik:enabled=true/false             # Enable/disable site
traefik:port=8080                      # Override backend port (default: server port)
traefik:cert-resolver=letsencrypt      # Use specific cert resolver
traefik:tls=true/false                 # Enable/disable TLS
traefik:http-redirect=true/false       # Enable/disable HTTP->HTTPS redirect
traefik:entrypoints=web,websecure      # Custom entry points (comma-separated)
```

### Site Tag Examples

#### Enable a Specific Site (Opt-In Mode)
```
traefik:enabled=true
```

#### Temporarily Disable a Site
```
traefik:enabled=false
```

#### Custom Backend Port (e.g., WebSocket Server)
```
traefik:port=8080
```

#### Use Production Certificates
```
traefik:cert-resolver=letsencrypt
```

#### Use Staging Certificates (Testing)
```
traefik:cert-resolver=letsencrypt-staging
```

#### Disable TLS for Dev Site
```
traefik:tls=false
```

#### Disable HTTP Redirect for Specific Site
```
traefik:http-redirect=false
```

#### Custom Entry Points
```
traefik:entrypoints=websecure
```

---

## Complete Examples

### Example 1: Simple Setup with Auto-Detection

**Traefik Config:**
```yaml
providers:
  plugin:
    forge:
      apiToken: "your-token"
      organization: "acme-corp"
      defaultCertResolver: "letsencrypt"
      serverMappings: []  # Empty!
```

**Server Tags (app01 in Forge):**
```
traefik:enabled=true
traefik:upstream-port=80
```

**Result:**
- Server is enabled for Traefik
- Uses private IP from Forge automatically (e.g., 10.0.1.10)
- All sites get HTTPS with Let's Encrypt (from defaultCertResolver)

---

### Example 2: Multi-Server with Mixed Configuration

**Server Tags:**

**app01:**
```
traefik:upstream-host=10.0.1.10
traefik:upstream-port=80
traefik:traefik-id=lb01
```

**app02:**
```
traefik:lb-host=10.0.1.11
traefik:upstream-port=8080
traefik:traefik-id=lb01
```

**app03-staging:**
```
traefik:enabled=true
traefik:upstream-port=80
```

**app04-old:**
```
traefik:enabled=false
```

**Site Tags:**

**example.com (on app01):**
- (no tags - uses defaults)

**staging.example.com (on app01):**
```
traefik:cert-resolver=letsencrypt-staging
```

**dev.test (on app03-staging):**
```
traefik:tls=false
```

**Result:**
| Server | IP | Sites | TLS |
|--------|---------|-------|-----|
| app01 | 10.0.1.10:80 | example.com | Production certs |
| app01 | 10.0.1.10:80 | staging.example.com | Staging certs |
| app02 | 10.0.1.11:8080 | (any sites) | Production certs |
| app03-staging | auto-detect:80 | dev.test | No TLS |
| app04-old | N/A | Skipped | N/A |

---

### Example 3: Opt-In Mode (Sites Disabled by Default)

**Scenario**: You want explicit control over which sites are exposed. Sites must be tagged to be enabled.

**Traefik Config:**
```yaml
providers:
  plugin:
    forge:
      apiToken: "..."
      organization: "..."
      defaultSitesEnabled: false  # Sites disabled by default!
      defaultCertResolver: "letsencrypt"
      serverMappings: []
```

**Server Tags (enable servers normally):**
```
traefik:enabled=true
```

**Site Tags (only tagged sites are enabled):**

**example.com:**
```
traefik:enabled=true
```

**api.example.com:**
```
traefik:enabled=true
```

**staging.example.com:**
- (no tag - site is **disabled**)

**Result:** Only sites with `traefik:enabled=true` get routers. Perfect for controlled rollouts or security-sensitive environments.

---

### Example 4: Tag-Only Production Setup

**Traefik Config (minimal):**
```yaml
providers:
  plugin:
    forge:
      apiToken: "..."
      organization: "..."
      defaultCertResolver: "letsencrypt"
      serverMappings: []

certificatesResolvers:
  letsencrypt:
    acme:
      email: ops@company.com
      storage: /acme.json
      httpChallenge:
        entryPoint: web
  letsencrypt-staging:
    acme:
      email: ops@company.com
      storage: /acme-staging.json
      caServer: https://acme-staging-v02.api.letsencrypt.org/directory
      httpChallenge:
        entryPoint: web
```

**Production Servers:**
```
traefik:upstream-host=10.0.1.10
traefik:upstream-port=80
traefik:traefik-id=lb01-prod
```

**Staging Servers:**
```
traefik:lb-host=10.0.2.10
traefik:upstream-port=80
traefik:traefik-id=lb01-staging
```

**Staging Sites:**
```
traefik:cert-resolver=letsencrypt-staging
```

---

## Tag Reference

### Server Tags

| Tag | Values | Default | Description |
|-----|--------|---------|-------------|
| `traefik:upstream-host` | IP address | auto-detect | Upstream backend IP |
| `traefik:upstream-port` | Port number | `80` | Upstream backend port |
| `traefik:traefik-id` | String | - | Traefik instance ID (informational) |

**Note:** Servers are automatically enabled/disabled based on whether they have any enabled sites.

**Backward Compatible Aliases:**
- `traefik:lb-host`, `traefik:loadbalancer-host` → `traefik:upstream-host`
- `traefik:lb-port`, `traefik:loadbalancer-port` → `traefik:upstream-port`

### Site Tags

| Tag | Values | Default | Description |
|-----|--------|---------|-------------|
| `traefik:enabled` | `true`, `false` | from config (`defaultSitesEnabled`) | Enable/disable site |
| `traefik:port` | Port number | server port | Override backend port for this site |
| `traefik:cert-resolver` | Resolver name | from config | Certificate resolver to use |
| `traefik:tls` | `true`, `false` | from config | Enable/disable TLS |
| `traefik:http-redirect` | `true`, `false` | from config (`httpRedirect`) | Enable/disable HTTP->HTTPS redirect |
| `traefik:entrypoints` | Comma-separated | auto | Custom entry points (e.g., `websecure` or `web,websecure`) |

**Aliases:**
- `traefik:certresolver` = `traefik:cert-resolver`

---

## Configuration Priority

The plugin uses this priority order (highest to lowest):

### For Servers:
1. **Server Tags** (highest priority)
2. **Config File `serverMappings`**
3. **Auto-Detection** from Forge API

### For Sites:
1. **Site Tags** (highest priority)
2. **Config File `defaultCertResolver`**
3. **No TLS** (if nothing configured)

---

## How to Add Tags in Forge

### Via Web UI:
1. Log into Laravel Forge
2. Navigate to your server or site
3. Find the "Tags" section
4. Add tags in the format: `traefik:key=value`
5. Save

### Via API:
```bash
# Add tag to server
curl -X POST https://forge.laravel.com/api/orgs/{org}/servers/{server}/tags \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "traefik:upstream-host=10.0.1.10"}'
```

---

## Migration from Config File

### Before (Config File):
```yaml
serverMappings:
  - forgeServerName: "app01"
    loadBalancerHost: "10.0.1.10"
    loadBalancerPort: 80
```

### After (Tags Only):
1. Add to app01 in Forge:
   ```
   traefik:upstream-host=10.0.1.10
   traefik:upstream-port=80
   ```

2. Update Traefik config:
   ```yaml
   serverMappings: []  # Empty!
   ```

3. Restart Traefik (one time only)

4. Done! All future changes via tags, no restarts needed.

---

## Troubleshooting

### Server Not Being Processed

**Check:**
1. Does the server have any `traefik:*` tags?
2. Is `traefik:enabled=false` set?
3. Check Traefik logs: should see "No configuration (tags or mapping) found"

**Solution:** Add at minimum:
```
traefik:enabled=true
```

### Site Not Getting TLS

**Check:**
1. Is `defaultCertResolver` set in config?
2. Does the site have `traefik:tls=false` tag?
3. Is the certificate resolver defined in `certificatesResolvers`?

**Solution:** Remove `traefik:tls=false` or add:
```
traefik:cert-resolver=letsencrypt
```

### Wrong IP Being Used

**Check logs:** Should show "Auto-detected" or "via tags"

**Solution:** Explicitly set IP:
```
traefik:upstream-host=10.0.1.10
```

---

## Best Practices

1. **Use Auto-Detection When Possible**
   - Let the plugin use Forge's `private_ip_address`
   - Only set `traefik:lb-host` when you need to override

2. **Use Prefixes for Organization**
   - Production: `traefik:traefik-id=lb01-prod`
   - Staging: `traefik:traefik-id=lb01-staging`

3. **Document in Traefik Config**
   - Add comments about which resolvers are available
   - Document your tagging convention

4. **Test with Staging Certificates**
   - Use `traefik:cert-resolver=letsencrypt-staging` for new sites
   - Switch to production once verified

5. **Use Consistent Naming**
   - Stick to either `traefik:lb-host` or `traefik:loadbalancer-host`
   - Don't mix aliases

---

## Advanced: Hybrid Configuration

You can mix tags and config file mappings:

```yaml
serverMappings:
  # Legacy servers still in config
  - forgeServerName: "old-app01"
    loadBalancerHost: "10.0.0.50"

  # New servers use tags
  # (just add tags in Forge, no config needed)
```

**Priority:** Tags override config file mappings for the same server.

---

## Summary

**Initial Setup:** 5 minutes
```yaml
providers:
  plugin:
    forge:
      apiToken: "..."
      organization: "..."
      serverMappings: []
```

**Per Server:** Add 2 tags
```
traefik:enabled=true
traefik:upstream-port=80
```

**Per Site (optional):** Override as needed
```
traefik:cert-resolver=letsencrypt-staging
traefik:tls=false
```

**Result:** Complete control via Forge tags, zero config file changes! 🎉
