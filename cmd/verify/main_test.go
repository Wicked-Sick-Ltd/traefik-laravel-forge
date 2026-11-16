package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	forge "github.com/Wicked-Sick-Ltd/traefik-laravel-forge"
	"github.com/traefik/genconf/dynamic"
)

type fakeForge struct {
	siteTags []string
	domains  []forge.ForgeDomain
}

func (f fakeForge) FetchServers() ([]forge.ForgeServer, error) {
	return []forge.ForgeServer{{
		ID:         "1",
		Attributes: forge.ForgeServerAttributes{Name: "web", PrivateIPAddress: "192.0.2.10"},
	}}, nil
}

func (f fakeForge) FetchSites(string) ([]forge.ForgeSite, error) {
	return []forge.ForgeSite{{
		ID:         "7",
		Attributes: forge.ForgeSiteAttributes{Name: "app.example.com", Status: "installed", Tags: f.siteTags},
	}}, nil
}

func (f fakeForge) FetchDomains(string, string) ([]forge.ForgeDomain, error) {
	return f.domains, nil
}

func (f fakeForge) FetchReverbIntegration(string, string) (*forge.ForgeReverbIntegration, error) {
	return nil, nil
}

func domain(name string, wildcard bool, www string) forge.ForgeDomain {
	return forge.ForgeDomain{Attributes: forge.ForgeDomainAttributes{
		Name: name, Status: "enabled", AllowWildcardSubdomains: wildcard, WWWRedirectType: www,
	}}
}

func generate(t *testing.T, client forge.ForgeClient) *dynamic.Configuration {
	t.Helper()
	p, err := forge.NewProviderWithClient(&forge.Config{PollInterval: "30s", DefaultSitesEnabled: true}, client)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := p.GenerateConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A Forge value that closes the Host string and appends another matcher ends up
// in the generated rule verbatim. The comparison must not report that rule's
// hostname as covered, and must name the extra clause in the summary itself.
func TestCompareRejectsRulesWithNonHostMatchers(t *testing.T) {
	tests := []struct {
		name   string
		client fakeForge
		clause string
	}{
		{
			name: "alias tag appends PathPrefix",
			client: fakeForge{
				siteTags: []string{"traefik:aliases=app.example.com`) || PathPrefix(`/"},
				domains:  []forge.ForgeDomain{domain("app.example.com", false, "")},
			},
			clause: "PathPrefix(`/`)",
		},
		{
			name: "alias tag appends a catch-all HostRegexp",
			client: fakeForge{
				siteTags: []string{"traefik:aliases=app.example.com`) || HostRegexp(`.*"},
				domains:  []forge.ForgeDomain{domain("app.example.com", false, "")},
			},
			clause: "HostRegexp(`.*`)",
		},
		{
			name: "domain name narrows with && Header",
			client: fakeForge{
				domains: []forge.ForgeDomain{domain("app.example.com`) && Header(`X-A`, `1", false, "")},
			},
			clause: "Header(`X-A`, `1`)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generated := generate(t, tt.client)

			var out bytes.Buffer
			err := compare(&out, generated, map[string]bool{"app.example.com": true})
			got := out.String()

			if err == nil {
				t.Errorf("compare() error = nil, want an error for a rule with a non-host matcher")
			}
			if strings.Contains(got, "OK: All hosts") {
				t.Errorf("summary claims full coverage:\n%s", got)
			}
			if !strings.Contains(got, tt.clause) {
				t.Errorf("summary does not name the extra clause %q:\n%s", tt.clause, got)
			}
			if strings.Contains(got, "= app.example.com") {
				t.Errorf("summary lists app.example.com as MATCHED:\n%s", got)
			}
		})
	}
}

func TestCompareAcceptsHostOnlyRules(t *testing.T) {
	generated := generate(t, fakeForge{
		siteTags: []string{"traefik:aliases=app.example.net"},
		domains:  []forge.ForgeDomain{domain("app.example.com", true, "to-www")},
	})
	existing := map[string]bool{
		"app.example.com":     true,
		"www.app.example.com": true,
		"*.app.example.com":   true,
		"app.example.net":     true,
	}

	var out bytes.Buffer
	if err := compare(&out, generated, existing); err != nil {
		t.Fatalf("compare() error = %v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "OK: All hosts") {
		t.Errorf("summary does not report full coverage:\n%s", got)
	}
	if !strings.Contains(got, "MATCHED (4 hosts") {
		t.Errorf("summary does not match all four hosts:\n%s", got)
	}
}

func TestExtractHosts(t *testing.T) {
	tests := []struct {
		name string
		rule string
		want []string
	}{
		{
			name: "single host",
			rule: "Host(`example.com`)",
			want: []string{"example.com"},
		},
		{
			name: "www variant is not dropped",
			rule: "Host(`example.com`) || Host(`www.example.com`)",
			want: []string{"example.com", "www.example.com"},
		},
		{
			name: "wildcard clause normalised to *.domain",
			rule: "Host(`example.com`) || HostRegexp(`^[^.]+\\.example\\.com$`)",
			want: []string{"example.com", "*.example.com"},
		},
		{
			name: "wildcard-only rule yields a host, not the raw rule",
			rule: "HostRegexp(`^[^.]+\\.example\\.com$`)",
			want: []string{"*.example.com"},
		},
		{
			name: "rule with no host clause yields nothing",
			rule: "PathPrefix(`/api`)",
			want: nil,
		},
		{
			name: "unterminated clause is ignored",
			rule: "Host(`example.com",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractHosts(tt.rule); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractHosts(%q) = %v, want %v", tt.rule, got, tt.want)
			}
		})
	}
}

func TestParseHostRule(t *testing.T) {
	tests := []struct {
		rule           string
		wantHosts      []string
		wantUnaccepted string
		wantOK         bool
	}{
		{rule: "Host(`example.com`)", wantHosts: []string{"example.com"}, wantOK: true},
		{
			rule:      "Host(`example.com`) || Host(`www.example.com`) || HostRegexp(`^[^.]+\\.example\\.com$`)",
			wantHosts: []string{"example.com", "www.example.com", "*.example.com"},
			wantOK:    true,
		},
		{rule: "Host(`a.com`) || PathPrefix(`/`)", wantUnaccepted: "PathPrefix(`/`)"},
		{rule: "Host(`a.com`) && PathPrefix(`/api`)", wantUnaccepted: "&& PathPrefix(`/api`)"},
		{rule: "Host(`a.com`)||Method(`GET`)", wantUnaccepted: "Method(`GET`)"},
		{rule: "!Host(`a.com`)", wantUnaccepted: "!Host(`a.com`)"},
		{rule: "(Host(`a.com`))", wantUnaccepted: "(Host(`a.com`))"},
		{rule: "Host(`a.com`, `b.com`)", wantUnaccepted: "Host(`a.com`, `b.com`)"},
		{rule: "HostRegexp(`.*`)", wantUnaccepted: "HostRegexp(`.*`)"},
		{rule: "HostRegexp(`^[^.]+\\.a.com$`)", wantUnaccepted: "HostRegexp(`^[^.]+\\.a.com$`)"},
		{rule: "Host(`*.a.com`)", wantUnaccepted: "Host(`*.a.com`)"},
		{rule: "Host(`a.com", wantUnaccepted: "Host(`a.com"},
		{rule: "", wantUnaccepted: "(empty rule)"},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			hosts, unaccepted, ok := parseHostRule(tt.rule)
			if ok != tt.wantOK || unaccepted != tt.wantUnaccepted || !reflect.DeepEqual(hosts, tt.wantHosts) {
				t.Errorf("parseHostRule(%q) = (%v, %q, %v), want (%v, %q, %v)",
					tt.rule, hosts, unaccepted, ok, tt.wantHosts, tt.wantUnaccepted, tt.wantOK)
			}
		})
	}
}

func TestExtractHostsFromConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dynamic.yml")
	content := `http:
  routers:
    site:
      rule: "Host(` + "`" + `acme.co.uk` + "`" + `) || Host(` + "`" + `www.acme.co.uk` + "`" + `)"
    wild:
      rule: "HostRegexp(` + "`" + `^[^.]+\.acme\.co\.uk$` + "`" + `)"
    api:
      rule: "PathPrefix(` + "`" + `/api` + "`" + `)"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := extractHostsFromConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		"acme.co.uk":     true,
		"www.acme.co.uk": true,
		"*.acme.co.uk":   true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractHostsFromConfig() = %v, want %v", got, want)
	}
}
