# Implementation Summary

## Overview

This Traefik provider plugin integrates with Laravel Forge to automatically create and manage HTTP routers. It supports **complete tag-based configuration**, allowing you to manage your entire routing setup through Forge tags without touching configuration files.

## Latest Updates

### Tag-Based Configuration (v1.0)
- **Complete tag support** for both servers and sites
- **Server tags**: `traefik:upstream-host`, `traefik:upstream-port`, `traefik:traefik-id`
- **Site tags**: `traefik:enabled`, `traefik:cert-resolver`, `traefik:tls`
- **Smart server auto-enable**: Servers enabled automatically if they have enabled sites
- **Opt-in mode**: `defaultSitesEnabled: false` for controlled exposure
- **Poll interval validation**: Minimum 10s enforced

## API Version

This plugin uses the **Laravel Forge API v2**, which follows the JSON:API specification. The v2 API is the current and recommended version.

- **Base URL**: `https://forge.laravel.com/api`
- **Format**: JSON:API
- **Authentication**: OAuth2 Bearer token

## Key Features Implemented

### 1. Server Mapping System

The plugin uses a flexible mapping system where you explicitly define which Forge servers should be exposed and where they're located:

```yaml
serverMappings:
  - forgeServerName: "app01"      # Name in Forge
    upstreamHost: "10.0.1.10:80"  # Internal IP where the server can be reached
    traefik: "lb01"                    # Which LB handles this (informational)
```

This design:
- Provides explicit control over which servers are exposed
- Allows multiple app servers behind the same load balancer
- Supports private network routing via internal IPs
- Prevents accidental exposure of unmapped servers

### 2. Forge API Integration

The plugin integrates with the Laravel Forge API v2:
- Uses JSON:API format for structured responses
- Requires organization slug for API access
- Fetches all servers from your Forge organization
- For each mapped server, fetches all sites
- Only creates routes for sites with status "installed"
- Polls at configurable intervals (default 30s)
- Server and site IDs are strings in v2 (changed from integers in v1)

### 3. Automatic Router Creation

For each site on a mapped server:
- Creates an HTTP router with Host rule matching the site's domain
- Creates a service pointing to the mapped `upstreamHost`
- Enables `PassHostHeader` so backends receive the original Host header
- Names routers systematically: `forge-{serverName}-{siteId}`

### 4. Configuration Structure

```go
type Config struct {
    APIToken       string          // Your Forge API token
    Organization   string          // Your Forge organization slug
    PollInterval   string          // How often to poll Forge
    ServerMappings []ServerMapping // Server to LB mappings
}

type ServerMapping struct {
    ForgeServerName   string // Server name in Forge
    LoadBalancerHost  string // Internal IP:port
    Traefik           string // LB identifier
}
```

## Files Modified/Created

### Core Implementation
- `forge.go` → Complete Forge integration implementation
  - Forge API v2 client methods
  - Server and site tag parsing
  - Dynamic configuration generation
  - Auto-IP detection logic

### Configuration
- `go.mod` → Updated module name to `github.com/wickedsick/traefik-laravel-forge`
- `.traefik.yml` → Plugin catalog manifest
- `traefik.example.yml` → Example Traefik configuration

### Tests
- `forge_test.go` → Comprehensive test suite with 8 test cases

### Documentation
- `README.md` → Comprehensive documentation
- `IMPLEMENTATION.md` → This file

## How It Works

1. **Initialization**: Plugin starts with configured API token and server mappings
2. **Polling Loop**: Every `pollInterval`, the plugin:
   - Fetches all servers from Forge
   - Iterates through each server
   - Checks if server has a mapping (if not, skips)
   - Fetches all sites for mapped servers
   - Creates routers for sites with status "installed"
3. **Configuration Push**: New configuration is pushed to Traefik via the `cfgChan`
4. **Traefik Application**: Traefik receives the configuration and updates its routing rules

## Example Scenario

### Infrastructure Setup
- Load Balancer: `lb01` (10.0.0.5) - runs Traefik
- App Server 1: `app01` (10.0.1.10) - managed by Forge
- App Server 2: `app02` (10.0.1.11) - managed by Forge

### Forge Sites
On `app01`:
- example.com (installed)
- test.com (installed)
- staging.com (installing) ← Will be skipped

On `app02`:
- another.com (installed)

### Configuration
```yaml
providers:
  plugin:
    forge:
      apiToken: "your-token"
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

### Result
Traefik will create routers:
- `example.com` → `http://10.0.1.10:80`
- `test.com` → `http://10.0.1.10:80`
- `another.com` → `http://10.0.1.11:80`
- `staging.com` ← NOT created (status: installing)

All requests to these domains hitting `lb01` will be routed to the appropriate backend server with the Host header preserved.

## Security Considerations

1. **Explicit Mapping**: Only servers with explicit mappings are exposed
2. **Status Check**: Only "installed" sites get routers
3. **API Token**: Securely store the Forge API token
4. **Internal Networks**: Use private IPs for `upstreamHost` when possible
5. **HTTPS**: This plugin handles HTTP routing; use Traefik's TLS features for HTTPS termination

## Next Steps

To use this plugin:

1. **Development Testing**:
   ```bash
   # Set up local plugin directory
   mkdir -p ./plugins-local/src/github.com/wickedsick
   cp -r /path/to/traefik-laravel-forge ./plugins-local/src/github.com/wickedsick/

   # Create traefik.yml with localPlugins config
   # Start Traefik
   traefik --configFile=traefik.yml
   ```

2. **Production Deployment**:
   ```bash
   # Commit changes
   git add .
   git commit -m "Implement Laravel Forge provider"

   # Tag a release
   git tag v1.0.0
   git push origin master --tags

   # Add 'traefik-plugin' topic to GitHub repo
   # Wait for Plugin Catalog to discover (daily scan)
   ```

3. **Configuration**:
   - Get your Forge API token from https://forge.laravel.com/user/profile#/api
   - Configure server mappings for your infrastructure
   - Set appropriate `pollInterval` based on your change frequency

## Known Limitations

1. **Single Load Balancer**: Currently, the `traefik` field in mappings is informational only. If you have multiple Traefik instances, you'll need to configure each separately with its own mappings.

2. **Organization Required**: The v2 API requires an organization slug. Personal accounts may need to be accessed through a default organization.

3. **HTTP Only**: The plugin creates HTTP routers. For HTTPS, configure Traefik's TLS options separately.

4. **No Site Aliases**: The plugin only uses the primary site name for the Host rule. Site aliases from Forge are not currently included.

## Future Enhancements

Potential improvements:
- Include site aliases in Host rules
- Support for TCP/UDP routers
- Health checks for backend servers
- Automatic SSL certificate detection from Forge
- Filtering by site tags
- Support for deployment status changes
