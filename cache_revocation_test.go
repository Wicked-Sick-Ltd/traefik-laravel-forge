package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/traefik/genconf/dynamic"
)

// These tests are the spec for the domain-cache revocation window. They go
// through the real Forge client and sendConfiguration, which is what each poll
// hands to Traefik. They fail while an unexpired cache is reused on a healthy
// poll, and they pass once a healthy poll publishes the domain list and Reverb
// host Forge just returned.
//
// Yaegi-compatible: no testify. See CLAUDE.md.

const (
	revocationCertResolver = "cloudflare"
	revocationOrg          = "wickedsick"
	revocationToken        = "placeholder-token"
)

// forgeAPI is a JSON:API stand-in for the current Forge records. The provider
// under test is a real forgeHTTPClient pointed at it, so decoding, the Reverb
// enabled/host/port filter, and tag mapping all run.
type forgeAPI struct {
	mu         sync.Mutex
	servers    []*apiServer
	domainHits map[string]int
	reverbHits map[string]int
	unknown    []string
}

type apiServer struct {
	ID        string
	Name      string
	PrivateIP string
	Sites     []*apiSite
}

type apiSite struct {
	ID           string
	Name         string
	Tags         []string
	Domains      []ForgeDomain
	DomainStatus int // 0 means 200. 429 refuses the domains call.
	ReverbStatus int // 0 means 200. 404 is absent. 429 refuses the call.
	ReverbHost   string
	ReverbPort   int
}

func newForgeAPI(servers ...*apiServer) *forgeAPI {
	return &forgeAPI{
		servers:    servers,
		domainHits: map[string]int{},
		reverbHits: map[string]int{},
	}
}

func (a *forgeAPI) siteLocked(serverID, siteID string) *apiSite {
	for _, server := range a.servers {
		if server.ID != serverID {
			continue
		}
		for _, site := range server.Sites {
			if site.ID == siteID {
				return site
			}
		}
	}
	return nil
}

func (a *forgeAPI) setDomains(serverID, siteID string, domains []ForgeDomain) {
	a.mu.Lock()
	defer a.mu.Unlock()
	site := a.siteLocked(serverID, siteID)
	if site == nil {
		panic("setDomains: unknown site " + serverID + "/" + siteID)
	}
	site.Domains = domains
	site.DomainStatus = 0
}

func (a *forgeAPI) setDomainStatus(serverID, siteID string, status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.siteLocked(serverID, siteID).DomainStatus = status
}

func (a *forgeAPI) setReverb(serverID, siteID, host string, port, status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	site := a.siteLocked(serverID, siteID)
	site.ReverbHost = host
	site.ReverbPort = port
	site.ReverbStatus = status
}

func (a *forgeAPI) removeSite(serverID, siteID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, server := range a.servers {
		if server.ID != serverID {
			continue
		}
		kept := server.Sites[:0]
		for _, site := range server.Sites {
			if site.ID != siteID {
				kept = append(kept, site)
			}
		}
		server.Sites = kept
	}
}

func (a *forgeAPI) domainCalls(serverID, siteID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.domainHits[serverID+"/"+siteID]
}

func (a *forgeAPI) reverbCalls(serverID, siteID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reverbHits[serverID+"/"+siteID]
}

func (a *forgeAPI) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// /api/orgs/{org}/servers[/{server}/sites[/{site}/domains|/integrations/reverb]]
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "orgs" || parts[2] != revocationOrg || parts[3] != "servers" {
		a.reject(w, r)
		return
	}
	if len(parts) == 4 {
		a.writeServers(w)
		return
	}
	serverID := parts[4]
	if len(parts) >= 6 && parts[5] == "sites" && len(parts) == 6 {
		a.writeSites(w, serverID)
		return
	}
	if len(parts) == 8 && parts[5] == "sites" && parts[7] == "domains" {
		a.writeDomains(w, serverID, parts[6])
		return
	}
	if len(parts) == 9 && parts[5] == "sites" && parts[7] == "integrations" && parts[8] == "reverb" {
		a.writeReverb(w, serverID, parts[6])
		return
	}
	a.reject(w, r)
}

func (a *forgeAPI) reject(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.unknown = append(a.unknown, r.Method+" "+r.URL.RequestURI())
	a.mu.Unlock()
	http.NotFound(w, r)
}

func (a *forgeAPI) writeServers(w http.ResponseWriter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	data := make([]ForgeServer, 0, len(a.servers))
	for _, server := range a.servers {
		item := ForgeServer{ID: server.ID, Type: "servers"}
		item.Attributes.Name = server.Name
		item.Attributes.PrivateIPAddress = server.PrivateIP
		data = append(data, item)
	}
	writeForgeJSON(w, ForgeServersResponse{Data: data, Included: []any{}, Links: []any{}, Meta: []any{}})
}

func (a *forgeAPI) writeSites(w http.ResponseWriter, serverID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var data []ForgeSite
	var included []any
	for _, server := range a.servers {
		if server.ID != serverID {
			continue
		}
		for _, site := range server.Sites {
			item := ForgeSite{ID: site.ID, Type: "sites"}
			item.Attributes.Name = site.Name
			item.Attributes.Status = statusInstalled
			for i, tag := range site.Tags {
				tagID := site.ID + "-tag-" + fmt.Sprint(i)
				item.Relationships.Tags.Data = append(item.Relationships.Tags.Data, ForgeTagRef{Type: "tags", ID: tagID})
				included = append(included, map[string]any{
					"type":       "tags",
					"id":         tagID,
					"attributes": map[string]any{"name": tag},
				})
			}
			data = append(data, item)
		}
	}
	writeForgeJSON(w, ForgeSitesResponse{Data: data, Included: included, Links: []any{}, Meta: []any{}})
}

func (a *forgeAPI) writeDomains(w http.ResponseWriter, serverID, siteID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.domainHits[serverID+"/"+siteID]++
	site := a.siteLocked(serverID, siteID)
	if site == nil {
		http.NotFound(w, &http.Request{RequestURI: "domains"})
		return
	}
	if site.DomainStatus != 0 && site.DomainStatus != http.StatusOK {
		w.WriteHeader(site.DomainStatus)
		_, _ = w.Write([]byte(`{"message":"Too many attempts."}`))
		return
	}
	// Copy so a later mutation of the fixture cannot race the encoder.
	domains := append([]ForgeDomain(nil), site.Domains...)
	writeForgeJSON(w, ForgeDomainsResponse{Data: domains, Links: []any{}, Meta: []any{}})
}

func (a *forgeAPI) writeReverb(w http.ResponseWriter, serverID, siteID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reverbHits[serverID+"/"+siteID]++
	site := a.siteLocked(serverID, siteID)
	if site == nil {
		http.NotFound(w, &http.Request{RequestURI: "reverb"})
		return
	}
	status := site.ReverbStatus
	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"Too many attempts."}`))
		return
	}
	body := ForgeReverbResponse{}
	body.Data.Attributes = ForgeReverbIntegration{
		Enabled: site.ReverbHost != "" && site.ReverbPort != 0,
		Host:    site.ReverbHost,
		Port:    site.ReverbPort,
	}
	writeForgeJSON(w, body)
}

func writeForgeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func apiServerWith(id, ip string, sites ...*apiSite) *apiServer {
	return &apiServer{ID: id, Name: id, PrivateIP: ip, Sites: sites}
}

func apiSiteWith(id, name string, tags []string, domains []ForgeDomain) *apiSite {
	return &apiSite{ID: id, Name: name, Tags: tags, Domains: domains, ReverbStatus: http.StatusNotFound}
}

func domainRecord(id, name, kind string, wildcard bool, www string) ForgeDomain {
	d := ForgeDomain{ID: id, Type: "domainRecords"}
	d.Attributes.Name = name
	d.Attributes.DomainType = kind
	d.Attributes.Status = statusEnabled
	d.Attributes.AllowWildcardSubdomains = wildcard
	d.Attributes.WWWRedirectType = www
	return d
}

// revocationProvider is the owner's default provider: 30s poll, 10m cache,
// HTTP redirect, and the cloudflare cert resolver. CacheTTL is left at the
// value CreateConfig ships, which is what Traefik keeps when static config
// omits the key (it decodes into the CreateConfig result).
func revocationProvider(t *testing.T, api *forgeAPI, clock *time.Time) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)

	client := newForgeHTTPClient(revocationToken, revocationOrg)
	client.baseURL = srv.URL

	config := CreateConfig()
	config.DefaultCertResolver = revocationCertResolver
	config.HTTPRedirect = true
	p, err := NewProviderWithClient(config, client)
	if err != nil {
		t.Fatal(err)
	}
	if p.cacheTTL != 10*time.Minute {
		t.Fatalf("cacheTTL = %s, want the default 10m", p.cacheTTL)
	}
	if p.pollInterval != 30*time.Second {
		t.Fatalf("pollInterval = %s, want the default 30s", p.pollInterval)
	}
	p.now = func() time.Time { return *clock }
	return p
}

// publishedConfig runs one poll the way loadConfiguration does and returns the
// configuration that would be sent to Traefik. Sending nothing keeps the
// previous routers in place, so an empty send is a failure for a revocation.
func publishedConfig(t *testing.T, p *Provider) *dynamic.Configuration {
	t.Helper()
	cfgChan := make(chan json.Marshaler, 1)
	p.sendConfiguration(context.Background(), cfgChan)
	select {
	case msg := <-cfgChan:
		payload, ok := msg.(*dynamic.JSONPayload)
		if !ok || payload == nil || payload.Configuration == nil {
			t.Fatalf("poll sent %#v, want a configuration", msg)
		}
		return payload.Configuration
	default:
		t.Fatal("poll sent nothing; Traefik keeps the previous configuration, including any hostname that poll was supposed to drop")
	}
	return nil
}

type hostHit struct {
	name    string
	rule    string
	service string
	urls    []string
	cert    string
}

func hostRouters(cfg *dynamic.Configuration, host string) []hostHit {
	var hits []hostHit
	if cfg == nil || cfg.HTTP == nil {
		return nil
	}
	for name, router := range cfg.HTTP.Routers {
		if router == nil || !ruleMentionsHost(router.Rule, host) {
			continue
		}
		hit := hostHit{name: name, rule: router.Rule, service: router.Service}
		if router.TLS != nil {
			hit.cert = router.TLS.CertResolver
		}
		if svc := cfg.HTTP.Services[router.Service]; svc != nil && svc.LoadBalancer != nil {
			for _, server := range svc.LoadBalancer.Servers {
				hit.urls = append(hit.urls, server.URL)
			}
		}
		hits = append(hits, hit)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].name < hits[j].name })
	return hits
}

func ruleMentionsHost(rule, host string) bool {
	if strings.Contains(rule, "Host(`"+host+"`)") || strings.Contains(rule, "Host(`www."+host+"`)") {
		return true
	}
	escaped := strings.ReplaceAll(host, ".", `\.`)
	return strings.Contains(rule, "HostRegexp(`^[^.]+\\."+escaped+"$`)")
}

func formatHits(hits []hostHit) string {
	parts := make([]string, 0, len(hits))
	for _, hit := range hits {
		parts = append(parts, fmt.Sprintf("{router %s rule %q service %s urls %v certResolver %q}",
			hit.name, hit.rule, hit.service, hit.urls, hit.cert))
	}
	return strings.Join(parts, "; ")
}

func requireHostBackend(t *testing.T, cfg *dynamic.Configuration, host, backendURL string) {
	t.Helper()
	hits := hostRouters(cfg, host)
	if len(hits) == 0 {
		t.Fatalf("hostname %s has no router in the published configuration", host)
	}
	var sawCert bool
	for _, hit := range hits {
		if len(hit.urls) != 1 || hit.urls[0] != backendURL {
			t.Fatalf("hostname %s is published to %v, want only %s (%s)", host, hit.urls, backendURL, formatHits(hits))
		}
		if hit.cert != "" {
			sawCert = true
			if hit.cert != revocationCertResolver {
				t.Fatalf("hostname %s cert resolver = %q, want %s", host, hit.cert, revocationCertResolver)
			}
		}
	}
	if !sawCert {
		t.Fatalf("hostname %s has no TLS router, so the cert resolver is not covering it (%s)", host, formatHits(hits))
	}
}

func requireHostAbsent(t *testing.T, cfg *dynamic.Configuration, host string) {
	t.Helper()
	if hits := hostRouters(cfg, host); len(hits) > 0 {
		t.Fatalf("hostname %s is still in the configuration sent to Traefik: %s", host, formatHits(hits))
	}
}

// TestRemovedHostnameLeavesTheNextHealthyPoll is the revocation window. The
// site stays installed; only the domain record disappears. The cache key used
// here sits in the top of the jitter range, so a 30s poll is still inside
// [TTL, 2*TTL) unless the whole jittered lifetime ends inside one poll.
func TestRemovedHostnameLeavesTheNextHealthyPoll(t *testing.T) {
	const (
		serverID = "srv-a"
		siteID   = "revoked"
		kept     = "kept.example.com"
		detached = "detached.example.com"
		backend  = "http://10.0.0.5:80"
	)
	key := cacheKey(serverID, siteID)
	if got := jitterFraction(key); got < 900 {
		t.Fatalf("cache key %s jitter fraction = %d, want >= 900", key, got)
	}

	delays := []time.Duration{
		30 * time.Second,             // the poll interval the docs tell operators to expect
		10*time.Minute - time.Second, // still before the earliest default expiry (TTL + 0)
	}
	for _, delay := range delays {
		t.Run(delay.String(), func(t *testing.T) {
			clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
			detachedRecord := domainRecord("d-detached", detached, "alias", true, "to-www")
			api := newForgeAPI(apiServerWith(serverID, "10.0.0.5",
				apiSiteWith(siteID, "app.internal", nil, []ForgeDomain{
					domainRecord("d-kept", kept, "primary", false, "none"),
					detachedRecord,
				}),
			))
			p := revocationProvider(t, api, &clock)

			first := publishedConfig(t, p)
			requireHostBackend(t, first, kept, backend)
			requireHostBackend(t, first, detached, backend)
			requireHostBackend(t, first, "www."+detached, backend)
			hits := hostRouters(first, detached)
			if len(hits) == 0 || !strings.Contains(hits[0].rule, "HostRegexp(") {
				t.Fatalf("first poll did not publish the wildcard rule: %s", formatHits(hits))
			}
			if api.domainCalls(serverID, siteID) != 1 {
				t.Fatalf("first poll made %d /domains calls, want 1", api.domainCalls(serverID, siteID))
			}

			api.setDomains(serverID, siteID, []ForgeDomain{
				domainRecord("d-kept", kept, "primary", false, "none"),
			})
			clock = clock.Add(delay)

			second := publishedConfig(t, p)
			if calls := api.domainCalls(serverID, siteID); calls < 2 {
				t.Fatalf("healthy poll %s later made %d /domains calls, want at least 2; published %s",
					delay, calls, formatHits(hostRouters(second, detached)))
			}
			requireHostAbsent(t, second, detached)
			requireHostAbsent(t, second, "www."+detached)
			requireHostBackend(t, second, kept, backend)
		})
	}
}

// TestMovedHostnameStopsUsingThePreviousBackend attaches the hostname to a
// different site on the same server. The sites already use different backend
// ports, so the published service URL shows which site still owns the name.
func TestMovedHostnameStopsUsingThePreviousBackend(t *testing.T) {
	const (
		serverID = "srv-move"
		host     = "moved.example.com"
		oldURL   = "http://10.8.0.5:8081"
		newURL   = "http://10.8.0.5:8082"
	)
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	api := newForgeAPI(apiServerWith(serverID, "10.8.0.5",
		apiSiteWith("site-old", "old.example.com", []string{"traefik:port=8081"}, []ForgeDomain{
			domainRecord("d-old", "old.example.com", "primary", false, "none"),
			domainRecord("d-moved", host, "alias", false, "none"),
		}),
		apiSiteWith("site-new", "new.example.com", []string{"traefik:port=8082"}, []ForgeDomain{
			domainRecord("d-new", "new.example.com", "primary", false, "none"),
		}),
	))
	p := revocationProvider(t, api, &clock)

	first := publishedConfig(t, p)
	requireHostBackend(t, first, host, oldURL)
	requireHostBackend(t, first, "new.example.com", newURL)
	if api.reverbCalls(serverID, "site-old") != 1 || api.reverbCalls(serverID, "site-new") != 1 {
		t.Fatalf("first poll Reverb calls old=%d new=%d, want 1",
			api.reverbCalls(serverID, "site-old"), api.reverbCalls(serverID, "site-new"))
	}

	api.setDomains(serverID, "site-old", []ForgeDomain{
		domainRecord("d-old", "old.example.com", "primary", false, "none"),
	})
	api.setDomains(serverID, "site-new", []ForgeDomain{
		domainRecord("d-new", "new.example.com", "primary", false, "none"),
		domainRecord("d-moved", host, "alias", false, "none"),
	})
	clock = clock.Add(30 * time.Second)

	second := publishedConfig(t, p)
	if api.domainCalls(serverID, "site-old") < 2 || api.domainCalls(serverID, "site-new") < 2 {
		t.Fatalf("healthy poll after the move called /domains old=%d new=%d, want at least 2 each; published %s",
			api.domainCalls(serverID, "site-old"), api.domainCalls(serverID, "site-new"),
			formatHits(hostRouters(second, host)))
	}
	requireHostBackend(t, second, host, newURL)
	requireHostBackend(t, second, "old.example.com", oldURL)
}

// TestDisabledDomainRecordIsDroppedOnNextHealthyPoll covers a record that is
// still returned but is no longer status "enabled". classifyDomains skips
// those, so a cached "enabled" copy keeps the router.
func TestDisabledDomainRecordIsDroppedOnNextHealthyPoll(t *testing.T) {
	const (
		serverID = "srv-a"
		siteID   = "site-paused"
		paused   = "paused.example.com"
		backend  = "http://10.0.0.5:80"
	)
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	api := newForgeAPI(apiServerWith(serverID, "10.0.0.5",
		apiSiteWith(siteID, "app.internal", nil, []ForgeDomain{
			domainRecord("d-kept", "kept.example.com", "primary", false, "none"),
			domainRecord("d-paused", paused, "alias", false, "none"),
		}),
	))
	p := revocationProvider(t, api, &clock)
	requireHostBackend(t, publishedConfig(t, p), paused, backend)

	disabled := domainRecord("d-paused", paused, "alias", false, "none")
	disabled.Attributes.Status = "disabled"
	api.setDomains(serverID, siteID, []ForgeDomain{
		domainRecord("d-kept", "kept.example.com", "primary", false, "none"),
		disabled,
	})
	clock = clock.Add(30 * time.Second)

	second := publishedConfig(t, p)
	if hits := hostRouters(second, paused); len(hits) > 0 || api.domainCalls(serverID, siteID) < 2 {
		t.Fatalf("healthy poll made %d /domains calls and still published %s",
			api.domainCalls(serverID, siteID), formatHits(hits))
	}
	requireHostBackend(t, second, "kept.example.com", backend)
}

// TestFreshDomainRecordDropsWildcardAndWWW covers the other fields stored on
// the same cache entry. The hostname stays; its wildcard and www clauses do not.
func TestFreshDomainRecordDropsWildcardAndWWW(t *testing.T) {
	const (
		serverID = "srv-a"
		siteID   = "site-apex"
		host     = "apex.example.com"
		router   = "forge-site-apex-apex.example.com"
	)
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	api := newForgeAPI(apiServerWith(serverID, "10.0.0.5",
		apiSiteWith(siteID, "app.internal", nil, []ForgeDomain{
			domainRecord("d-apex", host, "primary", true, "to-www"),
		}),
	))
	p := revocationProvider(t, api, &clock)

	first := publishedConfig(t, p)
	firstRule := ""
	if r := first.HTTP.Routers[router]; r != nil {
		firstRule = r.Rule
	}
	if !strings.Contains(firstRule, "Host(`www."+host+"`)") || !strings.Contains(firstRule, "HostRegexp(") {
		t.Fatalf("first poll rule = %q, want www and wildcard clauses", firstRule)
	}

	api.setDomains(serverID, siteID, []ForgeDomain{
		domainRecord("d-apex", host, "primary", false, "none"),
	})
	clock = clock.Add(30 * time.Second)

	second := publishedConfig(t, p)
	want := "Host(`" + host + "`)"
	for _, name := range []string{router, router + "-http"} {
		got := second.HTTP.Routers[name]
		if got == nil {
			t.Fatalf("router %s missing after the domain record changed (%d /domains calls)", name, api.domainCalls(serverID, siteID))
		}
		if got.Rule != want || api.domainCalls(serverID, siteID) < 2 {
			t.Fatalf("router %s rule = %q, want %q (%d /domains calls)", name, got.Rule, want, api.domainCalls(serverID, siteID))
		}
	}
	if second.HTTP.Routers[router].TLS == nil || second.HTTP.Routers[router].TLS.CertResolver != revocationCertResolver {
		t.Fatalf("apex router lost cert resolver %s", revocationCertResolver)
	}
}

// TestReverbHostChangeIsVisibleOnNextHealthyPoll switches which existing domain
// is the Reverb host. Domains are unchanged, so this only passes when the
// Reverb half is read again on a healthy poll. The Reverb router has no HTTP
// redirect; the previous Reverb host becomes an ordinary alias and gains one.
func TestReverbHostChangeIsVisibleOnNextHealthyPoll(t *testing.T) {
	const (
		serverID = "srv-a"
		siteID   = "site-reverb"
		oldHost  = "ws-old.example.com"
		newHost  = "ws-new.example.com"
	)
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	site := apiSiteWith(siteID, "app.example.com", nil, []ForgeDomain{
		domainRecord("d-app", "app.example.com", "primary", false, "none"),
		domainRecord("d-old", oldHost, "alias", false, "none"),
		domainRecord("d-new", newHost, "alias", false, "none"),
	})
	site.ReverbStatus = http.StatusOK
	site.ReverbHost = oldHost
	site.ReverbPort = 8081
	api := newForgeAPI(apiServerWith(serverID, "10.0.0.5", site))
	p := revocationProvider(t, api, &clock)

	first := publishedConfig(t, p)
	if first.HTTP.Routers["forge-"+siteID+"-"+oldHost+"-http"] != nil {
		t.Fatal("first poll gave the Reverb host an HTTP redirect router")
	}
	if first.HTTP.Routers["forge-"+siteID+"-"+newHost] == nil {
		t.Fatal("first poll did not route the future Reverb host")
	}

	api.setReverb(serverID, siteID, newHost, 8081, http.StatusOK)
	clock = clock.Add(30 * time.Second)

	second := publishedConfig(t, p)
	if second.HTTP.Routers["forge-"+siteID+"-"+newHost+"-http"] != nil || api.reverbCalls(serverID, siteID) < 2 {
		t.Fatalf("healthy poll made %d Reverb calls; new host redirect rule %q; previous host redirect rule %q",
			api.reverbCalls(serverID, siteID),
			routerRule(second, "forge-"+siteID+"-"+newHost+"-http"),
			routerRule(second, "forge-"+siteID+"-"+oldHost+"-http"))
	}
	if second.HTTP.Routers["forge-"+siteID+"-"+newHost] == nil {
		t.Fatal("new Reverb host has no router")
	}
	if second.HTTP.Routers["forge-"+siteID+"-"+newHost].TLS == nil ||
		second.HTTP.Routers["forge-"+siteID+"-"+newHost].TLS.CertResolver != revocationCertResolver {
		t.Fatal("new Reverb host is not covered by the site cert resolver")
	}
	if second.HTTP.Routers["forge-"+siteID+"-"+oldHost+"-http"] == nil {
		t.Fatal("previous Reverb host was not republished as an ordinary alias with an HTTP redirect")
	}
	requireHostBackend(t, second, newHost, "http://10.0.0.5:80")
}

// TestUnresolvedReverbRetryStillUsesFreshDomains is the other cache branch.
// A Reverb failure is not stored as resolved, and the next poll retries only
// that half. Domain records removed in the meantime must still disappear.
func TestUnresolvedReverbRetryStillUsesFreshDomains(t *testing.T) {
	const (
		serverID = "srv-a"
		siteID   = "site-open"
		detached = "detached.example.com"
		wsHost   = "ws.example.com"
		backend  = "http://10.0.0.5:80"
	)
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	site := apiSiteWith(siteID, "app.internal", nil, []ForgeDomain{
		domainRecord("d-app", "app.example.com", "primary", false, "none"),
		domainRecord("d-detached", detached, "alias", false, "none"),
	})
	site.ReverbStatus = http.StatusTooManyRequests
	api := newForgeAPI(apiServerWith(serverID, "10.0.0.5", site))
	p := revocationProvider(t, api, &clock)

	first := publishedConfig(t, p)
	requireHostBackend(t, first, detached, backend)

	api.setDomains(serverID, siteID, []ForgeDomain{
		domainRecord("d-app", "app.example.com", "primary", false, "none"),
		domainRecord("d-ws", wsHost, "alias", false, "none"),
	})
	api.setReverb(serverID, siteID, wsHost, 8081, http.StatusOK)
	clock = clock.Add(30 * time.Second)

	second := publishedConfig(t, p)
	if api.domainCalls(serverID, siteID) < 2 {
		t.Fatalf("Reverb retry poll made %d /domains calls, want at least 2; published %s",
			api.domainCalls(serverID, siteID), formatHits(hostRouters(second, detached)))
	}
	if api.reverbCalls(serverID, siteID) < 2 {
		t.Fatalf("Reverb calls = %d, want at least 2", api.reverbCalls(serverID, siteID))
	}
	requireHostAbsent(t, second, detached)
	if second.HTTP.Routers["forge-"+siteID+"-"+wsHost+"-http"] != nil {
		t.Fatal("fresh Reverb host was published with an HTTP redirect router")
	}
	requireHostBackend(t, second, wsHost, backend)
}

// TestDeletedSiteIsOmittedWhileItsDomainCacheIsWarm is the boundary the domain
// cache does not own: the site list is fetched on every poll, so removing the
// site drops its routers on the next poll even though a domain entry is warm.
func TestDeletedSiteIsOmittedWhileItsDomainCacheIsWarm(t *testing.T) {
	const (
		serverID = "srv-a"
		siteID   = "site-gone"
		host     = "gone.example.com"
	)
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	api := newForgeAPI(apiServerWith(serverID, "10.0.0.5",
		apiSiteWith(siteID, "app.internal", nil, []ForgeDomain{
			domainRecord("d-gone", host, "primary", false, "none"),
		}),
		apiSiteWith("site-stay", "stay.example.com", nil, []ForgeDomain{
			domainRecord("d-stay", "stay.example.com", "primary", false, "none"),
		}),
	))
	p := revocationProvider(t, api, &clock)
	requireHostBackend(t, publishedConfig(t, p), host, "http://10.0.0.5:80")

	api.removeSite(serverID, siteID)
	clock = clock.Add(30 * time.Second)

	second := publishedConfig(t, p)
	requireHostAbsent(t, second, host)
	requireHostBackend(t, second, "stay.example.com", "http://10.0.0.5:80")
}

// TestExpiredCacheFallsBackToLastDomainsWhenTheRefetchIsRefused keeps the
// error policy: a domains failure after the entry has expired still publishes
// the last good hostnames. A healthy poll is not this path.
func TestExpiredCacheFallsBackToLastDomainsWhenTheRefetchIsRefused(t *testing.T) {
	const (
		serverID = "srv-a"
		siteID   = "site-warm"
		host     = "kept.example.com"
		backend  = "http://10.0.0.5:80"
	)
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	api := newForgeAPI(apiServerWith(serverID, "10.0.0.5",
		apiSiteWith(siteID, "app.internal", nil, []ForgeDomain{
			domainRecord("d-kept", host, "primary", false, "none"),
		}),
	))
	p := revocationProvider(t, api, &clock)
	requireHostBackend(t, publishedConfig(t, p), host, backend)

	// Past TTL plus the maximum jitter, so the entry is expired for every key.
	clock = clock.Add(21 * time.Minute)
	api.setDomainStatus(serverID, siteID, http.StatusTooManyRequests)

	second := publishedConfig(t, p)
	requireHostBackend(t, second, host, backend)
}

func routerRule(cfg *dynamic.Configuration, name string) string {
	if cfg.HTTP == nil || cfg.HTTP.Routers[name] == nil {
		return ""
	}
	return cfg.HTTP.Routers[name].Rule
}
