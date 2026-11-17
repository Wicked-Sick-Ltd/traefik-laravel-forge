package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/traefik/genconf/dynamic"
)

// These tests are the specification for a poll that drops a Reverb hostname.
// They run the production provider against a stub of the Forge JSON:API: tag
// names are resolved from relationships and included, domain records come from
// /domains, and the Reverb host comes from /integrations/reverb. They must fail
// while a site whose only enabled main name is *.on-forge.com publishes neither
// that name nor its Reverb host, and pass once that host is still routed to the
// site that owns the integration.

const (
	reverbHost         = "ws.example.com"
	reverbBackend      = "http://10.1.0.10:80"
	attackerBackend    = "http://10.2.0.20:80"
	sharedCertResolver = "cloudflare"
)

func TestReverbHostPublishedWhenOnlyMainDomainIsForgeDefault(t *testing.T) {
	// The enabled main list is the default Forge name, so the site-name fallback
	// does not run. createDomainRouter then drops that name. The Reverb record
	// must still be published, and the filtered name must not.
	p := providerOnForgeStub(t, forgeFixture{
		siteName: "myapp.on-forge.com",
		domains: []stubDomain{
			{ID: "d-forge", Name: "myapp.on-forge.com", Type: "primary"},
			{ID: "d-forge-alias", Name: "myapp-2.on-forge.com", Type: "alias"},
			{ID: "d-ws", Name: reverbHost, Type: "alias"},
		},
		reverbHost: reverbHost,
	})

	for _, cfg := range publishPolls(t, p) {
		requireBackendRouter(t, cfg, reverbHost, reverbBackend, routerReverb)
		rejectHost(t, cfg, "myapp.on-forge.com")
		rejectHost(t, cfg, "myapp-2.on-forge.com")
	}
}

func TestReverbHostPublishedWhenForgeNameIsOnlyTheSiteNameFallback(t *testing.T) {
	// No enabled main record: the only enabled domain is the Reverb host, and
	// the site name itself is *.on-forge.com. The fallback synthesizes that
	// name, createDomainRouter drops it, and the created-router count stays
	// zero. The Reverb host must still be published.
	p := providerOnForgeStub(t, forgeFixture{
		siteName: "myapp.on-forge.com",
		domains: []stubDomain{
			{ID: "d-ws", Name: reverbHost, Type: "alias"},
		},
		reverbHost: reverbHost,
	})

	for _, cfg := range publishPolls(t, p) {
		requireBackendRouter(t, cfg, reverbHost, reverbBackend, routerReverb)
		rejectHost(t, cfg, "myapp.on-forge.com")
	}
}

func TestForgeDefaultFilteredBeforeSiteNameFallback(t *testing.T) {
	// An enabled *.on-forge.com record must not count as "this site already has
	// a main domain". Once it is filtered, the empty-list fallback is the site
	// name, and the Reverb host is routed to the same site.
	const siteName = "app.example.com"
	p := providerOnForgeStub(t, forgeFixture{
		siteName: siteName,
		domains: []stubDomain{
			{ID: "d-forge", Name: "myapp.on-forge.com", Type: "primary"},
			{ID: "d-ws", Name: reverbHost, Type: "alias"},
		},
		reverbHost: reverbHost,
	})

	for _, cfg := range publishPolls(t, p) {
		requireBackendRouter(t, cfg, siteName, reverbBackend, routerTLS)
		requireBackendRouter(t, cfg, reverbHost, reverbBackend, routerReverb)
		rejectHost(t, cfg, "myapp.on-forge.com")
	}
}

func TestReverbHostStaysOnOwningSiteWhenPeerPublishesAlias(t *testing.T) {
	// A second site in the same organization, with no custom domain of its own,
	// sets traefik:aliases to the victim Reverb host. The tag is carried in the
	// JSON:API included array. The owning site's router must still be published
	// to the owning server, with the shared cert resolver and without the HTTP
	// redirect used for ordinary hostnames.
	p := providerWithPeer(t, peerModeAliasTag)

	for _, cfg := range publishPolls(t, p) {
		requireBackendRouter(t, cfg, reverbHost, reverbBackend, routerReverb)
		requireBackendRouter(t, cfg, reverbHost, attackerBackend, routerTLS)
		rejectHost(t, cfg, "myapp.on-forge.com")
		rejectHost(t, cfg, "other.on-forge.com")
	}
}

func TestReverbHostStaysOnOwningSiteWhenPeerPublishesDomainRecord(t *testing.T) {
	// Same poll shape, but the peer publishes the hostname as a domain record
	// rather than a tag. The record is type "alias"; routing must follow the
	// name, and the owning site's Reverb router must point at the owning server.
	p := providerWithPeer(t, peerModeDomainRecord)

	for _, cfg := range publishPolls(t, p) {
		requireBackendRouter(t, cfg, reverbHost, reverbBackend, routerReverb)
		requireBackendRouter(t, cfg, reverbHost, attackerBackend, routerTLS)
		rejectHost(t, cfg, "myapp.on-forge.com")
	}
}

func TestCustomDomainStillPublishesReverbRouter(t *testing.T) {
	// A normal custom domain beside the Forge default already keeps the Reverb
	// router. That path has to stay pointed at this site.
	const siteName = "app.example.com"
	p := providerOnForgeStub(t, forgeFixture{
		siteName: siteName,
		domains: []stubDomain{
			{ID: "d-forge", Name: "myapp.on-forge.com", Type: "primary"},
			{ID: "d-app", Name: siteName, Type: "primary"},
			{ID: "d-ws", Name: reverbHost, Type: "alias"},
		},
		reverbHost: reverbHost,
	})

	for _, cfg := range publishPolls(t, p) {
		requireBackendRouter(t, cfg, siteName, reverbBackend, routerTLS)
		requireBackendRouter(t, cfg, reverbHost, reverbBackend, routerReverb)
		rejectHost(t, cfg, "myapp.on-forge.com")
	}
}

func TestForgeDomainOptInStillPublishesReverbAndForgeName(t *testing.T) {
	p := providerOnForgeStub(t, forgeFixture{
		siteName: "myapp.on-forge.com",
		tags:     []string{"traefik:forge-domain=true"},
		domains: []stubDomain{
			{ID: "d-forge", Name: "myapp.on-forge.com", Type: "primary"},
			{ID: "d-ws", Name: reverbHost, Type: "alias"},
		},
		reverbHost: reverbHost,
	})

	for _, cfg := range publishPolls(t, p) {
		requireBackendRouter(t, cfg, "myapp.on-forge.com", reverbBackend, routerForgeHTTP)
		requireBackendRouter(t, cfg, reverbHost, reverbBackend, routerReverb)
	}
}

type stubDomain struct {
	ID   string
	Name string
	Type string
}

type forgeFixture struct {
	siteName   string
	tags       []string
	domains    []stubDomain
	reverbHost string
}

func providerOnForgeStub(t *testing.T, fix forgeFixture) *Provider {
	t.Helper()

	org := testOrganization
	routes := map[string]string{
		"/api/orgs/" + org + "/servers?include=tags": serversJSON([]stubServer{
			{ID: "srv-victim", Name: "php-victim", PrivateIP: "10.1.0.10"},
		}),
		"/api/orgs/" + org + "/servers/srv-victim/sites?include=tags": sitesJSON([]stubSite{
			{ID: "site-victim", Name: fix.siteName, Tags: fix.tags},
		}),
		"/api/orgs/" + org + "/servers/srv-victim/sites/site-victim/domains":             domainsJSON(fix.domains),
		"/api/orgs/" + org + "/servers/srv-victim/sites/site-victim/integrations/reverb": reverbJSON(fix.reverbHost),
	}
	return newStubProvider(t, routes)
}

type peerMode int

const (
	peerModeAliasTag peerMode = iota
	peerModeDomainRecord
)

func providerWithPeer(t *testing.T, mode peerMode) *Provider {
	t.Helper()

	org := testOrganization
	attackerTags := []string(nil)
	attackerDomains := []stubDomain{
		{ID: "d-attacker-forge", Name: "other.on-forge.com", Type: "primary"},
	}
	if mode == peerModeAliasTag {
		attackerTags = []string{"traefik:aliases=" + reverbHost}
	} else {
		attackerDomains = append(attackerDomains, stubDomain{ID: "d-attacker-ws", Name: reverbHost, Type: "alias"})
	}

	routes := map[string]string{
		"/api/orgs/" + org + "/servers?include=tags": serversJSON([]stubServer{
			{ID: "srv-victim", Name: "php-victim", PrivateIP: "10.1.0.10"},
			{ID: "srv-attacker", Name: "php-attacker", PrivateIP: "10.2.0.20"},
		}),
		"/api/orgs/" + org + "/servers/srv-victim/sites?include=tags": sitesJSON([]stubSite{
			{ID: "site-victim", Name: "myapp.on-forge.com"},
		}),
		"/api/orgs/" + org + "/servers/srv-victim/sites/site-victim/domains": domainsJSON([]stubDomain{
			{ID: "d-forge", Name: "myapp.on-forge.com", Type: "primary"},
			{ID: "d-ws", Name: reverbHost, Type: "alias"},
		}),
		"/api/orgs/" + org + "/servers/srv-victim/sites/site-victim/integrations/reverb": reverbJSON(reverbHost),
		"/api/orgs/" + org + "/servers/srv-attacker/sites?include=tags": sitesJSON([]stubSite{
			{ID: "site-attacker", Name: "other.on-forge.com", Tags: attackerTags},
		}),
		"/api/orgs/" + org + "/servers/srv-attacker/sites/site-attacker/domains": domainsJSON(attackerDomains),
		// Answered and disabled. A 404 would also mean "no Reverb", but the stub
		// must not treat an unexpected path as that answer.
		"/api/orgs/" + org + "/servers/srv-attacker/sites/site-attacker/integrations/reverb": `{"data":{"attributes":{"enabled":false,"host":"","port":0}}}`,
	}
	return newStubProvider(t, routes)
}

type stubServer struct {
	ID        string
	Name      string
	PrivateIP string
}

type stubSite struct {
	ID   string
	Name string
	Tags []string
}

func serversJSON(servers []stubServer) string {
	data := make([]string, 0, len(servers))
	for _, s := range servers {
		data = append(data, fmt.Sprintf(`{
			"id": %q, "type": "servers",
			"attributes": {"name": %q, "private_ip_address": %q, "ip_address": "203.0.113.10"},
			"relationships": {"tags": {"data": []}}
		}`, s.ID, s.Name, s.PrivateIP))
	}
	return `{"data":[` + strings.Join(data, ",") + `],"included":[],"links":[],"meta":[]}`
}

func sitesJSON(sites []stubSite) string {
	data := make([]string, 0, len(sites))
	included := make([]string, 0, len(sites))
	for _, site := range sites {
		refs := make([]string, 0, len(site.Tags))
		for i, tag := range site.Tags {
			id := fmt.Sprintf("%s-tag-%d", site.ID, i)
			refs = append(refs, fmt.Sprintf(`{"type":"tags","id":%q}`, id))
			included = append(included, fmt.Sprintf(
				`{"type":"tags","id":%q,"attributes":{"name":%q}}`, id, tag))
		}
		refJSON := "[]"
		if len(refs) > 0 {
			refJSON = "[" + strings.Join(refs, ",") + "]"
		}
		data = append(data, fmt.Sprintf(`{
			"id": %q, "type": "sites",
			"attributes": {"name": %q, "status": "installed"},
			"relationships": {"tags": {"data": %s}}
		}`, site.ID, site.Name, refJSON))
	}
	inc := "[]"
	if len(included) > 0 {
		inc = "[" + strings.Join(included, ",") + "]"
	}
	return `{"data":[` + strings.Join(data, ",") + `],"included":` + inc + `,"links":[],"meta":[]}`
}

func domainsJSON(domains []stubDomain) string {
	data := make([]string, 0, len(domains))
	for _, d := range domains {
		data = append(data, fmt.Sprintf(`{
			"id": %q, "type": "domainRecords",
			"attributes": {"name": %q, "type": %q, "status": "enabled"}
		}`, d.ID, d.Name, d.Type))
	}
	body := "[]"
	if len(data) > 0 {
		body = "[" + strings.Join(data, ",") + "]"
	}
	return `{"data":` + body + `,"links":[],"meta":[]}`
}

func reverbJSON(host string) string {
	return fmt.Sprintf(`{"data":{"attributes":{"enabled":true,"host":%q,"port":8081}}}`, host)
}

func newStubProvider(t *testing.T, routes map[string]string) *Provider {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.RequestURI()]
		if !ok {
			t.Errorf("unexpected Forge request %s", r.URL.RequestURI())
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	cfg := CreateConfig()
	cfg.APIToken = testAPIToken
	cfg.Organization = testOrganization
	cfg.DefaultCertResolver = sharedCertResolver
	cfg.HTTPRedirect = true

	p, err := New(context.Background(), cfg, "forge")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client, ok := p.client.(*forgeHTTPClient)
	if !ok {
		t.Fatalf("client type %T, want the production HTTP client", p.client)
	}
	client.baseURL = srv.URL
	return p
}

// publishPolls returns the configuration from a cold poll, a second poll that
// reuses the cached Forge lookup, and the payload Provide actually sends.
func publishPolls(t *testing.T, p *Provider) []*dynamic.Configuration {
	t.Helper()

	cold := mustGenerate(t, p)
	cached := mustGenerate(t, p)
	sent := mustSend(t, p)
	return []*dynamic.Configuration{cold, cached, sent}
}

func mustGenerate(t *testing.T, p *Provider) *dynamic.Configuration {
	t.Helper()
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatalf("GenerateConfiguration() error = %v, want a successful poll", err)
	}
	if cfg == nil || cfg.HTTP == nil {
		t.Fatal("GenerateConfiguration() returned no HTTP configuration")
	}
	return cfg
}

func mustSend(t *testing.T, p *Provider) *dynamic.Configuration {
	t.Helper()

	ch := make(chan json.Marshaler, 1)
	if err := p.Provide(ch); err != nil {
		t.Fatalf("Provide() error = %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	select {
	case msg := <-ch:
		payload, ok := msg.(*dynamic.JSONPayload)
		if !ok || payload == nil || payload.Configuration == nil || payload.HTTP == nil {
			t.Fatalf("sent %T, want a dynamic configuration", msg)
		}
		return payload.Configuration
	case <-time.After(3 * time.Second):
		t.Fatal("successful poll sent no configuration")
		return nil
	}
}

type routerKind int

const (
	// routerReverb is the WebSocket hostname: TLS via the shared resolver, and
	// no HTTP redirect beside it.
	routerReverb routerKind = iota
	// routerTLS is an ordinary hostname. An HTTP redirect sibling is expected
	// when httpRedirect is on.
	routerTLS
	// routerForgeHTTP is an opted-in *.on-forge.com name: HTTP only, no cert.
	routerForgeHTTP
)

func requireBackendRouter(t *testing.T, cfg *dynamic.Configuration, host, backend string, kind routerKind) {
	t.Helper()

	wantRule := "Host(`" + host + "`)"
	for _, router := range cfg.HTTP.Routers {
		if router.Rule != wantRule || serviceURL(cfg, router.Service) != backend {
			continue
		}
		switch kind {
		case routerForgeHTTP:
			if router.TLS != nil {
				continue
			}
			if len(router.EntryPoints) != 1 || router.EntryPoints[0] != "web" {
				t.Errorf("forge-default router for %s entrypoints = %v, want web", host, router.EntryPoints)
			}
		case routerReverb, routerTLS:
			if router.TLS == nil {
				continue
			}
			if router.TLS.CertResolver != sharedCertResolver {
				t.Errorf("router for %s -> %s cert resolver = %q, want %s",
					host, backend, router.TLS.CertResolver, sharedCertResolver)
			}
			if !slices.Contains(router.EntryPoints, "websecure") {
				t.Errorf("router for %s -> %s entrypoints = %v, want websecure", host, backend, router.EntryPoints)
			}
			if kind == routerReverb && redirectSibling(cfg, wantRule, backend) {
				t.Errorf("Reverb hostname %s -> %s has an HTTP redirect router", host, backend)
			}
		}
		if slices.Contains(router.Middlewares, "forge-https-redirect") {
			t.Errorf("router for %s -> %s uses the HTTP redirect middleware on the TLS router", host, backend)
		}
		return
	}

	t.Fatalf("no router %s -> %s (%v)\n%s", wantRule, backend, kind, describeRouters(cfg))
}

func redirectSibling(cfg *dynamic.Configuration, rule, backend string) bool {
	for _, router := range cfg.HTTP.Routers {
		if router.Rule == rule && serviceURL(cfg, router.Service) == backend &&
			slices.Contains(router.Middlewares, "forge-https-redirect") {
			return true
		}
	}
	return false
}

func rejectHost(t *testing.T, cfg *dynamic.Configuration, host string) {
	t.Helper()
	want := "Host(`" + host + "`)"
	for name, router := range cfg.HTTP.Routers {
		if strings.Contains(router.Rule, want) {
			t.Errorf("router %s publishes filtered name %s (%s) -> %s",
				name, host, router.Rule, serviceURL(cfg, router.Service))
		}
	}
}

func serviceURL(cfg *dynamic.Configuration, serviceName string) string {
	if cfg.HTTP == nil || cfg.HTTP.Services == nil {
		return ""
	}
	svc := cfg.HTTP.Services[serviceName]
	if svc == nil || svc.LoadBalancer == nil || len(svc.LoadBalancer.Servers) == 0 {
		return ""
	}
	return svc.LoadBalancer.Servers[0].URL
}

func describeRouters(cfg *dynamic.Configuration) string {
	if cfg.HTTP == nil {
		return "no HTTP configuration"
	}
	lines := make([]string, 0, len(cfg.HTTP.Routers))
	for name, router := range cfg.HTTP.Routers {
		tls := ""
		if router.TLS != nil {
			tls = router.TLS.CertResolver
		}
		lines = append(lines, fmt.Sprintf("  %s rule=%q url=%s tls=%q eps=%v mws=%v",
			name, router.Rule, serviceURL(cfg, router.Service), tls, router.EntryPoints, router.Middlewares))
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return "routers: none"
	}
	return "routers:\n" + strings.Join(lines, "\n")
}
