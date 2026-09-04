package traefik_laravel_forge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests live in the root package so they can reach the unexported HTTP
// client, which means they must stay testify-free — yaegi cannot interpret
// testify (it reaches `unsafe` via go-spew) and CI runs `yaegi test .`.

// newTestClient points a real forgeHTTPClient at a stub server, so the JSON:API
// decoding path — which carries all the hard-won Forge quirks — is exercised for
// real rather than mocked away.
func newTestClient(t *testing.T, handler http.HandlerFunc) *forgeHTTPClient {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := newForgeHTTPClient("test-token", "wickedsick")
	c.baseURL = srv.URL

	return c
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFetchServersResolvesTagsFromIncluded(t *testing.T) {
	var gotPath, gotAuth, gotAccept string

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotAccept = r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("Accept")
		// "links" and "meta" are empty ARRAYS here, not objects. Forge really does
		// this, and typing them as map[string]interface{} fails the whole decode.
		_, _ = w.Write([]byte(`{
		  "data": [
		    {
		      "id": "srv-1",
		      "type": "servers",
		      "attributes": {"name": "php01", "ip_address": "203.0.113.1", "private_ip_address": "10.0.0.5"},
		      "relationships": {"tags": {"data": [{"type": "tags", "id": "t1"}, {"type": "tags", "id": "t2"}]}}
		    }
		  ],
		  "included": [
		    {"type": "tags", "id": "t1", "attributes": {"name": "traefik:upstream-host=10.0.0.5"}},
		    {"type": "tags", "id": "t2", "attributes": {"name": "production"}},
		    {"type": "regions", "id": "r1", "attributes": {"name": "not-a-tag"}}
		  ],
		  "links": [],
		  "meta": []
		}`))
	})

	servers, err := c.FetchServers()
	if err != nil {
		t.Fatalf("FetchServers() error = %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(servers))
	}

	if want := "/api/orgs/wickedsick/servers?include=tags"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if want := "Bearer test-token"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if want := "application/json"; gotAccept != want {
		t.Errorf("Accept = %q, want %q", gotAccept, want)
	}
	if servers[0].Attributes.PrivateIPAddress != "10.0.0.5" {
		t.Errorf("private IP = %q, want 10.0.0.5", servers[0].Attributes.PrivateIPAddress)
	}
	// Tags come from relationships + included, never from attributes.tags.
	assertStrings(t, servers[0].Attributes.Tags, []string{"traefik:upstream-host=10.0.0.5", "production"})
}

func TestFetchSitesResolvesTagsFromIncluded(t *testing.T) {
	var gotPath string

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		_, _ = w.Write([]byte(`{
		  "data": [
		    {
		      "id": "site-9",
		      "type": "sites",
		      "attributes": {"name": "bounceiq.com", "status": "installed"},
		      "relationships": {"tags": {"data": [{"type": "tags", "id": "t7"}]}}
		    }
		  ],
		  "included": [{"type": "tags", "id": "t7", "attributes": {"name": "traefik:port=8080"}}],
		  "links": [],
		  "meta": []
		}`))
	})

	sites, err := c.FetchSites("srv-1")
	if err != nil {
		t.Fatalf("FetchSites() error = %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("got %d sites, want 1", len(sites))
	}

	if want := "/api/orgs/wickedsick/servers/srv-1/sites?include=tags"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if sites[0].Attributes.Status != "installed" {
		t.Errorf("status = %q, want installed", sites[0].Attributes.Status)
	}
	assertStrings(t, sites[0].Attributes.Tags, []string{"traefik:port=8080"})
}

func TestFetchServersUnresolvableTagReferenceIsDropped(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
		  "data": [{"id": "srv-1", "attributes": {"name": "php01"},
		    "relationships": {"tags": {"data": [{"type": "tags", "id": "missing"}]}}}],
		  "included": []
		}`))
	})

	servers, err := c.FetchServers()
	if err != nil {
		t.Fatalf("FetchServers() error = %v", err)
	}
	if len(servers[0].Attributes.Tags) != 0 {
		t.Errorf("tags = %v, want none", servers[0].Attributes.Tags)
	}
}

func TestFetchServersNonOKIsAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
	})

	_, err := c.FetchServers()
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want it to mention the status code", err)
	}
}

func TestFetchServersMalformedBodyIsAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data": "not-an-array"}`))
	})

	_, err := c.FetchServers()
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if !strings.Contains(err.Error(), "failed to decode servers response") {
		t.Errorf("error = %v, want a decode error", err)
	}
}

func TestFetchDomainsUsesDomainsEndpoint(t *testing.T) {
	var gotPath string

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		_, _ = w.Write([]byte(`{
		  "data": [
		    {"id": "d1", "type": "domainRecords", "attributes": {
		       "name": "bounceiq.com", "type": "primary", "status": "enabled",
		       "allow_wildcard_subdomains": true, "www_redirect_type": "to-www"}},
		    {"id": "d2", "type": "domainRecords", "attributes": {
		       "name": "ws.bounceiq.net", "type": "alias", "status": "enabled"}}
		  ],
		  "links": [],
		  "meta": []
		}`))
	})

	domains, err := c.FetchDomains("srv-1", "site-9")
	if err != nil {
		t.Fatalf("FetchDomains() error = %v", err)
	}
	if len(domains) != 2 {
		t.Fatalf("got %d domains, want 2", len(domains))
	}

	if want := "/api/orgs/wickedsick/servers/srv-1/sites/site-9/domains"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if !domains[0].Attributes.AllowWildcardSubdomains {
		t.Error("allow_wildcard_subdomains did not decode")
	}
	if domains[0].Attributes.WWWRedirectType != "to-www" {
		t.Errorf("www_redirect_type = %q, want to-www", domains[0].Attributes.WWWRedirectType)
	}
	// ws.bounceiq.net is type "alias" yet is the Reverb endpoint — domain type
	// alone must never be used to infer routing intent.
	if domains[1].Attributes.DomainType != "alias" {
		t.Errorf("domain type = %q, want alias", domains[1].Attributes.DomainType)
	}
}

func TestFetchDomainsNonOKReturnsNothingWithoutError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	domains, err := c.FetchDomains("srv-1", "site-9")
	if err != nil {
		t.Fatalf("FetchDomains() error = %v", err)
	}
	if domains != nil {
		t.Errorf("domains = %v, want nil", domains)
	}
}

func TestFetchReverbIntegration(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantHost string
		wantNil  bool
	}{
		{
			name:     "enabled integration is returned",
			body:     `{"data":{"attributes":{"enabled":true,"host":"ws.bounceiq.net","port":8081}}}`,
			wantHost: "ws.bounceiq.net",
		},
		{
			name:    "disabled integration is nil",
			body:    `{"data":{"attributes":{"enabled":false,"host":"ws.bounceiq.net","port":8081}}}`,
			wantNil: true,
		},
		{
			name:    "enabled but hostless integration is nil",
			body:    `{"data":{"attributes":{"enabled":true,"host":"","port":8081}}}`,
			wantNil: true,
		},
		{
			name:    "enabled but portless integration is nil",
			body:    `{"data":{"attributes":{"enabled":true,"host":"ws.bounceiq.net","port":0}}}`,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.RequestURI()
				_, _ = w.Write([]byte(tt.body))
			})

			ri, err := c.FetchReverbIntegration("srv-1", "site-9")
			if err != nil {
				t.Fatalf("FetchReverbIntegration() error = %v", err)
			}
			if want := "/api/orgs/wickedsick/servers/srv-1/sites/site-9/integrations/reverb"; gotPath != want {
				t.Errorf("path = %q, want %q", gotPath, want)
			}

			if tt.wantNil {
				if ri != nil {
					t.Errorf("got %+v, want nil", ri)
				}
				return
			}
			if ri == nil {
				t.Fatal("got nil, want an integration")
			}
			if ri.Host != tt.wantHost {
				t.Errorf("host = %q, want %q", ri.Host, tt.wantHost)
			}
		})
	}
}

func TestFetchReverbIntegrationAbsentEndpointIsNotAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	ri, err := c.FetchReverbIntegration("srv-1", "site-9")
	if err != nil {
		t.Fatalf("FetchReverbIntegration() error = %v", err)
	}
	if ri != nil {
		t.Errorf("got %+v, want nil", ri)
	}
}

// -- Non-OK status handling --
//
// The dangerous case is a status that means "your request was not answered"
// being reported as an empty result. Callers turn an empty /domains result into
// routing decisions, so a rate-limited poll would quietly replace every customer
// hostname with the Forge site name.

func TestFetchDomainsRefusedRequestIsAnError(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"Too many attempts."}`))
			})

			domains, err := c.FetchDomains("srv-1", "site-9")
			if err == nil {
				t.Fatalf("status %d was reported as an empty result, not an error", status)
			}
			if domains != nil {
				t.Errorf("domains = %v, want nil", domains)
			}
			if !strings.Contains(err.Error(), "failed to fetch domains") {
				t.Errorf("error = %v, want it to name the failing call", err)
			}
		})
	}
}

func TestFetchDomainsAbsentResourceIsNotAnError(t *testing.T) {
	// 404 and 422 mean "there is nothing here", which is a legitimate answer and
	// must stay distinguishable from a refused request.
	for _, status := range []int{http.StatusNotFound, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})

			domains, err := c.FetchDomains("srv-1", "site-9")
			if err != nil {
				t.Fatalf("status %d should not be an error, got %v", status, err)
			}
			if domains != nil {
				t.Errorf("domains = %v, want nil", domains)
			}
		})
	}
}

func TestFetchReverbIntegrationRateLimitIsAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	if _, err := c.FetchReverbIntegration("srv-1", "site-9"); err == nil {
		t.Fatal("a 429 on the Reverb endpoint was reported as 'no Reverb configured'")
	}
}

// Forge's X-RateLimit-* headers are what separate genuine quota exhaustion from a
// "Too many attempts" returned for some other reason. Without them in the log the
// difference is invisible from outside.
func TestStatusErrorReportsRateLimitHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ratelimit-Limit", "60")
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.Header().Set("X-Ratelimit-Reset", "1757000000")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"Too many attempts."}`))
	})

	_, err := c.FetchServers()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"429", "rate limit 60", "remaining 0", "reset 1757000000", "Too many attempts."} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// A 429 carrying no rate-limit headers is what an abuse/flag path looks like, as
// opposed to the ordinary limiter. It must still surface as a clear error.
func TestStatusErrorWithoutRateLimitHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"Too many attempts. Please contact Forge support."}`))
	})

	_, err := c.FetchServers()
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error %q claims rate-limit headers that were not sent", err.Error())
	}
	if !strings.Contains(err.Error(), "contact Forge support") {
		t.Errorf("error %q should quote the Forge message", err.Error())
	}
}

func TestStatusErrorTruncatesLongBodies(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	})

	_, err := c.FetchServers()
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > maxErrorBodyBytes+200 {
		t.Errorf("error is %d bytes; body should be capped at %d", len(err.Error()), maxErrorBodyBytes)
	}
}
