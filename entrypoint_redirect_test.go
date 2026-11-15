package traefik_laravel_forge

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/traefik/genconf/dynamic"
)

// These tests drive GenerateConfiguration through the real Forge HTTP client.
// Site tags are delivered the way Forge delivers them: relationship IDs plus
// names in the top-level included array, never attributes.tags.
//
// A TLS site whose traefik:entrypoints list does not contain the public HTTP
// entrypoint "web" must not gain a cleartext router on "web". "websecure"
// contains the letters "web" and is still not that entrypoint. When a site is
// allowed to use "web", the cleartext router has to keep the redirect
// middleware in front of the origin service.
//
// Reverb is not asserted here. processSite clears HTTPRedirect on the copy of
// the tag config it passes for Reverb domains, so that call never builds the
// cleartext router. Opted-in .on-forge.com domains call createRouter with TLS
// forced off, so they do not build it either.

const publicHTTPEntrypoint = "web"

type fixtureDomain struct {
	name     string
	wildcard bool
	wwwType  string
}

type fixtureSite struct {
	id      string
	name    string
	tags    []string
	domains []fixtureDomain
}

func TestRestrictedEntrypointIsNotPublishedOnWeb(t *testing.T) {
	// Two sites in one poll. The unrestricted site must keep its redirect
	// router, so a fix cannot satisfy this by turning HTTP redirects off.
	cfg := generateFromFixture(t, true, "", []fixtureSite{
		{
			id:   "site-hidden",
			name: "hidden.example.test",
			tags: []string{
				"traefik:entrypoints=internal",
				"traefik:aliases=secret.example.test",
			},
			domains: []fixtureDomain{
				{name: "hidden.example.test", wildcard: true, wwwType: "to-www"},
				{name: "api.hidden.example.test"},
			},
		},
		{
			id:   "site-public",
			name: "public.example.test",
			domains: []fixtureDomain{
				{name: "public.example.test"},
			},
		},
	})

	hidden := cfg.HTTP.Routers["forge-site-hidden-hidden.example.test"]
	if hidden == nil {
		t.Fatal("missing TLS router for hidden.example.test")
	}
	if hidden.TLS == nil || hidden.TLS.CertResolver != "cloudflare" {
		t.Fatalf("hidden TLS router = %+v, want cert resolver cloudflare", hidden.TLS)
	}
	assertStringSlice(t, hidden.EntryPoints, []string{"internal"})
	wantRule := "Host(`hidden.example.test`) || Host(`www.hidden.example.test`) || HostRegexp(`^[^.]+\\.hidden\\.example\\.test$`)"
	if hidden.Rule != wantRule {
		t.Fatalf("hidden rule = %q, want %q", hidden.Rule, wantRule)
	}

	api := cfg.HTTP.Routers["forge-site-hidden-api.hidden.example.test"]
	if api == nil {
		t.Fatal("missing TLS router for api.hidden.example.test")
	}
	assertStringSlice(t, api.EntryPoints, []string{"internal"})

	alias := cfg.HTTP.Routers["forge-site-hidden-secret.example.test"]
	if alias == nil {
		t.Fatal("missing TLS router for alias secret.example.test")
	}
	assertStringSlice(t, alias.EntryPoints, []string{"internal"})
	if alias.Rule != "Host(`secret.example.test`)" {
		t.Fatalf("alias rule = %q", alias.Rule)
	}

	assertNoCleartextRouter(t, cfg, "forge-site-hidden-hidden.example.test")
	assertNoCleartextRouter(t, cfg, "forge-site-hidden-api.hidden.example.test")
	assertNoCleartextRouter(t, cfg, "forge-site-hidden-secret.example.test")
	assertHostsStayOffPublicHTTP(t, cfg,
		"hidden.example.test",
		"www.hidden.example.test",
		"api.hidden.example.test",
		"secret.example.test",
	)

	publicHTTP := cfg.HTTP.Routers["forge-site-public-public.example.test-http"]
	if publicHTTP == nil {
		t.Fatal("unrestricted site lost its HTTP redirect router")
	}
	assertStringSlice(t, publicHTTP.EntryPoints, []string{publicHTTPEntrypoint})
	assertStringSlice(t, publicHTTP.Middlewares, []string{"forge-https-redirect"})
	assertRedirectMiddleware(t, cfg, "forge-https-redirect")
	if publicHTTP.Service != "forge-public.example.test-site-public-service" {
		t.Fatalf("public redirect service = %q", publicHTTP.Service)
	}
}

func TestWebsecureEntrypointDoesNotCountAsWeb(t *testing.T) {
	cfg := generateFromFixture(t, true, "", []fixtureSite{
		{
			id:   "site-secure",
			name: "secure.example.test",
			tags: []string{"traefik:entry-points=websecure,internal"},
			domains: []fixtureDomain{
				{name: "secure.example.test"},
			},
		},
	})

	router := cfg.HTTP.Routers["forge-site-secure-secure.example.test"]
	if router == nil {
		t.Fatal("missing TLS router for secure.example.test")
	}
	assertStringSlice(t, router.EntryPoints, []string{"websecure", "internal"})
	assertNoCleartextRouter(t, cfg, "forge-site-secure-secure.example.test")
	assertHostsStayOffPublicHTTP(t, cfg, "secure.example.test")
}

func TestSynthesizedSiteNameStaysOffPublicHTTP(t *testing.T) {
	// An empty /domains list falls back to the Forge site name. That name is
	// still the site's hostname, so the entrypoint tag applies to it too.
	cfg := generateFromFixture(t, true, "", []fixtureSite{
		{
			id:   "site-fallback",
			name: "fallback.example.test",
			tags: []string{"traefik:entrypoints=internal"},
		},
	})

	router := cfg.HTTP.Routers["forge-site-fallback-fallback.example.test"]
	if router == nil {
		t.Fatal("missing router synthesized from the site name")
	}
	assertStringSlice(t, router.EntryPoints, []string{"internal"})
	if router.Rule != "Host(`fallback.example.test`)" {
		t.Fatalf("synthesized rule = %q", router.Rule)
	}
	assertNoCleartextRouter(t, cfg, "forge-site-fallback-fallback.example.test")
	assertHostsStayOffPublicHTTP(t, cfg, "fallback.example.test")
}

func TestExternalRedirectMiddlewareStaysOffRestrictedEntrypoint(t *testing.T) {
	cfg := generateFromFixture(t, true, "shared-https-redirect", []fixtureSite{
		{
			id:   "site-shared",
			name: "shared.example.test",
			tags: []string{"traefik:entrypoints=internal"},
			domains: []fixtureDomain{
				{name: "shared.example.test"},
			},
		},
	})

	router := cfg.HTTP.Routers["forge-site-shared-shared.example.test"]
	if router == nil {
		t.Fatal("missing TLS router for shared.example.test")
	}
	assertStringSlice(t, router.EntryPoints, []string{"internal"})
	if _, ok := cfg.HTTP.Middlewares["forge-https-redirect"]; ok {
		t.Fatal("plugin created forge-https-redirect even though a middleware name was configured")
	}
	assertNoCleartextRouter(t, cfg, "forge-site-shared-shared.example.test")
	assertHostsStayOffPublicHTTP(t, cfg, "shared.example.test")
}

func TestSiteHTTPRedirectWithoutGlobalMiddlewareStaysOffWeb(t *testing.T) {
	// The site tag turns the cleartext router on while the provider has no
	// redirect middleware. That router must not be published on "web", and it
	// must not point at the origin with an empty middleware list.
	cfg := generateFromFixture(t, false, "", []fixtureSite{
		{
			id:   "site-flag",
			name: "flag.example.test",
			tags: []string{
				"traefik:entrypoints=internal",
				"traefik:http-redirect=true",
			},
			domains: []fixtureDomain{
				{name: "flag.example.test"},
			},
		},
	})

	router := cfg.HTTP.Routers["forge-site-flag-flag.example.test"]
	if router == nil || router.TLS == nil {
		t.Fatal("missing TLS router for flag.example.test")
	}
	assertStringSlice(t, router.EntryPoints, []string{"internal"})
	assertNoCleartextRouter(t, cfg, "forge-site-flag-flag.example.test")
	assertHostsStayOffPublicHTTP(t, cfg, "flag.example.test")

	origin := "http://10.0.0.8:80"
	for name, rt := range cfg.HTTP.Routers {
		if !routerUsesEntrypoint(rt, publicHTTPEntrypoint) {
			continue
		}
		if rt.Service == "" || len(rt.Middlewares) > 0 {
			continue
		}
		svc := cfg.HTTP.Services[rt.Service]
		if svc == nil || svc.LoadBalancer == nil || len(svc.LoadBalancer.Servers) == 0 {
			continue
		}
		if svc.LoadBalancer.Servers[0].URL == origin {
			t.Errorf("router %q proxies %s on %s with no redirect middleware", name, origin, publicHTTPEntrypoint)
		}
	}
}

func generateFromFixture(t *testing.T, httpRedirect bool, redirectMiddleware string, sites []fixtureSite) *dynamic.Configuration {
	t.Helper()

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/api/orgs/wickedsick/servers":
			if r.URL.RawQuery != "include=tags" {
				t.Errorf("servers query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{
				"data": [{
					"id": "srv-1",
					"type": "servers",
					"attributes": {"name": "php01", "private_ip_address": "10.0.0.8", "status": "active"}
				}],
				"included": [],
				"links": [],
				"meta": []
			}`))
		case "/api/orgs/wickedsick/servers/srv-1/sites":
			if r.URL.RawQuery != "include=tags" {
				t.Errorf("sites query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(sitesDocument(sites)))
		default:
			siteID, kind, ok := fixturePath(r.URL.Path)
			if !ok {
				t.Errorf("unexpected request %s", r.URL.RequestURI())
				http.NotFound(w, r)
				return
			}
			switch kind {
			case "domains":
				_, _ = w.Write([]byte(domainsDocument(siteByID(sites, siteID))))
			case "reverb":
				// No Reverb integration. 404 is "not there", not a failed poll.
				w.WriteHeader(http.StatusNotFound)
			default:
				t.Errorf("unexpected request %s", r.URL.RequestURI())
				http.NotFound(w, r)
			}
		}
	})

	cfg := CreateConfig()
	cfg.DefaultCertResolver = "cloudflare"
	cfg.HTTPRedirect = httpRedirect
	cfg.RedirectMiddleware = redirectMiddleware

	provider, err := NewProviderWithClient(cfg, client)
	if err != nil {
		t.Fatalf("NewProviderWithClient() error = %v", err)
	}
	got, err := provider.GenerateConfiguration()
	if err != nil {
		t.Fatalf("GenerateConfiguration() error = %v", err)
	}
	return got
}

func fixturePath(path string) (siteID, kind string, ok bool) {
	const prefix = "/api/orgs/wickedsick/servers/srv-1/sites/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	siteID, kind, ok = strings.Cut(rest, "/")
	if !ok || siteID == "" {
		return "", "", false
	}
	switch kind {
	case "domains", "integrations/reverb":
		if kind == "integrations/reverb" {
			kind = "reverb"
		}
		return siteID, kind, true
	default:
		return "", "", false
	}
}

func siteByID(sites []fixtureSite, id string) fixtureSite {
	for _, site := range sites {
		if site.id == id {
			return site
		}
	}
	return fixtureSite{}
}

func sitesDocument(sites []fixtureSite) string {
	var data []string
	var included []string
	tagN := 0
	for _, site := range sites {
		var refs []string
		for _, tag := range site.tags {
			tagN++
			id := fmt.Sprintf("tag-%d", tagN)
			refs = append(refs, fmt.Sprintf(`{"type":"tags","id":%q}`, id))
			included = append(included, fmt.Sprintf(
				`{"type":"tags","id":%q,"attributes":{"name":%q}}`, id, tag))
		}
		data = append(data, fmt.Sprintf(`{
			"id": %q,
			"type": "sites",
			"attributes": {"name": %q, "status": "installed"},
			"relationships": {"tags": {"data": [%s]}}
		}`, site.id, site.name, strings.Join(refs, ",")))
	}
	return fmt.Sprintf(`{"data":[%s],"included":[%s],"links":[],"meta":[]}`,
		strings.Join(data, ","), strings.Join(included, ","))
}

func domainsDocument(site fixtureSite) string {
	if len(site.domains) == 0 {
		return `{"data":[],"links":[],"meta":[]}`
	}
	var data []string
	for i, domain := range site.domains {
		www := domain.wwwType
		if www == "" {
			www = "none"
		}
		data = append(data, fmt.Sprintf(`{
			"id": "dom-%s-%d",
			"type": "domainRecords",
			"attributes": {
				"name": %q,
				"type": "primary",
				"status": "enabled",
				"allow_wildcard_subdomains": %t,
				"www_redirect_type": %q
			}
		}`, site.id, i, domain.name, domain.wildcard, www))
	}
	return fmt.Sprintf(`{"data":[%s],"links":[],"meta":[]}`, strings.Join(data, ","))
}

func assertNoCleartextRouter(t *testing.T, cfg *dynamic.Configuration, routerName string) {
	t.Helper()
	if rt := cfg.HTTP.Routers[routerName+"-http"]; rt != nil {
		t.Errorf("cleartext router %q entrypoints=%v middlewares=%v rule=%q service=%q",
			routerName+"-http", rt.EntryPoints, rt.Middlewares, rt.Rule, rt.Service)
	}
}

func assertHostsStayOffPublicHTTP(t *testing.T, cfg *dynamic.Configuration, hosts ...string) {
	t.Helper()
	for name, rt := range cfg.HTTP.Routers {
		if !routerUsesEntrypoint(rt, publicHTTPEntrypoint) {
			continue
		}
		for _, host := range hosts {
			if ruleMentionsHost(rt.Rule, host) {
				t.Errorf("router %q publishes %q on %s (entrypoints=%v middlewares=%v rule=%q)",
					name, host, publicHTTPEntrypoint, rt.EntryPoints, rt.Middlewares, rt.Rule)
			}
		}
	}
}

func assertRedirectMiddleware(t *testing.T, cfg *dynamic.Configuration, name string) {
	t.Helper()
	mw := cfg.HTTP.Middlewares[name]
	if mw == nil || mw.RedirectScheme == nil {
		t.Fatalf("middleware %q = %+v, want redirectScheme", name, mw)
	}
	if mw.RedirectScheme.Scheme != "https" || !mw.RedirectScheme.Permanent {
		t.Fatalf("redirectScheme = %+v, want https permanent", mw.RedirectScheme)
	}
}

// ruleMentionsHost matches an exact host clause, not a longer name that merely
// contains that host as a suffix. Wildcard clauses escape dots, so those are
// matched too.
func ruleMentionsHost(rule, host string) bool {
	if strings.Contains(rule, "`"+host+"`") {
		return true
	}
	return strings.Contains(rule, strings.ReplaceAll(host, ".", `\.`))
}

func routerUsesEntrypoint(rt *dynamic.Router, name string) bool {
	for _, ep := range rt.EntryPoints {
		if ep == name {
			return true
		}
	}
	return false
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
