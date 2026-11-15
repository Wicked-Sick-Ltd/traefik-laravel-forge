package traefik_laravel_forge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

// pluginLogMu serializes redirection of the process-wide plugin logger.
var pluginLogMu sync.Mutex

// TestServerUpstreamHostTagCannotForgePluginLogLines is the spec for a Forge
// server tag that carries a newline into the plugin log.
//
// A member who can tag a server supplies the value. The poll decodes it from
// the JSON:API included tags, ParseServerTags stores it, and resolveUpstream
// returns it as the backend host. These tests fail while that host is written
// with an unquoted verb. They pass when the host is logged so the newline is
// not a record boundary, or when a host containing a newline is rejected
// before any of those writes.
//
// Operations covered, all on one poll:
//   - processServer's "Processing server" line
//   - processSite's "Created N router(s)" line, once per enabled site
//   - processSite's "Created Reverb router" line
//
// The same three writes are what a serverMappings upstream host reaches.
// That value is operator configuration, so it is not a separate attack input.
// cmd/verify prints the published backend URL from main and is not covered.
func TestServerUpstreamHostTagCannotForgePluginLogLines(t *testing.T) {
	const forged = "forge: kept previous configuration; routing unchanged"
	// TrimSpace only strips ends. The newline has to sit between the host and
	// the second line, which is the value a tag can actually carry.
	host := "10.0.0.8\n" + forged

	// Every key ParseServerTags copies into UpstreamHost.
	for _, key := range []string{"upstream-host", "upstreamhost", "lb-host", "loadbalancer-host"} {
		t.Run(key, func(t *testing.T) {
			out := pollForgedUpstreamTag(t, "traefik:"+key+"="+host)
			if strings.Contains(out, "\n"+forged) {
				t.Fatalf("tag %s forged a plugin log record\n%s", key, out)
			}
		})
	}
}

// pollForgedUpstreamTag runs one sendConfiguration poll against a Forge
// JSON:API stub whose server tag name is tagName, and returns the plugin
// stdout log plus the poll's stderr.
func pollForgedUpstreamTag(t *testing.T, tagName string) string {
	t.Helper()

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case "/api/orgs/wickedsick/servers?include=tags":
			writeJSON(t, w, serversPayload(tagName))
		case "/api/orgs/wickedsick/servers/srv-1/sites?include=tags":
			writeJSON(t, w, sitesPayload())
		case "/api/orgs/wickedsick/servers/srv-1/sites/site-shop/domains":
			writeJSON(t, w, domainsPayload("shop.example", "ws.shop.example"))
		case "/api/orgs/wickedsick/servers/srv-1/sites/site-shop/integrations/reverb":
			writeJSON(t, w, reverbPayload("ws.shop.example"))
		case "/api/orgs/wickedsick/servers/srv-1/sites/site-blog/domains":
			writeJSON(t, w, domainsPayload("blog.example"))
		case "/api/orgs/wickedsick/servers/srv-1/sites/site-blog/integrations/reverb":
			// Absent Reverb is a normal 404. The site must still be published.
			w.WriteHeader(http.StatusNotFound)
		default:
			http.Error(w, "unexpected "+r.URL.RequestURI(), http.StatusInternalServerError)
		}
	})

	provider, err := NewProviderWithClient(CreateConfig(), client)
	if err != nil {
		t.Fatalf("NewProviderWithClient() error = %v", err)
	}

	pluginLogMu.Lock()
	defer pluginLogMu.Unlock()

	var stdout bytes.Buffer
	pluginLog.SetOutput(&stdout)
	defer pluginLog.SetOutput(os.Stdout)

	stderr, restoreStderr := captureStderr(t)
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		restoreStderr()
	}
	defer restore()

	// sendConfiguration is the poll path. On failure it writes the error to
	// stderr with an unquoted verb, so a refusal that still embeds the host
	// forges a line of its own.
	cfgChan := make(chan json.Marshaler, 1)
	provider.sendConfiguration(context.Background(), cfgChan)
	restore()
	return stdout.String() + stderr.String()
}

// captureStderr redirects os.Stderr for the poll and returns what was written.
func captureStderr(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	return &buf, func() {
		os.Stderr = old
		_ = w.Close()
		<-done
		_ = r.Close()
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal stub response: %v", err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("write stub response: %v", err)
	}
}

func serversPayload(tagName string) map[string]any {
	return map[string]any{
		"data": []any{
			map[string]any{
				"id":   "srv-1",
				"type": "servers",
				"attributes": map[string]any{
					"name":               "php01",
					"ip_address":         "203.0.113.10",
					"private_ip_address": "10.0.0.5",
				},
				"relationships": map[string]any{
					"tags": map[string]any{
						"data": []any{map[string]any{"type": "tags", "id": "t-upstream"}},
					},
				},
			},
		},
		"included": []any{
			map[string]any{
				"type":       "tags",
				"id":         "t-upstream",
				"attributes": map[string]any{"name": tagName},
			},
		},
		"links": []any{},
		"meta":  []any{},
	}
}

func sitesPayload() map[string]any {
	site := func(id, name string) map[string]any {
		return map[string]any{
			"id":   id,
			"type": "sites",
			"attributes": map[string]any{
				"name":   name,
				"status": "installed",
			},
			"relationships": map[string]any{
				"tags": map[string]any{"data": []any{}},
			},
		}
	}
	return map[string]any{
		"data": []any{
			site("site-shop", "shop.example"),
			site("site-blog", "blog.example"),
		},
		"included": []any{},
		"links":    []any{},
		"meta":     []any{},
	}
}

func domainsPayload(names ...string) map[string]any {
	data := make([]any, 0, len(names))
	for _, name := range names {
		data = append(data, map[string]any{
			"id":   "dom-" + name,
			"type": "domainRecords",
			"attributes": map[string]any{
				"name":   name,
				"type":   "alias",
				"status": "enabled",
			},
		})
	}
	return map[string]any{
		"data":  data,
		"links": []any{},
		"meta":  []any{},
	}
}

func reverbPayload(host string) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"attributes": map[string]any{
				"enabled": true,
				"host":    host,
				"port":    8081,
			},
		},
	}
}
