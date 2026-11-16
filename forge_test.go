package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/traefik/genconf/dynamic"
)

const (
	testAPIToken     = "test-token"
	testOrganization = "test-org"
)

// -- Config / lifecycle tests (yaegi-compatible, no testify) --

func TestNew(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "10s"
	config.APIToken = testAPIToken
	config.Organization = testOrganization

	provider, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err = provider.Stop(); err != nil {
			t.Fatal(err)
		}
	})
	if err = provider.Init(); err != nil {
		t.Fatal(err)
	}
}

func TestNewMissingAPIToken(t *testing.T) {
	config := CreateConfig()
	config.APIToken = ""
	config.Organization = testOrganization
	_, err := New(context.Background(), config, "test")
	if err == nil {
		t.Fatal("expected error for missing API token, got nil")
	}
}

func TestNewMissingOrganization(t *testing.T) {
	config := CreateConfig()
	config.APIToken = testAPIToken
	config.Organization = ""
	_, err := New(context.Background(), config, "test")
	if err == nil {
		t.Fatal("expected error for missing organization, got nil")
	}
}

func TestNewInvalidPollInterval(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "invalid"
	config.APIToken = testAPIToken
	config.Organization = testOrganization
	_, err := New(context.Background(), config, "test")
	if err == nil {
		t.Fatal("expected error for invalid poll interval, got nil")
	}
}

func TestInitPollIntervalZero(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "0s"
	config.APIToken = testAPIToken
	config.Organization = testOrganization
	p, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Init(); err == nil {
		t.Fatal("expected error for zero poll interval in Init, got nil")
	}
}

func TestInitPollIntervalTooShort(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "5s"
	config.APIToken = testAPIToken
	config.Organization = testOrganization
	p, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}
	err = p.Init()
	if err == nil {
		t.Fatal("expected error for poll interval < 10s in Init, got nil")
	}
	if !strings.Contains(err.Error(), "at least 10s") {
		t.Fatalf("expected error containing %q, got: %v", "at least 10s", err)
	}
}

func TestInitPollIntervalValid(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "10s"
	config.APIToken = testAPIToken
	config.Organization = testOrganization
	p, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Init(); err != nil {
		t.Fatalf("unexpected error for 10s poll interval: %v", err)
	}
}

func TestTraefikIDStoredOnProvider(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "10s"
	config.APIToken = testAPIToken
	config.Organization = testOrganization
	config.TraefikID = "lb01"
	p, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.traefikID != "lb01" {
		t.Errorf("traefikID = %q, want %q", p.traefikID, "lb01")
	}
}

// -- ParseTagConfig unit tests --

func TestParseTagConfig(t *testing.T) {
	tests := []struct {
		tag       string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{"traefik:cert-resolver=letsencrypt", "cert-resolver", "letsencrypt", true},
		{"traefik:tls=true", "tls", "true", true},
		{"traefik:tls=false", "tls", "false", true},
		{"traefik:enabled", "enabled", "true", true},
		{"traefik:key=value=extra", "key", "value=extra", true},
		{"production", "", "", false},
		{"", "", "", false},
		{"traefik:", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			key, value, ok := ParseTagConfig(tt.tag)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if value != tt.wantValue {
				t.Errorf("value = %q, want %q", value, tt.wantValue)
			}
		})
	}
}

// -- ParseServerTags unit tests --

func TestParseServerTags(t *testing.T) {
	tests := []struct {
		name             string
		tags             []string
		wantUpstreamHost string
		wantUpstreamPort int
		wantTraefikID    string
	}{
		{
			name:             "upstream host and port",
			tags:             []string{"traefik:upstream-host=10.0.1.10", "traefik:upstream-port=8080"},
			wantUpstreamHost: "10.0.1.10",
			wantUpstreamPort: 8080,
		},
		{
			name:             "host only — port defaults to 80",
			tags:             []string{"traefik:upstream-host=192.168.1.50"},
			wantUpstreamHost: "192.168.1.50",
			wantUpstreamPort: 80,
		},
		{
			name:             "port only — host empty",
			tags:             []string{"traefik:upstream-port=9090"},
			wantUpstreamPort: 9090,
		},
		{
			name:             "traefik-id",
			tags:             []string{"traefik:upstream-host=10.0.1.10", "traefik:traefik-id=lb01"},
			wantUpstreamHost: "10.0.1.10",
			wantUpstreamPort: 80,
			wantTraefikID:    "lb01",
		},
		{
			name:             "no traefik tags",
			tags:             []string{"production", "app-server"},
			wantUpstreamPort: 80,
		},
		{
			name:             "empty tags",
			tags:             []string{},
			wantUpstreamPort: 80,
		},
		{
			name:             "lb-host/lb-port aliases",
			tags:             []string{"traefik:lb-host=10.0.0.1", "traefik:lb-port=3000"},
			wantUpstreamHost: "10.0.0.1",
			wantUpstreamPort: 3000,
		},
		{
			name:             "loadbalancer aliases",
			tags:             []string{"traefik:loadbalancer-host=10.0.0.2", "traefik:loadbalancer-port=4000"},
			wantUpstreamHost: "10.0.0.2",
			wantUpstreamPort: 4000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseServerTags(tt.tags)
			if got.UpstreamHost != tt.wantUpstreamHost {
				t.Errorf("UpstreamHost = %q, want %q", got.UpstreamHost, tt.wantUpstreamHost)
			}
			if got.UpstreamPort != tt.wantUpstreamPort {
				t.Errorf("UpstreamPort = %d, want %d", got.UpstreamPort, tt.wantUpstreamPort)
			}
			if got.TraefikID != tt.wantTraefikID {
				t.Errorf("TraefikID = %q, want %q", got.TraefikID, tt.wantTraefikID)
			}
		})
	}
}

// -- buildHostRule unit tests --

func TestBuildHostRule(t *testing.T) {
	tests := []struct {
		name     string
		hosts    []string
		wildcard []string
		want     string
	}{
		{
			name:  "single host",
			hosts: []string{"example.com"},
			want:  "Host(`example.com`)",
		},
		{
			name:  "multiple hosts",
			hosts: []string{"example.com", "www.example.com"},
			want:  "Host(`example.com`) || Host(`www.example.com`)",
		},
		{
			name:     "host with wildcard",
			hosts:    []string{"example.com"},
			wildcard: []string{"example.com"},
			want:     `Host(` + "`example.com`" + `) || HostRegexp(` + "`^[^.]+\\.example\\.com$`" + `)`,
		},
		{
			name:     "multiple hosts one wildcard",
			hosts:    []string{"example.com", "other.com"},
			wildcard: []string{"example.com"},
			want: "Host(`example.com`) || Host(`other.com`) || " +
				"HostRegexp(`^[^.]+\\.example\\.com$`)",
		},
		{
			name:  "empty",
			hosts: []string{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildHostRule(tt.hosts, tt.wildcard)
			if got != tt.want {
				t.Errorf("buildHostRule() = %q, want %q", got, tt.want)
			}
		})
	}
}

// -- buildTagMap unit tests --

func TestBuildTagMap(t *testing.T) {
	included := []any{
		map[string]any{
			"type": "tags",
			"id":   "1",
			"attributes": map[string]any{
				"name": "traefik:upstream-host=10.0.0.1",
			},
		},
		map[string]any{
			"type": "tags",
			"id":   "2",
			"attributes": map[string]any{
				"name": "production",
			},
		},
		// non-tag item should be ignored
		map[string]any{
			"type": "servers",
			"id":   "3",
		},
		// item with empty ID should be ignored
		map[string]any{
			"type":       "tags",
			"id":         "",
			"attributes": map[string]any{"name": "orphan"},
		},
	}

	got := buildTagMap(included)

	if got["1"] != "traefik:upstream-host=10.0.0.1" {
		t.Errorf("tag 1 = %q, want %q", got["1"], "traefik:upstream-host=10.0.0.1")
	}
	if got["2"] != "production" {
		t.Errorf("tag 2 = %q, want %q", got["2"], "production")
	}
	if _, exists := got["3"]; exists {
		t.Error("non-tag item should not appear in tag map")
	}
	if len(got) != 2 {
		t.Errorf("tag map length = %d, want 2", len(got))
	}
}

// -- parseSiteTags unit tests --

func TestParseSiteTags(t *testing.T) {
	defaults := siteDefaults{
		CertResolver: "cloudflare",
		SitesEnabled: true,
		HTTPRedirect: true,
		Port:         80,
	}

	t.Run("no tags — all defaults applied", func(t *testing.T) {
		got := parseSiteTags(nil, defaults)
		if !got.Enabled {
			t.Error("Enabled should be true by default")
		}
		if got.CertResolver != "cloudflare" {
			t.Errorf("CertResolver = %q, want %q", got.CertResolver, "cloudflare")
		}
		if !got.EnableTLS {
			t.Error("EnableTLS should be true when CertResolver is set")
		}
		if got.Port != 80 {
			t.Errorf("Port = %d, want 80", got.Port)
		}
		if !got.HTTPRedirect {
			t.Error("HTTPRedirect should be true by default")
		}
	})

	t.Run("enabled=false disables site", func(t *testing.T) {
		got := parseSiteTags([]string{"traefik:enabled=false"}, defaults)
		if got.Enabled {
			t.Error("Enabled should be false after tag override")
		}
	})

	t.Run("cert-resolver override also sets EnableTLS", func(t *testing.T) {
		noTLSDefaults := siteDefaults{SitesEnabled: true, HTTPRedirect: false, Port: 80}
		got := parseSiteTags([]string{"traefik:cert-resolver=letsencrypt"}, noTLSDefaults)
		if got.CertResolver != "letsencrypt" {
			t.Errorf("CertResolver = %q, want letsencrypt", got.CertResolver)
		}
		if !got.EnableTLS {
			t.Error("EnableTLS should be true when cert-resolver tag is set")
		}
	})

	t.Run("tls=false overrides cert resolver", func(t *testing.T) {
		got := parseSiteTags([]string{"traefik:tls=false"}, defaults)
		if got.EnableTLS {
			t.Error("EnableTLS should be false after tls=false tag")
		}
	})

	t.Run("port override", func(t *testing.T) {
		got := parseSiteTags([]string{"traefik:port=3000"}, defaults)
		if got.Port != 3000 {
			t.Errorf("Port = %d, want 3000", got.Port)
		}
	})

	t.Run("http-redirect=false overrides global", func(t *testing.T) {
		got := parseSiteTags([]string{"traefik:http-redirect=false"}, defaults)
		if got.HTTPRedirect {
			t.Error("HTTPRedirect should be false after tag override")
		}
	})

	t.Run("entrypoints parsed", func(t *testing.T) {
		got := parseSiteTags([]string{"traefik:entrypoints=web,websecure"}, defaults)
		if len(got.EntryPoints) != 2 || got.EntryPoints[0] != "web" || got.EntryPoints[1] != "websecure" {
			t.Errorf("EntryPoints = %v, want [web websecure]", got.EntryPoints)
		}
	})

	t.Run("aliases parsed", func(t *testing.T) {
		got := parseSiteTags([]string{"traefik:aliases=api.example.com,app.example.com"}, defaults)
		if len(got.Aliases) != 2 {
			t.Errorf("Aliases = %v, want 2 entries", got.Aliases)
		}
	})

	t.Run("middlewares and middleware alias", func(t *testing.T) {
		got := parseSiteTags([]string{"traefik:middlewares=auth,rate-limit"}, defaults)
		if len(got.Middlewares) != 2 || got.Middlewares[0] != "auth" {
			t.Errorf("Middlewares = %v, want [auth rate-limit]", got.Middlewares)
		}
	})

	t.Run("forge-domain opt-in", func(t *testing.T) {
		got := parseSiteTags([]string{"traefik:forge-domain=true"}, defaults)
		if !got.IncludeForgeDomain {
			t.Error("IncludeForgeDomain should be true")
		}
	})
}

// -- classifyDomains unit tests --

func TestClassifyDomains(t *testing.T) {
	domains := []ForgeDomain{
		{Attributes: ForgeDomainAttributes{Name: "example.com", Status: "enabled", DomainType: "primary"}},
		{Attributes: ForgeDomainAttributes{Name: "www.example.com", Status: "enabled", DomainType: "alias"}},
		{Attributes: ForgeDomainAttributes{Name: "ws.example.com", Status: "enabled", DomainType: "alias"}},
		{Attributes: ForgeDomainAttributes{Name: "disabled.com", Status: "disabled", DomainType: "primary"}},
	}

	main, reverb := classifyDomains(domains, "ws.example.com")

	if len(main) != 2 {
		t.Errorf("main len = %d, want 2", len(main))
	}
	if len(reverb) != 1 || reverb[0].Attributes.Name != "ws.example.com" {
		t.Errorf("reverb = %v, want [{ws.example.com}]", reverb)
	}
}

func TestClassifyDomainsWildcard(t *testing.T) {
	domains := []ForgeDomain{
		{Attributes: ForgeDomainAttributes{
			Name:                    "example.com",
			Status:                  "enabled",
			AllowWildcardSubdomains: true,
		}},
	}

	main, reverb := classifyDomains(domains, "")

	if len(main) != 1 || main[0].Attributes.Name != "example.com" {
		t.Errorf("main = %v, want [example.com]", main)
	}
	if !main[0].Attributes.AllowWildcardSubdomains {
		t.Error("AllowWildcardSubdomains should be preserved on returned domain")
	}
	if len(reverb) != 0 {
		t.Errorf("reverb = %v, want empty", reverb)
	}
}

func TestClassifyDomainsReverbNoMatch(t *testing.T) {
	// When reverbHost is empty, all enabled domains go to main.
	domains := []ForgeDomain{
		{Attributes: ForgeDomainAttributes{Name: "example.com", Status: "enabled"}},
		{Attributes: ForgeDomainAttributes{Name: "alias.com", Status: "enabled"}},
	}

	main, reverb := classifyDomains(domains, "")

	if len(main) != 2 {
		t.Errorf("main len = %d, want 2", len(main))
	}
	if len(reverb) != 0 {
		t.Errorf("reverb = %v, want empty", reverb)
	}
}

func TestClassifyDomainsSkipsDisabled(t *testing.T) {
	domains := []ForgeDomain{
		{Attributes: ForgeDomainAttributes{Name: "active.com", Status: "enabled"}},
		{Attributes: ForgeDomainAttributes{Name: "inactive.com", Status: "disabled"}},
	}

	main, _ := classifyDomains(domains, "")

	if len(main) != 1 || main[0].Attributes.Name != "active.com" {
		t.Errorf("main = %v, want only active.com", main)
	}
}

// -- domainPriority unit tests --

func TestDomainPriority(t *testing.T) {
	tests := []struct {
		domain string
		want   int
	}{
		{"bounceiq.com", 200},
		{"staging.bounceiq.com", 300},
		{"ws.bounceiq.com", 300},
		{"ws.staging.bounceiq.com", 400},
		{"app.us.staging.bounceiq.com", 500},
	}
	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			if got := domainPriority(tt.domain); got != tt.want {
				t.Errorf("domainPriority(%q) = %d, want %d", tt.domain, got, tt.want)
			}
		})
	}
}

// -- Port parsing --

func TestParsePort(t *testing.T) {
	tests := []struct {
		value    string
		wantPort int
		wantOK   bool
	}{
		{"80", 80, true},
		{"8080", 8080, true},
		{"1", 1, true},
		{"65535", 65535, true},
		{" 8080 ", 8080, true},
		{"0", 0, false},
		{"-1", 0, false},
		{"65536", 0, false},
		{"999999", 0, false},
		{"80x", 0, false},
		{"abc", 0, false},
		{"", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			port, ok := parsePort(tt.value)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if port != tt.wantPort {
				t.Errorf("port = %d, want %d", port, tt.wantPort)
			}
		})
	}
}

// -- Upstream resolution priority --

func TestResolveUpstream(t *testing.T) {
	bothIPs := ForgeServer{Attributes: ForgeServerAttributes{
		Name:             "app01",
		PrivateIPAddress: "10.0.0.1",
		IPAddress:        "203.0.113.1",
	}}
	publicOnly := ForgeServer{Attributes: ForgeServerAttributes{Name: "app01", IPAddress: "203.0.113.1"}}
	noIP := ForgeServer{Attributes: ForgeServerAttributes{Name: "app01"}}

	tests := []struct {
		name     string
		server   ForgeServer
		tag      ServerConfig
		mapping  *ServerMapping
		wantHost string
		wantPort int
	}{
		{
			name:     "tag wins over mapping and auto-detection",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamHost: "172.16.0.1", UpstreamPort: 9000},
			mapping:  &ServerMapping{UpstreamHost: "192.168.0.1", UpstreamPort: 8080},
			wantHost: "172.16.0.1",
			wantPort: 9000,
		},
		{
			name:     "mapping wins over auto-detection",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamPort: 80},
			mapping:  &ServerMapping{UpstreamHost: "192.168.0.1", UpstreamPort: 8080},
			wantHost: "192.168.0.1",
			wantPort: 8080,
		},
		{
			name:     "port-only mapping keeps the auto-detected private IP",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamPort: 80},
			mapping:  &ServerMapping{ForgeServerName: "app01", UpstreamPort: 8080},
			wantHost: "10.0.0.1",
			wantPort: 8080,
		},
		{
			name:     "empty mapping falls back entirely",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamPort: 80},
			mapping:  &ServerMapping{ForgeServerName: "app01"},
			wantHost: "10.0.0.1",
			wantPort: 80,
		},
		{
			name:     "private IP preferred over public",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamPort: 80},
			wantHost: "10.0.0.1",
			wantPort: 80,
		},
		{
			name:     "public IP used when there is no private one",
			server:   publicOnly,
			tag:      ServerConfig{UpstreamPort: 80},
			wantHost: "203.0.113.1",
			wantPort: 80,
		},
		{
			name:     "no address at all yields no host",
			server:   noIP,
			tag:      ServerConfig{UpstreamPort: 80},
			wantHost: "",
			wantPort: 80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{}
			host, port := p.resolveUpstream(tt.server, tt.tag, tt.mapping)
			if host != tt.wantHost {
				t.Errorf("host = %q, want %q", host, tt.wantHost)
			}
			if port != tt.wantPort {
				t.Errorf("port = %d, want %d", port, tt.wantPort)
			}
		})
	}
}

// -- Lifecycle --

// Stop must be safe to call at any point in the lifecycle: after Provide, twice
// in a row, and on a provider that never started.
type catalogForge struct {
	servers []ForgeServer
	sites   map[string][]ForgeSite
	domains map[string][]ForgeDomain
	reverb  map[string]*ForgeReverbIntegration
}

func (c catalogForge) FetchServers() ([]ForgeServer, error) { return c.servers, nil }
func (c catalogForge) FetchSites(serverID string) ([]ForgeSite, error) {
	return c.sites[serverID], nil
}
func (c catalogForge) FetchDomains(_, siteID string) ([]ForgeDomain, error) {
	return c.domains[siteID], nil
}
func (c catalogForge) FetchReverbIntegration(_, siteID string) (*ForgeReverbIntegration, error) {
	return c.reverb[siteID], nil
}

func ownedProvider(t *testing.T, client ForgeClient) *Provider {
	t.Helper()
	p, err := NewProviderWithClient(&Config{
		PollInterval:        "30s",
		DefaultSitesEnabled: true,
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func installedSite(id, name string) ForgeSite {
	site := ForgeSite{ID: id}
	site.Type = "sites"
	site.Attributes.Name = name
	site.Attributes.Status = statusInstalled
	return site
}

func domainRecord(name, wwwRedirect string) ForgeDomain {
	domain := ForgeDomain{ID: name}
	domain.Type = "domainRecords"
	domain.Attributes.Name = name
	domain.Attributes.Status = statusEnabled
	domain.Attributes.WWWRedirectType = wwwRedirect
	return domain
}

func routerRule(cfg *dynamic.Configuration, name string) string {
	r := cfg.HTTP.Routers[name]
	if r == nil {
		return ""
	}
	return r.Rule
}

func ruleHas(rule, host string) bool {
	return strings.Contains(rule, "Host(`"+host+"`)")
}

// TestExactTieStaysOnline pins the startup case where two sites list the same
// canonical hostname and there is no remembered owner. The lower site key
// keeps the name, and the following poll keeps that same site.
func TestExactTieStaysOnline(t *testing.T) {
	client := catalogForge{
		servers: []ForgeServer{{
			ID:   "s1",
			Type: "servers",
			Attributes: ForgeServerAttributes{
				Name:             "app01",
				PrivateIPAddress: "10.0.0.1",
			},
		}},
		sites: map[string][]ForgeSite{
			"s1": {installedSite("b", "shop.example.com"), installedSite("a", "shop.example.com")},
		},
		domains: map[string][]ForgeDomain{
			"a": {domainRecord("shop.example.com", "")},
			"b": {domainRecord("shop.example.com", "")},
		},
	}
	p := ownedProvider(t, client)
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if routerRule(cfg, "forge-a-shop.example.com") == "" {
		t.Fatal("exact tie published no router for the lower site key")
	}
	if routerRule(cfg, "forge-b-shop.example.com") != "" {
		t.Fatal("exact tie published a second router")
	}

	cfg, err = p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if routerRule(cfg, "forge-a-shop.example.com") == "" || routerRule(cfg, "forge-b-shop.example.com") != "" {
		t.Fatal("remembered owner changed on the second poll")
	}
}

// TestCaseVariantDoesNotCaptureExactHost keeps the protection that a
// non-canonical spelling loses to one exact spelling.
func TestCaseVariantDoesNotCaptureExactHost(t *testing.T) {
	p := ownedProvider(t, catalogForge{
		servers: []ForgeServer{{
			ID: "s1", Type: "servers",
			Attributes: ForgeServerAttributes{Name: "app01", PrivateIPAddress: "10.0.0.1"},
		}},
		sites: map[string][]ForgeSite{
			"s1": {installedSite("low", "shop.example.com"), installedSite("high", "shop.example.com")},
		},
		domains: map[string][]ForgeDomain{
			"low":  {domainRecord("Shop.Example.com.", "")},
			"high": {domainRecord("shop.example.com", "")},
		},
	})
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if routerRule(cfg, "forge-high-shop.example.com") == "" {
		t.Fatal("exact spelling lost the hostname")
	}
	if routerRule(cfg, "forge-low-shop.example.com") != "" {
		t.Fatal("case and trailing-dot variant captured the hostname")
	}
}

// TestExplicitWWWOutranksImpliedRedirect gives www to the site that has a
// real www record, and still serves an uncontested www host after the apex
// is lost.
func TestExplicitWWWOutranksImpliedRedirect(t *testing.T) {
	p := ownedProvider(t, catalogForge{
		servers: []ForgeServer{{
			ID: "s1", Type: "servers",
			Attributes: ForgeServerAttributes{Name: "app01", PrivateIPAddress: "10.0.0.1"},
		}},
		sites: map[string][]ForgeSite{
			"s1": {installedSite("apex", "example.com"), installedSite("wwwsite", "www.example.com")},
		},
		domains: map[string][]ForgeDomain{
			"apex":    {domainRecord("example.com", "to-www")},
			"wwwsite": {domainRecord("www.example.com", "")},
		},
	})
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	apexRule := routerRule(cfg, "forge-apex-example.com")
	if !ruleHas(apexRule, "example.com") {
		t.Fatalf("apex rule = %q", apexRule)
	}
	if ruleHas(apexRule, "www.example.com") {
		t.Fatalf("implied www stayed on the apex router: %s", apexRule)
	}
	wwwRule := routerRule(cfg, "forge-wwwsite-www.example.com")
	if !ruleHas(wwwRule, "www.example.com") {
		t.Fatalf("explicit www record was not routed: %q", wwwRule)
	}
}

func TestLostApexStillServesOwnedWWW(t *testing.T) {
	p := ownedProvider(t, catalogForge{
		servers: []ForgeServer{{
			ID: "s1", Type: "servers",
			Attributes: ForgeServerAttributes{Name: "app01", PrivateIPAddress: "10.0.0.1"},
		}},
		sites: map[string][]ForgeSite{
			"s1": {installedSite("owner", "example.com"), installedSite("other", "example.com")},
		},
		domains: map[string][]ForgeDomain{
			"owner": {domainRecord("Example.com", "to-www")},
			"other": {domainRecord("example.com", "")},
		},
	})
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if routerRule(cfg, "forge-other-example.com") == "" {
		t.Fatal("exact apex was not routed")
	}
	wwwRule := routerRule(cfg, "forge-owner-example.com")
	if !ruleHas(wwwRule, "www.example.com") {
		t.Fatalf("uncontested www host was dropped: %q", wwwRule)
	}
	if ruleHas(wwwRule, "example.com") {
		t.Fatalf("lost apex was included in the rule: %s", wwwRule)
	}
}

func TestReverbSurvivesLossOfMainHost(t *testing.T) {
	p := ownedProvider(t, catalogForge{
		servers: []ForgeServer{{
			ID: "s1", Type: "servers",
			Attributes: ForgeServerAttributes{Name: "app01", PrivateIPAddress: "10.0.0.1"},
		}},
		sites: map[string][]ForgeSite{
			"s1": {installedSite("b", "shop.example.com"), installedSite("a", "shop.example.com")},
		},
		domains: map[string][]ForgeDomain{
			"a": {domainRecord("shop.example.com", "")},
			"b": {domainRecord("shop.example.com", ""), domainRecord("ws.shop.example.com", "")},
		},
		reverb: map[string]*ForgeReverbIntegration{
			"b": {Enabled: true, Host: "ws.shop.example.com"},
		},
	})
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if routerRule(cfg, "forge-a-shop.example.com") == "" {
		t.Fatal("lower key did not keep the shared apex")
	}
	if routerRule(cfg, "forge-b-shop.example.com") != "" {
		t.Fatal("losing site still published the apex")
	}
	if routerRule(cfg, "forge-b-ws.shop.example.com") == "" {
		t.Fatal("reverb host the site alone owns was not published")
	}
	if cfg.HTTP.Services["forge-shop.example.com-b-service"] == nil {
		t.Fatal("service was not created for the reverb-only publish")
	}
}

func TestExactApexTieKeepsWWWWithTheApex(t *testing.T) {
	p := ownedProvider(t, catalogForge{
		servers: []ForgeServer{{
			ID: "s1", Type: "servers",
			Attributes: ForgeServerAttributes{Name: "app01", PrivateIPAddress: "10.0.0.1"},
		}},
		sites: map[string][]ForgeSite{
			"s1": {installedSite("b", "example.com"), installedSite("a", "example.com")},
		},
		domains: map[string][]ForgeDomain{
			"a": {domainRecord("example.com", "to-www")},
			"b": {domainRecord("example.com", "to-www")},
		},
	})
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	rule := routerRule(cfg, "forge-a-example.com")
	if !ruleHas(rule, "example.com") || !ruleHas(rule, "www.example.com") {
		t.Fatalf("apex winner rule = %q", rule)
	}
	if routerRule(cfg, "forge-b-example.com") != "" {
		t.Fatal("losing site still published the apex or www host")
	}
}

func TestDomainRecordBeatsLowerAlias(t *testing.T) {
	alias := installedSite("a", "other.example.com")
	alias.Attributes.Tags = []string{"traefik:aliases=shop.example.com"}
	p := ownedProvider(t, catalogForge{
		servers: []ForgeServer{{
			ID: "s1", Type: "servers",
			Attributes: ForgeServerAttributes{Name: "app01", PrivateIPAddress: "10.0.0.1"},
		}},
		sites: map[string][]ForgeSite{
			"s1": {alias, installedSite("b", "shop.example.com")},
		},
		domains: map[string][]ForgeDomain{
			"a": {domainRecord("other.example.com", "")},
			"b": {domainRecord("shop.example.com", "")},
		},
	})
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if !ruleHas(routerRule(cfg, "forge-b-shop.example.com"), "shop.example.com") {
		t.Fatal("domain record lost the hostname to an alias")
	}
	if ruleHas(routerRule(cfg, "forge-a-other.example.com"), "shop.example.com") {
		t.Fatal("alias captured a hostname that has a domain record")
	}
}

func TestStopIsSafeAndIdempotent(t *testing.T) {
	p, err := NewProviderWithClient(&Config{PollInterval: "30s"}, stubClient{})
	if err != nil {
		t.Fatal(err)
	}

	cfgChan := make(chan json.Marshaler) // deliberately never drained
	if err := p.Provide(cfgChan); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	// Stop must be idempotent, and safe on a provider that never ran.
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := (&Provider{}).Stop(); err != nil {
		t.Fatal(err)
	}
}

type stubClient struct{}

func (stubClient) FetchServers() ([]ForgeServer, error)               { return nil, nil }
func (stubClient) FetchSites(string) ([]ForgeSite, error)             { return nil, nil }
func (stubClient) FetchDomains(string, string) ([]ForgeDomain, error) { return nil, nil }
func (stubClient) FetchReverbIntegration(string, string) (*ForgeReverbIntegration, error) {
	return nil, nil
}
