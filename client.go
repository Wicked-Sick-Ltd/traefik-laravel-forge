package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ForgeServersResponse is the JSON:API response from listing servers.
type ForgeServersResponse struct {
	Data     []ForgeServer `json:"data"`
	Included []any         `json:"included,omitempty"`
	Links    any           `json:"links"`
	Meta     any           `json:"meta"`
}

// ForgeSitesResponse is the JSON:API response from listing sites.
type ForgeSitesResponse struct {
	Data     []ForgeSite `json:"data"`
	Included []any       `json:"included,omitempty"`
	Links    any         `json:"links"`
	Meta     any         `json:"meta"`
}

// ForgeDomainsResponse is the JSON:API response from the /domains endpoint.
type ForgeDomainsResponse struct {
	Data  []ForgeDomain `json:"data"`
	Links any           `json:"links"`
	Meta  any           `json:"meta"`
}

// ForgeReverbResponse is the JSON:API response from /integrations/reverb.
type ForgeReverbResponse struct {
	Data struct {
		Attributes ForgeReverbIntegration `json:"attributes"`
	} `json:"data"`
}

// forgeHTTPClient is the production ForgeClient implementation that calls the real API.
type forgeHTTPClient struct {
	apiToken     string
	organization string
	baseURL      string
	httpClient   *http.Client
}

func newForgeHTTPClient(apiToken, organization string) *forgeHTTPClient {
	return &forgeHTTPClient{
		apiToken:     apiToken,
		organization: organization,
		baseURL:      "https://forge.laravel.com",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchServers retrieves all servers in the organization from the Forge API.
func (c *forgeHTTPClient) FetchServers() ([]ForgeServer, error) {
	servers, _, err := c.fetchServersRaw()
	return servers, err
}

// FetchSites retrieves all sites for a specific server.
func (c *forgeHTTPClient) FetchSites(serverID string) ([]ForgeSite, error) {
	sites, _, err := c.fetchSitesRaw(serverID)
	return sites, err
}

// FetchDomains retrieves all domain records for a site via the /domains endpoint.
func (c *forgeHTTPClient) FetchDomains(serverID, siteID string) ([]ForgeDomain, error) {
	url := fmt.Sprintf("%s/api/orgs/%s/servers/%s/sites/%s/domains",
		c.baseURL, c.organization, serverID, siteID)
	raw, err := c.fetchRaw(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch domains: %w", err)
	}
	if raw == nil {
		return nil, nil
	}
	var resp ForgeDomainsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode domains response: %w", err)
	}
	return resp.Data, nil
}

// FetchReverbIntegration retrieves the Reverb WebSocket config for a site.
// Returns nil if Reverb is not enabled or the endpoint is unavailable.
func (c *forgeHTTPClient) FetchReverbIntegration(serverID, siteID string) (*ForgeReverbIntegration, error) {
	url := fmt.Sprintf("%s/api/orgs/%s/servers/%s/sites/%s/integrations/reverb",
		c.baseURL, c.organization, serverID, siteID)
	raw, err := c.fetchRaw(url)
	if err != nil || raw == nil {
		return nil, err
	}
	var resp ForgeReverbResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode reverb response: %w", err)
	}
	ri := resp.Data.Attributes
	if !ri.Enabled || ri.Host == "" || ri.Port == 0 {
		return nil, nil
	}
	return &ri, nil
}

func (c *forgeHTTPClient) fetchServersRaw() ([]ForgeServer, json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/orgs/%s/servers?include=tags", c.baseURL, c.organization)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch servers: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("forge API returned status %d: %s", resp.StatusCode, string(body))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read servers response: %w", err)
	}

	var serversResp ForgeServersResponse
	if err := json.Unmarshal(raw, &serversResp); err != nil {
		return nil, nil, fmt.Errorf("failed to decode servers response: %w", err)
	}

	mapTagsToServers(&serversResp)
	return serversResp.Data, json.RawMessage(raw), nil
}

func (c *forgeHTTPClient) fetchSitesRaw(serverID string) ([]ForgeSite, json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/orgs/%s/servers/%s/sites?include=tags",
		c.baseURL, c.organization, serverID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch sites: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("forge API returned status %d: %s", resp.StatusCode, string(body))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read sites response: %w", err)
	}

	var sitesResp ForgeSitesResponse
	if err := json.Unmarshal(raw, &sitesResp); err != nil {
		return nil, nil, fmt.Errorf("failed to decode sites response: %w", err)
	}

	mapTagsToSites(&sitesResp)
	return sitesResp.Data, json.RawMessage(raw), nil
}

// fetchRaw performs a GET and returns the response body, or nil for non-200 responses.
func (c *forgeHTTPClient) fetchRaw(url string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

func (c *forgeHTTPClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Accept", "application/json")
}

// dumpRaw fetches raw API responses for all servers, sites, domains, and Reverb
// configs. Used by the verify tool's --dump flag.
func (c *forgeHTTPClient) dumpRaw() ([]byte, error) {
	servers, rawServers, err := c.fetchServersRaw()
	if err != nil {
		return nil, err
	}

	type siteDump struct {
		ServerID   string            `json:"server_id"`
		ServerName string            `json:"server_name"`
		SitesList  json.RawMessage   `json:"sites_list"`
		Domains    []json.RawMessage `json:"domains"`
		Reverb     []json.RawMessage `json:"reverb"`
	}

	var siteDumps []siteDump
	for _, server := range servers {
		sites, raw, err := c.fetchSitesRaw(server.ID)
		if err != nil {
			return nil, fmt.Errorf("sites for server %s: %w", server.Attributes.Name, err)
		}

		dump := siteDump{
			ServerID:   server.ID,
			ServerName: server.Attributes.Name,
			SitesList:  raw,
		}

		for _, site := range sites {
			base := fmt.Sprintf("%s/api/orgs/%s/servers/%s/sites/%s",
				c.baseURL, c.organization, server.ID, site.ID)
			if d, _ := c.fetchRaw(base + "/domains"); d != nil {
				dump.Domains = append(dump.Domains, d)
			}
			if r, _ := c.fetchRaw(base + "/integrations/reverb"); r != nil {
				dump.Reverb = append(dump.Reverb, r)
			}
		}

		siteDumps = append(siteDumps, dump)
	}

	out := struct {
		Servers json.RawMessage `json:"servers"`
		Sites   []siteDump      `json:"sites"`
	}{
		Servers: rawServers,
		Sites:   siteDumps,
	}
	return json.MarshalIndent(out, "", "  ")
}

// mapTagsToServers resolves tag names from the included array and populates each
// server's Tags slice.
func mapTagsToServers(resp *ForgeServersResponse) {
	tagMap := buildTagMap(resp.Included)
	for i := range resp.Data {
		for _, ref := range resp.Data[i].Relationships.Tags.Data {
			if name, ok := tagMap[ref.ID]; ok {
				resp.Data[i].Attributes.Tags = append(resp.Data[i].Attributes.Tags, name)
			}
		}
	}
}

// mapTagsToSites resolves tag names from the included array and populates each
// site's Tags slice.
func mapTagsToSites(resp *ForgeSitesResponse) {
	tagMap := buildTagMap(resp.Included)
	for i := range resp.Data {
		for _, ref := range resp.Data[i].Relationships.Tags.Data {
			if name, ok := tagMap[ref.ID]; ok {
				resp.Data[i].Attributes.Tags = append(resp.Data[i].Attributes.Tags, name)
			}
		}
	}
}

// buildTagMap builds a map of tag ID -> tag name from a JSON:API included array.
func buildTagMap(included []any) map[string]string {
	tagMap := make(map[string]string, len(included))
	for _, item := range included {
		m, ok := item.(map[string]any)
		if !ok || m["type"] != "tags" {
			continue
		}
		id, _ := m["id"].(string)
		attrs, _ := m["attributes"].(map[string]any)
		name, _ := attrs["name"].(string)
		if id != "" && name != "" {
			tagMap[id] = name
		}
	}
	return tagMap
}
