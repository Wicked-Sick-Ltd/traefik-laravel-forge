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

func TestSiteLookupCachesWithinTTL(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{domains: testDomains("a.example.com")}
	p := testProvider(t, c, "10m", &clock)

	for i := 0; i < 3; i++ { //nolint:modernize // yaegi cannot type range-over-int; see .golangci.yml
		got, err := p.siteLookupFor("1", testSite("10", "a.example.com"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.domains) != 1 || got.domains[0].Attributes.Name != "a.example.com" {
			t.Fatalf("wrong domains: %+v", got.domains)
		}
	}
	if c.domainCalls != 1 {
		t.Errorf("domains fetched %d times, want 1", c.domainCalls)
	}
	if c.reverbCalls != 1 {
		t.Errorf("reverb fetched %d times, want 1", c.reverbCalls)
	}
}

func TestSiteLookupRefetchesAfterTTL(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{domains: testDomains("a.example.com")}
	p := testProvider(t, c, "10m", &clock)

	if _, err := p.siteLookupFor("1", testSite("10", "a.example.com")); err != nil {
		t.Fatal(err)
	}
	// Past the TTL plus the maximum jitter, so the entry must be stale.
	clock = clock.Add(21 * time.Minute)
	if _, err := p.siteLookupFor("1", testSite("10", "a.example.com")); err != nil {
		t.Fatal(err)
	}
	if c.domainCalls != 2 {
		t.Errorf("domains fetched %d times, want 2", c.domainCalls)
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

// The whole point of the cache is to shrink the per-poll burst, so refreshes
// must not all fall due on the same cycle. Expiry is jittered per site.
func TestSiteLookupJitterSpreadsExpiryAcrossSites(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := &countingClient{domains: testDomains("a.example.com")}
	p := testProvider(t, c, "10m", &clock)

	seen := map[time.Time]bool{}
	for _, id := range []string{"10", "11", "12", "13", "14", "15", "16", "17"} {
		if _, err := p.siteLookupFor("1", testSite(id, "s"+id+".example.com")); err != nil {
			t.Fatal(err)
		}
		seen[p.lookups["1/"+id].expiresAt] = true
	}
	if len(seen) < 4 {
		t.Errorf("only %d distinct expiry times across 8 sites; jitter is not spreading refreshes", len(seen))
	}
	for exp := range seen {
		minExp, maxExp := clock.Add(10*time.Minute), clock.Add(20*time.Minute)
		if exp.Before(minExp) || exp.After(maxExp) {
			t.Errorf("expiry %s outside [TTL, 2*TTL] window", exp)
		}
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

// A warm cache must collapse the per-poll burst to the uncached calls only:
// one servers list plus one sites list per server. Anything more and the burst
// still exceeds Forge's 60 requests/minute when two proxies poll together.
func TestWarmPollBurstIsOnlyServerAndSiteLists(t *testing.T) {
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := newFleetClient(14, 2)
	p := testProvider(t, c, "10m", &clock)

	if _, err := p.GenerateConfiguration(); err != nil {
		t.Fatal(err)
	}
	cold := c.calls
	wantCold := 1 + 2 + 16*2
	if cold != wantCold {
		t.Errorf("cold poll made %d calls, want %d", cold, wantCold)
	}

	c.calls = 0
	clock = clock.Add(30 * time.Second) // one poll interval later
	if _, err := p.GenerateConfiguration(); err != nil {
		t.Fatal(err)
	}
	if c.calls != 3 {
		t.Errorf("warm poll made %d calls, want 3 (servers + 2 site lists)", c.calls)
	}
}
