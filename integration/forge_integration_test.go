// Package integration contains mock-based integration tests for the forge
// provider. These tests live in a subdirectory so that yaegi test . (which only
// processes the current directory) never attempts to interpret them. They are
// compiled and run normally by go test ./...
package integration

import (
	"testing"

	forge "github.com/Wicked-Sick-Ltd/traefik-laravel-forge"
	"github.com/Wicked-Sick-Ltd/traefik-laravel-forge/mocks"

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

	router := cfg.HTTP.Routers["forge-example.com-site1"]
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

	router := cfg.HTTP.Routers["forge-example.com-site1"]
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

	assert.NotNil(t, cfg.HTTP.Routers["forge-example.com-site1"])

	httpRouter := cfg.HTTP.Routers["forge-example.com-site1-http"]
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
	httpRouter := cfg.HTTP.Routers["forge-example.com-site1-http"]
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
	assert.NotNil(t, cfg.HTTP.Routers["forge-opt-in.com-site2"])
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
	assert.NotNil(t, cfg.HTTP.Routers["forge-active.com-site1"])
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
	assert.NotNil(t, cfg.HTTP.Routers["forge-lb01-site.com-site1"])
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

	router := cfg.HTTP.Routers["forge-site-name.com-site1"]
	require.NotNil(t, router)
	assert.Equal(t, "Host(`custom-domain.com`) || Host(`alias.com`)", router.Rule)
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

	router := cfg.HTTP.Routers["forge-example.com-site1"]
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

	router := cfg.HTTP.Routers["forge-example.com-site1"]
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

	// Main + HTTP redirect + Reverb = 3; no HTTP redirect for Reverb.
	assert.Len(t, cfg.HTTP.Routers, 3)

	reverbRouter := cfg.HTTP.Routers["forge-example.com-site1-reverb"]
	require.NotNil(t, reverbRouter)
	assert.Equal(t, "Host(`ws.example.com`)", reverbRouter.Rule)
	require.NotNil(t, reverbRouter.TLS)

	reverbSvc := cfg.HTTP.Services["forge-example.com-site1-reverb-service"]
	require.NotNil(t, reverbSvc)
	assert.Equal(t, "http://10.0.0.1:8081", reverbSvc.LoadBalancer.Servers[0].URL)

	assert.Nil(t, cfg.HTTP.Routers["forge-example.com-site1-reverb-http"])
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

	router := cfg.HTTP.Routers["forge-myapp.on-forge.com-site1"]
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

	cfg, err := newProvider(t, m).GenerateConfiguration()
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

	router := cfg.HTTP.Routers["forge-example.com-site1"]
	require.NotNil(t, router)
	assert.Contains(t, router.Rule, "Host(`api.example.com`)")
	assert.Contains(t, router.Rule, "Host(`app.example.com`)")
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

	router := cfg.HTTP.Routers["forge-example.com-site1"]
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

	router := cfg.HTTP.Routers["forge-example.com-site1"]
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

func TestGenerateConfiguration_ReverbPortTagOverride(t *testing.T) {
	m := mocks.NewMockForgeClient(t)
	m.EXPECT().FetchServers().Return([]forge.ForgeServer{makeServer("s1", "app01", "10.0.0.1")}, nil)
	m.EXPECT().FetchSites("s1").Return([]forge.ForgeSite{
		makeSite("site1", "example.com", "traefik:reverb-port=9000"),
	}, nil)
	m.EXPECT().FetchDomains("s1", "site1").Return([]forge.ForgeDomain{
		makeDomain("example.com", "primary"),
		makeDomain("ws.example.com", "alias"),
	}, nil)
	m.EXPECT().FetchReverbIntegration("s1", "site1").Return(&forge.ForgeReverbIntegration{
		Enabled: true,
		Host:    "ws.example.com",
		Port:    8081,
	}, nil)

	cfg, err := newProvider(t, m).GenerateConfiguration()
	require.NoError(t, err)

	reverbSvc := cfg.HTTP.Services["forge-example.com-site1-reverb-service"]
	require.NotNil(t, reverbSvc)
	assert.Equal(t, "http://10.0.0.1:9000", reverbSvc.LoadBalancer.Servers[0].URL)
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
