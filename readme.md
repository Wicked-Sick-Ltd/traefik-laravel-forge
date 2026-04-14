# Traefik Laravel Forge Provider Plugin

[![Build Status](https://github.com/wickedsick/traefik-laravel-forge/workflows/Main/badge.svg?branch=master)](https://github.com/wickedsick/traefik-laravel-forge/actions)

A Traefik provider plugin that automatically creates HTTP routers based on sites managed in Laravel Forge. **Configure everything via Forge tags** - no config file changes needed after initial setup!

## Features

- **🏷️ 100% Tag-Based Configuration**: Configure everything via Forge tags after initial setup
- **🔄 Automatic Site Discovery**: Discovers sites from Laravel Forge via API v2
- **🎯 Smart Server Mapping**: Configure servers via tags or config file
- **🔍 Auto-IP Detection**: Automatically uses private IPs from Forge
- **🔒 TLS/HTTPS Support**: Automatic certificate management with Let's Encrypt
- **⚡ Real-time Updates**: Polls Forge API at configurable intervals (default: 30s)
- **🛡️ Security First**: Only creates routes for sites with "installed" status
- **📝 Flexible Configuration**: Use tags, config file, or both (tags take priority)

## Use Case

This plugin is ideal when you have:
- Multiple application servers (e.g., app01, app02, app03) managed by Forge
- One or more Traefik load balancers (e.g., lb01, lb02) in front of them
- A need to automatically route traffic based on domain names to the appropriate backend servers
- Want to manage configuration through Forge UI without editing config files

## Quick Start: Tag-Based Setup

After initial Traefik configuration, manage everything via tags:

**1. (Optional) Add server tags only if you need to override IP/port:**
```
traefik:lb-host=10.0.1.10
traefik:lb-port=8080
```
Otherwise, the plugin auto-detects the private IP and uses port 80.

**2. Control which sites are exposed via site tags:**
```
traefik:enabled=true              # Enable a site
traefik:cert-resolver=letsencrypt # Override cert resolver
traefik:tls=false                 # Disable TLS
```

**3. Done!** Servers are automatically enabled if they have enabled sites. Changes apply on next poll (30s default), no config file edits or restarts needed.

See [TAG_CONFIGURATION.md](TAG_CONFIGURATION.md) for the complete guide.

## Installation

### Local Mode (Development/Testing)

1. Clone this repository to your local `plugins-local` directory:

```bash
mkdir -p ./plugins-local/src/github.com/wickedsick
cd ./plugins-local/src/github.com/wickedsick
git clone <your-repo-url> traefik-laravel-forge
```

2. Configure Traefik to use the local plugin:

```yaml
# traefik.yml (static configuration)
entryPoints:
  web:
    address: :80
  websecure:
    address: :443

log:
  level: DEBUG

experimental:
  localPlugins:
    forge:
      moduleName: github.com/wickedsick/traefik-laravel-forge

providers:
  plugin:
    forge:
      apiToken: "your-forge-api-token"
      organization: "your-org-slug"
      pollInterval: "30s"
      serverMappings:
        - forgeServerName: "app01"
          upstreamHost: "10.0.1.10:80"
          traefik: "lb01"
        - forgeServerName: "app02"
          upstreamHost: "10.0.1.11:80"
          traefik: "lb01"
```

### Production Mode (GitHub)

Once published to GitHub with the `traefik-plugin` topic:

```yaml
# traefik.yml (static configuration)
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
      serverMappings:
        - forgeServerName: "app01"
          upstreamHost: "10.0.1.10:80"
          traefik: "lb01"
```

## Configuration

### Required Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `apiToken` | string | Your Laravel Forge API token (get it from forge.laravel.com/user/profile#/api) |
| `organization` | string | Your Forge organization slug (found in URL: forge.laravel.com/orgs/{organization}) |
| `defaultCertResolver` | string | Default certificate resolver for automatic TLS (optional) |
| `defaultSitesEnabled` | bool | Whether sites are enabled by default (default: `true`). Set to `false` for opt-in mode. |
| `httpRedirect` | bool | Create HTTP->HTTPS redirect routers (default: `false`) |
| `redirectMiddleware` | string | Name of middleware to use for HTTP redirects (e.g., `"https-redirect"`) |
| `serverMappings` | array | List of server mappings (see below) |

### Optional Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `pollInterval` | string | "30s" | How often to poll the Forge API for changes (minimum: 10s) |

### Server Mappings

Each server mapping defines how a Forge server should be exposed:

| Field | Type | Description |
|-------|------|-------------|
| `forgeServerName` | string | The name of the server in Laravel Forge (e.g., "app01") |
| `upstreamHost` | string | (Optional) The upstream IP where the server can be reached. If omitted, auto-detected from Forge API |
| `upstreamPort` | int | (Optional) The upstream port to use (default: 80) |
| `traefik` | string | Identifier for which Traefik instance should handle this (informational) |

## How It Works

1. **Polling**: The plugin polls the Forge API at the specified interval
2. **Server Discovery**: It fetches all servers from your Forge account
3. **Mapping**: For each server, it checks if there's a corresponding `serverMapping`
4. **Site Discovery**: For mapped servers, it fetches all sites
5. **Route Creation**: For each site with status "installed", it creates:
   - An HTTP router with a Host rule matching the site's domain
   - A service pointing to the `upstreamHost` from the mapping
   - PassHostHeader is enabled so the backend receives the original Host header

### Example Flow

Given this configuration:
```yaml
serverMappings:
  - forgeServerName: "app01"
    upstreamHost: "10.0.1.10:80"
    traefik: "lb01"
```

If Forge server "app01" has sites:
- example.com (status: installed)
- test.com (status: installing)

The plugin will create:
- Router for `example.com` → `http://10.0.1.10:80` (with Host header preserved)
- No router for `test.com` (status not "installed")

## Security Considerations

1. **API Token**: Store your Forge API token securely. Consider using environment variables or secrets management.
2. **Unmapped Servers**: Servers without explicit mappings are ignored, preventing unintended exposure.
3. **Network Access**: Ensure your Traefik instance can reach the internal IPs specified in `upstreamHost`.
4. **HTTPS**: This plugin creates HTTP routers. Use Traefik's built-in TLS features for HTTPS termination.

## Advanced Features

### Auto-Detection of Server IPs

The plugin can automatically detect server IP addresses from Forge, preferring private IPs for internal networks:

```yaml
serverMappings:
  # Automatically uses private_ip_address from Forge
  - forgeServerName: "app01"
    upstreamPort: 80
```

See [FEATURES.md](FEATURES.md#auto-detection-of-server-ips) for details.

### TLS Certificate Management

Configure automatic HTTPS with Let's Encrypt:

```yaml
providers:
  plugin:
    forge:
      defaultCertResolver: "letsencrypt"

certificatesResolvers:
  letsencrypt:
    acme:
      email: your-email@example.com
      storage: /acme.json
      httpChallenge:
        entryPoint: web
```

All sites will automatically get TLS enabled with the specified resolver.

### Per-Site Configuration via Tags

Configure individual sites using tags in Forge (no config file changes needed):

| Tag | Effect |
|-----|--------|
| `traefik:enabled=true/false` | Enable/disable site |
| `traefik:port=8080` | Override backend port |
| `traefik:cert-resolver=letsencrypt` | Use specific cert resolver |
| `traefik:tls=true/false` | Enable/disable TLS |
| `traefik:http-redirect=true/false` | Enable/disable HTTP->HTTPS redirect |
| `traefik:entrypoints=websecure` | Custom entry points |

**Example**: Add `traefik:cert-resolver=letsencrypt-staging` tag to a site in Forge to use staging certificates for testing.

See [FEATURES.md](FEATURES.md) for comprehensive documentation on all advanced features.

## Logging

The plugin logs to stdout and stderr:
- Server and site discovery information
- IP auto-detection results
- TLS configuration decisions
- Tag parsing results
- Mapping decisions
- Errors from the Forge API

Enable Traefik's DEBUG log level to see detailed plugin output:
```yaml
log:
  level: DEBUG
```

## Troubleshooting

### No routes are being created

1. Check that your API token is valid
2. Verify server names match exactly (case-sensitive)
3. Ensure sites have status "installed"
4. Check Traefik logs for API errors

### Routes created but traffic not flowing

1. Verify the `upstreamHost` IPs are reachable from Traefik
2. Check that the backend servers are listening on the specified ports
3. Ensure DNS is resolving the domain names to your Traefik instance

### Poll interval too short

**Error:** "poll interval must be at least 10s"

**Solution:** Set `pollInterval` to at least "10s". The minimum is enforced to:
- Avoid overwhelming the Forge API
- Prevent rate limiting issues
- Reduce unnecessary load on both systems

Recommended values:
- Development: "10s" (minimum)
- Production: "30s" to "60s"

### API rate limiting

If you're polling very frequently with many servers/sites, you may hit Forge's rate limits. Increase `pollInterval` if this occurs.

## Development

### Building

```bash
make vendor
```

### Testing

```bash
make test
```

### Linting

```bash
make lint
```

## Publishing to Traefik Plugin Catalog

To publish this plugin to the official Traefik Plugin Catalog:

1. Ensure the repository has the `traefik-plugin` topic
2. Ensure `.traefik.yml` is present and valid
3. Tag a release: `git tag v1.0.0 && git push --tags`
4. Wait for the Plugin Catalog to discover your plugin (runs daily)

## License

See LICENSE file.

## Contributing

Contributions welcome! Please open an issue or pull request.

## API Reference

This plugin uses the Laravel Forge API v2:
- Base URL: `https://forge.laravel.com/api`
- Authentication: Bearer token (OAuth2)
- Format: JSON:API specification
- Endpoints used:
  - `GET /orgs/{organization}/servers` - List all servers in organization
  - `GET /orgs/{organization}/servers/{serverId}/sites` - List sites on a server

The v2 API uses the JSON:API specification for structured, consistent responses with support for pagination, filtering, sorting, and including related resources.
