# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

#### Auto-Detection of Server IPs
- Plugin now automatically detects server IP addresses from Forge API
- Prefers `private_ip_address` over public IP for internal networks
- `loadBalancerHost` in server mappings is now optional
- Added `loadBalancerPort` field (defaults to 80)
- Logs show whether IP was auto-detected or manually configured

#### TLS Certificate Management
- Added `defaultCertResolver` configuration option
- Automatic TLS/HTTPS setup with Let's Encrypt or other ACME providers
- All sites inherit default cert resolver unless overridden
- Routers automatically configured with TLS when resolver is set

#### Tag-Based Per-Site Configuration
- Sites can now be configured via tags in Laravel Forge
- Supported tags:
  - `traefik:cert-resolver=<name>` - Override certificate resolver
  - `traefik:tls=true` - Enable TLS
  - `traefik:tls=false` - Disable TLS
- Tag format: `traefik:key=value` or `traefik:flag`
- Tags fetched via `?include=tags` API parameter
- Exported `ParseTagConfig()` function for testing

#### Enhanced Logging
- Added IP auto-detection logging
- Added TLS configuration decision logging
- Added tag parsing result logging
- More detailed backend URL logging with port numbers

### Changed
- Server IDs now use string type (was int) for API v2 compatibility
- Site IDs now use string type (was int) for API v2 compatibility
- Backend URLs now include explicit port numbers
- Updated data structures to include `private_ip_address` field
- Updated data structures to include tags support with `included` resources

### Documentation
- Created comprehensive [FEATURES.md](FEATURES.md) documentation
- Updated [README.md](README.md) with advanced features section
- Updated [traefik.example.yml](traefik.example.yml) with:
  - TLS configuration examples
  - Auto-IP detection examples
  - Tag-based configuration documentation
  - Multiple certificate resolver examples
- Created [V2_MIGRATION.md](V2_MIGRATION.md) for API migration details
- Updated [IMPLEMENTATION.md](IMPLEMENTATION.md) with new features

### Tests
- Added comprehensive tests for `ParseTagConfig()` function
- All 6 test cases passing
- Build verified successfully

## [1.0.0] - Initial Release

### Added
- Laravel Forge API v2 integration
- Automatic site discovery from Forge
- Server-to-load-balancer mapping configuration
- Organization-scoped API access
- Configurable poll intervals
- Status-based filtering (only "installed" sites)
- JSON:API format support
- HTTP router creation with Host rules
- PassHostHeader enabled for all services
- Comprehensive error handling and logging
