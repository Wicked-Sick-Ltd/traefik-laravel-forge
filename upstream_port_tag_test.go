package traefik_laravel_forge

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/traefik/genconf/dynamic"
)

// Server-level traefik:upstream-port (and its aliases) is documented to override
// the backend port for every site on that server, ahead of serverMappings and
// the default of 80, even when traefik:upstream-host is not set. These tests
// feed the tag the way Forge does — a JSON:API tag resource referenced from
// relationships, not attributes.tags — and require the load-balancer URL that
// GenerateConfiguration hands to Traefik to use that port.

func TestServerUpstreamPortTagReachesBackendURL(t *testing.T) {
	const (
		org        = "wickedsick"
		serverID   = "srv-1"
		serverName = "php01"
		serverPort = 8443
		sitePort   = 3000
		publicIP   = "203.0.113.10"
		privateIP  = "10.0.0.5"
		reverbPort = 8081
	)

	mainSiteID, mainSite := "site-a", "app.example.com"
	overrideSiteID, overrideSite := "site-b", "api.example.com"
	reverbHost := "ws.app.example.com"

	routes := map[string]string{
		"/api/orgs/" + org + "/servers?include=tags": jsonAPIDocument(
			serverObject(serverID, serverName, publicIP, privateIP, tagRef("t-id")+","+tagRef("t-port")),
			tagObject("t-id", "traefik:traefik-id=lb01")+","+tagObject("t-port", fmt.Sprintf("traefik:upstream-port=%d", serverPort)),
		),
		"/api/orgs/" + org + "/servers/" + serverID + "/sites?include=tags": jsonAPIDocument(
			siteObject(mainSiteID, mainSite, "")+","+siteObject(overrideSiteID, overrideSite, tagRef("t-site-port")),
			tagObject("t-site-port", fmt.Sprintf("traefik:port=%d", sitePort)),
		),
		domainsPath(org, serverID, mainSiteID):     domainDocument(mainSite, reverbHost),
		domainsPath(org, serverID, overrideSiteID): domainDocument(overrideSite),
		reverbPath(org, serverID, mainSiteID): fmt.Sprintf(
			`{"data":{"attributes":{"enabled":true,"host":%q,"port":%d}}}`, reverbHost, reverbPort),
		reverbPath(org, serverID, overrideSiteID): `{"data":{"attributes":{"enabled":false,"host":"","port":0}}}`,
	}

	cfg := generateAgainstForge(t, routes, nil, "lb01")

	// The traefik-id tag was honored (this server was not skipped), so the port
	// tag on the same server was parsed too. It still has to reach the URL.
	mainURL := serviceBackendURL(t, cfg, mainSite, mainSiteID)
	if mainURL != fmt.Sprintf("http://%s:%d", privateIP, serverPort) {
		t.Errorf("site %s backend = %s, want the server upstream-port on the auto-detected private IP", mainSite, mainURL)
	}
	if strings.Contains(mainURL, publicIP) {
		t.Errorf("backend %s used the public IP; private_ip_address is the auto-detected host", mainURL)
	}
	if strings.Contains(mainURL, fmt.Sprintf(":%d", reverbPort)) {
		t.Errorf("backend %s used the Reverb integration port; that port is not the backend", mainURL)
	}

	reverbRouter := cfg.HTTP.Routers["forge-"+mainSiteID+"-"+reverbHost]
	if reverbRouter == nil {
		t.Fatalf("reverb router missing; domain %s was not classified from the integration host", reverbHost)
	}
	if reverbRouter.Service != "forge-"+mainSite+"-"+mainSiteID+"-service" {
		t.Errorf("reverb router service = %s, want the site service whose URL carries the server port", reverbRouter.Service)
	}

	overrideURL := serviceBackendURL(t, cfg, overrideSite, overrideSiteID)
	if overrideURL != fmt.Sprintf("http://%s:%d", privateIP, sitePort) {
		t.Errorf("site %s backend = %s, want the site traefik:port to outrank the server upstream-port", overrideSite, overrideURL)
	}
}

func TestServerUpstreamPortAliasesReachBackendURL(t *testing.T) {
	aliases := []struct {
		tag  string
		port int
	}{
		{tag: "traefik:lb-port=3000", port: 3000},
		{tag: "traefik:loadbalancer-port=4000", port: 4000},
		{tag: "traefik:upstreamport=9091", port: 9091},
	}

	for _, tt := range aliases {
		t.Run(tt.tag, func(t *testing.T) {
			const (
				org        = "wickedsick"
				serverID   = "srv-1"
				serverName = "php01"
				publicIP   = "203.0.113.10"
				siteID     = "site-1"
				siteName   = "app.example.com"
			)

			routes := map[string]string{
				"/api/orgs/" + org + "/servers?include=tags": jsonAPIDocument(
					serverObject(serverID, serverName, publicIP, "", tagRef("t-port")),
					tagObject("t-port", tt.tag),
				),
				"/api/orgs/" + org + "/servers/" + serverID + "/sites?include=tags": jsonAPIDocument(
					siteObject(siteID, siteName, ""),
					"",
				),
				domainsPath(org, serverID, siteID): domainDocument(siteName),
				reverbPath(org, serverID, siteID):  `{"data":{"attributes":{"enabled":false,"host":"","port":0}}}`,
			}

			cfg := generateAgainstForge(t, routes, nil, "")
			got := serviceBackendURL(t, cfg, siteName, siteID)
			want := fmt.Sprintf("http://%s:%d", publicIP, tt.port)
			if got != want {
				t.Errorf("backend = %s, want %s", got, want)
			}
		})
	}
}

func TestServerUpstreamPortOutranksServerMapping(t *testing.T) {
	const (
		org        = "wickedsick"
		serverID   = "srv-1"
		serverName = "php01"
		serverPort = 9090
		publicIP   = "203.0.113.10"
		privateIP  = "10.0.0.5"
		siteID     = "site-1"
		siteName   = "app.example.com"
	)

	routes := map[string]string{
		"/api/orgs/" + org + "/servers?include=tags": jsonAPIDocument(
			serverObject(serverID, serverName, publicIP, privateIP, tagRef("t-port")),
			tagObject("t-port", fmt.Sprintf("traefik:upstream-port=%d", serverPort)),
		),
		"/api/orgs/" + org + "/servers/" + serverID + "/sites?include=tags": jsonAPIDocument(
			siteObject(siteID, siteName, ""),
			"",
		),
		domainsPath(org, serverID, siteID): domainDocument(siteName),
		reverbPath(org, serverID, siteID):  `{"data":{"attributes":{"enabled":false,"host":"","port":0}}}`,
	}

	t.Run("mapping host and port", func(t *testing.T) {
		cfg := generateAgainstForge(t, routes, []ServerMapping{{
			ForgeServerName: serverName,
			UpstreamHost:    "192.168.100.5",
			UpstreamPort:    8080,
		}}, "")
		got := serviceBackendURL(t, cfg, siteName, siteID)
		want := "http://192.168.100.5:9090"
		if got != want {
			t.Errorf("backend = %s, want %s (mapping supplies the host, the server port tag supplies the port)", got, want)
		}
	})

	t.Run("port-only mapping", func(t *testing.T) {
		cfg := generateAgainstForge(t, routes, []ServerMapping{{
			ForgeServerName: serverName,
			UpstreamPort:    8080,
		}}, "")
		got := serviceBackendURL(t, cfg, siteName, siteID)
		want := fmt.Sprintf("http://%s:%d", privateIP, serverPort)
		if got != want {
			t.Errorf("backend = %s, want %s (server port tag outranks a port-only mapping)", got, want)
		}
	})

	t.Run("explicit port 80 outranks mapping", func(t *testing.T) {
		tagged := map[string]string{
			"/api/orgs/" + org + "/servers?include=tags": jsonAPIDocument(
				serverObject(serverID, serverName, publicIP, privateIP, tagRef("t-port")),
				tagObject("t-port", "traefik:upstream-port=80"),
			),
		}
		for path, body := range routes {
			if _, ok := tagged[path]; !ok {
				tagged[path] = body
			}
		}
		cfg := generateAgainstForge(t, tagged, []ServerMapping{{
			ForgeServerName: serverName,
			UpstreamHost:    "192.168.100.5",
			UpstreamPort:    8080,
		}}, "")
		got := serviceBackendURL(t, cfg, siteName, siteID)
		want := "http://192.168.100.5:80"
		if got != want {
			t.Errorf("backend = %s, want %s (an explicit upstream-port of 80 is a server tag, not an unset port)", got, want)
		}
	})
}

func TestServerUpstreamPortAppliesToEverySiteOnThatServerOnly(t *testing.T) {
	const (
		org      = "wickedsick"
		taggedID = "srv-tagged"
		plainID  = "srv-plain"
		port     = 8443
	)

	routes := map[string]string{
		"/api/orgs/" + org + "/servers?include=tags": jsonAPIDocument(
			serverObject(taggedID, "php01", "203.0.113.10", "10.0.0.5", tagRef("t-port"))+","+
				serverObject(plainID, "php02", "203.0.113.11", "10.0.0.6", ""),
			tagObject("t-port", fmt.Sprintf("traefik:upstream-port=%d", port)),
		),
		"/api/orgs/" + org + "/servers/" + taggedID + "/sites?include=tags": jsonAPIDocument(
			siteObject("site-1", "one.example.com", "")+","+siteObject("site-2", "two.example.com", ""),
			"",
		),
		"/api/orgs/" + org + "/servers/" + plainID + "/sites?include=tags": jsonAPIDocument(
			siteObject("site-3", "three.example.com", ""),
			"",
		),
		domainsPath(org, taggedID, "site-1"): domainDocument("one.example.com"),
		domainsPath(org, taggedID, "site-2"): domainDocument("two.example.com"),
		domainsPath(org, plainID, "site-3"):  domainDocument("three.example.com"),
		reverbPath(org, taggedID, "site-1"):  `{"data":{"attributes":{"enabled":false,"host":"","port":0}}}`,
		reverbPath(org, taggedID, "site-2"):  `{"data":{"attributes":{"enabled":false,"host":"","port":0}}}`,
		reverbPath(org, plainID, "site-3"):   `{"data":{"attributes":{"enabled":false,"host":"","port":0}}}`,
	}

	cfg := generateAgainstForge(t, routes, nil, "")

	for _, site := range []struct{ name, id string }{{"one.example.com", "site-1"}, {"two.example.com", "site-2"}} {
		got := serviceBackendURL(t, cfg, site.name, site.id)
		want := fmt.Sprintf("http://10.0.0.5:%d", port)
		if got != want {
			t.Errorf("site %s backend = %s, want %s", site.name, got, want)
		}
	}

	plain := serviceBackendURL(t, cfg, "three.example.com", "site-3")
	if plain != "http://10.0.0.6:80" {
		t.Errorf("untagged server backend = %s, want http://10.0.0.6:80", plain)
	}
}

func generateAgainstForge(t *testing.T, routes map[string]string, mappings []ServerMapping, traefikID string) *dynamic.Configuration {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.RequestURI()]
		if !ok {
			http.Error(w, "unexpected "+r.URL.RequestURI(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	client := newForgeHTTPClient("placeholder-token", "wickedsick")
	client.baseURL = srv.URL

	cfg := CreateConfig()
	cfg.TraefikID = traefikID
	if mappings != nil {
		cfg.ServerMappings = mappings
	}
	provider, err := NewProviderWithClient(cfg, client)
	if err != nil {
		t.Fatalf("NewProviderWithClient: %v", err)
	}

	generated, err := provider.GenerateConfiguration()
	if err != nil {
		t.Fatalf("GenerateConfiguration: %v", err)
	}
	return generated
}

func serviceBackendURL(t *testing.T, cfg *dynamic.Configuration, siteName, siteID string) string {
	t.Helper()

	name := fmt.Sprintf("forge-%s-%s-service", siteName, siteID)
	svc := cfg.HTTP.Services[name]
	if svc == nil || svc.LoadBalancer == nil || len(svc.LoadBalancer.Servers) != 1 {
		t.Fatalf("service %s missing from generated config", name)
	}
	return svc.LoadBalancer.Servers[0].URL
}

func jsonAPIDocument(data, included string) string {
	return `{"data":[` + data + `],"included":[` + included + `],"links":[],"meta":[]}`
}

func tagRef(id string) string {
	return fmt.Sprintf(`{"type":"tags","id":%q}`, id)
}

func tagObject(id, name string) string {
	return fmt.Sprintf(`{"type":"tags","id":%q,"attributes":{"name":%q}}`, id, name)
}

func serverObject(id, name, publicIP, privateIP, tagRefs string) string {
	privateField := ""
	if privateIP != "" {
		privateField = fmt.Sprintf(`,"private_ip_address":%q`, privateIP)
	}
	return fmt.Sprintf(
		`{"id":%q,"type":"servers","attributes":{"name":%q,"ip_address":%q%s},"relationships":{"tags":{"data":[%s]}}}`,
		id, name, publicIP, privateField, tagRefs,
	)
}

func siteObject(id, name, tagRefs string) string {
	return fmt.Sprintf(
		`{"id":%q,"type":"sites","attributes":{"name":%q,"status":"installed"},"relationships":{"tags":{"data":[%s]}}}`,
		id, name, tagRefs,
	)
}

func domainDocument(names ...string) string {
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf(
			`{"id":%q,"type":"domainRecords","attributes":{"name":%q,"type":"primary","status":"enabled"}}`,
			name, name,
		)
	}
	return `{"data":[` + strings.Join(parts, ",") + `],"links":[],"meta":[]}`
}

func domainsPath(org, serverID, siteID string) string {
	return "/api/orgs/" + org + "/servers/" + serverID + "/sites/" + siteID + "/domains"
}

func reverbPath(org, serverID, siteID string) string {
	return "/api/orgs/" + org + "/servers/" + serverID + "/sites/" + siteID + "/integrations/reverb"
}
