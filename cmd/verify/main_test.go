package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestUnexpectedArgsMessage(t *testing.T) {
	secret := "forge-token-not-on-argv-9f3c"
	if got := unexpectedArgsMessage(0); got != "" {
		t.Fatalf("narg 0 = %q, want empty", got)
	}

	got := unexpectedArgsMessage(1)
	if got != unexpectedArgsMessage(2) {
		t.Fatalf("message depends on the argument count: %q vs %q", got, unexpectedArgsMessage(2))
	}
	if strings.Contains(got, secret) {
		t.Fatalf("message included a value: %q", got)
	}
	for _, part := range []string{"FORGE_TOKEN", "-flag=false", "positional arguments"} {
		if !strings.Contains(got, part) {
			t.Fatalf("message %q missing %q", got, part)
		}
	}
}

// TestMainPositionalArgsExitBeforeForge runs the real main path in a child
// process. A leftover word must exit 2 before the Forge token is used, and
// the equals form of a boolean flag must not be treated as that leftover word.
func TestMainPositionalArgsExitBeforeForge(t *testing.T) {
	const childEnv = "VERIFY_CHILD_ARGS"
	if raw := os.Getenv(childEnv); raw != "" {
		os.Args = strings.Split(raw, "\x1e")
		main()
		os.Exit(0)
	}

	secret := "forge-token-not-on-argv-9f3c"
	tests := []struct {
		name      string
		args      []string
		withToken bool
		exitCode  int
		want      string
		absent    []string
	}{
		{
			name:      "bare token",
			args:      []string{"verify", secret},
			withToken: true,
			exitCode:  2,
			want:      "boolean flags need -flag=false",
			absent:    []string{secret},
		},
		{
			name:      "bool flag with a separate word",
			args:      []string{"verify", "--default-sites-enabled", "false"},
			withToken: true,
			exitCode:  2,
			want:      "the token belongs in FORGE_TOKEN",
			absent:    []string{secret},
		},
		{
			name:      "http-redirect with a separate true",
			args:      []string{"verify", "--http-redirect", "true"},
			withToken: true,
			exitCode:  2,
			want:      "-flag=false",
			absent:    []string{"true", secret},
		},
		{
			name:     "equals form reaches the missing-token check",
			args:     []string{"verify", "--default-sites-enabled=false"},
			exitCode: 1,
			want:     "error: FORGE_TOKEN env var is required",
			absent:   []string{"positional arguments"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			// G204: the command is this test binary, re-executed with a fixed -test.run.
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainPositionalArgsExitBeforeForge$") //nolint:gosec
			env := filteredEnv("FORGE_TOKEN", "FORGE_ORG", childEnv)
			env = append(env, childEnv+"="+strings.Join(tt.args, "\x1e"), "FORGE_ORG=example")
			if tt.withToken {
				env = append(env, "FORGE_TOKEN="+secret)
			}
			cmd.Env = env

			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("exit err = %v; output:\n%s", err, out)
			}
			if exitErr.ExitCode() != tt.exitCode {
				t.Fatalf("exit = %d, want %d; output:\n%s", exitErr.ExitCode(), tt.exitCode, out)
			}
			msg := string(out)
			if !strings.Contains(msg, tt.want) {
				t.Fatalf("output missing %q:\n%s", tt.want, msg)
			}
			for _, bad := range tt.absent {
				if strings.Contains(msg, bad) {
					t.Fatalf("output contains %q:\n%s", bad, msg)
				}
			}
		})
	}
}

func filteredEnv(drop ...string) []string {
	skip := make(map[string]bool, len(drop))
	for _, key := range drop {
		skip[key] = true
	}

	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if skip[name] {
			continue
		}
		env = append(env, entry)
	}
	return env
}
