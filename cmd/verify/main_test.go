package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestParsePortList(t *testing.T) {
	tests := []struct {
		value   string
		want    []int
		wantErr bool
	}{
		{value: "", want: nil},
		{value: "3000", want: []int{3000}},
		{value: " 3000, 8080 ,", want: []int{3000, 8080}},
		{value: "3000,abc", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parsePortList(tt.value)
		if (err != nil) != tt.wantErr {
			t.Errorf("parsePortList(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parsePortList(%q) = %v, want %v", tt.value, got, tt.want)
		}
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
