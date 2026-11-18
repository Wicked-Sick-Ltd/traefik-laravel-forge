package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/traefik/genconf/dynamic"
)

const (
	testAPIToken     = "test-token"
	testOrganization = "test-org"
	testResolver     = "cloudflare"
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
		name                string
		tags                []string
		wantUpstreamHost    string
		wantUpstreamPort    int
		wantUpstreamPortSet bool
		wantTraefikID       string
	}{
		{
			name:                "upstream host and port",
			tags:                []string{"traefik:upstream-host=10.0.1.10", "traefik:upstream-port=8080"},
			wantUpstreamHost:    "10.0.1.10",
			wantUpstreamPort:    8080,
			wantUpstreamPortSet: true,
		},
		{
			name:             "host only — port defaults to 80",
			tags:             []string{"traefik:upstream-host=192.168.1.50"},
			wantUpstreamHost: "192.168.1.50",
			wantUpstreamPort: 80,
		},
		{
			name:                "port only — host empty",
			tags:                []string{"traefik:upstream-port=9090"},
			wantUpstreamPort:    9090,
			wantUpstreamPortSet: true,
		},
		{
			name:                "explicit port 80 still counts as set",
			tags:                []string{"traefik:upstream-port=80"},
			wantUpstreamPort:    80,
			wantUpstreamPortSet: true,
		},
		{
			name:             "invalid port is ignored and does not count as set",
			tags:             []string{"traefik:upstream-port=80x"},
			wantUpstreamPort: 80,
		},
		{
			name:                "a later invalid port leaves the earlier valid one set",
			tags:                []string{"traefik:upstream-port=9090", "traefik:upstream-port=999999"},
			wantUpstreamPort:    9090,
			wantUpstreamPortSet: true,
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
			name:                "lb-host/lb-port aliases",
			tags:                []string{"traefik:lb-host=10.0.0.1", "traefik:lb-port=3000"},
			wantUpstreamHost:    "10.0.0.1",
			wantUpstreamPort:    3000,
			wantUpstreamPortSet: true,
		},
		{
			name:                "loadbalancer aliases",
			tags:                []string{"traefik:loadbalancer-host=10.0.0.2", "traefik:loadbalancer-port=4000"},
			wantUpstreamHost:    "10.0.0.2",
			wantUpstreamPort:    4000,
			wantUpstreamPortSet: true,
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
			if got.UpstreamPortSet != tt.wantUpstreamPortSet {
				t.Errorf("UpstreamPortSet = %v, want %v", got.UpstreamPortSet, tt.wantUpstreamPortSet)
			}
			if got.TraefikID != tt.wantTraefikID {
				t.Errorf("TraefikID = %q, want %q", got.TraefikID, tt.wantTraefikID)
			}
		})
	}
}

// TestParseServerTagsUpstreamHostSafety pins the characters an upstream-host
// tag may contain. A newline must not be copied into a log line, and a
// bracketed IPv6 literal must still be used as the backend host.
func TestParseServerTagsUpstreamHostSafety(t *testing.T) {
	keys := []string{"upstream-host", "upstreamhost", "lb-host", "loadbalancer-host"}
	rejected := []string{
		"10.0.0.1\nforged",
		"10.0.0.1\rforged",
		"10.0.0.1\u2028forged",
		"host:8080",
		"2001:db8::1",
		"[2001:db8::1]:80",
		"10.0.0.1/24",
		"user@host",
		"[fe80::1%eth0]",
		"[10.0.0.1]",
	}
	accepted := []struct{ value, want string }{
		{"app-01.internal_lan", "app-01.internal_lan"},
		{"10.0.1.10", "10.0.1.10"},
		{"[2001:db8::1]", "[2001:db8::1]"},
		{"[fd00::10]", "[fd00::10]"},
	}
	for _, key := range keys {
		for _, value := range rejected {
			got := ParseServerTags([]string{"traefik:" + key + "=" + value})
			if got.UpstreamHost != "" {
				t.Errorf("traefik:%s=%q accepted as %q", key, value, got.UpstreamHost)
			}
		}
		for _, acc := range accepted {
			got := ParseServerTags([]string{"traefik:" + key + "=" + acc.value})
			if got.UpstreamHost != acc.want {
				t.Errorf("traefik:%s=%q host = %q, want %q", key, acc.value, got.UpstreamHost, acc.want)
			}
		}
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
		CertResolver: testResolver,
		SitesEnabled: true,
		HTTPRedirect: true,
		Port:         80,
	}

	t.Run("no tags — all defaults applied", func(t *testing.T) {
		got := parseSiteTags(nil, defaults)
		if !got.Enabled {
			t.Error("Enabled should be true by default")
		}
		if got.CertResolver != testResolver {
			t.Errorf("CertResolver = %q, want %q", got.CertResolver, testResolver)
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
		{
			name:     "port tag with no host tag applies to the auto-detected host",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamPort: 9090, UpstreamPortSet: true},
			wantHost: "10.0.0.1",
			wantPort: 9090,
		},
		{
			name:     "port tag outranks a mapping host's port",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamPort: 9090, UpstreamPortSet: true},
			mapping:  &ServerMapping{UpstreamHost: "192.168.0.1", UpstreamPort: 8080},
			wantHost: "192.168.0.1",
			wantPort: 9090,
		},
		{
			name:     "port tag outranks a port-only mapping",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamPort: 9090, UpstreamPortSet: true},
			mapping:  &ServerMapping{ForgeServerName: "app01", UpstreamPort: 8080},
			wantHost: "10.0.0.1",
			wantPort: 9090,
		},
		{
			name:     "explicit port 80 outranks a mapping port",
			server:   bothIPs,
			tag:      ServerConfig{UpstreamPort: 80, UpstreamPortSet: true},
			mapping:  &ServerMapping{UpstreamHost: "192.168.0.1", UpstreamPort: 8080},
			wantHost: "192.168.0.1",
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

func emptyHTTPConfig() *dynamic.Configuration {
	return &dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{
			Routers:     map[string]*dynamic.Router{},
			Services:    map[string]*dynamic.Service{},
			Middlewares: map[string]*dynamic.Middleware{},
		},
	}
}

// TestHTTPRedirectRouterFollowsPublicEntrypoints pins when the -http router is
// emitted. websecure is the public TLS entry point, so a site tagged with only
// that name keeps its redirect. A site tagged with only an internal entry point
// does not, and a site-level redirect with no global middleware still attaches
// forge-https-redirect.
func TestHTTPRedirectRouterFollowsPublicEntrypoints(t *testing.T) {
	const routerName = "forge-1-example.com"

	t.Run("websecure only keeps the redirect", func(t *testing.T) {
		cfg := emptyHTTPConfig()
		createRouter(routerName, "Host(`example.com`)", "svc", siteTagConfig{
			EntryPoints:  []string{httpsEntryPoint},
			HTTPRedirect: true,
		}, true, "cloudflare", 100, cfg, "https-redirect")
		httpRouter := cfg.HTTP.Routers[routerName+"-http"]
		if httpRouter == nil {
			t.Fatal("websecure-only site has no HTTP redirect router")
		}
		if len(httpRouter.EntryPoints) != 1 || httpRouter.EntryPoints[0] != httpEntryPoint {
			t.Errorf("redirect entrypoints = %v", httpRouter.EntryPoints)
		}
		if len(httpRouter.Middlewares) != 1 || httpRouter.Middlewares[0] != "https-redirect" {
			t.Errorf("redirect middlewares = %v", httpRouter.Middlewares)
		}
	})

	t.Run("internal only skips the redirect", func(t *testing.T) {
		cfg := emptyHTTPConfig()
		createRouter(routerName, "Host(`example.com`)", "svc", siteTagConfig{
			EntryPoints:  []string{"internal"},
			HTTPRedirect: true,
		}, true, "cloudflare", 100, cfg, "https-redirect")
		if _, ok := cfg.HTTP.Routers[routerName+"-http"]; ok {
			t.Fatal("internal-only site published an HTTP redirect router")
		}
		if cfg.HTTP.Routers[routerName] == nil {
			t.Fatal("main router was not created")
		}
	})

	t.Run("websecure plus internal keeps the redirect", func(t *testing.T) {
		cfg := emptyHTTPConfig()
		createRouter(routerName, "Host(`example.com`)", "svc", siteTagConfig{
			EntryPoints:  []string{httpsEntryPoint, "internal"},
			HTTPRedirect: true,
		}, true, "cloudflare", 100, cfg, "https-redirect")
		if cfg.HTTP.Routers[routerName+"-http"] == nil {
			t.Fatal("site on websecure lost its HTTP redirect router")
		}
	})

	t.Run("site redirect without a global middleware attaches forge-https-redirect", func(t *testing.T) {
		cfg := emptyHTTPConfig()
		createRouter(routerName, "Host(`example.com`)", "svc", siteTagConfig{
			HTTPRedirect: true,
		}, true, "cloudflare", 100, cfg, "")
		httpRouter := cfg.HTTP.Routers[routerName+"-http"]
		if httpRouter == nil {
			t.Fatal("unset entrypoints dropped the HTTP redirect router")
		}
		if len(httpRouter.Middlewares) != 1 || httpRouter.Middlewares[0] != defaultRedirectMiddleware {
			t.Errorf("middlewares = %v, want [%s]", httpRouter.Middlewares, defaultRedirectMiddleware)
		}
		if cfg.HTTP.Middlewares[defaultRedirectMiddleware] == nil {
			t.Fatal("forge-https-redirect middleware was not defined")
		}
	})
}

// -- Lifecycle --

// Stop must be safe to call at any point in the lifecycle: after Provide, twice
// in a row, and on a provider that never started.
// catalogForge serves one server and whatever sites, domains and Reverb
// records the test put on it.
type catalogForge struct {
	servers []ForgeServer
	sites   map[string][]ForgeSite
	domains map[string][]ForgeDomain
	reverb  map[string]*ForgeReverbIntegration
}

func (c *catalogForge) FetchServers() ([]ForgeServer, error) { return c.servers, nil }
func (c *catalogForge) FetchSites(serverID string) ([]ForgeSite, error) {
	return c.sites[serverID], nil
}
func (c *catalogForge) FetchDomains(_, siteID string) ([]ForgeDomain, error) {
	return c.domains[siteID], nil
}
func (c *catalogForge) FetchReverbIntegration(_, siteID string) (*ForgeReverbIntegration, error) {
	return c.reverb[siteID], nil
}

func routingProvider(t *testing.T, client ForgeClient) *Provider {
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

func enabledDomain(name string) ForgeDomain {
	domain := ForgeDomain{ID: name}
	domain.Type = "domainRecords"
	domain.Attributes.Name = name
	domain.Attributes.Status = statusEnabled
	return domain
}

func namedSite(id, name string, tags []string) ForgeSite {
	site := ForgeSite{ID: id}
	site.Type = "sites"
	site.Attributes.Name = name
	site.Attributes.Status = statusInstalled
	site.Attributes.Tags = tags
	return site
}

func appServer() ForgeServer {
	server := ForgeServer{ID: "s1"}
	server.Type = "servers"
	server.Attributes.Name = "app01"
	server.Attributes.PrivateIPAddress = "10.0.0.1"
	return server
}

func newCatalog(server ForgeServer, sites []ForgeSite, domains map[string][]ForgeDomain, reverb map[string]*ForgeReverbIntegration) *catalogForge {
	c := &catalogForge{}
	c.servers = append(c.servers, server)
	c.sites = map[string][]ForgeSite{}
	c.sites[server.ID] = sites
	if domains == nil {
		domains = map[string][]ForgeDomain{}
	}
	c.domains = domains
	if reverb == nil {
		reverb = map[string]*ForgeReverbIntegration{}
	}
	c.reverb = reverb
	return c
}

// TestOnForgeFilterDoesNotPublishSiteName pins the site-name fallback to sites
// that have no main domain records, and keeps a Reverb router when the only
// main record is a Forge default name.
func TestOnForgeFilterDoesNotPublishSiteName(t *testing.T) {
	server := appServer()

	t.Run("on-forge plus reverb publishes only the reverb host", func(t *testing.T) {
		sites := append([]ForgeSite{}, namedSite("site1", "example.com", nil))
		domains := map[string][]ForgeDomain{}
		domains["site1"] = append(domains["site1"], enabledDomain("app.on-forge.com"), enabledDomain("ws.example.com"))
		reverb := map[string]*ForgeReverbIntegration{"site1": {Enabled: true, Host: "ws.example.com"}}
		p := routingProvider(t, newCatalog(server, sites, domains, reverb))
		cfg, err := p.GenerateConfiguration()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.HTTP.Routers["forge-site1-example.com"] != nil {
			t.Fatal("site name was published after .on-forge.com records were filtered")
		}
		if cfg.HTTP.Routers["forge-site1-app.on-forge.com"] != nil {
			t.Fatal(".on-forge.com router was published")
		}
		reverbRouter := cfg.HTTP.Routers["forge-site1-ws.example.com"]
		if reverbRouter == nil {
			t.Fatal("reverb router was not published")
		}
		if reverbRouter.Priority != domainPriority("ws.example.com") {
			t.Errorf("reverb priority = %d, want %d", reverbRouter.Priority, domainPriority("ws.example.com"))
		}
		if cfg.HTTP.Services["forge-example.com-site1-service"] == nil {
			t.Fatal("service was not created for the reverb-only site")
		}
	})

	t.Run("no domain records falls back to the site name", func(t *testing.T) {
		sites := append([]ForgeSite{}, namedSite("site1", "example.com", nil))
		p := routingProvider(t, newCatalog(server, sites, nil, nil))
		cfg, err := p.GenerateConfiguration()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.HTTP.Routers["forge-site1-example.com"] == nil {
			t.Fatal("site with no domain records did not fall back to its name")
		}
	})
}

// TestContestedHostIsPublishedOnNoSite drops a hostname that two sites both
// publish, including a Reverb host another site also aliases. Neither router
// keeps the name, so a Reverb record cannot capture someone else's host.
func TestContestedHostStaysWithLowestSiteID(t *testing.T) {
	server := appServer()
	sites := append([]ForgeSite{},
		namedSite("owner", "shop.example.com", nil),
		namedSite("peer", "other.example.com", []string{"traefik:aliases=ws.example.com"}),
	)
	domains := map[string][]ForgeDomain{}
	domains["owner"] = append(domains["owner"], enabledDomain("shop.example.com"), enabledDomain("ws.example.com"))
	domains["peer"] = append(domains["peer"], enabledDomain("other.example.com"))
	reverb := map[string]*ForgeReverbIntegration{"owner": {Enabled: true, Host: "ws.example.com"}}
	p := routingProvider(t, newCatalog(server, sites, domains, reverb))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Routers["forge-owner-ws.example.com"] == nil {
		t.Fatal("lowest site id lost the contested host")
	}
	if cfg.HTTP.Routers["forge-peer-ws.example.com"] != nil {
		t.Fatal("higher site id still published the contested host")
	}
	if cfg.HTTP.Routers["forge-owner-shop.example.com"] == nil || cfg.HTTP.Routers["forge-peer-other.example.com"] == nil {
		t.Fatal("uncontested hosts were dropped with the contested one")
	}
}

func TestSameSiteWWWAliasIsNotAContest(t *testing.T) {
	server := appServer()
	domain := enabledDomain("example.com")
	domain.Attributes.WWWRedirectType = "to-www"
	site := namedSite("site1", "example.com", []string{"traefik:aliases=www.example.com"})
	domains := map[string][]ForgeDomain{}
	domains["site1"] = append(domains["site1"], domain)
	p := routingProvider(t, newCatalog(server, []ForgeSite{site}, domains, nil))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	apex := cfg.HTTP.Routers["forge-site1-example.com"]
	alias := cfg.HTTP.Routers["forge-site1-www.example.com"]
	if apex == nil || alias == nil {
		t.Fatalf("apex=%v alias=%v", apex != nil, alias != nil)
	}
	if !strings.Contains(apex.Rule, "Host(`www.example.com`)") || !strings.Contains(alias.Rule, "Host(`www.example.com`)") {
		t.Fatalf("www was treated as a contest: apex=%s alias=%s", apex.Rule, alias.Rule)
	}
}

func TestDisjointEntrypointsAreNotAContest(t *testing.T) {
	server := appServer()
	web := namedSite("a", "shop.example.com", []string{"traefik:entrypoints=web", "traefik:tls=false"})
	secure := namedSite("b", "shop.example.com", []string{"traefik:entrypoints=websecure", "traefik:tls=true"})
	domains := map[string][]ForgeDomain{}
	domains["a"] = append(domains["a"], enabledDomain("shop.example.com"))
	domains["b"] = append(domains["b"], enabledDomain("shop.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{web, secure}, domains, nil))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Routers["forge-a-shop.example.com"] == nil || cfg.HTTP.Routers["forge-b-shop.example.com"] == nil {
		t.Fatal("disjoint entry points were treated as one contest")
	}
}

func TestAliasRuleInjectionIsIgnored(t *testing.T) {
	server := appServer()
	owner := namedSite("owner", "shop.example.com", nil)
	peer := namedSite("peer", "other.example.com", []string{"traefik:aliases=shop.example.com`) && PathPrefix(`/admin"})
	domains := map[string][]ForgeDomain{}
	domains["owner"] = append(domains["owner"], enabledDomain("shop.example.com"))
	domains["peer"] = append(domains["peer"], enabledDomain("other.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{owner, peer}, domains, nil))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Routers["forge-owner-shop.example.com"] == nil {
		t.Fatal("owner router was removed because of an alias that is not a hostname")
	}
	for name := range cfg.HTTP.Routers {
		if strings.Contains(name, "PathPrefix") || strings.Contains(cfg.HTTP.Routers[name].Rule, "PathPrefix") {
			t.Fatalf("injected rule was published: %s %s", name, cfg.HTTP.Routers[name].Rule)
		}
	}
}

func TestParentWildcardDoesNotCaptureKeptExactHost(t *testing.T) {
	server := appServer()
	exact := namedSite("a", "shop.example.com", nil)
	wild := namedSite("b", "example.com", nil)
	wildDomain := enabledDomain("example.com")
	wildDomain.Attributes.AllowWildcardSubdomains = true
	domains := map[string][]ForgeDomain{}
	domains["a"] = append(domains["a"], enabledDomain("shop.example.com"))
	domains["b"] = append(domains["b"], wildDomain)
	p := routingProvider(t, newCatalog(server, []ForgeSite{exact, wild}, domains, nil))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	child := cfg.HTTP.Routers["forge-a-shop.example.com"]
	parent := cfg.HTTP.Routers["forge-b-example.com"]
	if child == nil || parent == nil {
		t.Fatalf("child=%v parent=%v", child != nil, parent != nil)
	}
	if !strings.Contains(child.Rule, "Host(`shop.example.com`)") {
		t.Fatalf("exact router lost its host: %s", child.Rule)
	}
	if child.Priority <= parent.Priority {
		t.Fatalf("exact priority %d does not beat wildcard priority %d", child.Priority, parent.Priority)
	}
}

func TestNumericSiteIDBeatsLongerText(t *testing.T) {
	server := appServer()
	older := namedSite("2", "shop.example.com", nil)
	newer := namedSite("10", "shop.example.com", nil)
	domains := map[string][]ForgeDomain{}
	domains["2"] = append(domains["2"], enabledDomain("shop.example.com"))
	domains["10"] = append(domains["10"], enabledDomain("shop.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{older, newer}, domains, nil))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Routers["forge-2-shop.example.com"] == nil {
		t.Fatal("numeric id 2 lost the host to id 10")
	}
	if cfg.HTTP.Routers["forge-10-shop.example.com"] != nil {
		t.Fatal("id 10 kept a host that id 2 also published")
	}
}

func TestSingleLabelAliasIsRouted(t *testing.T) {
	server := appServer()
	site := namedSite("site1", "example.com", []string{"traefik:aliases=intranet"})
	domains := map[string][]ForgeDomain{"site1": {enabledDomain("example.com")}}
	p := routingProvider(t, newCatalog(server, []ForgeSite{site}, domains, nil))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Routers["forge-site1-intranet"] == nil {
		t.Fatal("single-label alias was not routed")
	}
}

func TestWebOverlapDoesNotDropHTTPS(t *testing.T) {
	server := appServer()
	webOnly := namedSite("1", "shop.example.com", []string{"traefik:entrypoints=web", "traefik:tls=false"})
	both := namedSite("2", "shop.example.com", []string{"traefik:entrypoints=web,websecure", "traefik:tls=true"})
	domains := map[string][]ForgeDomain{}
	domains["1"] = append(domains["1"], enabledDomain("shop.example.com"))
	domains["2"] = append(domains["2"], enabledDomain("shop.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{webOnly, both}, domains, nil))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	httpsRouter := cfg.HTTP.Routers["forge-2-shop.example.com"]
	if httpsRouter == nil {
		t.Fatal("site that also listens on websecure lost the host")
	}
	if !slices.Contains(httpsRouter.EntryPoints, "websecure") {
		t.Fatalf("websecure was removed: %v", httpsRouter.EntryPoints)
	}
}

func TestCaseVariantAliasIsTheSameHost(t *testing.T) {
	server := appServer()
	owner := namedSite("5", "shop.example.com", nil)
	peer := namedSite("9", "other.example.com", []string{"traefik:aliases=Shop.example.com"})
	domains := map[string][]ForgeDomain{}
	domains["5"] = append(domains["5"], enabledDomain("shop.example.com"))
	domains["9"] = append(domains["9"], enabledDomain("other.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{owner, peer}, domains, nil))
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Routers["forge-5-shop.example.com"] == nil {
		t.Fatal("canonical owner lost the host")
	}
	if cfg.HTTP.Routers["forge-9-Shop.example.com"] != nil {
		t.Fatal("case-variant alias was treated as a different host")
	}
}

func TestRedirectEntrypointCountsInTheConflict(t *testing.T) {
	server := appServer()
	owner := namedSite("1", "shop.example.com", nil)
	peer := namedSite("2", "shop.example.com", []string{"traefik:tls=false"})
	domains := map[string][]ForgeDomain{}
	domains["1"] = append(domains["1"], enabledDomain("shop.example.com"))
	domains["2"] = append(domains["2"], enabledDomain("shop.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{owner, peer}, domains, nil))
	p.defaultCertResolver = testResolver
	p.httpRedirect = true
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Routers["forge-1-shop.example.com"] == nil {
		t.Fatal("TLS owner lost the host")
	}
	if cfg.HTTP.Routers["forge-2-shop.example.com"] != nil {
		t.Fatal("plain-HTTP peer still publishes the owner's host")
	}
}

func TestRedirectWinDoesNotBlankEntrypoints(t *testing.T) {
	server := appServer()
	lower := namedSite("1", "shop.example.com", []string{"traefik:entrypoints=websecure", "traefik:http-redirect=false", "traefik:tls=true"})
	higher := namedSite("2", "shop.example.com", nil)
	domains := map[string][]ForgeDomain{}
	domains["1"] = append(domains["1"], enabledDomain("shop.example.com"))
	domains["2"] = append(domains["2"], enabledDomain("shop.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{lower, higher}, domains, nil))
	p.defaultCertResolver = testResolver
	p.httpRedirect = true
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	for name, router := range cfg.HTTP.Routers {
		if len(router.EntryPoints) == 0 {
			t.Fatalf("router %s has no entry points", name)
		}
	}
	if cfg.HTTP.Routers["forge-1-shop.example.com"] == nil {
		t.Fatal("lower id lost websecure")
	}
	if cfg.HTTP.Routers["forge-2-shop.example.com-http"] == nil {
		t.Fatal("redirect router was removed from the site that won web")
	}
}

func TestOneContestedHostDoesNotDropOtherRedirects(t *testing.T) {
	server := appServer()
	apex := namedSite("2", "example.com", nil)
	alias := namedSite("1", "other.example.com", []string{"traefik:tls=false", "traefik:aliases=www.example.com"})
	apexDomain := enabledDomain("example.com")
	apexDomain.Attributes.WWWRedirectType = "to-www"
	domains := map[string][]ForgeDomain{}
	domains["2"] = append(domains["2"], apexDomain)
	domains["1"] = append(domains["1"], enabledDomain("other.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{alias, apex}, domains, nil))
	p.defaultCertResolver = testResolver
	p.httpRedirect = true
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	redirect := cfg.HTTP.Routers["forge-2-example.com-http"]
	if redirect == nil {
		t.Fatal("uncontested apex lost its HTTP redirect")
	}
	if !strings.Contains(redirect.Rule, "Host(`example.com`)") {
		t.Fatalf("redirect rule = %s", redirect.Rule)
	}
}

func TestWildcardSurvivesApexContest(t *testing.T) {
	server := appServer()
	owner := namedSite("2", "example.com", nil)
	peer := namedSite("1", "other.example.com", []string{"traefik:tls=false", "traefik:aliases=example.com"})
	wild := enabledDomain("example.com")
	wild.Attributes.AllowWildcardSubdomains = true
	domains := map[string][]ForgeDomain{}
	domains["2"] = append(domains["2"], wild)
	domains["1"] = append(domains["1"], enabledDomain("other.example.com"))
	p := routingProvider(t, newCatalog(server, []ForgeSite{peer, owner}, domains, nil))
	p.defaultCertResolver = testResolver
	p.httpRedirect = true
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	parent := cfg.HTTP.Routers["forge-2-example.com"]
	redirect := cfg.HTTP.Routers["forge-2-example.com-http"]
	if parent == nil || !strings.Contains(parent.Rule, "HostRegexp") {
		t.Fatalf("wildcard router = %v", parent)
	}
	if redirect == nil || !strings.Contains(redirect.Rule, "HostRegexp") {
		t.Fatalf("wildcard redirect = %v", redirect)
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
