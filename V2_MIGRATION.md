# Migration to Forge API v2

This document explains the changes made to migrate from the deprecated Forge API v1 to the current v2 API.

## Why v2?

The Forge API v1 is deprecated and will be discontinued on **March 31st, 2026**. The v2 API is the current, supported version and uses modern standards like JSON:API.

## What Changed

### 1. API Endpoints

**Before (v1):**
```
GET https://forge.laravel.com/api/v1/servers
GET https://forge.laravel.com/api/v1/servers/{serverId}/sites
```

**After (v2):**
```
GET https://forge.laravel.com/api/orgs/{organization}/servers
GET https://forge.laravel.com/api/orgs/{organization}/servers/{serverId}/sites
```

### 2. Response Format

**v1 Response:**
```json
{
  "servers": [
    {
      "id": 1,
      "name": "app01",
      "ip_address": "10.0.1.10"
    }
  ]
}
```

**v2 Response (JSON:API):**
```json
{
  "data": [
    {
      "id": "1",
      "type": "server",
      "attributes": {
        "name": "app01",
        "ip_address": "10.0.1.10"
      }
    }
  ],
  "links": { ... },
  "meta": { ... }
}
```

### 3. Configuration Changes

**Before:**
```yaml
providers:
  plugin:
    forge:
      apiToken: "your-token"
      pollInterval: "30s"
      serverMappings: [...]
```

**After:**
```yaml
providers:
  plugin:
    forge:
      apiToken: "your-token"
      organization: "your-org-slug"  # NEW - Required
      pollInterval: "30s"
      serverMappings: [...]
```

### 4. Data Type Changes

- **Server IDs**: Changed from `int` to `string`
- **Site IDs**: Changed from `int` to `string`
- **Response Structure**: Flat objects → JSON:API format with `data`, `type`, `attributes`

## Benefits of v2

1. **Future-proof**: Won't be deprecated like v1
2. **JSON:API Standard**: Consistent, predictable format
3. **Better Features**: Built-in support for:
   - Pagination with cursors
   - Filtering and sorting
   - Including related resources
   - Sparse fieldsets
4. **Organization-scoped**: Better multi-tenant support

## Finding Your Organization Slug

1. Log into Laravel Forge
2. Look at the URL in your browser
3. The organization slug is in the URL: `forge.laravel.com/orgs/{organization}`

For personal accounts, you may have a default organization like `personal` or your username.

## Code Changes Summary

### Updated Structs

```go
// Now uses JSON:API format
type ForgeServer struct {
    ID         string                `json:"id"`         // Was: int
    Type       string                `json:"type"`       // NEW
    Attributes ForgeServerAttributes `json:"attributes"` // NEW
}

type ForgeServerAttributes struct {
    Name      string `json:"name"`
    IPAddress string `json:"ip_address"`
    Provider  string `json:"provider"`
    Region    string `json:"region"`
}
```

### Updated Provider

```go
type Provider struct {
    // ...
    organization   string // NEW - Required for v2 API
    // ...
}
```

### Updated API Calls

All API calls now:
1. Include the organization in the URL path
2. Parse JSON:API responses with `data`, `type`, `attributes`
3. Handle string IDs instead of integers

## Testing

All tests have been updated to include the organization parameter:

```bash
go test -v
```

All tests pass with the v2 implementation.

## Backwards Compatibility

**None.** This is a breaking change. If you were using the v1 implementation, you must:

1. Update your configuration to include `organization`
2. Ensure your API token has access to the specified organization
3. Update any custom code that depends on the data structures

## References

- [Forge API v2 Documentation](https://forge.laravel.com/docs/api-reference/introduction)
- [JSON:API Specification](https://jsonapi.org/)
- [Forge API Servers Endpoint](https://forge.laravel.com/docs/api-reference/servers/list-servers)
- [Forge API Sites Endpoint](https://forge.laravel.com/docs/api-reference/sites/list-sites-for-server)
