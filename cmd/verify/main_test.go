package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
