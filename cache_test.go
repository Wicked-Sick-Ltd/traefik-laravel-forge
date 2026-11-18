package traefik_laravel_forge

import (
	"errors"
	"testing"
	"time"
)

// Yaegi-compatible: no testify. See CLAUDE.md.

const (
	statusInstalled = "installed"
	statusEnabled   = "enabled"
)

// countingClient records how many times each Forge call was made and can be
// told to start failing, so tests can pin cache behavior without HTTP.
type countingClient struct {
	domainCalls int
	reverbCalls int
	domains     []ForgeDomain
	reverbHost  string
	failDomains bool
	failReverb  bool
}

func (c *countingClient) FetchServers() ([]ForgeServer, error)   { return nil, nil }
func (c *countingClient) FetchSites(string) ([]ForgeSite, error) { return nil, nil }

func (c *countingClient) FetchDomains(_, _ string) ([]ForgeDomain, error) {
	c.domainCalls++
	if c.failDomains {
		return nil, errors.New("forge API returned status 429")
	}
	return c.domains, nil
}

func (c *countingClient) FetchReverbIntegration(_, _ string) (*ForgeReverbIntegration, error) {
	c.reverbCalls++
	if c.failReverb {
		return nil, errors.New("forge API returned status 429")
	}
	if c.reverbHost == "" {
		return nil, nil
	}
	return &ForgeReverbIntegration{Enabled: true, Host: c.reverbHost, Port: 8080}, nil
}

// testProvider builds a Provider with a controllable clock and the given cache TTL.
func testProvider(t *testing.T, client ForgeClient, ttl string, clock *time.Time) *Provider {
	t.Helper()
	config := CreateConfig()
	config.PollInterval = "30s"
	config.CacheTTL = ttl
	p, err := NewProviderWithClient(config, client)
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return *clock }
	return p
}

// jitterFraction derives a stable value in [0,1000) from a cache key using
// FNV-1a, so a test can pick keys that land in a known part of the range.
func jitterFraction(key string) int64 {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return int64(h % 1000)
}

func TestJitterFractionIsStableAndBounded(t *testing.T) {
	seen := map[int64]bool{}
	for _, id := range []string{"10", "11", "12", "13", "14", "15", "16", "17"} {
		key := cacheKey("1", id)
		got := jitterFraction(key)
		if got < 0 || got >= 1000 {
			t.Fatalf("jitterFraction(%q) = %d, want [0,1000)", key, got)
		}
		if again := jitterFraction(key); again != got {
			t.Fatalf("jitterFraction(%q) changed between calls: %d then %d", key, got, again)
		}
		seen[got] = true
	}
	if len(seen) < 4 {
		t.Errorf("only %d distinct values across 8 keys", len(seen))
	}
}

func testSite(id, name string) ForgeSite {
	s := ForgeSite{ID: id}
	s.Attributes.Name = name
	s.Attributes.Status = statusInstalled
	return s
}

func testDomains(names ...string) []ForgeDomain {
	out := make([]ForgeDomain, 0, len(names))
	for i, n := range names {
		d := ForgeDomain{ID: string(rune('1' + i))}
		d.Attributes.Name = n
		d.Attributes.DomainType = "primary"
		d.Attributes.Status = statusEnabled
		out = append(out, d)
	}
	return out
}

// A stored answer must never stand in for one Forge is willing to give: a
// hostname removed in Forge has to leave on the very next poll.
func TestSiteLookupFetchesOnEveryCall(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{domains: testDomains("a.example.com", "gone.example.com"), reverbHost: "ws.example.com"}
	p := testProvider(t, c, "10m", &clock)

	if _, err := p.siteLookupFor("1", testSite("10", "a.example.com")); err != nil {
		t.Fatal(err)
	}
	c.domains = testDomains("a.example.com")
	c.reverbHost = ""
	clock = clock.Add(30 * time.Second)

	got, err := p.siteLookupFor("1", testSite("10", "a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if c.domainCalls != 2 || c.reverbCalls != 2 {
		t.Errorf("fetched domains %d and reverb %d times, want 2 each", c.domainCalls, c.reverbCalls)
	}
	if len(got.domains) != 1 || got.domains[0].Attributes.Name != "a.example.com" {
		t.Errorf("removed domain still returned: %+v", got.domains)
	}
	if got.reverbHost != "" {
		t.Errorf("reverbHost = %q, want the removed integration to be gone", got.reverbHost)
	}
}

func TestSiteLookupServesStaleWhenFetchFails(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{domains: testDomains("a.example.com")}
	p := testProvider(t, c, "10m", &clock)

	if _, err := p.siteLookupFor("1", testSite("10", "a.example.com")); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(21 * time.Minute)
	c.failDomains = true

	got, err := p.siteLookupFor("1", testSite("10", "a.example.com"))
	if err != nil {
		t.Fatalf("a warm cache must absorb the error, got: %v", err)
	}
	if len(got.domains) != 1 || got.domains[0].Attributes.Name != "a.example.com" {
		t.Errorf("stale value not served, got %+v", got.domains)
	}
}

func TestSiteLookupErrorsWhenFetchFailsAndNothingCached(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{failDomains: true}
	p := testProvider(t, c, "10m", &clock)

	if _, err := p.siteLookupFor("1", testSite("10", "a.example.com")); err == nil {
		t.Fatal("a cold cache must propagate the error so the poll aborts")
	}
}

func TestSiteLookupReverbFailureIsNotFatal(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{
		domains:    testDomains("a.example.com"),
		failReverb: true,
	}
	p := testProvider(t, c, "10m", &clock)

	got, err := p.siteLookupFor("1", testSite("10", "a.example.com"))
	if err != nil {
		t.Fatalf("reverb failure must not abort the poll, got: %v", err)
	}
	if got.reverbHost != "" {
		t.Errorf("reverbHost = %q, want empty", got.reverbHost)
	}
}

func TestSiteLookupZeroTTLDisablesCaching(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{domains: testDomains("a.example.com")}
	p := testProvider(t, c, "0s", &clock)

	for i := 0; i < 3; i++ { //nolint:modernize // yaegi cannot type range-over-int; see .golangci.yml
		if _, err := p.siteLookupFor("1", testSite("10", "a.example.com")); err != nil {
			t.Fatal(err)
		}
	}
	if c.domainCalls != 3 {
		t.Errorf("domains fetched %d times with caching off, want 3", c.domainCalls)
	}
}

func TestCacheTTLDefaultIsSet(t *testing.T) {
	if CreateConfig().CacheTTL == "" {
		t.Fatal("CreateConfig must set a default CacheTTL")
	}
}

func TestInitRejectsNegativeCacheTTL(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "30s"
	config.CacheTTL = "-1m"
	config.APIToken = testAPIToken
	config.Organization = testOrganization

	p, err := New(nil, config, "test") //nolint:staticcheck // nil ctx is unused by New
	if err != nil {
		return // rejecting at construction is also acceptable
	}
	if err := p.Init(); err == nil {
		t.Fatal("a negative cacheTTL must be rejected")
	}
}

// fleetClient mimics the real deployment shape: two servers, sixteen sites,
// counting every HTTP-backed call so a poll's request burst can be measured.
type fleetClient struct {
	servers []ForgeServer
	sites   map[string][]ForgeSite
	calls   int
}

func newFleetClient(sitesOnA, sitesOnB int) *fleetClient {
	mk := func(id, name string) ForgeServer {
		s := ForgeServer{ID: id}
		s.Attributes.Name = name
		s.Attributes.PrivateIPAddress = "10.0.0.1"
		return s
	}
	f := &fleetClient{
		servers: []ForgeServer{mk("1", "php01"), mk("2", "php02")},
		sites:   map[string][]ForgeSite{},
	}
	for i := 0; i < sitesOnA; i++ { //nolint:modernize // yaegi cannot type range-over-int; see .golangci.yml
		f.sites["1"] = append(f.sites["1"], testSite("a"+string(rune('a'+i)), "a"+string(rune('a'+i))+".example.com"))
	}
	for i := 0; i < sitesOnB; i++ { //nolint:modernize // yaegi cannot type range-over-int; see .golangci.yml
		f.sites["2"] = append(f.sites["2"], testSite("b"+string(rune('a'+i)), "b"+string(rune('a'+i))+".example.com"))
	}
	return f
}

func (f *fleetClient) FetchServers() ([]ForgeServer, error) {
	f.calls++
	return f.servers, nil
}

func (f *fleetClient) FetchSites(serverID string) ([]ForgeSite, error) {
	f.calls++
	return f.sites[serverID], nil
}

func (f *fleetClient) FetchDomains(_, siteID string) ([]ForgeDomain, error) {
	f.calls++
	return testDomains(siteID + ".example.com"), nil
}

func (f *fleetClient) FetchReverbIntegration(_, _ string) (*ForgeReverbIntegration, error) {
	f.calls++
	return nil, nil
}

// Every poll costs what the README's API rate limits section documents: one
// servers list, one sites list per server, and domains plus Reverb per site.
// The stored answers must not cut that down, because skipping a fetch is what
// keeps a removed hostname routed.
func TestEveryPollFetchesDomainsAndReverbPerSite(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := newFleetClient(14, 2)
	p := testProvider(t, c, "10m", &clock)
	want := 1 + 2 + 16*2

	for i := 0; i < 2; i++ { //nolint:modernize // yaegi cannot type range-over-int; see .golangci.yml
		c.calls = 0
		if _, err := p.GenerateConfiguration(); err != nil {
			t.Fatal(err)
		}
		if c.calls != want {
			t.Errorf("poll %d made %d calls, want %d", i+1, c.calls, want)
		}
		clock = clock.Add(30 * time.Second)
	}
}

// A site that a completed poll did not route must lose its stored answer.
// Otherwise a site disabled with traefik:enabled=false, edited in Forge, and
// re-enabled while /domains is refused would come back on the hostnames it had
// before it was disabled.
func TestUnroutedSiteDoesNotKeepItsFallback(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &toggleClient{records: testDomains("old.example.com"), site: testSite("10", "app.example.com")}
	p := testProvider(t, c, "10m", &clock)

	if _, err := p.GenerateConfiguration(); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.cachedLookup(cacheKey("1", "10")); !ok {
		t.Fatal("a routed site must keep its last good answer")
	}

	c.site.Attributes.Tags = []string{"traefik:enabled=false"}
	if _, err := p.GenerateConfiguration(); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.cachedLookup(cacheKey("1", "10")); ok {
		t.Fatal("a site the poll did not route still holds a fallback")
	}

	c.site.Attributes.Tags = nil
	c.refuseRecords = true
	cfg, err := p.GenerateConfiguration()
	if err == nil {
		t.Fatalf("re-enabled site with a refused /domains was routed on old records: %v", cfg.HTTP.Routers)
	}
}

// An aborted poll has not visited every site, so it must not prune the
// fallbacks of the sites it never reached.
func TestAbortedPollKeepsFallbacks(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &toggleClient{records: testDomains("a.example.com"), site: testSite("10", "app.example.com")}
	p := testProvider(t, c, "10m", &clock)

	if _, err := p.GenerateConfiguration(); err != nil {
		t.Fatal(err)
	}
	c.refuseSites = true
	if _, err := p.GenerateConfiguration(); err == nil {
		t.Fatal("a sites failure must abort the poll")
	}
	if _, ok := p.cachedLookup(cacheKey("1", "10")); !ok {
		t.Fatal("an aborted poll dropped a fallback")
	}
}

// toggleClient serves one server with one site whose tags a test can change.
type toggleClient struct {
	site          ForgeSite
	records       []ForgeDomain
	refuseSites   bool
	refuseRecords bool
}

func (c *toggleClient) FetchServers() ([]ForgeServer, error) {
	s := ForgeServer{ID: "1"}
	s.Attributes.Name = "php01"
	s.Attributes.PrivateIPAddress = "10.0.0.1"
	return []ForgeServer{s}, nil
}

// The parameter is named because yaegi panics calling a method whose only
// parameter is unnamed through the ForgeClient interface.
func (c *toggleClient) FetchSites(_ string) ([]ForgeSite, error) {
	if c.refuseSites {
		return nil, errors.New("forge API returned status 503")
	}
	return []ForgeSite{c.site}, nil
}

func (c *toggleClient) FetchDomains(_, _ string) ([]ForgeDomain, error) {
	if c.refuseRecords {
		return nil, errors.New("forge API returned status 429")
	}
	return c.records, nil
}

func (c *toggleClient) FetchReverbIntegration(_, _ string) (*ForgeReverbIntegration, error) {
	return nil, nil
}

// Setting cacheTTL to zero is the documented escape hatch for turning caching
// off. It must disable the stale-on-error path too, not just the fresh read:
// otherwise entries are still stored, and the first fetch error serves a stale
// value indefinitely to someone who asked for no caching at all.
func TestZeroTTLAlsoDisablesStaleServing(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{domains: testDomains("a.example.com")}
	p := testProvider(t, c, "0s", &clock)

	if _, err := p.siteLookupFor("1", testSite("10", "a.example.com")); err != nil {
		t.Fatal(err)
	}
	c.failDomains = true

	if _, err := p.siteLookupFor("1", testSite("10", "a.example.com")); err == nil {
		t.Fatal("with caching disabled a fetch error must propagate, not serve a stale entry")
	}
}

// A transient 429 on /integrations/reverb must self-correct on the very next
// poll rather than misclassifying the Reverb domain until something expires.
func TestFailedReverbIsRetriedOnNextPoll(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{domains: testDomains("a.example.com"), reverbHost: "ws.a.example.com", failReverb: true}
	p := testProvider(t, c, "10m", &clock)

	got, err := p.siteLookupFor("1", testSite("10", "a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if got.reverbHost != "" {
		t.Fatalf("reverbHost = %q, want empty while the fetch is failing", got.reverbHost)
	}

	// Forge recovers, and the next poll comes round.
	c.failReverb = false
	clock = clock.Add(30 * time.Second)

	got, err = p.siteLookupFor("1", testSite("10", "a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if c.reverbCalls != 2 {
		t.Errorf("reverb fetched %d times, want 2 — a failed Reverb lookup must be retried", c.reverbCalls)
	}
	if got.reverbHost != "ws.a.example.com" {
		t.Errorf("reverbHost = %q, want the recovered host", got.reverbHost)
	}
}
