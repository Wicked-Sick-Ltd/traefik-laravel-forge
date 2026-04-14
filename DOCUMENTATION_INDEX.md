# Documentation Index

Quick reference guide to find the documentation you need.

## 🚀 Getting Started

**New to this plugin?** Start here:

1. **[README.md](README.md)** - Main documentation
   - Installation instructions
   - Quick start guide
   - Configuration reference
   - Troubleshooting

2. **[traefik.example.yml](traefik.example.yml)** - Example config
   - Copy and customize for your setup
   - Includes all configuration options with comments
   - Shows both tag-based and config-file approaches

## 🏷️ Tag-Based Configuration

**Want to configure everything via Forge tags?**

1. **[TAG_CONFIGURATION.md](TAG_CONFIGURATION.md)** - Complete guide
   - Step-by-step examples
   - All supported tags
   - Best practices
   - Migration from config file to tags

2. **[TAG_BASED_SUMMARY.md](TAG_BASED_SUMMARY.md)** - Quick reference
   - Tag format reference
   - Priority system explained
   - Real-world examples

## 🔧 Advanced Features

**Need specific functionality?**

**[FEATURES.md](FEATURES.md)** - Deep dive into:
- Auto-IP detection
- TLS certificate management
- Tag parsing system
- Configuration priority

## 👨‍💻 Development & Technical

**Contributing or customizing?**

1. **[IMPLEMENTATION.md](IMPLEMENTATION.md)** - Technical details
   - Architecture overview
   - Code structure
   - How it works internally
   - Design decisions

2. **[forge.go](forge.go)** - Source code
   - Main plugin implementation
   - Well-commented code

3. **[forge_test.go](forge_test.go)** - Test suite
   - 8 test cases covering core functionality

## 📜 Reference Documents

**Background information:**

- **[V2_MIGRATION.md](V2_MIGRATION.md)** - API v1 to v2 migration
  - Why v2?
  - What changed
  - Response format differences

- **[CHANGELOG.md](CHANGELOG.md)** - Version history
  - All features added
  - Changes by version
  - Breaking changes

- **[PROJECT_OVERVIEW.md](PROJECT_OVERVIEW.md)** - Repository guide
  - File structure
  - Quick reference
  - Development workflow

## 📑 By Use Case

### "I want to get started quickly"
→ [README.md](README.md#quick-start-tag-based-setup)

### "I want to use tags for everything"
→ [TAG_CONFIGURATION.md](TAG_CONFIGURATION.md)

### "How do I configure TLS?"
→ [FEATURES.md](FEATURES.md#tls-certificate-resolvers)

### "How does auto-IP detection work?"
→ [FEATURES.md](FEATURES.md#auto-detection-of-server-ips)

### "I want opt-in mode (sites disabled by default)"
→ [TAG_CONFIGURATION.md](TAG_CONFIGURATION.md#example-3-opt-in-mode-sites-disabled-by-default)

### "How do I migrate from the demo template?"
→ This is already done! Files renamed, ready to use.

### "How do I migrate from API v1?"
→ [V2_MIGRATION.md](V2_MIGRATION.md)

### "What tags are supported?"
→ [TAG_CONFIGURATION.md](TAG_CONFIGURATION.md#tag-reference)

### "How do I publish to Traefik Plugin Catalog?"
→ [README.md](README.md#publishing-to-traefik-plugin-catalog)

### "How does the priority system work?"
→ [TAG_BASED_SUMMARY.md](TAG_BASED_SUMMARY.md#priority-system)

### "I found a bug / want to contribute"
→ Open an issue on GitHub or submit a PR

## 📊 Repository Status

- **Status**: Production ready
- **Tests**: 8 passing, 12.9% coverage
- **API Version**: Laravel Forge API v2
- **Traefik Plugin Type**: Provider
- **Go Version**: 1.19+

## 🎯 Most Common Questions

**Q: Do I need to edit config files after initial setup?**
A: No! Use tags for everything after the initial Traefik configuration.

**Q: How often does it check Forge for changes?**
A: Configurable via `pollInterval` (minimum 10s, default 30s)

**Q: Can I use private IPs?**
A: Yes! Auto-detected by default. Plugin prefers `private_ip_address` over public IP.

**Q: Do I need tags on servers?**
A: No! Servers auto-enable if they have enabled sites. Tags only needed to override IP/port.

**Q: How do I enable HTTPS?**
A: Set `defaultCertResolver: "letsencrypt"` in config and define the resolver.

**Q: Can I disable specific sites?**
A: Yes! Add tag `traefik:enabled=false` to any site.

**Q: Can I use different cert resolvers per site?**
A: Yes! Add tag `traefik:cert-resolver=your-resolver` to override.

## 📞 Support & Links

- **GitHub**: https://github.com/wickedsick/traefik-laravel-forge
- **Forge API Docs**: https://forge.laravel.com/docs/api-reference
- **Traefik Plugins**: https://plugins.traefik.io
- **Traefik Docs**: https://doc.traefik.io/traefik/

---

**Last Updated**: v1.0.0
**Maintained By**: wickedsick
