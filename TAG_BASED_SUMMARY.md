# Tag-Based Configuration Summary

## What We Built

A **complete tag-based configuration system** for the Laravel Forge Traefik provider plugin. After initial setup, you can manage your entire Traefik routing configuration through Forge tags - zero config file changes needed!

## How It Works

### Priority System

The plugin uses this priority order (highest to lowest):

**For Servers:**
1. **Server Tags** in Forge (e.g., `traefik:lb-host=10.0.1.10`)
2. **Config File** `serverMappings`
3. **Auto-Detection** from Forge API

**For Sites:**
1. **Site Tags** in Forge (e.g., `traefik:cert-resolver=letsencrypt`)
2. **Config File** `defaultCertResolver`
3. **No TLS** (if nothing configured)

### Configuration Flow

```
┌─────────────────┐
│  Forge Server   │
│   Tags Added    │
└────────┬────────┘
         │
         v
┌─────────────────┐
│   Forge API     │
│  ?include=tags  │
└────────┬────────┘
         │
         v
┌─────────────────┐
│ Traefik Plugin  │
│  Parses Tags    │
└────────┬────────┘
         │
         v
┌─────────────────┐
│    Traefik      │
│   Updates       │
│   Routers       │
└─────────────────┘
```

## Tag Formats

### Server Tags

Configure which servers Traefik manages and how to reach them:

| Tag | Example | Effect |
|-----|---------|--------|
| `traefik:enabled=true` | Enable server | Auto-detect IP |
| `traefik:lb-host=10.0.1.10` | Set IP explicitly | Use this IP |
| `traefik:lb-port=8080` | Set custom port | Use port 8080 |
| `traefik:enabled=false` | Disable server | Skip entirely |

### Site Tags

Override TLS/certificate settings per site:

| Tag | Example | Effect |
|-----|---------|--------|
| `traefik:cert-resolver=letsencrypt` | Use specific resolver | Override default |
| `traefik:tls=true` | Force TLS | Enable HTTPS |
| `traefik:tls=false` | Disable TLS | HTTP only |

## Real-World Example

### Initial Traefik Config (One Time)

```yaml
providers:
  plugin:
    forge:
      apiToken: "your-token"
      organization: "your-org"
      defaultCertResolver: "letsencrypt"
      serverMappings: []  # Empty! Everything via tags

certificatesResolvers:
  letsencrypt:
    acme:
      email: ops@company.com
      storage: /acme.json
```

### Forge Configuration (Via Tags)

**Server: app01**
```
traefik:lb-host=10.0.1.10
traefik:lb-port=80
```

**Server: app02**
```
traefik:enabled=true
```
(Auto-detects private IP)

**Site: example.com (on app01)**
- No tags needed (uses defaults)

**Site: staging.example.com (on app01)**
```
traefik:cert-resolver=letsencrypt-staging
```

**Site: dev.test (on app02)**
```
traefik:tls=false
```

### Result

| Site | Server | Backend | TLS |
|------|--------|---------|-----|
| example.com | app01 | 10.0.1.10:80 | Production Let's Encrypt |
| staging.example.com | app01 | 10.0.1.10:80 | Staging Let's Encrypt |
| dev.test | app02 | (auto):80 | HTTP only |

## Benefits

### For Operators

- **No Config File Edits**: Manage everything in Forge UI
- **No Traefik Restarts**: Changes apply on next poll
- **Immediate Rollback**: Remove a tag to revert
- **Self-Documenting**: Configuration visible in Forge

### For Teams

- **Separation of Concerns**: Developers can add sites, ops control routing via tags
- **Audit Trail**: Forge tracks who changed what
- **No Git Commits**: No need to commit config changes
- **Test Before Prod**: Use staging cert resolver for new sites

### For Infrastructure

- **Dynamic Scaling**: Add new servers with just tags
- **Multi-Environment**: Use different tags for dev/staging/prod
- **Hybrid Mode**: Mix tags and config file as needed
- **Gradual Migration**: Migrate from config to tags over time

## Implementation Details

### Code Changes

1. **Added Server Tag Support**
   - `ForgeServerAttributes.Tags` field
   - `?include=tags` in server API calls
   - `parseServerTags()` function

2. **Enhanced Configuration Priority**
   - Tags checked first
   - Config file as fallback
   - Auto-detection last resort

3. **Extended Tag Parsing**
   - Server tags: `lb-host`, `lb-port`, `enabled`, `traefik-id`
   - Site tags: `cert-resolver`, `tls`
   - Alias support: `loadbalancer-host`, `certresolver`

4. **Improved Logging**
   - Shows configuration source (tags/config/auto)
   - Logs tag parsing results
   - Clearer error messages

### API Calls

**Servers:**
```
GET /api/orgs/{org}/servers?include=tags
```

**Sites:**
```
GET /api/orgs/{org}/servers/{id}/sites?include=tags
```

Both endpoints now include tags in the response via JSON:API `included` relationship.

## Migration Path

### From Config File to Tags

**Before:**
```yaml
serverMappings:
  - forgeServerName: "app01"
    loadBalancerHost: "10.0.1.10"
    loadBalancerPort: 80
```

**Migration Steps:**

1. Add tags to server in Forge:
   ```
   traefik:lb-host=10.0.1.10
   traefik:lb-port=80
   ```

2. Test (tags override config, so it still works)

3. Remove from config file:
   ```yaml
   serverMappings: []
   ```

4. Restart Traefik (one time)

5. All future changes via tags!

## Documentation

### New Files

- **[TAG_CONFIGURATION.md](TAG_CONFIGURATION.md)** - Complete tag-based guide
  - Examples for every scenario
  - Best practices
  - Troubleshooting
  - Migration guide

### Updated Files

- **[README.md](README.md)** - Quick start with tags
- **[FEATURES.md](FEATURES.md)** - Technical details
- **[traefik.example.yml](traefik.example.yml)** - Shows `serverMappings: []`

## Testing

All tests passing:
- ✅ Tag parsing tests
- ✅ Configuration priority tests
- ✅ Build successful
- ✅ 6 test cases, 0 failures

## Future Enhancements

Potential additions:
- `traefik:middleware=auth` - Attach middlewares
- `traefik:priority=100` - Router priority
- `traefik:entrypoints=web,websecure` - Custom entry points
- `traefik:rate-limit=100` - Rate limiting

## Summary

**Before:** Mixed config file and manual IP tracking
```yaml
serverMappings:
  - forgeServerName: "app01"
    loadBalancerHost: "10.0.1.10"
  - forgeServerName: "app02"
    loadBalancerHost: "10.0.1.11"
```

**After:** Pure tag-based configuration
```yaml
serverMappings: []  # Empty!
```

In Forge:
- app01: `traefik:lb-host=10.0.1.10`
- app02: `traefik:enabled=true` (auto-detects)

**Result:** Zero config file maintenance, all changes via Forge UI! 🎉
