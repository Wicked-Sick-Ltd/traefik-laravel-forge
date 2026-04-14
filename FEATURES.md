# New Features

This document describes the advanced features added to the Laravel Forge Traefik provider plugin.

## Table of Contents

1. [Auto-Detection of Server IPs](#auto-detection-of-server-ips)
2. [TLS Certificate Resolvers](#tls-certificate-resolvers)
3. [Site Tags for Per-Site Configuration](#site-tags-for-per-site-configuration)

---

## Auto-Detection of Server IPs

### Overview

The plugin can automatically detect server IP addresses from the Forge API, eliminating the need to manually specify `loadBalancerHost` in your configuration.

### How It Works

When `loadBalancerHost` is omitted from a server mapping, the plugin will:

1. **First**: Try to use the `private_ip_address` from Forge (recommended for internal networks)
2. **Fallback**: Use the public `ip_address` if no private IP is available
3. **Skip**: If no IP address is found, the server is skipped with a warning

### Configuration

```yaml
providers:
  plugin:
    forge:
      serverMappings:
        # Manual configuration (explicit IP)
        - forgeServerName: "app01"
          loadBalancerHost: "10.0.1.10"
          loadBalancerPort: 80

        # Auto-detect (uses Forge API)
        - forgeServerName: "app02"
          loadBalancerPort: 80  # Port still configurable, defaults to 80
```

### Benefits

- **Less Configuration**: Don't need to duplicate IP addresses
- **Automatic Updates**: If a server's IP changes in Forge, it's automatically picked up
- **Private Network Preference**: Automatically uses private IPs when available for better security

### Port Configuration

The `loadBalancerPort` is optional and defaults to `80`:

```yaml
serverMappings:
  # Uses port 80 (default)
  - forgeServerName: "app01"

  # Uses port 8080
  - forgeServerName: "app02"
    loadBalancerPort: 8080
```

---

## TLS Certificate Resolvers

### Overview

Configure TLS/HTTPS certificates automatically for your sites using Let's Encrypt or other ACME providers.

### Global Configuration

Set a default certificate resolver that applies to all sites:

```yaml
providers:
  plugin:
    forge:
      defaultCertResolver: "letsencrypt"

certificatesResolvers:
  letsencrypt:
    acme:
      email: your-email@example.com
      storage: /path/to/acme.json
      httpChallenge:
        entryPoint: web
```

When `defaultCertResolver` is set:
- All sites automatically get TLS enabled
- Certificates are automatically requested and renewed
- HTTP to HTTPS redirection is handled by Traefik

### Multiple Resolvers

You can define multiple resolvers for different use cases:

```yaml
certificatesResolvers:
  # Production Let's Encrypt
  letsencrypt:
    acme:
      email: your-email@example.com
      storage: /acme.json
      httpChallenge:
        entryPoint: web

  # Staging for testing
  letsencrypt-staging:
    acme:
      email: your-email@example.com
      storage: /acme-staging.json
      caServer: https://acme-staging-v02.api.letsencrypt.org/directory
      httpChallenge:
        entryPoint: web
```

### Per-Site Overrides

Override the default resolver for specific sites using tags (see below).

---

## Site Tags for Per-Site Configuration

### Overview

Use tags in Laravel Forge to configure Traefik behavior on a per-site basis. This allows you to override global settings without modifying the Traefik configuration file.

### Tag Format

Tags must use the format: `traefik:key=value` or `traefik:flag`

### Supported Tags

| Tag | Description | Example |
|-----|-------------|---------|
| `traefik:cert-resolver=<name>` | Use specific certificate resolver | `traefik:cert-resolver=letsencrypt-staging` |
| `traefik:tls=true` | Enable TLS for this site | `traefik:tls=true` |
| `traefik:tls=false` | Disable TLS for this site | `traefik:tls=false` |

### How to Add Tags in Forge

1. Go to your site in Laravel Forge
2. Navigate to the site settings
3. Add tags to the site
4. Tags are picked up on the next poll interval

### Examples

#### Example 1: Use Staging Resolver for Testing

**Scenario**: You want to test certificate generation without hitting Let's Encrypt rate limits.

**Configuration**:
```yaml
providers:
  plugin:
    forge:
      defaultCertResolver: "letsencrypt"
```

**Forge Tag** (on test site):
```
traefik:cert-resolver=letsencrypt-staging
```

**Result**: The test site uses staging certificates while all other sites use production.

---

#### Example 2: Disable TLS for Development Site

**Scenario**: You have a `.test` domain that doesn't need SSL.

**Configuration**:
```yaml
providers:
  plugin:
    forge:
      defaultCertResolver: "letsencrypt"
```

**Forge Tag** (on dev.test site):
```
traefik:tls=false
```

**Result**: The dev site serves HTTP only, while all other sites use HTTPS.

---

#### Example 3: Enable TLS Without Global Default

**Scenario**: You want TLS disabled by default, but enabled for specific production sites.

**Configuration**:
```yaml
providers:
  plugin:
    forge:
      # No defaultCertResolver set
```

**Forge Tag** (on production sites):
```
traefik:cert-resolver=letsencrypt
```

**Result**: Only tagged sites get TLS; others remain HTTP.

---

### Tag Parsing Details

- **Prefix**: Tags must start with `traefik:` to be recognized
- **Case Sensitive**: Tag names are case-sensitive
- **Key Aliases**: `cert-resolver` and `certresolver` both work
- **Values**: Values can contain `=` characters (e.g., `key=value=with=equals`)
- **Boolean**: Use `true` or `false` for boolean values
- **Flags**: Tags without `=` are treated as `key=true`

### Tag Examples

| Tag | Parsed As | Effect |
|-----|-----------|--------|
| `traefik:cert-resolver=letsencrypt` | `cert-resolver` = `letsencrypt` | Uses letsencrypt resolver |
| `traefik:tls=true` | `tls` = `true` | Enables TLS |
| `traefik:enabled` | `enabled` = `true` | Sets flag to true |
| `production` | (ignored) | Not a traefik tag |
| `traefik:` | (ignored) | No key specified |

---

## Complete Example

### Traefik Configuration

```yaml
providers:
  plugin:
    forge:
      apiToken: "your-token"
      organization: "your-org"
      defaultCertResolver: "letsencrypt"
      serverMappings:
        - forgeServerName: "app01"  # Auto-detects IP
        - forgeServerName: "app02"  # Auto-detects IP

certificatesResolvers:
  letsencrypt:
    acme:
      email: admin@example.com
      storage: /acme.json
      httpChallenge:
        entryPoint: web

  letsencrypt-staging:
    acme:
      email: admin@example.com
      storage: /acme-staging.json
      caServer: https://acme-staging-v02.api.letsencrypt.org/directory
      httpChallenge:
        entryPoint: web
```

### Sites in Forge

| Site | Tags | Result |
|------|------|--------|
| example.com | (none) | HTTPS with letsencrypt (default) |
| staging.example.com | `traefik:cert-resolver=letsencrypt-staging` | HTTPS with staging certs |
| internal.test | `traefik:tls=false` | HTTP only |
| api.example.com | (none) | HTTPS with letsencrypt (default) |

### Plugin Output

```
Auto-detected private IP for server 'app01': 10.0.1.10
Auto-detected private IP for server 'app02': 10.0.1.11
Created router for site 'example.com' -> http://10.0.1.10:80 (TLS with letsencrypt)
Created router for site 'staging.example.com' -> http://10.0.1.10:80 (TLS with letsencrypt-staging)
Created router for site 'internal.test' -> http://10.0.1.11:80 (no TLS)
Created router for site 'api.example.com' -> http://10.0.1.11:80 (TLS with letsencrypt)
```

---

## Troubleshooting

### IP Auto-Detection Not Working

**Problem**: Server is skipped with "No IP address found"

**Solution**:
- Check that the server exists in Forge
- Verify the server name matches exactly (case-sensitive)
- Check if the server has either `ip_address` or `private_ip_address` set in Forge
- Manually specify `loadBalancerHost` as a workaround

### TLS Not Applied

**Problem**: Sites don't get TLS even with `defaultCertResolver` set

**Solution**:
- Ensure certificate resolver is defined in `certificatesResolvers`
- Check that the resolver name matches exactly
- Verify entry points include both `web` and `websecure`
- Check Traefik logs for certificate request errors

### Tags Not Being Recognized

**Problem**: Site tags don't affect configuration

**Solution**:
- Ensure tags start with `traefik:` prefix
- Check tag format: `traefik:key=value`
- Wait for the next poll interval (default 30s)
- Check Traefik logs with DEBUG level for tag parsing output
- Verify the Forge API includes tags in the response (use `?include=tags`)

---

## API Details

### Forge API Calls

The plugin makes these API calls:

1. `GET /api/orgs/{org}/servers` - Fetch all servers
2. `GET /api/orgs/{org}/servers/{id}/sites?include=tags` - Fetch sites with tags

### Response Fields Used

**From Servers**:
- `attributes.name` - Server name for mapping
- `attributes.private_ip_address` - Preferred IP for backend
- `attributes.ip_address` - Fallback IP

**From Sites**:
- `attributes.name` - Domain name for Host rule
- `attributes.status` - Must be "installed"
- `attributes.tags` - Array of tag strings for configuration

---

## Future Enhancements

Potential improvements for tag-based configuration:

- `traefik:middleware=<name>` - Attach middlewares
- `traefik:priority=<number>` - Set router priority
- `traefik:entrypoints=web,websecure` - Custom entry points
- `traefik:redirect-to-https=true` - Force HTTPS redirect
- Server-level tags for shared configuration across all sites on a server
