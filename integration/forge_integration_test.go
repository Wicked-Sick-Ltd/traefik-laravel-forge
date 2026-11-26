// Package integration contains mock-based integration tests for the forge
// provider. These tests live in a subdirectory so that yaegi test . (which only
// processes the current directory) never attempts to interpret them. They are
// compiled and run normally by go test ./...
package integration

import (
	"testing"

	forge "github.com/Wicked-Sick-Ltd/traefik-laravel-forge"
	"github.com/Wicked-Sick-Ltd/traefik-laravel-forge/mocks"
	"github.com/traefik/genconf/dynamic"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCertResolver = "cloudflare"

// -- Test helpers --

// newProvider creates a Provider with default config and the given mock client.
func newProvider(t *testing.T, m *mocks.MockForgeClient) *forge.Provider {
	t.Helper()
	cfg := forge.CreateConfig()
	p, err := forge.NewProviderWithClient(cfg, m)
	require.NoError(t, err)
	return p
}

// newProviderWithConfig creates a Provider from a modified config and the given mock.
func newProviderWithConfig(t *testing.T, m *mocks.MockForgeClient, modify func(*forge.Config)) *forge.Provider {
	t.Helper()
	cfg := forge.CreateConfig()
	modify(cfg)
	p, err := forge.NewProviderWithClient(cfg, m)
	require.NoError(t, err)
	return p
}

func makeServer(id, name, privateIP string, tags ...string) forge.ForgeServer {
	return forge.ForgeServer{
		ID:   id,
		Type: "servers",
		Attributes: forge.ForgeServerAttributes{
			Name:             name,
			PrivateIPAddress: privateIP,
			Tags:             tags,
		},
	}
}

func makeSite(id, name string, tags ...string) forge.ForgeSite {
	return forge.ForgeSite{
		ID:   id,
		Type: "sites",
		Attributes: forge.ForgeSiteAttributes{
			Name:   name,
			Status: "installed",
			Tags:   tags,
		},
	}
}

func makeDomain(name, domainType string) forge.ForgeDomain {
	return forge.ForgeDomain{
		ID:   name,
		Type: "domainRecords",
		Attributes: forge.ForgeDomainAttributes{
			Name:       name,
			DomainType: domainType,
			Status:     "enabled",
		},
	}
}

func noReverb(m *mocks.MockForgeClient, serverID, siteID string) {
	m.EXPECT().FetchReverbIntegration(serverID, siteID).Return(nil, nil)
}

func noDomains(m *mocks.MockForgeClient, serverID, siteID string) {
	m.EXPECT().FetchDomains(serverID, siteID).Return(nil, nil)
}

// -- Integration tests --

func TestGenerateConfiguration_BasicRouting(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	assert.Len(t, cfg.HTTP.Routers, 1)
	assert.Len(t, cfg.HTTP.Services, 1)

	router := cfg.HTTP.Routers["forge-site1-example.com"]
	require.NotNil(t, router)
	assert.Equal(t, "Host(`example.com`)", router.Rule)
	assert.Equal(t, []string{"web"}, router.EntryPoints)
	assert.Nil(t, router.TLS)

	svc := cfg.HTTP.Services["forge-example.com-site1-service"]
	require.NotNil(t, svc)
	require.Len(t, svc.LoadBalancer.Servers, 1)
	assert.Equal(t, "http://10.0.0.1:80", svc.LoadBalancer.Servers[0].URL)
}

func TestGenerateConfiguration_PublicIPFallback(t *testing.T) {
	server := forge.ForgeServer{
		ID:   "s1",
		Type: "servers",
		Attributes: forge.ForgeServerAttributes{
			Name:      "app01",
			IPAddress: "203.0.113.1",
		},
	}

	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{server}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	svc := cfg.HTTP.Services["forge-example.com-site1-service"]
	require.NotNil(t, svc)
	assert.Equal(t, "http://203.0.113.1:80", svc.LoadBalancer.Servers[0].URL)
}

func TestGenerateConfiguration_TLSwithCertResolver(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.DefaultCertResolver = testCertResolver
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	router := cfg.HTTP.Routers["forge-site1-example.com"]
	require.NotNil(t, router.TLS)
	assert.Equal(t, "cloudflare", router.TLS.CertResolver)
	assert.Equal(t, []string{"websecure"}, router.EntryPoints)
}

func TestGenerateConfiguration_HTTPRedirectRouter(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.DefaultCertResolver = testCertResolver
		c.HTTPRedirect = true
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	assert.NotNil(t, cfg.HTTP.Routers["forge-site1-example.com"])

	httpRouter := cfg.HTTP.Routers["forge-site1-example.com-http"]
	require.NotNil(t, httpRouter)
	assert.Equal(t, []string{"web"}, httpRouter.EntryPoints)
	assert.Equal(t, []string{"forge-https-redirect"}, httpRouter.Middlewares)
	assert.NotNil(t, cfg.HTTP.Middlewares["forge-https-redirect"])
}

func TestGenerateConfiguration_ExternalRedirectMiddleware(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.DefaultCertResolver = testCertResolver
		c.HTTPRedirect = true
		c.RedirectMiddleware = "my-redirect"
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	assert.NotContains(t, cfg.HTTP.Middlewares, "forge-https-redirect")
	httpRouter := cfg.HTTP.Routers["forge-site1-example.com-http"]
	require.NotNil(t, httpRouter)
	assert.Equal(t, []string{"my-redirect"}, httpRouter.Middlewares)
}

func TestGenerateConfiguration_DefaultSitesDisabled(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "opt-out.com"),
		makeSite("site2", "opt-in.com", "traefik:enabled=true"),
	}, nil)
	noDomains(m, "s1", "site2")
	noReverb(m, "s1", "site2")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.DefaultSitesEnabled = false
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	assert.Len(t, cfg.HTTP.Routers, 1)
	assert.NotNil(t, cfg.HTTP.Routers["forge-site2-opt-in.com"])
}

func TestGenerateConfiguration_SiteExplicitlyDisabled(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "active.com"),
		makeSite("site2", "disabled.com", "traefik:enabled=false"),
	}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	assert.Len(t, cfg.HTTP.Routers, 1)
	assert.NotNil(t, cfg.HTTP.Routers["forge-site1-active.com"])
}

func TestGenerateConfiguration_TraefikIDFilter(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{
		makeServer("s1", "lb01-server", "10.0.0.1", "traefik:traefik-id=lb01"),
		makeServer("s2", "lb02-server", "10.0.0.2", "traefik:traefik-id=lb02"),
	}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "lb01-site.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.TraefikID = "lb01" })

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	assert.Len(t, cfg.HTTP.Routers, 1)
	assert.NotNil(t, cfg.HTTP.Routers["forge-site1-lb01-site.com"])
}

func TestGenerateConfiguration_UntaggedServerSkippedInMultiLBMode(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{
		makeServer("s1", "untagged-server", "10.0.0.1"),
	}, nil)

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.TraefikID = "lb01" })

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)
	assert.Empty(t, cfg.HTTP.Routers)
}

func TestGenerateConfiguration_DomainRecords(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "site-name.com")}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("custom-domain.com", "primary"),
		makeDomain("alias.com", "alias"),
	}, nil)
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	// Two domain records → two routers, each with a single Host() rule.
	assert.Len(t, cfg.HTTP.Routers, 2)
	assert.Equal(t, "Host(`custom-domain.com`)", cfg.HTTP.Routers["forge-site1-custom-domain.com"].Rule)
	assert.Equal(t, "Host(`alias.com`)", cfg.HTTP.Routers["forge-site1-alias.com"].Rule)
	// One shared service for the site.
	assert.NotNil(t, cfg.HTTP.Services["forge-site-name.com-site1-service"])
}

func TestGenerateConfiguration_WildcardSubdomain(t *testing.T) {
	domain := forge.ForgeDomain{
		ID:   "1",
		Type: "domainRecords",
		Attributes: forge.ForgeDomainAttributes{
			Name:                    "example.com",
			Status:                  "enabled",
			DomainType:              "primary",
			AllowWildcardSubdomains: true,
		},
	}

	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{domain}, nil)
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	router := cfg.HTTP.Routers["forge-site1-example.com"]
	require.NotNil(t, router)
	assert.Contains(t, router.Rule, "Host(`example.com`)")
	assert.Contains(t, router.Rule, "HostRegexp(`^[^.]+\\.example\\.com$`)")
}

func TestGenerateConfiguration_WWWRedirect(t *testing.T) {
	domain := forge.ForgeDomain{
		ID:   "1",
		Type: "domainRecords",
		Attributes: forge.ForgeDomainAttributes{
			Name:            "example.com",
			Status:          "enabled",
			WWWRedirectType: "to-www",
		},
	}

	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{domain}, nil)
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	router := cfg.HTTP.Routers["forge-site1-example.com"]
	require.NotNil(t, router)
	assert.Contains(t, router.Rule, "Host(`example.com`)")
	assert.Contains(t, router.Rule, "Host(`www.example.com`)")
}

func TestGenerateConfiguration_ReverbRouter(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("example.com", "primary"),
		makeDomain("ws.example.com", "alias"),
	}, nil)
	m.EXPECT().FetchReverbIntegration("s1", "site1").Return(&forge.ForgeReverbIntegration{
		Enabled: true,
		Host:    "ws.example.com",
		Port:    8081,
	}, nil)

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.DefaultCertResolver = testCertResolver
		c.HTTPRedirect = true
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	// Main + HTTP redirect (main only) + Reverb = 3.
	// Reverb gets no HTTP redirect — WebSocket clients don't follow redirects.
	assert.Len(t, cfg.HTTP.Routers, 3)

	// Reverb router exists with correct rule and TLS.
	reverbRouter := cfg.HTTP.Routers["forge-site1-ws.example.com"]
	require.NotNil(t, reverbRouter)
	assert.Equal(t, "Host(`ws.example.com`)", reverbRouter.Rule)
	require.NotNil(t, reverbRouter.TLS)

	// Reverb router uses the same service as the main site — traffic goes to
	// Nginx (port 80) which handles the WebSocket proxy internally.
	assert.Equal(t, "forge-example.com-site1-service", reverbRouter.Service)

	// No separate Reverb service and no HTTP redirect for Reverb.
	assert.Nil(t, cfg.HTTP.Services["forge-site1-ws.example.com-reverb-service"])
	assert.Nil(t, cfg.HTTP.Routers["forge-site1-ws.example.com-http"])
}

func TestGenerateConfiguration_ForgeDomainSkipped(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "myapp.on-forge.com"),
	}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("myapp.on-forge.com", "primary"),
	}, nil)
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)
	assert.Empty(t, cfg.HTTP.Routers)
}

func TestGenerateConfiguration_ForgeDomainOptIn(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "myapp.on-forge.com", "traefik:forge-domain=true"),
	}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("myapp.on-forge.com", "primary"),
	}, nil)
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.DefaultCertResolver = testCertResolver
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	router := cfg.HTTP.Routers["forge-site1-myapp.on-forge.com"]
	require.NotNil(t, router)
	assert.Nil(t, router.TLS, "TLS must be disabled for .on-forge.com-only domains")
	assert.Equal(t, []string{"web"}, router.EntryPoints)
}

func TestGenerateConfiguration_SiteTagPortOverride(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "example.com", "traefik:port=3000"),
	}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.AllowedSitePorts = []int{3000} })

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	svc := cfg.HTTP.Services["forge-example.com-site1-service"]
	require.NotNil(t, svc)
	assert.Equal(t, "http://10.0.0.1:3000", svc.LoadBalancer.Servers[0].URL)
}

func TestGenerateConfiguration_SiteTagAliases(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "example.com", "traefik:aliases=api.example.com,app.example.com"),
	}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("example.com", "primary"),
	}, nil)
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	// Each alias gets its own router alongside the primary domain router.
	assert.Len(t, cfg.HTTP.Routers, 3)
	assert.NotNil(t, cfg.HTTP.Routers["forge-site1-example.com"])
	assert.NotNil(t, cfg.HTTP.Routers["forge-site1-api.example.com"])
	assert.NotNil(t, cfg.HTTP.Routers["forge-site1-app.example.com"])
	assert.Equal(t, "Host(`api.example.com`)", cfg.HTTP.Routers["forge-site1-api.example.com"].Rule)
}

func TestGenerateConfiguration_SiteTagMiddlewares(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "example.com", "traefik:middlewares=auth,rate-limit"),
	}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	router := cfg.HTTP.Routers["forge-site1-example.com"]
	require.NotNil(t, router)
	assert.Equal(t, []string{"auth", "rate-limit"}, router.Middlewares)
}

func TestGenerateConfiguration_SiteTagCertResolverOverride(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "example.com", "traefik:cert-resolver=letsencrypt-staging"),
	}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.DefaultCertResolver = testCertResolver })

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	router := cfg.HTTP.Routers["forge-site1-example.com"]
	require.NotNil(t, router.TLS)
	assert.Equal(t, "letsencrypt-staging", router.TLS.CertResolver)
}

func TestGenerateConfiguration_ServerMappingOverridesIP(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.ServerMappings = []forge.ServerMapping{
			{ForgeServerName: "app01", UpstreamHost: "192.168.100.5", UpstreamPort: 8080},
		}
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	svc := cfg.HTTP.Services["forge-example.com-site1-service"]
	require.NotNil(t, svc)
	assert.Equal(t, "http://192.168.100.5:8080", svc.LoadBalancer.Servers[0].URL)
}

func TestGenerateConfiguration_ServerTagOverridesMapping(t *testing.T) {
	server := makeServer("s1", "app01", "10.0.0.1", "traefik:upstream-host=172.16.0.10")

	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{server}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.ServerMappings = []forge.ServerMapping{
			{ForgeServerName: "app01", UpstreamHost: "should-not-be-used.internal", UpstreamPort: 9999},
		}
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	svc := cfg.HTTP.Services["forge-example.com-site1-service"]
	require.NotNil(t, svc)
	assert.Equal(t, "http://172.16.0.10:80", svc.LoadBalancer.Servers[0].URL)
}

func TestGenerateConfiguration_NotInstalledSiteSkipped(t *testing.T) {
	site := forge.ForgeSite{
		ID:   "site1",
		Type: "sites",
		Attributes: forge.ForgeSiteAttributes{
			Name:   "example.com",
			Status: "deploying",
		},
	}

	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{site}, nil)

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)
	assert.Empty(t, cfg.HTTP.Routers)
}

func TestGenerateConfiguration_MultiSiteMultiServer(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{
		makeServer("s1", "server-a", "10.0.0.1"),
		makeServer("s2", "server-b", "10.0.0.2"),
	}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "site-a.com"),
		makeSite("site2", "site-b.com"),
	}, nil)
	m.EXPECT().FetchSites("s2").Return([]forge.ForgeSite{
		makeSite("site3", "site-c.com"),
	}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")
	noDomains(m, "s1", "site2")
	noReverb(m, "s1", "site2")
	noDomains(m, "s2", "site3")
	noReverb(m, "s2", "site3")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	assert.Len(t, cfg.HTTP.Routers, 3)
	assert.Len(t, cfg.HTTP.Services, 3)

	svc1 := cfg.HTTP.Services["forge-site-a.com-site1-service"]
	require.NotNil(t, svc1)
	assert.Equal(t, "http://10.0.0.1:80", svc1.LoadBalancer.Servers[0].URL)

	svc3 := cfg.HTTP.Services["forge-site-c.com-site3-service"]
	require.NotNil(t, svc3)
	assert.Equal(t, "http://10.0.0.2:80", svc3.LoadBalancer.Servers[0].URL)
}

func TestGenerateConfiguration_NoHTTPRedirectWithoutTLS(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.HTTPRedirect = true })

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)
	assert.Len(t, cfg.HTTP.Routers, 1)
}

// -- Fail-safe behavior on transient Forge API errors --
//
// A partial config is worse than no config: Traefik applies whatever it is given,
// so a config that is missing routers causes Traefik to delete those routes and
// take live sites offline. Every fetch that determines which routers exist must
// therefore fail the whole poll, leaving Traefik on its last good configuration.

func TestGenerateConfiguration_SitesFetchFailureAbortsPoll(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return(nil, assert.AnError)

	cfg, err := newProvider(t, m).GenerateConfiguration()

	require.Error(t, err, "a sites fetch failure must not yield a routerless config")
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "app01")
}

func TestGenerateConfiguration_DomainsFetchFailureAbortsPoll(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "internal-name")}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return(nil, assert.AnError)

	cfg, err := newProvider(t, m).GenerateConfiguration()

	require.Error(t, err, "falling back to the site name would drop every real customer hostname")
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "internal-name")
}

func TestGenerateConfiguration_OneServerFailureDoesNotSilentlyDropAnother(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{
		makeServer("s1", "app01", "10.0.0.1"),
		makeServer("s2", "app02", "10.0.0.2"),
	}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil).Maybe()
	m.EXPECT().FetchSites("s2").Return(nil, assert.AnError).Maybe()
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	_, err := newProvider(t, m).GenerateConfiguration()

	require.Error(t, err)
}

// A Reverb failure only costs us the ability to single out the Reverb domain when
// every router still shares the Nginx port, so it degrades rather than aborting.
func TestGenerateConfiguration_ReverbFetchFailureDegradesGracefully(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{makeDomain("example.com", "primary")}, nil)
	m.EXPECT().FetchReverbIntegration("s1", "site1").Return(nil, assert.AnError)

	cfg, err := newProvider(t, m).GenerateConfiguration()

	require.NoError(t, err)
	assert.NotNil(t, cfg.HTTP.Routers["forge-site1-example.com"])
}

// An accepted traefik:port sends main routers to the app. Without a Reverb
// answer there is no way to keep the WebSocket hostname off that port, so this
// site stays on the upstream for the poll. Another site on the same server
// still gets its override.
func TestGenerateConfiguration_UnansweredReverbKeepsUpstreamPort(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "example.com", "traefik:port=3000"),
		makeSite("site2", "other.example.com", "traefik:port=3000"),
	}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("example.com", "primary"),
		makeDomain("ws.example.com", "alias"),
	}, nil)
	m.EXPECT().FetchDomains("s1", "site2").Return([]forge.ForgeDomain{makeDomain("other.example.com", "primary")}, nil)
	m.EXPECT().FetchReverbIntegration("s1", "site1").Return(nil, assert.AnError)
	noReverb(m, "s1", "site2")

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.AllowedSitePorts = []int{3000} })
	cfg, err := p.GenerateConfiguration()

	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, "forge-site1-example.com"))
	assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, "forge-site1-ws.example.com"))
	assert.Equal(t, "http://10.0.0.1:3000", serviceURLFor(t, cfg, "forge-site2-other.example.com"))
}

// A rejected port tag leaves the site on the upstream port, so a missed Reverb
// answer still shares that backend and the poll continues.
func TestGenerateConfiguration_DisallowedPortDoesNotAbortOnReverbFailure(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com", "traefik:port=6379")}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{makeDomain("example.com", "primary")}, nil)
	m.EXPECT().FetchReverbIntegration("s1", "site1").Return(nil, assert.AnError)

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.AllowedSitePorts = []int{3000} })
	cfg, err := p.GenerateConfiguration()

	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, "forge-site1-example.com"))
}

// A previous Reverb answer is enough to keep the WebSocket hostname on Nginx
// when the next lookup is refused.
func TestGenerateConfiguration_CachedReverbHostSurvivesLaterFailure(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil).Times(2)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com", "traefik:port=3000")}, nil).Times(2)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("example.com", "primary"),
		makeDomain("ws.example.com", "alias"),
	}, nil).Times(2)
	calls := 0
	m.EXPECT().FetchReverbIntegration("s1", "site1").RunAndReturn(
		func(string, string) (*forge.ForgeReverbIntegration, error) {
			calls++
			if calls == 1 {
				return &forge.ForgeReverbIntegration{Enabled: true, Host: "ws.example.com", Port: 8081}, nil
			}
			return nil, assert.AnError
		},
	).Times(2)

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.AllowedSitePorts = []int{3000} })
	if _, err := p.GenerateConfiguration(); err != nil {
		t.Fatal(err)
	}
	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.1:3000", serviceURLFor(t, cfg, "forge-site1-example.com"))
	assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, "forge-site1-ws.example.com"))
}

// With an accepted traefik:port and only a .on-forge.com main domain, no main
// router is built, but the Reverb router must still be kept (#20) and must still
// go to Nginx on the upstream port rather than the app port (#25).
func TestGenerateConfiguration_ReverbOnlySiteWithPortOverrideUsesNginx(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "myapp.on-forge.com", "traefik:port=3000")}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("myapp.on-forge.com", "primary"),
		makeDomain("ws.example.com", "alias"),
	}, nil)
	m.EXPECT().FetchReverbIntegration("s1", "site1").Return(&forge.ForgeReverbIntegration{
		Enabled: true, Host: "ws.example.com", Port: 8081,
	}, nil)

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.AllowedSitePorts = []int{3000} })
	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	assert.Nil(t, cfg.HTTP.Routers["forge-site1-myapp.on-forge.com"])
	assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, "forge-site1-ws.example.com"))
	assert.Nil(t, cfg.HTTP.Services["forge-myapp.on-forge.com-site1-service"],
		"no main router, so no app-port service")
}

// -- serverMappings --

// A mapping that overrides only the port must keep the auto-detected Forge IP.
// Previously the empty UpstreamHost won the priority chain and the server — and
// every site on it — was skipped entirely.
func TestGenerateConfiguration_ServerMappingPortOnlyKeepsDetectedIP(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.ServerMappings = []forge.ServerMapping{{ForgeServerName: "app01", UpstreamPort: 8080}}
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	svc := cfg.HTTP.Services["forge-example.com-site1-service"]
	require.NotNil(t, svc, "server must not be skipped when a mapping sets only the port")
	assert.Equal(t, "http://10.0.0.1:8080", svc.LoadBalancer.Servers[0].URL)
}

// -- Port tag validation --

func TestGenerateConfiguration_InvalidPortTagFallsBackToDefault(t *testing.T) {
	for _, value := range []string{"-1", "0", "999999", "80x", "abc"} {
		t.Run(value, func(t *testing.T) {
			m := mocks.NewMockForgeClient(t)
			m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
			m.EXPECT().FetchSites("s1").
				Return([]forge.ForgeSite{makeSite("site1", "example.com", "traefik:port="+value)}, nil)
			noDomains(m, "s1", "site1")
			noReverb(m, "s1", "site1")

			cfg, err := newProvider(t, m).GenerateConfiguration()
			require.NoError(t, err)

			svc := cfg.HTTP.Services["forge-example.com-site1-service"]
			require.NotNil(t, svc)
			assert.Equal(t, "http://10.0.0.1:80", svc.LoadBalancer.Servers[0].URL,
				"an unparseable port must not reach the backend URL")
		})
	}
}

// -- Site port allowlist --
//
// traefik:port is a site tag, so anyone who can tag one site could otherwise
// point that site's public hostnames at any port on the server address the load
// balancer dials. The tag only takes effect for ports the operator listed in
// allowedSitePorts.

// serviceURLFor returns the backend URL of the service a router points at.
func serviceURLFor(t *testing.T, cfg *dynamic.Configuration, routerName string) string {
	t.Helper()
	router := cfg.HTTP.Routers[routerName]
	require.NotNil(t, router, "router %q must exist", routerName)
	svc := cfg.HTTP.Services[router.Service]
	require.NotNil(t, svc, "service %q for router %q must exist", router.Service, routerName)
	require.Len(t, svc.LoadBalancer.Servers, 1)
	return svc.LoadBalancer.Servers[0].URL
}

func reverbSite(m *mocks.MockForgeClient, siteTags ...string) {
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com", siteTags...)}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("example.com", "primary"),
		makeDomain("ws.example.com", "alias"),
	}, nil)
	m.EXPECT().FetchReverbIntegration("s1", "site1").Return(&forge.ForgeReverbIntegration{
		Enabled: true,
		Host:    "ws.example.com",
		Port:    8081,
	}, nil)
}

func TestGenerateConfiguration_SitePortTagIgnoredWithoutAllowlist(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	reverbSite(m, "traefik:port=6379", "traefik:aliases=extra.example.com")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	for name := range cfg.HTTP.Routers {
		assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, name),
			"router %q must not follow a site port tag the operator never allowed", name)
	}
	assert.Len(t, cfg.HTTP.Routers, 3)
}

func TestGenerateConfiguration_SitePortTagNotOnAllowlistIgnored(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	reverbSite(m, "traefik:port=6379")

	p := newProviderWithConfig(t, m, func(c *forge.Config) { c.AllowedSitePorts = []int{3000, 8080} })

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	for name := range cfg.HTTP.Routers {
		assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, name),
			"router %q must not follow a site port tag outside allowedSitePorts", name)
	}
}

func TestGenerateConfiguration_SitePortTagSpellingsCannotBypassAllowlist(t *testing.T) {
	cases := map[string][]string{
		"sign prefix":             {"traefik:port=+6379"},
		"leading zero":            {"traefik:port=06379"},
		"padded key and value":    {"traefik: port = 6379 "},
		"second tag overrides":    {"traefik:port=3000", "traefik:port=6379"},
		"allowed then disallowed": {"traefik:port=6379", "traefik:port=3000", "traefik:port=22"},
	}
	for name, tags := range cases {
		t.Run(name, func(t *testing.T) {
			m := mocks.NewMockForgeClient(t)
			reverbSite(m, tags...)

			p := newProviderWithConfig(t, m, func(c *forge.Config) { c.AllowedSitePorts = []int{3000} })

			cfg, err := p.GenerateConfiguration()
			require.NoError(t, err)

			got := serviceURLFor(t, cfg, "forge-site1-example.com")
			assert.Contains(t, []string{"http://10.0.0.1:80", "http://10.0.0.1:3000"}, got,
				"tags %v must only ever select the server port or an allowed port", tags)
			assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, "forge-site1-ws.example.com"))
		})
	}
}

func TestGenerateConfiguration_AllowedSitePortKeepsReverbOnNginx(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	reverbSite(m, "traefik:port=3000")

	p := newProviderWithConfig(t, m, func(c *forge.Config) {
		c.AllowedSitePorts = []int{3000}
		c.DefaultCertResolver = testCertResolver
		c.HTTPRedirect = true
	})

	cfg, err := p.GenerateConfiguration()
	require.NoError(t, err)

	assert.Equal(t, "http://10.0.0.1:3000", serviceURLFor(t, cfg, "forge-site1-example.com"))
	assert.Equal(t, "http://10.0.0.1:3000", serviceURLFor(t, cfg, "forge-site1-example.com-http"))
	assert.Equal(t, "http://10.0.0.1:80", serviceURLFor(t, cfg, "forge-site1-ws.example.com"),
		"Reverb traffic must reach Nginx on the server port, not the site override")
	assert.Nil(t, cfg.HTTP.Routers["forge-site1-ws.example.com-http"])
}

// The server's own upstream port is what the site would use anyway, so a site tag
// naming it is accepted without an allowlist entry.
func TestGenerateConfiguration_SitePortTagMatchingServerPortAccepted(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().
		Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1",
			"traefik:upstream-host=10.0.0.1", "traefik:upstream-port=8080")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "tagged.com", "traefik:port=8080"),
		makeSite("site2", "untagged.com"),
		makeSite("site3", "rejected.com", "traefik:port=80"),
	}, nil)
	for _, site := range []string{"site1", "site2", "site3"} {
		noDomains(m, "s1", site)
		noReverb(m, "s1", site)
	}

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	assert.Equal(t, "http://10.0.0.1:8080", serviceURLFor(t, cfg, "forge-site1-tagged.com"))
	assert.Equal(t, "http://10.0.0.1:8080", serviceURLFor(t, cfg, "forge-site2-untagged.com"))
	assert.Equal(t, "http://10.0.0.1:8080", serviceURLFor(t, cfg, "forge-site3-rejected.com"))
}

func TestGenerateConfiguration_InvalidUpstreamPortTagFallsBackToDefault(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().
		Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1", "traefik:upstream-port=-8080")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{makeSite("site1", "example.com")}, nil)
	noDomains(m, "s1", "site1")
	noReverb(m, "s1", "site1")

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	svc := cfg.HTTP.Services["forge-example.com-site1-service"]
	require.NotNil(t, svc)
	assert.Equal(t, "http://10.0.0.1:80", svc.LoadBalancer.Servers[0].URL)
}
