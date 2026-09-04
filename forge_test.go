package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
