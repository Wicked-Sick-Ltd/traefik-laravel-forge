package traefik_laravel_forge

import (
	"context"
	"testing"
)

const (
	testAPIToken     = "test-token"
	testOrganization = "test-org"
)

func TestNew(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "10s" // Use minimum valid interval
	config.APIToken = testAPIToken
	config.Organization = testOrganization

	provider, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err = provider.Stop()
		if err != nil {
			t.Fatal(err)
		}
	})

	err = provider.Init()
	if err != nil {
		t.Fatal(err)
	}
}

func TestNewMissingAPIToken(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "1s"
	config.APIToken = "" // Missing token
	config.Organization = testOrganization

	_, err := New(context.Background(), config, "test")
	if err == nil {
		t.Fatal("expected error for missing API token, got nil")
	}
}

func TestNewMissingOrganization(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "1s"
	config.APIToken = testAPIToken
	config.Organization = "" // Missing organization

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

func TestInitInvalidPollInterval(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "0s"
	config.APIToken = testAPIToken
	config.Organization = testOrganization

	provider, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}

	err = provider.Init()
	if err == nil {
		t.Fatal("expected error for zero poll interval in Init, got nil")
	}
}

func TestInitPollIntervalTooShort(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "5s" // Less than 10s minimum
	config.APIToken = testAPIToken
	config.Organization = testOrganization

	provider, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}

	err = provider.Init()
	if err == nil {
		t.Fatal("expected error for poll interval < 10s in Init, got nil")
	}

	// Check error message
	expectedSubstring := "at least 10s"
	if !contains(err.Error(), expectedSubstring) {
		t.Fatalf("expected error containing %q, got: %v", expectedSubstring, err)
	}
}

func TestInitPollIntervalValid(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "10s" // Exactly 10s - should be valid
	config.APIToken = testAPIToken
	config.Organization = testOrganization

	provider, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}

	err = provider.Init()
	if err != nil {
		t.Fatalf("expected no error for 10s poll interval, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestTraefikIDFilter(t *testing.T) {
	config := CreateConfig()
	config.PollInterval = "10s"
	config.APIToken = testAPIToken
	config.Organization = testOrganization
	config.TraefikID = "lb01"

	provider, err := New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}

	if provider.traefikID != "lb01" {
		t.Errorf("traefikID = %q, want %q", provider.traefikID, "lb01")
	}
}

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
			wantTraefikID:    "",
		},
		{
			name:             "only host, default port",
			tags:             []string{"traefik:upstream-host=192.168.1.50"},
			wantUpstreamHost: "192.168.1.50",
			wantUpstreamPort: 80,
			wantTraefikID:    "",
		},
		{
			name:             "only port, no host",
			tags:             []string{"traefik:upstream-port=9090"},
			wantUpstreamHost: "",
			wantUpstreamPort: 9090,
			wantTraefikID:    "",
		},
		{
			name:             "with traefik ID",
			tags:             []string{"traefik:upstream-host=10.0.1.10", "traefik:traefik-id=lb01"},
			wantUpstreamHost: "10.0.1.10",
			wantUpstreamPort: 80,
			wantTraefikID:    "lb01",
		},
		{
			name:             "no traefik tags",
			tags:             []string{"production", "app-server"},
			wantUpstreamHost: "",
			wantUpstreamPort: 80,
			wantTraefikID:    "",
		},
		{
			name:             "empty tags",
			tags:             []string{},
			wantUpstreamHost: "",
			wantUpstreamPort: 80,
			wantTraefikID:    "",
		},
		{
			name:             "backward compat: lb-host/lb-port aliases",
			tags:             []string{"traefik:lb-host=10.0.0.1", "traefik:lb-port=3000"},
			wantUpstreamHost: "10.0.0.1",
			wantUpstreamPort: 3000,
			wantTraefikID:    "",
		},
		{
			name:             "backward compat: loadbalancer aliases",
			tags:             []string{"traefik:loadbalancer-host=10.0.0.2", "traefik:loadbalancer-port=4000"},
			wantUpstreamHost: "10.0.0.2",
			wantUpstreamPort: 4000,
			wantTraefikID:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ParseServerTags(tt.tags)
			if cfg.UpstreamHost != tt.wantUpstreamHost {
				t.Errorf("UpstreamHost = %q, want %q", cfg.UpstreamHost, tt.wantUpstreamHost)
			}
			if cfg.UpstreamPort != tt.wantUpstreamPort {
				t.Errorf("UpstreamPort = %d, want %d", cfg.UpstreamPort, tt.wantUpstreamPort)
			}
			if cfg.TraefikID != tt.wantTraefikID {
				t.Errorf("TraefikID = %q, want %q", cfg.TraefikID, tt.wantTraefikID)
			}
		})
	}
}

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
		{"production", "", "", false},
		{"", "", "", false},
		{"traefik:", "", "", false},
		{"traefik:key=value=extra", "key", "value=extra", true},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			key, value, ok := ParseTagConfig(tt.tag)
			if ok != tt.wantOK {
				t.Errorf("ParseTagConfig(%q) ok = %v, want %v", tt.tag, ok, tt.wantOK)
			}
			if key != tt.wantKey {
				t.Errorf("ParseTagConfig(%q) key = %q, want %q", tt.tag, key, tt.wantKey)
			}
			if value != tt.wantValue {
				t.Errorf("ParseTagConfig(%q) value = %q, want %q", tt.tag, value, tt.wantValue)
			}
		})
	}
}
