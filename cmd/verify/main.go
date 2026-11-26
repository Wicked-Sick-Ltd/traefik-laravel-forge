// Command verify previews what the traefik-laravel-forge plugin would generate
// and optionally compares it against an existing Traefik dynamic config file.
//
// Usage:
//
//	FORGE_TOKEN=xxx FORGE_ORG=my-org go run ./cmd/verify
//	FORGE_TOKEN=xxx go run ./cmd/verify -org my-org -compare path/to/dynamic.yml
//
// The Forge API token is read only from the FORGE_TOKEN environment variable.
// There is deliberately no flag for it: a process command line is readable by
// every local user, while its environment is readable only by its owner.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	forge "github.com/Wicked-Sick-Ltd/traefik-laravel-forge"
	"github.com/traefik/genconf/dynamic"
)

// unexpectedArgsMessage reports leftover positional arguments.
// Go's flag package leaves the word after a boolean flag as an argument
// (only -flag=false is accepted), and a token pasted on the command line
// lands here too. The text names both causes and does not include the values.
func unexpectedArgsMessage(narg int) string {
	if narg == 0 {
		return ""
	}

	return "error: unexpected positional arguments (boolean flags need -flag=false; the token belongs in FORGE_TOKEN)"
}

func main() {
	org := flag.String("org", os.Getenv("FORGE_ORG"), "Forge organization slug (or set FORGE_ORG)")
	certResolver := flag.String("cert-resolver", "", "Default cert resolver name (e.g. letsencrypt)")
	sitesEnabled := flag.Bool("default-sites-enabled", true, "Whether sites are enabled by default")
	httpRedirect := flag.Bool("http-redirect", false, "Generate HTTP->HTTPS redirect routers")
	redirectMiddleware := flag.String("redirect-middleware", "", "Name of redirect middleware to use with http-redirect")
	traefikID := flag.String("traefik-id", "", "Only process servers tagged traefik:traefik-id=<value> (for multi-LB setups)")
	allowedSitePorts := flag.String("allowed-site-ports", "", "Comma-separated ports a traefik:port site tag may select (e.g. 3000,8080)")
	comparePath := flag.String("compare", "", "Path to existing Traefik dynamic config file (.yml) to compare against")
	jsonOut := flag.Bool("json", false, "Output generated config as JSON")
	dumpRaw := flag.Bool("dump", false, "Dump raw Forge API responses as JSON (useful for inspecting available fields)")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		_, _ = fmt.Fprintf(out, "Usage: FORGE_TOKEN=... %s [flags]\n\n", os.Args[0])
		_, _ = fmt.Fprintln(out, "The Forge API token is read only from the FORGE_TOKEN environment variable.")
		_, _ = fmt.Fprintln(out, "Boolean flags take -flag or -flag=false. A separate word is a positional argument.")
		_, _ = fmt.Fprintln(out)
		flag.PrintDefaults()
	}
	flag.Parse()

	if msg := unexpectedArgsMessage(flag.NArg()); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(2)
	}

	token := os.Getenv("FORGE_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "error: FORGE_TOKEN env var is required")
		os.Exit(1)
	}
	if *org == "" {
		fmt.Fprintln(os.Stderr, "error: -org flag or FORGE_ORG env var is required")
		os.Exit(1)
	}

	cfg := forge.CreateConfig()
	cfg.APIToken = token
	cfg.Organization = *org
	cfg.DefaultCertResolver = *certResolver
	cfg.DefaultSitesEnabled = *sitesEnabled
	cfg.HTTPRedirect = *httpRedirect
	cfg.RedirectMiddleware = *redirectMiddleware
	cfg.TraefikID = *traefikID

	ports, err := parsePortList(*allowedSitePorts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: -allowed-site-ports: %v\n", err)
		os.Exit(1)
	}
	cfg.AllowedSitePorts = ports

	provider, err := forge.New(context.Background(), cfg, "verify")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating provider: %v\n", err)
		os.Exit(1)
	}
	if err = provider.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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

	fmt.Printf("%-44s %-15s %-20s %s\n", "ROUTER", "ENTRY POINTS", "SERVICE -> BACKEND", "RULE")
	fmt.Println(strings.Repeat("-", 120))

	for _, name := range routerNames {
		r := generated.HTTP.Routers[name]
		eps := strings.Join(r.EntryPoints, ",")
		backend := routerBackend(generated, r)

		tlsMark := ""
		if r.TLS != nil {
			if r.TLS.CertResolver != "" {
				tlsMark = fmt.Sprintf(" [TLS:%s]", r.TLS.CertResolver)
			} else {
				tlsMark = " [TLS]"
			}
		}

		fmt.Printf("%-44s %-15s %-20s %s%s\n", name, eps, backend+tlsMark, r.Rule, "")
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

		if err := compare(os.Stdout, generated, existingHosts); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

// parsePortList parses a comma-separated list of ports such as "3000,8080".
func parsePortList(value string) ([]int, error) {
	var ports []int
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		port, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("invalid port %q", field)
		}
		ports = append(ports, port)
	}
	return ports, nil
}

type routerInfo struct {
	name    string
	backend string
	tls     bool
}

// routerBackend returns the first load-balancer URL behind a router's service.
func routerBackend(generated *dynamic.Configuration, r *dynamic.Router) string {
	if s, ok := generated.HTTP.Services[r.Service]; ok && s.LoadBalancer != nil && len(s.LoadBalancer.Servers) > 0 {
		return s.LoadBalancer.Servers[0].URL
	}
	return ""
}

// compare writes the MISSING / NEW / MATCHED summary of the generated routers
// against the hosts found in an existing static config.
func compare(w io.Writer, generated *dynamic.Configuration, existingHosts map[string]bool) error {
	routerNames := make([]string, 0, len(generated.HTTP.Routers))
	for name := range generated.HTTP.Routers {
		routerNames = append(routerNames, name)
	}
	sort.Strings(routerNames)

	generatedHosts := make(map[string]routerInfo)
	var rejected []rejectedRule
	tainted := make(map[string]bool)
	for _, name := range routerNames {
		r := generated.HTTP.Routers[name]
		// Collect every host the rule matches, so --compare does not report the
		// www./wildcard variants of a multi-host rule as missing.
		hosts, unaccepted, ok := parseHostRule(r.Rule)
		if !ok {
			rejected = append(rejected, rejectedRule{name: name, rule: r.Rule, unaccepted: unaccepted})
			for _, host := range extractHosts(r.Rule) {
				tainted[host] = true
			}
			continue
		}
		for _, host := range hosts {
			generatedHosts[host] = routerInfo{name: name, backend: routerBackend(generated, r), tls: r.TLS != nil}
		}
	}
	// A host named in a rejected rule is reachable through that rule's extra
	// matchers too, so a clean router for the same host does not make it covered.
	for host := range tainted {
		delete(generatedHosts, host)
	}

	_, _ = fmt.Fprintf(w, "\nExisting config has %d Host() rules\n", len(existingHosts))
	_, _ = fmt.Fprintf(w, "Generated config has %d Host() rules\n\n", len(generatedHosts))

	if len(rejected) > 0 {
		_, _ = fmt.Fprintf(w, "REJECTED (generated rules that match on more than hostnames; their hosts are not counted as covered):\n")
		for _, rr := range rejected {
			_, _ = fmt.Fprintf(w, "  ! %s\n", rr.name)
			_, _ = fmt.Fprintf(w, "      rule:       %s\n", rr.rule)
			_, _ = fmt.Fprintf(w, "      not a host: %s\n", rr.unaccepted)
		}
		_, _ = fmt.Fprintln(w)
	}

	// In existing but NOT in generated
	missing := []string{}
	for host := range existingHosts {
		if _, found := generatedHosts[host]; !found {
			missing = append(missing, host)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		_, _ = fmt.Fprintf(w, "MISSING from generated (in your config but not in Forge):\n")
		for _, h := range missing {
			_, _ = fmt.Fprintf(w, "  - %s\n", h)
		}
		_, _ = fmt.Fprintln(w)
	} else if len(rejected) == 0 {
		_, _ = fmt.Fprintln(w, "OK: All hosts from your existing config are covered by the generated config.")
		_, _ = fmt.Fprintln(w)
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
		_, _ = fmt.Fprintf(w, "NEW in generated (Forge sites not in your existing config):\n")
		for _, h := range extra {
			info := generatedHosts[h]
			tlsMark := ""
			if info.tls {
				tlsMark = " [TLS]"
			}
			_, _ = fmt.Fprintf(w, "  + %s -> %s%s\n", h, info.backend, tlsMark)
		}
		_, _ = fmt.Fprintln(w)
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
		_, _ = fmt.Fprintf(w, "MATCHED (%d hosts in both configs):\n", len(matched))
		for _, h := range matched {
			_, _ = fmt.Fprintf(w, "  = %s\n", h)
		}
		_, _ = fmt.Fprintln(w)
	}

	if len(rejected) > 0 {
		return fmt.Errorf("%d generated router(s) match on more than hostnames; see REJECTED", len(rejected))
	}
	return nil
}

type rejectedRule struct {
	name       string
	rule       string
	unaccepted string
}

// parseHostRule accepts only the rule shape the plugin is meant to generate:
// Host(`name`) and wildcard HostRegexp(`^[^.]+\.name$`) clauses joined by ||.
// Anything else — another matcher, &&, negation, grouping, a hostname that is
// not a plain DNS name — makes ok false, and unaccepted holds the rule text
// from the first clause that could not be accepted.
func parseHostRule(rule string) (hosts []string, unaccepted string, ok bool) {
	rest := strings.TrimSpace(rule)
	if rest == "" {
		return nil, "(empty rule)", false
	}
	for {
		host, tail, clauseOK := parseHostClause(rest)
		if !clauseOK {
			return nil, rest, false
		}
		hosts = append(hosts, host)

		tail = strings.TrimSpace(tail)
		if tail == "" {
			return hosts, "", true
		}
		next, found := strings.CutPrefix(tail, "||")
		if !found {
			return nil, tail, false
		}
		rest = strings.TrimSpace(next)
	}
}

// parseHostClause reads one host clause from the start of s and returns the
// hostname (wildcards as "*.domain") and the text after the clause.
func parseHostClause(s string) (host, tail string, ok bool) {
	if body, ok := strings.CutPrefix(s, "Host(`"); ok {
		name, tail, closed := strings.Cut(body, "`)")
		if !closed || !isHostname(name) {
			return "", "", false
		}
		return name, tail, true
	}

	if body, ok := strings.CutPrefix(s, "HostRegexp(`"); ok {
		pattern, tail, closed := strings.Cut(body, "`)")
		if !closed {
			return "", "", false
		}
		const wildcardPrefix = `^[^.]+\.`
		escaped, hasPrefix := strings.CutPrefix(pattern, wildcardPrefix)
		escaped, hasSuffix := strings.CutSuffix(escaped, "$")
		if !hasPrefix || !hasSuffix {
			return "", "", false
		}
		name := strings.ReplaceAll(escaped, `\.`, ".")
		if strings.Count(escaped, `\.`) != strings.Count(name, ".") || !isHostname(name) {
			return "", "", false
		}
		return "*." + name, tail, true
	}

	return "", "", false
}

// isHostname reports whether s is a dot-separated DNS name made only of
// letters, digits, hyphens and underscores.
func isHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, c := range label {
			isAlnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
			if !isAlnum && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

// extractHosts pulls every hostname out of a Traefik rule fragment. A rule may
// carry several clauses — "Host(`a.com`) || Host(`www.a.com`)" yields both — and
// a v3 wildcard clause such as HostRegexp(`^[^.]+\.a\.com$`) is normalised to
// "*.a.com" so the two sides of --compare can be matched on equal terms.
func extractHosts(rule string) []string {
	var hosts []string

	// Fold HostRegexp(`...`) into the same shape as Host(`...`) so a single scan
	// picks up both; normaliseHostPattern converts the regexp body afterwards.
	rest := strings.ReplaceAll(rule, "HostRegexp(`", "Host(`")
	for {
		_, after, found := strings.Cut(rest, "Host(`")
		if !found {
			break
		}
		host, tail, closed := strings.Cut(after, "`)")
		if !closed {
			break
		}
		if h := strings.TrimSpace(host); h != "" {
			hosts = append(hosts, normaliseHostPattern(h))
		}
		rest = tail
	}

	return hosts
}

// normaliseHostPattern turns the wildcard HostRegexp body the plugin generates
// back into a readable "*.domain" form, and leaves plain hostnames untouched.
func normaliseHostPattern(host string) string {
	const wildcardPrefix = `^[^.]+\.`

	if !strings.HasPrefix(host, wildcardPrefix) || !strings.HasSuffix(host, "$") {
		return host
	}

	domain := strings.TrimSuffix(strings.TrimPrefix(host, wildcardPrefix), "$")

	return "*." + strings.ReplaceAll(domain, `\.`, ".")
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
		// Reuse the generated-side parser so both halves of the comparison
		// normalise hostnames and wildcard clauses identically.
		for _, h := range extractHosts(scanner.Text()) {
			hosts[h] = true
		}
	}
	return hosts, scanner.Err()
}
