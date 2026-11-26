package main

// These tests are the spec for the verify --compare verdict. They fail while
// a static hostname is treated as covered as soon as the same name appears in
// any generated Host or HostRegexp clause.
//
// A static router is covered only when some generated router for that same
// hostname has the same backend URL, the same entrypoints, and the same TLS
// certificate resolver (or both have TLS off). Extra generated routers, such
// as the HTTP redirect router produced by -http-redirect, do not cancel
// coverage of a static HTTPS router and do not by themselves fail the
// command. Every static router must be covered. The command exits non-zero
// when any static router is not, and it does not print the full-coverage
// verdict. On success, every MATCHED line prints the hostname, backend URL,
// entrypoints, and certificate resolver.
//
// The tests run cmd/verify's main against the real Forge HTTP client. The
// client dials https://forge.laravel.com with a nil Transport, so Go's
// net/http Client uses http.DefaultTransport at request time. The test
// process substitutes that transport and returns JSON:API documents. Decoding,
// tag resolution, router generation, and the compare verdict all run in the
// product code.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	forge "github.com/Wicked-Sick-Ltd/traefik-laravel-forge"
)

const (
	placeholderToken = "placeholder-token"
	testOrg          = "test-org"
	customerHost     = "customer.example"
	wwwHost          = "www.customer.example"
	otherHost        = "other.example"
	memberHost       = "member.example"
	legitBackend     = "http://10.0.0.1:80"
	memberBackend    = "http://203.0.113.50:80"
	otherBackend     = "http://192.0.2.10:80"
	shiftedBackend   = "http://10.0.0.1:9091"
	coveredVerdict   = "OK: All hosts from your existing config are covered by the generated config."
)

func TestMain(m *testing.M) {
	if os.Getenv("RUN_VERIFY_MAIN") == "1" {
		runVerifyMain()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCompareRejectsDomainRecordOnAnotherBackend(t *testing.T) {
	fix := org(server("member-srv", "member", memberIP, nil,
		site("member-site", memberHost, nil, domain{
			Name: customerHost, Type: "alias", WWW: "to-www",
		})))
	body := tomlStatic(staticRoute{
		Router: "customer", Service: "customer-svc", Backend: legitBackend,
		Rule: hostRule(customerHost, wwwHost), EntryPoints: []string{"websecure"},
		CertResolver: "cloudflare",
	})

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, wwwHost, memberBackend)
	requireDivergence(t, out, code, customerHost, wwwHost, legitBackend, memberBackend)
}

func TestCompareRejectsAliasTagOnAnotherBackend(t *testing.T) {
	fix := org(server("member-srv", "member", memberIP, nil,
		site("member-site", memberHost, []string{"traefik:aliases=" + customerHost})))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, "Host(`"+customerHost+"`)", memberBackend)
	requireDivergence(t, out, code, customerHost, legitBackend, memberBackend)
}

func TestCompareRejectsSiteNameFallbackOnAnotherBackend(t *testing.T) {
	fix := org(server("member-srv", "member", memberIP, nil,
		site("member-site", customerHost, nil)))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, "Host(`"+customerHost+"`)", memberBackend)
	requireDivergence(t, out, code, customerHost, legitBackend, memberBackend)
}

func TestCompareRejectsPortTagBackend(t *testing.T) {
	fix := org(server("legit-srv", "php01", legitIP, nil,
		site("legit-site", customerHost, []string{"traefik:port=9091"},
			domain{Name: customerHost, Type: "primary"})))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, customerHost, shiftedBackend)
	requireDivergence(t, out, code, customerHost, legitBackend, shiftedBackend)
}

func TestCompareRejectsUpstreamHostTag(t *testing.T) {
	fix := org(server("legit-srv", "php01", legitIP, []string{"traefik:upstream-host=" + memberIP},
		site("legit-site", customerHost, nil, domain{Name: customerHost, Type: "primary"})))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, customerHost, memberBackend)
	requireDivergence(t, out, code, customerHost, legitBackend, memberBackend)
}

func TestCompareRejectsCertResolverMismatch(t *testing.T) {
	fix := org(server("legit-srv", "php01", legitIP, nil,
		site("legit-site", customerHost, []string{
			"traefik:cert-resolver=letsencrypt",
			"traefik:entrypoints=websecure",
		}, domain{Name: customerHost, Type: "primary"})))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, "letsencrypt", legitBackend)
	requireDivergence(t, out, code, customerHost, "cloudflare", "letsencrypt")
}

func TestCompareRejectsEntrypointMismatch(t *testing.T) {
	fix := org(server("legit-srv", "php01", legitIP, nil,
		site("legit-site", customerHost, []string{"traefik:entrypoints=public"},
			domain{Name: customerHost, Type: "primary"})))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, "public", legitBackend)
	requireDivergence(t, out, code, customerHost, "websecure", "public")
}

func TestCompareRejectsTLSDisabled(t *testing.T) {
	fix := org(server("legit-srv", "php01", legitIP, nil,
		site("legit-site", customerHost, []string{
			"traefik:tls=false",
			"traefik:entrypoints=websecure",
		}, domain{Name: customerHost, Type: "primary"})))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	summary := summaryOf(t, out)
	if strings.Contains(summary, "[TLS") {
		t.Fatalf("generated router still has TLS\n%s", summary)
	}
	requireGenerated(t, out, customerHost, legitBackend)
	requireDivergence(t, out, code, customerHost, "cloudflare", "websecure")
}

func TestCompareRejectsWildcardBackendMismatch(t *testing.T) {
	fix := org(server("member-srv", "member", memberIP, nil,
		site("member-site", memberHost, nil, domain{
			Name: customerHost, Type: "alias", Wildcard: true,
		})))
	body := tomlStatic(staticRoute{
		Router: "wild", Service: "customer-svc", Backend: legitBackend,
		Rule: wildcardRule(customerHost), EntryPoints: []string{"websecure"},
		CertResolver: "cloudflare",
	})

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, customerHost, memberBackend)
	requireDivergence(t, out, code, "*."+customerHost, legitBackend, memberBackend)
}

func TestCompareRejectsYAMLBackendMismatch(t *testing.T) {
	fix := org(server("member-srv", "member", memberIP, nil,
		site("member-site", memberHost, nil, domain{Name: customerHost, Type: "alias"})))
	body := fmt.Sprintf(`http:
  routers:
    customer:
      rule: "Host(`+"`%s`"+`)"
      service: customer-svc
      entryPoints: ["websecure"]
      tls:
        certResolver: cloudflare
  services:
    customer-svc:
      loadBalancer:
        servers:
          - url: "%s"
`, customerHost, legitBackend)

	out, code := runCompare(t, fix, "static.yml", body)
	requireGenerated(t, out, customerHost, memberBackend)
	requireDivergence(t, out, code, customerHost, legitBackend, memberBackend)
}

func TestCompareRejectsSwappedBackends(t *testing.T) {
	fix := org(
		server("legit-srv", "php01", legitIP, nil,
			site("legit-site", otherHost, nil, domain{Name: otherHost, Type: "primary"})),
		server("member-srv", "member", memberIP, nil,
			site("member-site", memberHost, nil, domain{Name: customerHost, Type: "alias"})),
	)
	body := tomlStatic(
		httpsRoute("customer", "customer-svc", customerHost, legitBackend),
		httpsRoute("other", "other-svc", otherHost, memberBackend),
	)

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, customerHost, memberBackend)
	requireGenerated(t, out, otherHost, legitBackend)
	requireDivergence(t, out, code, customerHost, legitBackend, memberBackend)
}

func TestCompareRejectsWhenOneOfTwoHostsDiverges(t *testing.T) {
	fix := org(
		server("legit-srv", "php01", legitIP, nil,
			site("legit-site", otherHost, nil, domain{Name: otherHost, Type: "primary"})),
		server("member-srv", "member", memberIP, nil,
			site("member-site", memberHost, []string{"traefik:aliases=" + customerHost})),
	)
	body := tomlStatic(
		httpsRoute("other", "other-svc", otherHost, legitBackend),
		httpsRoute("customer", "customer-svc", customerHost, otherBackend),
	)

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, otherHost, legitBackend)
	requireGenerated(t, out, customerHost, memberBackend)
	requireDivergence(t, out, code, customerHost, otherBackend, memberBackend)
}

func TestCompareAcceptsEquivalentHTTPSRoute(t *testing.T) {
	fix := org(server("legit-srv", "php01", legitIP, nil,
		site("legit-site", customerHost, nil, domain{Name: customerHost, Type: "primary"})))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	requireGenerated(t, out, "Host(`"+customerHost+"`)", legitBackend)
	requireCovered(t, out, code, customerHost, legitBackend, "websecure", "cloudflare")
}

func TestCompareRejectsReverbDomainOnAnotherBackend(t *testing.T) {
	fix := org(server("member-srv", "member", memberIP, nil,
		site("member-site", memberHost, nil, domain{Name: customerHost, Type: "alias"}).reverb(customerHost, 8081)))
	body := tomlStatic(httpsRoute("customer", "customer-svc", customerHost, legitBackend))

	out, code := runCompare(t, fix, "static.toml", body)
	summary := summaryOf(t, out)
	if strings.Contains(summary, ":8081") {
		t.Fatalf("generated backend used the Reverb port\n%s", summary)
	}
	requireGenerated(t, out, customerHost, memberBackend)
	requireDivergence(t, out, code, customerHost, legitBackend, memberBackend)
}

const (
	legitIP  = "10.0.0.1"
	memberIP = "203.0.113.50"
)

type fixture struct {
	Org     string      `json:"org"`
	Servers []serverFix `json:"servers"`
}

type serverFix struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	PrivateIP string    `json:"privateIP"`
	Tags      []string  `json:"tags,omitempty"`
	Sites     []siteFix `json:"sites"`
}

type siteFix struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Tags       []string    `json:"tags,omitempty"`
	Domains    []domainFix `json:"domains,omitempty"`
	ReverbHost string      `json:"reverbHost,omitempty"`
	ReverbPort int         `json:"reverbPort,omitempty"`
}

type domainFix struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Wildcard bool   `json:"wildcard,omitempty"`
	WWW      string `json:"www,omitempty"`
}

type domain = domainFix

func org(servers ...serverFix) fixture {
	return fixture{Org: testOrg, Servers: servers}
}

func server(id, name, ip string, tags []string, sites ...siteFix) serverFix {
	return serverFix{ID: id, Name: name, PrivateIP: ip, Tags: tags, Sites: sites}
}

func site(id, name string, tags []string, domains ...domainFix) siteFix {
	return siteFix{ID: id, Name: name, Tags: tags, Domains: domains}
}

func (s siteFix) reverb(host string, port int) siteFix {
	s.ReverbHost = host
	s.ReverbPort = port
	return s
}

type staticRoute struct {
	Router       string
	Service      string
	Backend      string
	Rule         string
	EntryPoints  []string
	CertResolver string
}

func httpsRoute(router, service, host, backend string) staticRoute {
	return staticRoute{
		Router: router, Service: service, Backend: backend,
		Rule: hostRule(host), EntryPoints: []string{"websecure"},
		CertResolver: "cloudflare",
	}
}

func hostRule(hosts ...string) string {
	parts := make([]string, len(hosts))
	for i, host := range hosts {
		parts[i] = "Host(`" + host + "`)"
	}
	return strings.Join(parts, " || ")
}

// wildcardRule is the HostRegexp text buildHostRule emits for one wildcard domain.
func wildcardRule(host string) string {
	escaped := strings.ReplaceAll(host, ".", `\.`)
	return "HostRegexp(`^[^.]+\\." + escaped + "$`)"
}

func tomlStatic(routes ...staticRoute) string {
	var b strings.Builder
	for _, rt := range routes {
		fmt.Fprintf(&b, "[http.routers.%s]\n", rt.Router)
		fmt.Fprintf(&b, "  rule = %s\n", tomlQuoted(rt.Rule))
		fmt.Fprintf(&b, "  service = %q\n", rt.Service)
		fmt.Fprintf(&b, "  entryPoints = [%s]\n", tomlList(rt.EntryPoints))
		if rt.CertResolver != "" {
			fmt.Fprintf(&b, "  [http.routers.%s.tls]\n", rt.Router)
			fmt.Fprintf(&b, "    certResolver = %q\n", rt.CertResolver)
		}
		b.WriteString("\n")
	}
	seen := map[string]bool{}
	for _, rt := range routes {
		if seen[rt.Service] {
			continue
		}
		seen[rt.Service] = true
		fmt.Fprintf(&b, "[http.services.%s]\n", rt.Service)
		fmt.Fprintf(&b, "  [http.services.%s.loadBalancer]\n", rt.Service)
		fmt.Fprintf(&b, "    [[http.services.%s.loadBalancer.servers]]\n", rt.Service)
		fmt.Fprintf(&b, "      url = %q\n\n", rt.Backend)
	}
	return b.String()
}

func tomlQuoted(value string) string {
	if strings.Contains(value, `\`) {
		return `"` + value + `"`
	}
	return fmt.Sprintf("%q", value)
}

func tomlList(values []string) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%q", value)
	}
	return strings.Join(parts, ", ")
}

func runCompare(t *testing.T, fix fixture, fileName, body string) (string, int) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwdBefore := dirNames(t, cwd)
	dirBefore := dirNames(t, dir)

	fixtureJSON, err := json.Marshal(fix)
	if err != nil {
		t.Fatal(err)
	}
	argsJSON, err := json.Marshal([]string{
		"-token", placeholderToken,
		"-org", fix.Org,
		"-cert-resolver", "cloudflare",
		"-http-redirect",
		"-compare", path,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Re-executes this test binary so main() can call os.Exit without ending the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$") //nolint:gosec // argument is this test binary, not user input
	cmd.Env = append(os.Environ(),
		"RUN_VERIFY_MAIN=1",
		"VERIFY_FIXTURE="+string(fixtureJSON),
		"VERIFY_ARGS="+string(argsJSON),
	)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("verify subprocess timed out: %v\n%s", ctx.Err(), out)
	}
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
		code = exitErr.ExitCode()
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("compare removed %s: %v", path, err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("compare replaced %s", path)
	}
	got, err := os.ReadFile(path) //nolint:gosec // path is the temp file created above
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(body)) {
		t.Fatalf("compare rewrote %s", path)
	}
	sameNames(t, cwd, cwdBefore, dirNames(t, cwd))
	sameNames(t, dir, dirBefore, dirNames(t, dir))

	return string(out), code
}

func dirNames(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = struct{}{}
	}
	return names
}

func sameNames(t *testing.T, label string, before, after map[string]struct{}) {
	t.Helper()
	for name := range after {
		if _, ok := before[name]; !ok {
			t.Fatalf("%s gained %s before compare finished", label, name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Fatalf("%s lost %s before compare finished", label, name)
		}
	}
}

func summaryOf(t *testing.T, out string) string {
	t.Helper()
	summary, _, found := strings.Cut(out, "COMPARISON:")
	if !found {
		t.Fatalf("compare produced no comparison section\n%s", out)
	}
	return summary
}

func comparisonOf(t *testing.T, out string) string {
	t.Helper()
	_, section, found := strings.Cut(out, "COMPARISON:")
	if !found {
		t.Fatalf("compare produced no comparison section\n%s", out)
	}
	return section
}

func requireGenerated(t *testing.T, out, hostFragment, backend string) {
	t.Helper()
	summary := summaryOf(t, out)
	if !strings.Contains(summary, hostFragment) || !strings.Contains(summary, backend) {
		t.Fatalf("generated summary missing %s -> %s\n%s", hostFragment, backend, summary)
	}
}

func requireDivergence(t *testing.T, out string, code int, terms ...string) {
	t.Helper()
	if code == 0 {
		t.Fatalf("exit = 0, compare accepted a static route that is not equivalent\n%s", out)
	}
	if strings.Contains(out, coveredVerdict) {
		t.Fatalf("compare printed the full-coverage verdict\n%s", out)
	}
	section := comparisonOf(t, out)
	for _, term := range terms {
		if !strings.Contains(section, term) {
			t.Fatalf("comparison section missing %q\n%s", term, section)
		}
	}
}

func requireCovered(t *testing.T, out string, code int, terms ...string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit = %d, equivalent static route was rejected\n%s", code, out)
	}
	if !strings.Contains(out, coveredVerdict) {
		t.Fatalf("missing full-coverage verdict\n%s", out)
	}
	_, matched, found := strings.Cut(out, "MATCHED")
	if !found {
		t.Fatalf("MATCHED section missing\n%s", out)
	}
	for _, term := range terms {
		if !strings.Contains(matched, term) {
			t.Fatalf("MATCHED section missing %q\n%s", term, matched)
		}
	}
}

func runVerifyMain() {
	var fix fixture
	if err := json.Unmarshal([]byte(os.Getenv("VERIFY_FIXTURE")), &fix); err != nil {
		fmt.Fprintf(os.Stderr, "fixture: %v\n", err)
		os.Exit(2)
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("VERIFY_ARGS")), &args); err != nil {
		fmt.Fprintf(os.Stderr, "args: %v\n", err)
		os.Exit(2)
	}

	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return dispatch(fix, req), nil
	})
	flag.CommandLine = flag.NewFlagSet("verify", flag.ExitOnError)
	os.Args = append([]string{"verify"}, args...)
	main()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func dispatch(fix fixture, req *http.Request) *http.Response {
	if req.Header.Get("Authorization") != "Bearer "+placeholderToken || req.Header.Get("Accept") != "application/json" {
		return textResponse(req, http.StatusUnauthorized, "unauthorized")
	}
	if req.URL.Scheme != "https" || req.URL.Host != "forge.laravel.com" {
		return textResponse(req, http.StatusInternalServerError, "unexpected url")
	}

	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "orgs" || parts[2] != fix.Org || parts[3] != "servers" {
		return textResponse(req, http.StatusInternalServerError, "unexpected path "+req.URL.Path)
	}
	if len(parts) == 4 {
		return jsonResponse(req, serversDocument(fix))
	}

	srv, ok := serverByID(fix, parts[4])
	if !ok || len(parts) < 6 || parts[5] != "sites" {
		return textResponse(req, http.StatusInternalServerError, "unexpected path "+req.URL.Path)
	}
	if len(parts) == 6 {
		return jsonResponse(req, sitesDocument(srv))
	}

	one, ok := siteByID(srv, parts[6])
	if !ok {
		return textResponse(req, http.StatusInternalServerError, "unknown site")
	}
	switch {
	case len(parts) == 8 && parts[7] == "domains":
		return jsonResponse(req, domainsDocument(one))
	case len(parts) == 9 && parts[7] == "integrations" && parts[8] == "reverb":
		if one.ReverbHost == "" {
			return textResponse(req, http.StatusNotFound, "")
		}
		return jsonResponse(req, map[string]any{
			"data": map[string]any{
				"attributes": map[string]any{
					"enabled": true,
					"host":    one.ReverbHost,
					"port":    one.ReverbPort,
				},
			},
		})
	default:
		return textResponse(req, http.StatusInternalServerError, "unexpected path "+req.URL.Path)
	}
}

func serverByID(fix fixture, id string) (serverFix, bool) {
	for _, srv := range fix.Servers {
		if srv.ID == id {
			return srv, true
		}
	}
	return serverFix{}, false
}

func siteByID(srv serverFix, id string) (siteFix, bool) {
	for _, one := range srv.Sites {
		if one.ID == id {
			return one, true
		}
	}
	return siteFix{}, false
}

func serversDocument(fix fixture) forge.ForgeServersResponse {
	data := make([]forge.ForgeServer, 0, len(fix.Servers))
	var included []any
	for _, srv := range fix.Servers {
		rel, tags := tagResources(srv.Tags)
		included = append(included, tags...)
		data = append(data, forge.ForgeServer{
			ID:   srv.ID,
			Type: "servers",
			Attributes: forge.ForgeServerAttributes{
				Name:             srv.Name,
				PrivateIPAddress: srv.PrivateIP,
			},
			Relationships: rel,
		})
	}
	if included == nil {
		included = []any{}
	}
	return forge.ForgeServersResponse{Data: data, Included: included, Links: []any{}, Meta: []any{}}
}

func sitesDocument(srv serverFix) forge.ForgeSitesResponse {
	data := make([]forge.ForgeSite, 0, len(srv.Sites))
	var included []any
	for _, one := range srv.Sites {
		rel, tags := tagResources(one.Tags)
		included = append(included, tags...)
		data = append(data, forge.ForgeSite{
			ID:   one.ID,
			Type: "sites",
			Attributes: forge.ForgeSiteAttributes{
				Name:   one.Name,
				Status: "installed",
			},
			Relationships: rel,
		})
	}
	if included == nil {
		included = []any{}
	}
	return forge.ForgeSitesResponse{Data: data, Included: included, Links: []any{}, Meta: []any{}}
}

func domainsDocument(one siteFix) forge.ForgeDomainsResponse {
	data := make([]forge.ForgeDomain, 0, len(one.Domains))
	for i, d := range one.Domains {
		kind := d.Type
		if kind == "" {
			kind = "alias"
		}
		data = append(data, forge.ForgeDomain{
			ID:   fmt.Sprintf("dom-%d", i),
			Type: "domainRecords",
			Attributes: forge.ForgeDomainAttributes{
				Name:                    d.Name,
				DomainType:              kind,
				Status:                  "enabled",
				AllowWildcardSubdomains: d.Wildcard,
				WWWRedirectType:         d.WWW,
			},
		})
	}
	return forge.ForgeDomainsResponse{Data: data, Links: []any{}, Meta: []any{}}
}

func tagResources(tags []string) (forge.ForgeRelationships, []any) {
	var rel forge.ForgeRelationships
	included := make([]any, 0, len(tags))
	for i, name := range tags {
		id := fmt.Sprintf("tag-%d", i)
		rel.Tags.Data = append(rel.Tags.Data, forge.ForgeTagRef{Type: "tags", ID: id})
		included = append(included, map[string]any{
			"type": "tags",
			"id":   id,
			"attributes": map[string]string{
				"name": name,
			},
		})
	}
	return rel, included
}

func jsonResponse(req *http.Request, payload any) *http.Response {
	body, err := json.Marshal(payload)
	if err != nil {
		return textResponse(req, http.StatusInternalServerError, err.Error())
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
}

func textResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
}
