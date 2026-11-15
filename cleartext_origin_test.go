package traefik_laravel_forge

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/traefik/genconf/dynamic"
)

// These tests are the specification for how origin URLs are published.
// They exercise the production poll: Forge JSON:API decode, tag mapping,
// resolveUpstream, and processSite. They fail while a non-private origin is
// published with a cleartext scheme.

func TestNonPrivateOriginIsNotPublishedCleartext(t *testing.T) {
	t.Parallel()

	const (
		publicV4  = "203.0.113.10"
		publicV4b = "198.51.100.20"
		publicV6  = "2001:db8::10"
		mappedV4  = "::ffff:203.0.113.40"
		privateV4 = "10.8.0.4"
	)

	cases := []struct {
		name         string
		servers      []originServer
		sites        map[string][]originSite
		domains      map[string][]originDomain
		reverb       map[string]string
		mappings     []ServerMapping
		wantHost     string // a published server must still target this host
		httpRedirect bool
		certResolver string
	}{
		{
			name: "public ip fallback with client tls",
			servers: []originServer{{
				ID: "srv-public", Name: "app-public", PublicIP: publicV4, OmitPrivate: true,
			}},
			sites: map[string][]originSite{
				"srv-public": {
					{ID: "site-1", Name: "example.com"},
					{ID: "site-2", Name: "other.example"},
				},
			},
			httpRedirect: true,
			certResolver: "cloudflare",
		},
		{
			name: "explicit empty private ip",
			servers: []originServer{{
				ID: "srv-public", Name: "app-public", PublicIP: publicV4, PrivateIP: "",
			}},
			sites: map[string][]originSite{
				"srv-public": {{ID: "site-1", Name: "example.com"}},
			},
			certResolver: "cloudflare",
		},
		{
			name: "site port override on public ip",
			servers: []originServer{{
				ID: "srv-public", Name: "app-public", PublicIP: publicV4, OmitPrivate: true,
			}},
			sites: map[string][]originSite{
				"srv-public": {{ID: "site-1", Name: "example.com", Tags: []string{"traefik:port=8081"}}},
			},
		},
		{
			// UpstreamPort is returned only together with UpstreamHost. A port tag
			// on its own still leaves the public address on the default cleartext URL.
			name: "upstream-port tag alone still publishes the public ip",
			servers: []originServer{{
				ID: "srv-public", Name: "app-public", PublicIP: publicV4, OmitPrivate: true,
				Tags: []string{"traefik:upstream-port=9090"},
			}},
			sites: map[string][]originSite{
				"srv-public": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "upstream-host and upstream-port tags together",
			servers: []originServer{{
				ID: "srv-tagged", Name: "app-tagged", PublicIP: "192.0.2.8", PrivateIP: privateV4,
				Tags: []string{"traefik:upstream-host=" + publicV4, "traefik:upstream-port=9090"},
			}},
			sites: map[string][]originSite{
				"srv-tagged": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "mapping port only keeps the public ip",
			servers: []originServer{{
				ID: "srv-public", Name: "app-public", PublicIP: publicV4, OmitPrivate: true,
			}},
			sites: map[string][]originSite{
				"srv-public": {{ID: "site-1", Name: "example.com"}},
			},
			mappings: []ServerMapping{{ForgeServerName: "app-public", UpstreamPort: 9443}},
		},
		{
			name: "upstream-host tag is a public ip",
			servers: []originServer{{
				ID: "srv-tagged", Name: "app-tagged", PublicIP: "192.0.2.8", PrivateIP: privateV4,
				Tags: []string{"traefik:upstream-host=" + publicV4b},
			}},
			sites: map[string][]originSite{
				"srv-tagged": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "lb-host alias is a public ip",
			servers: []originServer{{
				ID: "srv-tagged", Name: "app-tagged", PublicIP: "192.0.2.8", PrivateIP: privateV4,
				Tags: []string{"traefik:lb-host=" + publicV4},
			}},
			sites: map[string][]originSite{
				"srv-tagged": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "loadbalancer-host alias is a public ip",
			servers: []originServer{{
				ID: "srv-tagged", Name: "app-tagged", PublicIP: "192.0.2.8", PrivateIP: privateV4,
				Tags: []string{"traefik:loadbalancer-host=" + publicV4},
			}},
			sites: map[string][]originSite{
				"srv-tagged": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "upstreamhost alias is a public ip",
			servers: []originServer{{
				ID: "srv-tagged", Name: "app-tagged", PublicIP: "192.0.2.8", PrivateIP: privateV4,
				Tags: []string{"traefik:upstreamhost=" + publicV4},
			}},
			sites: map[string][]originSite{
				"srv-tagged": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "server mapping host is a public ip",
			servers: []originServer{{
				ID: "srv-mapped", Name: "app-mapped", PublicIP: "192.0.2.8", PrivateIP: privateV4,
			}},
			sites: map[string][]originSite{
				"srv-mapped": {{ID: "site-1", Name: "example.com"}},
			},
			mappings: []ServerMapping{{
				ForgeServerName: "app-mapped",
				UpstreamHost:    publicV4b,
				UpstreamPort:    8080,
			}},
		},
		{
			name: "upstream-host tag is a hostname",
			servers: []originServer{{
				ID: "srv-named", Name: "app-named", PublicIP: publicV4, PrivateIP: privateV4,
				Tags: []string{"traefik:upstream-host=origin.example.com"},
			}},
			sites: map[string][]originSite{
				"srv-named": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "server mapping host is a hostname",
			servers: []originServer{{
				ID: "srv-named", Name: "app-named", PublicIP: publicV4, PrivateIP: privateV4,
			}},
			sites: map[string][]originSite{
				"srv-named": {{ID: "site-1", Name: "example.com"}},
			},
			mappings: []ServerMapping{{
				ForgeServerName: "app-named",
				UpstreamHost:    "origin.example.com",
			}},
		},
		{
			name: "reverb domain on a public ip server",
			servers: []originServer{{
				ID: "srv-public", Name: "app-public", PublicIP: publicV4, OmitPrivate: true,
			}},
			sites: map[string][]originSite{
				"srv-public": {{ID: "site-1", Name: "example.com"}},
			},
			domains: map[string][]originDomain{
				"srv-public/site-1": {
					{Name: "example.com", Type: "primary"},
					{Name: "ws.example.com", Type: "alias"},
				},
			},
			reverb:       map[string]string{"srv-public/site-1": "ws.example.com"},
			certResolver: "cloudflare",
		},
		{
			name: "public ipv6 fallback",
			servers: []originServer{{
				ID: "srv-public", Name: "app-public", PublicIP: publicV6, OmitPrivate: true,
			}},
			sites: map[string][]originSite{
				"srv-public": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "ipv4-mapped public address fallback",
			servers: []originServer{{
				ID: "srv-public", Name: "app-public", PublicIP: mappedV4, OmitPrivate: true,
			}},
			sites: map[string][]originSite{
				"srv-public": {{ID: "site-1", Name: "example.com"}},
			},
		},
		{
			name: "public server does not suppress a private server",
			servers: []originServer{
				{ID: "srv-public", Name: "app-public", PublicIP: publicV4, OmitPrivate: true},
				{ID: "srv-private", Name: "app-private", PublicIP: "192.0.2.9", PrivateIP: privateV4},
			},
			sites: map[string][]originSite{
				"srv-public":  {{ID: "site-pub", Name: "public.example"}},
				"srv-private": {{ID: "site-priv", Name: "private.example"}},
			},
			wantHost:     privateV4,
			certResolver: "cloudflare",
		},
		{
			name: "private ip is preferred and may stay cleartext",
			servers: []originServer{{
				ID: "srv-private", Name: "app-private", PublicIP: publicV4, PrivateIP: privateV4,
			}},
			sites: map[string][]originSite{
				"srv-private": {{ID: "site-1", Name: "example.com"}},
			},
			wantHost:     privateV4,
			certResolver: "cloudflare",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient(t, forgeAPIHandler(t, tc.servers, tc.sites, tc.domains, tc.reverb))
			p := providerForOriginTest(t, client, func(cfg *Config) {
				cfg.HTTPRedirect = tc.httpRedirect
				cfg.DefaultCertResolver = tc.certResolver
				cfg.ServerMappings = tc.mappings
			})

			got, err := p.GenerateConfiguration()
			if err != nil {
				t.Fatalf("GenerateConfiguration() error = %v", err)
			}
			assertNoCleartextNonPrivateOrigin(t, got)
			if tc.wantHost != "" && !configurationTargets(got, tc.wantHost) {
				t.Errorf("configuration does not publish an origin for host %s", tc.wantHost)
			}
		})
	}
}

func TestPrivateNetworkTagAndMappingStayPublishable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		servers  []originServer
		mappings []ServerMapping
		wantHost string
	}{
		{
			name: "upstream-host tag on a private address",
			servers: []originServer{{
				ID: "srv-1", Name: "app01", PublicIP: "203.0.113.10", OmitPrivate: true,
				Tags: []string{"traefik:upstream-host=192.168.1.9"},
			}},
			wantHost: "192.168.1.9",
		},
		{
			name: "mapping host on a private address",
			servers: []originServer{{
				ID: "srv-1", Name: "app01", PublicIP: "203.0.113.10", OmitPrivate: true,
			}},
			mappings: []ServerMapping{{ForgeServerName: "app01", UpstreamHost: "172.16.4.4", UpstreamPort: 8080}},
			wantHost: "172.16.4.4",
		},
		{
			name: "loopback upstream tag",
			servers: []originServer{{
				ID: "srv-1", Name: "app01", PublicIP: "203.0.113.10", OmitPrivate: true,
				Tags: []string{"traefik:upstream-host=127.0.0.1"},
			}},
			wantHost: "127.0.0.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sites := map[string][]originSite{
				"srv-1": {{ID: "site-1", Name: "example.com"}},
			}
			client := newTestClient(t, forgeAPIHandler(t, tc.servers, sites, nil, nil))
			p := providerForOriginTest(t, client, func(cfg *Config) {
				cfg.ServerMappings = tc.mappings
			})
			got, err := p.GenerateConfiguration()
			if err != nil {
				t.Fatalf("GenerateConfiguration() error = %v", err)
			}
			assertNoCleartextNonPrivateOrigin(t, got)
			if !configurationTargets(got, tc.wantHost) {
				t.Errorf("configuration does not publish an origin for host %s", tc.wantHost)
			}
		})
	}
}

type originServer struct {
	ID          string
	Name        string
	PublicIP    string
	PrivateIP   string
	OmitPrivate bool
	Tags        []string
}

type originSite struct {
	ID   string
	Name string
	Tags []string
}

type originDomain struct {
	Name string
	Type string
}

func providerForOriginTest(t *testing.T, client ForgeClient, modify func(*Config)) *Provider {
	t.Helper()
	cfg := CreateConfig()
	if modify != nil {
		modify(cfg)
	}
	p, err := NewProviderWithClient(cfg, client)
	if err != nil {
		t.Fatalf("NewProviderWithClient() error = %v", err)
	}
	return p
}

func forgeAPIHandler(
	t *testing.T,
	servers []originServer,
	sites map[string][]originSite,
	domains map[string][]originDomain,
	reverb map[string]string,
) http.HandlerFunc {
	t.Helper()

	serversBody := mustJSON(t, serversDocument(servers))
	siteBodies := map[string]string{}
	for id, list := range sites {
		siteBodies[id] = mustJSON(t, sitesDocument(list))
	}
	domainBodies := map[string]string{}
	for key, list := range domains {
		domainBodies[key] = mustJSON(t, domainsDocument(list))
	}
	reverbBodies := map[string]string{}
	for key, host := range reverb {
		reverbBodies[key] = mustJSON(t, map[string]any{
			"data": map[string]any{
				"attributes": map[string]any{"enabled": true, "host": host, "port": 8081},
			},
		})
	}

	return func(w http.ResponseWriter, r *http.Request) {
		rest := pathAfter(r.URL.Path, "servers")
		var body string
		ok := false
		switch {
		case len(rest) == 0:
			body, ok = serversBody, true
		case len(rest) == 2 && rest[1] == "sites":
			body, ok = siteBodies[rest[0]]
		case len(rest) == 4 && rest[1] == "sites" && rest[3] == "domains":
			body, ok = domainBodies[rest[0]+"/"+rest[2]]
		case len(rest) == 5 && rest[1] == "sites" && rest[3] == "integrations" && rest[4] == "reverb":
			body, ok = reverbBodies[rest[0]+"/"+rest[2]]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func pathAfter(path, marker string) []string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range segs {
		if seg == marker {
			return segs[i+1:]
		}
	}
	return nil
}

func serversDocument(servers []originServer) map[string]any {
	data := make([]any, 0, len(servers))
	included := []any{}
	for _, server := range servers {
		refs := []any{}
		for i, tag := range server.Tags {
			tagID := server.ID + "-tag-" + strconv.Itoa(i)
			refs = append(refs, map[string]any{"type": "tags", "id": tagID})
			included = append(included, map[string]any{
				"type":       "tags",
				"id":         tagID,
				"attributes": map[string]any{"name": tag},
			})
		}
		attrs := map[string]any{
			"name":       server.Name,
			"ip_address": server.PublicIP,
		}
		if !server.OmitPrivate {
			attrs["private_ip_address"] = server.PrivateIP
		}
		data = append(data, map[string]any{
			"id":         server.ID,
			"type":       "servers",
			"attributes": attrs,
			"relationships": map[string]any{
				"tags": map[string]any{"data": refs},
			},
		})
	}
	return envelope(data, included)
}

func sitesDocument(sites []originSite) map[string]any {
	data := make([]any, 0, len(sites))
	included := []any{}
	for _, site := range sites {
		refs := []any{}
		for i, tag := range site.Tags {
			tagID := site.ID + "-tag-" + strconv.Itoa(i)
			refs = append(refs, map[string]any{"type": "tags", "id": tagID})
			included = append(included, map[string]any{
				"type":       "tags",
				"id":         tagID,
				"attributes": map[string]any{"name": tag},
			})
		}
		data = append(data, map[string]any{
			"id":   site.ID,
			"type": "sites",
			"attributes": map[string]any{
				"name":   site.Name,
				"status": "installed",
			},
			"relationships": map[string]any{
				"tags": map[string]any{"data": refs},
			},
		})
	}
	return envelope(data, included)
}

func domainsDocument(domains []originDomain) map[string]any {
	data := make([]any, 0, len(domains))
	for i, domain := range domains {
		data = append(data, map[string]any{
			"id":   "dom-" + strconv.Itoa(i),
			"type": "domainRecords",
			"attributes": map[string]any{
				"name":   domain.Name,
				"type":   domain.Type,
				"status": "enabled",
			},
		})
	}
	return envelope(data, []any{})
}

func envelope(data, included []any) map[string]any {
	return map[string]any{
		"data":     data,
		"included": included,
		"links":    []any{},
		"meta":     []any{},
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(raw)
}

func assertNoCleartextNonPrivateOrigin(t *testing.T, cfg *dynamic.Configuration) {
	t.Helper()
	if cfg == nil || cfg.HTTP == nil {
		t.Fatal("GenerateConfiguration returned no HTTP configuration")
	}
	for name, svc := range cfg.HTTP.Services {
		if svc == nil || svc.LoadBalancer == nil {
			continue
		}
		pass := "unset"
		if svc.LoadBalancer.PassHostHeader != nil {
			if *svc.LoadBalancer.PassHostHeader {
				pass = "true"
			} else {
				pass = "false"
			}
		}
		for _, server := range svc.LoadBalancer.Servers {
			if cleartextNonPrivate(server.URL) {
				t.Errorf("service %s publishes cleartext non-private origin %s (passHostHeader=%s)",
					name, server.URL, pass)
			}
		}
	}
	for name, router := range cfg.HTTP.Routers {
		if router == nil {
			continue
		}
		svc := cfg.HTTP.Services[router.Service]
		if svc == nil || svc.LoadBalancer == nil {
			continue
		}
		for _, server := range svc.LoadBalancer.Servers {
			if cleartextNonPrivate(server.URL) {
				t.Errorf("router %s -> service %s publishes cleartext non-private origin %s",
					name, router.Service, server.URL)
			}
		}
	}
}

func configurationTargets(cfg *dynamic.Configuration, host string) bool {
	if cfg == nil || cfg.HTTP == nil {
		return false
	}
	for _, svc := range cfg.HTTP.Services {
		if svc == nil || svc.LoadBalancer == nil {
			continue
		}
		for _, server := range svc.LoadBalancer.Servers {
			u, err := url.Parse(server.URL)
			if err != nil {
				continue
			}
			if u.Hostname() == host {
				return true
			}
		}
	}
	return false
}

// cleartextNonPrivate reports whether raw is an origin URL whose scheme carries
// bytes without TLS and whose host is not a loopback, link-local, or private address.
func cleartextNonPrivate(raw string) bool {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "h2c://") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return true
	}
	return !hostOnPrivateNetwork(u.Hostname())
}

func hostOnPrivateNetwork(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}
