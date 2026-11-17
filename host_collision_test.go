package traefik_laravel_forge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/traefik/genconf/dynamic"
)

// These tests are the spec for a missing hostname canonicalization bug.
// They fail while a trailing dot, or a case-only variant, of a hostname another
// site already owns is published as its own router. Traefik v3 treats that
// router as the real host (pkg/muxer/http/matcher.go host, v3.7.19 lines 87-117)
// and serves the higher user priority first (pkg/muxer/http/mux.go routes.Less,
// lines 216-218; pkg/server/router/router.go buildEntryPointHandler, lines 246-282).
// HTTPS routers and HTTP routers are separate muxers split on TLS
// (pkg/config/runtime/runtime_http.go GetRoutersByEntryPoints, lines 21-24).

const (
	victimBackend   = "http://10.0.0.1:80"
	attackerBackend = "http://10.0.0.1:9090"
	otherBackend    = "http://10.0.0.2:80"
	ownedHost       = "shop.example.com"
)

type collisionServer struct {
	id   string
	name string
	ip   string
}

type collisionSite struct {
	id      string
	name    string
	tags    []string
	domains []ForgeDomain
	reverb  *ForgeReverbIntegration
}

func TestTrailingDotAndCaseDoNotCaptureOwnedHost(t *testing.T) {
	t.Parallel()

	victimDomains := []ForgeDomain{ownedDomain(ownedHost, false, "")}
	victim := collisionSite{id: "victim", name: "victim-app", domains: victimDomains}

	tests := []struct {
		name            string
		attackerFirst   bool
		separateServer  bool
		victim          collisionSite
		attacker        collisionSite
		httpsHost       string
		httpHost        string
		httpsMustHave   bool
		httpMustHave    bool
		subdomainHost   string
		subdomainServer string
	}{
		{
			name:          "alias_trailing_dot_same_server",
			victim:        victim,
			attacker:      siteWithAlias("attacker-app", "traefik:aliases=shop.example.com.", "traefik:port=9090"),
			httpsHost:     ownedHost,
			httpHost:      ownedHost,
			httpsMustHave: true,
			httpMustHave:  true,
		},
		{
			name:          "alias_trailing_dot_attacker_listed_first",
			attackerFirst: true,
			victim:        victim,
			attacker:      siteWithAlias("attacker-app", "traefik:aliases=shop.example.com.", "traefik:port=9090"),
			httpsHost:     ownedHost,
			httpHost:      ownedHost,
			httpsMustHave: true,
			httpMustHave:  true,
		},
		{
			name:           "alias_trailing_dot_other_server",
			separateServer: true,
			victim:         victim,
			attacker:       siteWithAlias("attacker-app", "traefik:aliases=shop.example.com."),
			httpsHost:      ownedHost,
			httpHost:       ownedHost,
			httpsMustHave:  true,
			httpMustHave:   true,
		},
		{
			name:   "domain_record_trailing_dot",
			victim: victim,
			attacker: collisionSite{
				id:      "attacker",
				name:    "attacker-app",
				tags:    []string{"traefik:port=9090"},
				domains: []ForgeDomain{ownedDomain("shop.example.com.", false, "")},
			},
			httpsHost:     ownedHost,
			httpHost:      ownedHost,
			httpsMustHave: true,
			httpMustHave:  true,
		},
		{
			name: "domain_www_redirect_trailing_dot",
			victim: collisionSite{
				id:      "victim",
				name:    "victim-app",
				domains: []ForgeDomain{ownedDomain(ownedHost, false, "from-www")},
			},
			attacker: collisionSite{
				id:      "attacker",
				name:    "attacker-app",
				tags:    []string{"traefik:port=9090"},
				domains: []ForgeDomain{ownedDomain("shop.example.com.", false, "from-www")},
			},
			httpsHost:     "www.shop.example.com",
			httpHost:      "www.shop.example.com",
			httpsMustHave: true,
			httpMustHave:  true,
		},
		{
			name: "wildcard_domain_trailing_dot",
			victim: collisionSite{
				id:      "victim",
				name:    "victim-app",
				domains: []ForgeDomain{ownedDomain(ownedHost, true, "")},
			},
			attacker: collisionSite{
				id:      "attacker",
				name:    "attacker-app",
				tags:    []string{"traefik:port=9090"},
				domains: []ForgeDomain{ownedDomain("shop.example.com.", true, "")},
			},
			httpsHost:       ownedHost,
			httpHost:        ownedHost,
			httpsMustHave:   true,
			httpMustHave:    true,
			subdomainHost:   "foo.shop.example.com",
			subdomainServer: victimBackend,
		},
		{
			name:   "reverb_domain_trailing_dot",
			victim: victim,
			attacker: collisionSite{
				id:      "attacker",
				name:    "attacker-app",
				tags:    []string{"traefik:port=9090"},
				domains: []ForgeDomain{ownedDomain("shop.example.com.", false, "")},
				reverb:  &ForgeReverbIntegration{Enabled: true, Host: "shop.example.com.", Port: 8081},
			},
			httpsHost:     ownedHost,
			httpHost:      ownedHost,
			httpsMustHave: true,
			httpMustHave:  true,
		},
		{
			name:   "site_name_fallback_trailing_dot",
			victim: victim,
			attacker: collisionSite{
				id:   "attacker",
				name: "shop.example.com.",
				tags: []string{"traefik:port=9090"},
			},
			httpsHost:     ownedHost,
			httpHost:      ownedHost,
			httpsMustHave: true,
			httpMustHave:  true,
		},
		{
			name: "on_forge_domain_trailing_dot",
			victim: collisionSite{
				id:      "victim",
				name:    "victim-app",
				tags:    []string{"traefik:forge-domain=true"},
				domains: []ForgeDomain{ownedDomain("app.on-forge.com", false, "")},
			},
			attacker: collisionSite{
				id:      "attacker",
				name:    "attacker-app",
				tags:    []string{"traefik:port=9090"},
				domains: []ForgeDomain{ownedDomain("app.on-forge.com.", false, "")},
			},
			httpsHost:     "app.on-forge.com",
			httpHost:      "app.on-forge.com",
			httpsMustHave: false,
			httpMustHave:  true,
		},
		{
			name:          "alias_case_variant_attacker_listed_first",
			attackerFirst: true,
			victim:        victim,
			attacker: collisionSite{
				id:   "attacker",
				name: "attacker-app",
				tags: []string{"traefik:aliases=Shop.Example.com", "traefik:port=9090"},
				domains: []ForgeDomain{
					ownedDomain("attacker.example", false, ""),
				},
			},
			httpsHost:     ownedHost,
			httpHost:      ownedHost,
			httpsMustHave: true,
			httpMustHave:  true,
		},
		{
			name:   "alias_case_variant",
			victim: victim,
			attacker: collisionSite{
				id:   "attacker",
				name: "attacker-app",
				tags: []string{"traefik:aliases=Shop.Example.com", "traefik:port=9090"},
				domains: []ForgeDomain{
					ownedDomain("attacker.example", false, ""),
				},
			},
			httpsHost:     ownedHost,
			httpHost:      ownedHost,
			httpsMustHave: true,
			httpMustHave:  true,
		},
		{
			name:   "request_host_trailing_dot",
			victim: victim,
			attacker: collisionSite{
				id:   "attacker",
				name: "attacker-app",
				tags: []string{"traefik:aliases=shop.example.com.", "traefik:port=9090"},
				domains: []ForgeDomain{
					ownedDomain("attacker.example", false, ""),
				},
			},
			httpsHost:     "shop.example.com.",
			httpHost:      "shop.example.com.",
			httpsMustHave: true,
			httpMustHave:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			attackerBackendURL := attackerBackend
			servers := []collisionServer{{id: "srv-1", name: "php01", ip: "10.0.0.1"}}
			sitesByServer := map[string][]collisionSite{"srv-1": {tt.victim, tt.attacker}}
			if tt.attackerFirst {
				sitesByServer["srv-1"] = []collisionSite{tt.attacker, tt.victim}
			}
			if tt.separateServer {
				attackerBackendURL = otherBackend
				servers = []collisionServer{
					{id: "srv-victim", name: "php01", ip: "10.0.0.1"},
					{id: "srv-attacker", name: "php02", ip: "10.0.0.2"},
				}
				if tt.attackerFirst {
					servers[0], servers[1] = servers[1], servers[0]
				}
				sitesByServer = map[string][]collisionSite{
					"srv-victim":   {tt.victim},
					"srv-attacker": {tt.attacker},
				}
			}

			cfg := generateFromForgeJSON(t, servers, sitesByServer)
			assertOwned(t, cfg, true, "websecure", tt.httpsHost, victimBackend, attackerBackendURL, tt.httpsMustHave)
			assertOwned(t, cfg, false, "web", tt.httpHost, victimBackend, attackerBackendURL, tt.httpMustHave)
			if tt.subdomainHost != "" {
				assertOwned(t, cfg, true, "websecure", tt.subdomainHost, tt.subdomainServer, attackerBackendURL, true)
				assertOwned(t, cfg, false, "web", tt.subdomainHost, tt.subdomainServer, attackerBackendURL, true)
			}
		})
	}
}

func siteWithAlias(name string, tags ...string) collisionSite {
	return collisionSite{
		id:      "attacker",
		name:    name,
		tags:    tags,
		domains: []ForgeDomain{ownedDomain("attacker.example", false, "")},
	}
}

func ownedDomain(name string, wildcard bool, www string) ForgeDomain {
	return ForgeDomain{
		ID:   "dom-" + name,
		Type: "domainRecords",
		Attributes: ForgeDomainAttributes{
			Name:                    name,
			DomainType:              "alias",
			Status:                  "enabled",
			AllowWildcardSubdomains: wildcard,
			WWWRedirectType:         www,
		},
	}
}

func generateFromForgeJSON(t *testing.T, servers []collisionServer, sitesByServer map[string][]collisionSite) *dynamic.Configuration {
	t.Helper()

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeForgeFixture(t, w, r, servers, sitesByServer)
	})

	cfg := CreateConfig()
	cfg.DefaultCertResolver = "cloudflare"
	cfg.HTTPRedirect = true
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

func writeForgeFixture(t *testing.T, w http.ResponseWriter, r *http.Request, servers []collisionServer, sitesByServer map[string][]collisionSite) {
	t.Helper()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// api / orgs / {org} / servers [/ {server} / sites [/ {site} / domains|integrations / reverb]]
	if len(parts) == 4 && parts[3] == "servers" {
		writeJSON(t, w, serversDocument(servers))
		return
	}
	if len(parts) == 6 && parts[3] == "servers" && parts[5] == "sites" {
		writeJSON(t, w, sitesDocument(sitesByServer[parts[4]]))
		return
	}
	if len(parts) == 8 && parts[7] == "domains" {
		site := findSite(sitesByServer[parts[4]], parts[6])
		body := ForgeDomainsResponse{Data: site.domains, Links: []any{}, Meta: []any{}}
		writeJSON(t, w, body)
		return
	}
	if len(parts) == 9 && parts[7] == "integrations" && parts[8] == "reverb" {
		site := findSite(sitesByServer[parts[4]], parts[6])
		if site.reverb == nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"data": map[string]any{"attributes": site.reverb},
		})
		return
	}

	http.NotFound(w, r)
}

func findSite(sites []collisionSite, id string) collisionSite {
	for _, site := range sites {
		if site.id == id {
			return site
		}
	}
	return collisionSite{}
}

func serversDocument(servers []collisionServer) ForgeServersResponse {
	resp := ForgeServersResponse{Links: []any{}, Meta: []any{}}
	for _, server := range servers {
		resp.Data = append(resp.Data, ForgeServer{
			ID:   server.id,
			Type: "servers",
			Attributes: ForgeServerAttributes{
				Name:             server.name,
				PrivateIPAddress: server.ip,
			},
		})
	}
	return resp
}

func sitesDocument(sites []collisionSite) ForgeSitesResponse {
	resp := ForgeSitesResponse{Links: []any{}, Meta: []any{}}
	for _, site := range sites {
		raw := ForgeSite{
			ID:   site.id,
			Type: "sites",
			Attributes: ForgeSiteAttributes{
				Name:   site.name,
				Status: "installed",
			},
		}
		for i, tag := range site.tags {
			id := fmt.Sprintf("%s-tag-%d", site.id, i)
			raw.Relationships.Tags.Data = append(raw.Relationships.Tags.Data, ForgeTagRef{Type: "tags", ID: id})
			resp.Included = append(resp.Included, map[string]any{
				"type":       "tags",
				"id":         id,
				"attributes": map[string]any{"name": tag},
			})
		}
		resp.Data = append(resp.Data, raw)
	}
	return resp
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode fixture: %v", err)
	}
}

// assertOwned fails when any router Traefik would place on this muxer matches
// requestHost and forwards to a backend other than the site that owns the name.
// Matching follows Traefik v3.7.19 host() with CNAME flattening left off, which
// is the default (static HostResolver is nil; RequestDecorator only flattens
// when that resolver is set).
func assertOwned(t *testing.T, cfg *dynamic.Configuration, https bool, entryPoint, requestHost, ownerURL, attackerURL string, ownerMustMatch bool) {
	t.Helper()

	var matched []string
	ownerMatched := false
	for name, router := range cfg.HTTP.Routers {
		if !onMux(router, https, entryPoint) || !ruleMatches(router.Rule, requestHost) {
			continue
		}
		backend := serviceURL(cfg, router.Service)
		matched = append(matched, fmt.Sprintf("%s priority=%d rule=%s service=%s backend=%s",
			name, router.Priority, router.Rule, router.Service, backend))
		switch {
		case backend == attackerURL:
			t.Errorf("%s %s %q is proxied to %s via %s priority %d rule %s",
				scheme(https), entryPoint, requestHost, backend, name, router.Priority, router.Rule)
		case backend != ownerURL:
			t.Errorf("%s %s %q is proxied to unexpected backend %s via %s",
				scheme(https), entryPoint, requestHost, backend, name)
		default:
			ownerMatched = true
		}
	}
	if ownerMustMatch && !ownerMatched {
		t.Errorf("%s %s %q has no router to the owning backend %s; routers: %v",
			scheme(https), entryPoint, requestHost, ownerURL, matched)
	}
}

func scheme(https bool) string {
	if https {
		return "https"
	}
	return "http"
}

func onMux(router *dynamic.Router, https bool, entryPoint string) bool {
	if router == nil {
		return false
	}
	if https && router.TLS == nil {
		return false
	}
	if !https && router.TLS != nil {
		return false
	}
	return slices.Contains(router.EntryPoints, entryPoint)
}

func serviceURL(cfg *dynamic.Configuration, name string) string {
	svc := cfg.HTTP.Services[name]
	if svc == nil || svc.LoadBalancer == nil || len(svc.LoadBalancer.Servers) == 0 {
		return ""
	}
	return svc.LoadBalancer.Servers[0].URL
}

func ruleMatches(rule, requestHost string) bool {
	for _, host := range hostLiterals(rule) {
		if traefikHostMatch(host, requestHost) {
			return true
		}
	}
	canonical := canonicalRequestHost(requestHost)
	for _, pattern := range hostRegexps(rule) {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		if re.MatchString(canonical) {
			return true
		}
	}
	return false
}

func hostLiterals(rule string) []string {
	return quotedMatchers(rule, "Host(`")
}

func hostRegexps(rule string) []string {
	return quotedMatchers(rule, "HostRegexp(`")
}

func quotedMatchers(rule, opener string) []string {
	var out []string
	rest := rule
	for {
		i := strings.Index(rest, opener)
		if i < 0 {
			return out
		}
		// Host(` must not consume the prefix of HostRegexp(`.
		if opener == "Host(`" {
			if j := strings.Index(rest, "HostRegexp(`"); j >= 0 && j < i {
				rest = rest[j+len("HostRegexp(`"):]
				if end := strings.Index(rest, "`)"); end >= 0 {
					rest = rest[end+2:]
				}
				continue
			}
		}
		rest = rest[i+len(opener):]
		end := strings.Index(rest, "`)")
		if end < 0 {
			return out
		}
		out = append(out, rest[:end])
		rest = rest[end+2:]
	}
}

func canonicalRequestHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

// traefikHostMatch is the Host matcher from Traefik v3.7.19
// pkg/muxer/http/matcher.go lines 87-117, with the CNAME-flatten branch omitted
// because a nil HostResolver never stores one (request_decorator.go lines 42-45).
func traefikHostMatch(hostExpr, reqHost string) bool {
	hostExpr = strings.ToLower(hostExpr)
	reqHost = canonicalRequestHost(reqHost)
	if hostExpr == "*" {
		return true
	}
	if domainMatch(reqHost, hostExpr) {
		return true
	}
	if last := len(hostExpr) - 1; last >= 0 && hostExpr[last] == '.' && domainMatch(reqHost, hostExpr[:last]) {
		return true
	}
	if last := len(reqHost) - 1; last >= 0 && reqHost[last] == '.' && domainMatch(reqHost[:last], hostExpr) {
		return true
	}
	return false
}

// domainMatch is muxer.DomainMatchHostExpression from Traefik v3.7.19
// pkg/muxer/muxer.go lines 23-31.
func domainMatch(domain, hostExpr string) bool {
	if strings.HasPrefix(hostExpr, "*") {
		labels := strings.Split(domain, ".")
		if len(labels) == 0 {
			return false
		}
		labels[0] = "*"
		return strings.EqualFold(hostExpr, strings.Join(labels, "."))
	}
	return strings.EqualFold(domain, hostExpr)
}
