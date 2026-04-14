# Project Overview

## What Is This?

A Traefik provider plugin that integrates with Laravel Forge to automatically create HTTP routers based on your Forge-managed sites. Supports complete tag-based configuration for zero-touch operation after initial setup.

## Repository Structure

### Core Files

| File | Purpose |
|------|---------|
| `forge.go` | Main plugin implementation with Forge API v2 integration |
| `forge_test.go` | Comprehensive test suite (8 test cases, 12.9% coverage) |
| `go.mod` | Go module definition |
| `go.sum` | Go dependency checksums |

### Configuration Files

| File | Purpose |
|------|---------|
| `.traefik.yml` | Plugin catalog manifest (required for Traefik Plugin Catalog) |
| `traefik.example.yml` | Complete example Traefik configuration |

### Documentation

| File | Purpose | Audience |
|------|---------|----------|
| `README.md` | Main documentation, quick start, API reference | Everyone |
| `TAG_CONFIGURATION.md` | Complete guide to tag-based configuration | Users |
| `FEATURES.md` | Advanced features deep dive | Advanced users |
| `IMPLEMENTATION.md` | Technical implementation details | Developers |
| `V2_MIGRATION.md` | API v1 to v2 migration notes | Developers |
| `CHANGELOG.md` | Version history and changes | Everyone |
| `TAG_BASED_SUMMARY.md` | Tag system overview | Users |
| `PROJECT_OVERVIEW.md` | This file | Everyone |

### Build & CI

| File/Directory | Purpose |
|----------------|---------|
| `Makefile` | Build tasks (lint, test, vendor) |
| `.github/workflows/` | GitHub Actions CI/CD workflows |
| `.golangci.yml` | Linting configuration |
| `vendor/` | Vendored dependencies (required for Traefik plugins) |

### Assets

| File/Directory | Purpose |
|----------------|---------|
| `.assets/` | Plugin icons and images for catalog |
| `LICENSE` | MIT License |

## Quick Reference

### For Users

**Start here:**
1. [README.md](README.md) - Installation and quick start
2. [TAG_CONFIGURATION.md](TAG_CONFIGURATION.md) - Complete tag guide

**Key features:**
- Tag-based configuration (no config file edits needed)
- Auto-IP detection from Forge
- TLS/Let's Encrypt support
- Opt-in mode for controlled exposure

### For Developers

**Start here:**
1. [forge.go](forge.go) - Main implementation
2. [IMPLEMENTATION.md](IMPLEMENTATION.md) - Technical details

**Key patterns:**
- Forge API v2 with JSON:API format
- Tag parsing: `traefik:key=value`
- Priority: Tags > Config > Auto-detect
- Poll-based updates (min: 10s)

## Configuration Summary

### Minimal Config (Tag-Based)

```yaml
providers:
  plugin:
    forge:
      apiToken: "your-token"
      organization: "your-org"
      serverMappings: []  # Empty!
```

Then add tags in Forge:
- **Servers**: `traefik:enabled=true` (or specify `traefik:lb-host`)
- **Sites**: No tags needed (auto-enabled) or `traefik:enabled=true` for opt-in

### Full Config (All Options)

```yaml
providers:
  plugin:
    forge:
      apiToken: "your-token"
      organization: "your-org"
      pollInterval: "30s"                # Min: 10s
      defaultCertResolver: "letsencrypt" # Optional
      defaultSitesEnabled: true          # Optional: false for opt-in
      serverMappings:                    # Optional: can be empty
        - forgeServerName: "app01"
          loadBalancerHost: "10.0.1.10"  # Optional: auto-detected
          loadBalancerPort: 80            # Optional: default 80
```

## Tag Reference Card

### Server Tags (Optional)
```
traefik:lb-host=IP          Override IP (auto-detects if omitted)
traefik:lb-port=PORT        Override port (default: 80)
traefik:traefik-id=NAME     Informational label
```

### Site Tags
```
traefik:enabled=true/false              Enable/disable site
traefik:cert-resolver=NAME              Override cert resolver
traefik:tls=true/false                  Enable/disable TLS
```

## Development

### Running Tests

```bash
make test
# or
go test -v -cover ./...
```

### Building

```bash
make vendor
# or
go mod vendor
```

### Linting

```bash
make lint
# or
golangci-lint run
```

### Local Testing with Traefik

```bash
# Set up local plugin directory
mkdir -p ./plugins-local/src/github.com/wickedsick
cp -r . ./plugins-local/src/github.com/wickedsick/traefik-laravel-forge

# Create traefik.yml with localPlugins config
# Start Traefik
traefik --configFile=traefik.yml
```

## Publishing

### To GitHub

```bash
git add .
git commit -m "Release v1.0.0"
git tag v1.0.0
git push origin master --tags
```

### To Traefik Plugin Catalog

1. Add `traefik-plugin` topic to GitHub repo
2. Ensure `.traefik.yml` is valid
3. Tag a release (as above)
4. Wait for daily catalog scan

## Key Design Decisions

### 1. Tag-Based Over Config File
**Why:** Allows dynamic updates without Traefik restarts, configuration visible in Forge UI

### 2. Smart Server Auto-Enable
**Why:** Servers are backends - only relevant if they have enabled sites. Eliminates redundant configuration.

### 3. Priority System (Tags > Config > Auto)
**Why:** Maximum flexibility - migrate from config to tags gradually, override when needed

### 4. Minimum 10s Poll Interval
**Why:** Prevents API abuse, rate limiting, and unnecessary load

### 5. Opt-In Mode Support
**Why:** Security-sensitive environments need explicit control over exposure

## API Compatibility

- **Current:** Laravel Forge API v2 (JSON:API format)
- **Base URL:** `https://forge.laravel.com/api`
- **Endpoints:**
  - `GET /orgs/{org}/servers?include=tags`
  - `GET /orgs/{org}/servers/{id}/sites?include=tags`

## Known Limitations

1. **Organization Required**: API v2 requires organization slug
2. **HTTP Only**: Creates HTTP routers (use Traefik's TLS for HTTPS termination)
3. **No Site Aliases**: Only primary site name used in Host rule
4. **Single Load Balancer per Config**: Multiple Traefik instances need separate configs
5. **Installed Sites Only**: Only sites with status "installed" get routers

## Future Enhancements

- Support for site aliases in Host rules
- Additional tag-based features (middleware, priority, rate limiting)
- Health checks for backend servers
- TCP/UDP router support
- Filtering by site tags for advanced routing

## Support

- **Issues**: https://github.com/wickedsick/traefik-laravel-forge/issues
- **Forge API Docs**: https://forge.laravel.com/docs/api-reference
- **Traefik Plugin Docs**: https://plugins.traefik.io

## License

MIT License - See [LICENSE](LICENSE) file for details.
