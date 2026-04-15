// Command verify previews what the traefik-laravel-forge plugin would generate
// and optionally compares it against an existing Traefik dynamic config file.
//
// Usage:
//
//	FORGE_TOKEN=xxx FORGE_ORG=my-org go run ./cmd/verify
//	go run ./cmd/verify -token xxx -org my-org -compare path/to/dynamic.yml
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	forge "github.com/wickedsick/traefik-laravel-forge"
)

func main() {
	token := flag.String("token", os.Getenv("FORGE_TOKEN"), "Forge API token (or set FORGE_TOKEN)")
	org := flag.String("org", os.Getenv("FORGE_ORG"), "Forge organization slug (or set FORGE_ORG)")
	certResolver := flag.String("cert-resolver", "", "Default cert resolver name (e.g. letsencrypt)")
	sitesEnabled := flag.Bool("default-sites-enabled", true, "Whether sites are enabled by default")
	httpRedirect := flag.Bool("http-redirect", false, "Generate HTTP->HTTPS redirect routers")
	redirectMiddleware := flag.String("redirect-middleware", "", "Name of redirect middleware to use with http-redirect")
	traefikID := flag.String("traefik-id", "", "Only process servers tagged traefik:traefik-id=<value> (for multi-LB setups)")
	comparePath := flag.String("compare", "", "Path to existing Traefik dynamic config file (.yml) to compare against")
	jsonOut := flag.Bool("json", false, "Output generated config as JSON")
	dumpRaw := flag.Bool("dump", false, "Dump raw Forge API responses as JSON (useful for inspecting available fields)")
	flag.Parse()

	if *token == "" {
		fmt.Fprintln(os.Stderr, "error: -token flag or FORGE_TOKEN env var is required")
		os.Exit(1)
	}
	if *org == "" {
		fmt.Fprintln(os.Stderr, "error: -org flag or FORGE_ORG env var is required")
		os.Exit(1)
	}

	cfg := forge.CreateConfig()
	cfg.APIToken = *token
	cfg.Organization = *org
	cfg.DefaultCertResolver = *certResolver
	cfg.DefaultSitesEnabled = *sitesEnabled
	cfg.HTTPRedirect = *httpRedirect
	cfg.RedirectMiddleware = *redirectMiddleware
	cfg.TraefikID = *traefikID

	provider, err := forge.New(context.Background(), cfg, "verify")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating provider: %v\n", err)
		os.Exit(1)
	}

	if *dumpRaw {
		var dumpData []byte
		dumpData, err = provider.DumpRaw()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(dumpData))
		return
	}

	fmt.Println("Fetching configuration from Forge API...")
	fmt.Println(strings.Repeat("-", 60))

	generated, err := provider.GenerateConfiguration()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating configuration: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(strings.Repeat("-", 60))
	fmt.Println()

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(generated)
		return
	}

	// Print summary table
	routerNames := make([]string, 0, len(generated.HTTP.Routers))
	for name := range generated.HTTP.Routers {
		routerNames = append(routerNames, name)
	}
	sort.Strings(routerNames)

	fmt.Printf("Generated %d routers, %d services\n\n", len(generated.HTTP.Routers), len(generated.HTTP.Services))

	// Collect generated hosts for comparison
	generatedHosts := make(map[string]routerInfo)

	fmt.Printf("%-44s %-15s %-20s %s\n", "ROUTER", "ENTRY POINTS", "SERVICE -> BACKEND", "RULE")
	fmt.Println(strings.Repeat("-", 120))

	for _, name := range routerNames {
		r := generated.HTTP.Routers[name]
		eps := strings.Join(r.EntryPoints, ",")
		svc := r.Service

		backend := ""
		if s, ok := generated.HTTP.Services[svc]; ok && s.LoadBalancer != nil && len(s.LoadBalancer.Servers) > 0 {
			backend = s.LoadBalancer.Servers[0].URL
		}

		tlsMark := ""
		if r.TLS != nil {
			if r.TLS.CertResolver != "" {
				tlsMark = fmt.Sprintf(" [TLS:%s]", r.TLS.CertResolver)
			} else {
				tlsMark = " [TLS]"
			}
		}

		fmt.Printf("%-44s %-15s %-20s %s%s\n", name, eps, backend+tlsMark, r.Rule, "")

		// Still collect primary host for --compare
		if host := extractHost(r.Rule); host != "" {
			generatedHosts[host] = routerInfo{name: name, backend: backend, tls: r.TLS != nil}
		}
	}

	// Compare against existing config if provided
	if *comparePath != "" {
		fmt.Println()
		fmt.Println(strings.Repeat("=", 60))
		fmt.Printf("COMPARISON: %s\n", *comparePath)
		fmt.Println(strings.Repeat("=", 60))

		existingHosts, err := extractHostsFromConfig(*comparePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading compare file: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\nExisting config has %d Host() rules\n", len(existingHosts))
		fmt.Printf("Generated config has %d Host() rules\n\n", len(generatedHosts))

		// In existing but NOT in generated
		missing := []string{}
		for host := range existingHosts {
			if _, found := generatedHosts[host]; !found {
				missing = append(missing, host)
			}
		}
		sort.Strings(missing)

		if len(missing) > 0 {
			fmt.Printf("MISSING from generated (in your config but not in Forge):\n")
			for _, h := range missing {
				fmt.Printf("  - %s\n", h)
			}
			fmt.Println()
		} else {
			fmt.Println("OK: All hosts from your existing config are covered by the generated config.")
			fmt.Println()
		}

		// In generated but NOT in existing
		extra := []string{}
		for host := range generatedHosts {
			if _, found := existingHosts[host]; !found {
				extra = append(extra, host)
			}
		}
		sort.Strings(extra)

		if len(extra) > 0 {
			fmt.Printf("NEW in generated (Forge sites not in your existing config):\n")
			for _, h := range extra {
				info := generatedHosts[h]
				tlsMark := ""
				if info.tls {
					tlsMark = " [TLS]"
				}
				fmt.Printf("  + %s -> %s%s\n", h, info.backend, tlsMark)
			}
			fmt.Println()
		}

		// Matched hosts
		matched := []string{}
		for host := range generatedHosts {
			if _, found := existingHosts[host]; found {
				matched = append(matched, host)
			}
		}
		sort.Strings(matched)

		if len(matched) > 0 {
			fmt.Printf("MATCHED (%d hosts in both configs):\n", len(matched))
			for _, h := range matched {
				fmt.Printf("  = %s\n", h)
			}
			fmt.Println()
		}
	}
}

type routerInfo struct {
	name    string
	backend string
	tls     bool
}

// extractHost pulls the domain from a Traefik Host() rule, e.g. "Host(`example.com`)" -> "example.com".
func extractHost(rule string) string {
	start := strings.Index(rule, "Host(`")
	if start == -1 {
		return rule
	}
	start += len("Host(`")
	end := strings.Index(rule[start:], "`)")
	if end == -1 {
		return rule
	}
	return rule[start : start+end]
}

// extractHostsFromConfig does a simple line-by-line scan of a YAML/TOML dynamic config
// looking for Host(`...`) patterns. Works without a full YAML parser.
func extractHostsFromConfig(path string) (map[string]bool, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from CLI flag, intentional
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	hosts := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Look for Host(`...`) anywhere in the line (handles YAML values and TOML values)
		for {
			_, after, found := strings.Cut(line, "Host(`")
			if !found {
				break
			}
			host, rest, closed := strings.Cut(after, "`)")
			if !closed {
				break
			}
			if h := strings.TrimSpace(host); h != "" {
				hosts[h] = true
			}
			line = rest
		}
	}
	return hosts, scanner.Err()
}
