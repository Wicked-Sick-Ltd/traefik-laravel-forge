package traefik_laravel_forge

// NewProviderWithClient creates a Provider with the given ForgeClient and
// sensible test defaults. Used by the integration test package.
func NewProviderWithClient(client ForgeClient) *Provider {
	return &Provider{
		name:                "test",
		defaultSitesEnabled: true,
		client:              client,
	}
}

// SetDefaultSitesEnabled sets the defaultSitesEnabled field for testing.
func SetDefaultSitesEnabled(p *Provider, v bool) { p.defaultSitesEnabled = v }

// SetDefaultCertResolver sets the defaultCertResolver field for testing.
func SetDefaultCertResolver(p *Provider, v string) { p.defaultCertResolver = v }

// SetHTTPRedirect sets the httpRedirect field for testing.
func SetHTTPRedirect(p *Provider, v bool) { p.httpRedirect = v }

// SetRedirectMiddleware sets the redirectMiddleware field for testing.
func SetRedirectMiddleware(p *Provider, v string) { p.redirectMiddleware = v }

// SetTraefikID sets the traefikID field for testing.
func SetTraefikID(p *Provider, v string) { p.traefikID = v }

// SetServerMappings sets the serverMappings field for testing.
func SetServerMappings(p *Provider, v []ServerMapping) { p.serverMappings = v }
