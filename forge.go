// Package traefik_laravel_forge provides a Traefik provider plugin for Laravel Forge.
package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/traefik/genconf/dynamic"
	"github.com/traefik/genconf/dynamic/tls"
)

// ServerMapping maps a Forge server to an upstream backend.
type ServerMapping struct {
	ForgeServerName string `json:"forgeServerName,omitempty"`
	UpstreamHost    string `json:"upstreamHost,omitempty"` // Optional: auto-detected from Forge if not set
	UpstreamPort    int    `json:"upstreamPort,omitempty"` // Optional: default 80
	Traefik         string `json:"traefik,omitempty"`
}

// Config the plugin configuration.
type Config struct {
	APIToken            string          `json:"apiToken,omitempty"`
	Organization        string          `json:"organization,omitempty"`
	PollInterval        string          `json:"pollInterval,omitempty"`
	DefaultCertResolver string          `json:"defaultCertResolver,omitempty"` // Default cert resolver for TLS
	DefaultSitesEnabled bool            `json:"defaultSitesEnabled,omitempty"` // Default: true - whether sites are enabled by default
	HTTPRedirect        bool            `json:"httpRedirect,omitempty"`        // Create HTTP->HTTPS redirect routers
	RedirectMiddleware  string          `json:"redirectMiddleware,omitempty"`  // Name of redirect middleware to use
	TraefikID           string          `json:"traefikID,omitempty"`           // Only process servers tagged traefik:traefik-id=<this value>
	ServerMappings      []ServerMapping `json:"serverMappings,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		PollInterval:        "30s",
		DefaultSitesEnabled: true, // Sites enabled by default
		ServerMappings:      []ServerMapping{},
	}
}

// ForgeTagRef is a JSON:API relationship reference to a tag resource.
type ForgeTagRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ForgeRelationships holds the relationships block returned by the API.
type ForgeRelationships struct {
	Tags struct {
		Data []ForgeTagRef `json:"data"`
	} `json:"tags"`
}

// ForgeServer represents a server from the Forge API v2 (JSON:API format).
type ForgeServer struct {
	ID            string               `json:"id"`
	Type          string               `json:"type"`
	Attributes    ForgeServerAttributes `json:"attributes"`
	Relationships ForgeRelationships   `json:"relationships"`
}

// ForgeServerAttributes contains the server attributes.
type ForgeServerAttributes struct {
	Name             string   `json:"name"`
	IPAddress        string   `json:"ip_address"`
	PrivateIPAddress string   `json:"private_ip_address"`
	Provider         string   `json:"provider"`
	Region           string   `json:"region"`
	Tags             []string `json:"-"` // Populated from relationships+included after decode
}

// ForgeSite represents a site from the Forge API v2 (JSON:API format).
type ForgeSite struct {
	ID            string             `json:"id"`
	Type          string             `json:"type"`
	Attributes    ForgeSiteAttributes `json:"attributes"`
	Relationships ForgeRelationships `json:"relationships"`
}

// ForgeSiteAttributes contains the site attributes.
type ForgeSiteAttributes struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	URL    string   `json:"url"`
	Tags   []string `json:"-"` // Populated from relationships+included after decode
}

// ForgeReverbIntegration represents the Reverb integration config for a site.
type ForgeReverbIntegration struct {
	Enabled bool    `json:"enabled"`
	Host    string  `json:"host"`
	Port    int     `json:"port"`
}

// ForgeReverbResponse is the response from /integrations/reverb.
type ForgeReverbResponse struct {
	Data struct {
		Attributes ForgeReverbIntegration `json:"attributes"`
	} `json:"data"`
}

// ForgeDomain represents a domain record attached to a Forge site.
// The /domains endpoint returns these (type: "domainRecords").
type ForgeDomain struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	Attributes ForgeDomainAttributes `json:"attributes"`
}

// ForgeDomainAttributes holds domain record details.
// DomainType is one of: "primary", "alias", "reverb".
type ForgeDomainAttributes struct {
	Name                  string `json:"name"`
	DomainType            string `json:"type"`
	Status                string `json:"status"`
	AllowWildcardSubdomains bool  `json:"allow_wildcard_subdomains"`
}

// ForgeDomainsResponse is the JSON:API response from the /domains endpoint.
type ForgeDomainsResponse struct {
	Data  []ForgeDomain `json:"data"`
	Links interface{}   `json:"links"`
	Meta  interface{}   `json:"meta"`
}

// ForgeServersResponse represents the JSON:API response from listing servers.
type ForgeServersResponse struct {
	Data     []ForgeServer `json:"data"`
	Included []interface{} `json:"included,omitempty"`
	Links    interface{}   `json:"links"`
	Meta     interface{}   `json:"meta"`
}

// ForgeSitesResponse represents the JSON:API response from listing sites.
type ForgeSitesResponse struct {
	Data     []ForgeSite   `json:"data"`
	Included []interface{} `json:"included,omitempty"`
	Links    interface{}   `json:"links"`
	Meta     interface{}   `json:"meta"`
}

// Provider a Laravel Forge provider plugin.
type Provider struct {
	name                string
	apiToken            string
	organization        string
	pollInterval        time.Duration
	defaultCertResolver string
	defaultSitesEnabled bool
	httpRedirect        bool
	redirectMiddleware  string
	traefikID           string
	serverMappings      []ServerMapping
	httpClient          *http.Client

	cancel func()
}

// New creates a new Provider plugin.
func New(_ context.Context, config *Config, name string) (*Provider, error) {
	if config.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	if config.Organization == "" {
		return nil, fmt.Errorf("organization is required")
	}

	pi, err := time.ParseDuration(config.PollInterval)
	if err != nil {
		return nil, err
	}

	return &Provider{
		name:                name,
		apiToken:            config.APIToken,
		organization:        config.Organization,
		pollInterval:        pi,
		defaultCertResolver: config.DefaultCertResolver,
		defaultSitesEnabled: config.DefaultSitesEnabled,
		httpRedirect:        config.HTTPRedirect,
		redirectMiddleware:  config.RedirectMiddleware,
		traefikID:           config.TraefikID,
		serverMappings:      config.ServerMappings,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Init the provider.
func (p *Provider) Init() error {
	if p.pollInterval <= 0 {
		return fmt.Errorf("poll interval must be greater than 0")
	}

	// Enforce minimum poll interval of 10 seconds
	minInterval := 10 * time.Second
	if p.pollInterval < minInterval {
		return fmt.Errorf("poll interval must be at least 10s, got %s", p.pollInterval)
	}

	return nil
}

// Provide creates and send dynamic configuration.
func (p *Provider) Provide(cfgChan chan<- json.Marshaler) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Print(err)
			}
		}()

		p.loadConfiguration(ctx, cfgChan)
	}()

	return nil
}

func (p *Provider) loadConfiguration(ctx context.Context, cfgChan chan<- json.Marshaler) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Send initial configuration immediately
	p.sendConfiguration(cfgChan)

	for {
		select {
		case <-ticker.C:
			p.sendConfiguration(cfgChan)

		case <-ctx.Done():
			return
		}
	}
}

func (p *Provider) sendConfiguration(cfgChan chan<- json.Marshaler) {
	configuration, err := p.generateConfiguration()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating configuration: %v\n", err)
		return
	}

	cfgChan <- &dynamic.JSONPayload{Configuration: configuration}
}

// fetchForgeServers retrieves all servers from the Forge API v2.
func (p *Provider) fetchForgeServers() ([]ForgeServer, error) {
	servers, _, err := p.fetchForgeServersRaw()
	return servers, err
}

func (p *Provider) fetchForgeServersRaw() ([]ForgeServer, json.RawMessage, error) {
	url := fmt.Sprintf("https://forge.laravel.com/api/orgs/%s/servers?include=tags", p.organization)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
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

	p.mapTagsToServers(&serversResp)

	return serversResp.Data, json.RawMessage(raw), nil
}

// mapTagsToServers resolves tag names from the included array and populates each server's Tags slice.
func (p *Provider) mapTagsToServers(resp *ForgeServersResponse) {
	tagMap := buildTagMap(resp.Included)
	for i := range resp.Data {
		for _, ref := range resp.Data[i].Relationships.Tags.Data {
			if name, ok := tagMap[ref.ID]; ok {
				resp.Data[i].Attributes.Tags = append(resp.Data[i].Attributes.Tags, name)
			}
		}
	}
}

// fetchForgeSites retrieves all sites for a specific server from the Forge API v2.
func (p *Provider) fetchForgeSites(serverID string) ([]ForgeSite, error) {
	sites, _, err := p.fetchForgeSitesRaw(serverID)
	return sites, err
}

func (p *Provider) fetchForgeSitesRaw(serverID string) ([]ForgeSite, json.RawMessage, error) {
	url := fmt.Sprintf("https://forge.laravel.com/api/orgs/%s/servers/%s/sites?include=tags", p.organization, serverID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
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

	p.mapTagsToSites(&sitesResp)

	return sitesResp.Data, json.RawMessage(raw), nil
}

// mapTagsToSites resolves tag names from the included array and populates each site's Tags slice.
func (p *Provider) mapTagsToSites(resp *ForgeSitesResponse) {
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
func buildTagMap(included []interface{}) map[string]string {
	tagMap := make(map[string]string)
	for _, item := range included {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] != "tags" {
			continue
		}
		id, _ := m["id"].(string)
		attrs, _ := m["attributes"].(map[string]interface{})
		name, _ := attrs["name"].(string)
		if id != "" && name != "" {
			tagMap[id] = name
		}
	}
	return tagMap
}

// fetchForgeDomains retrieves all domain records for a site from the /domains endpoint.
func (p *Provider) fetchForgeDomains(serverID, siteID string) ([]ForgeDomain, error) {
	url := fmt.Sprintf("https://forge.laravel.com/api/orgs/%s/servers/%s/sites/%s/domains", p.organization, serverID, siteID)
	raw, err := p.fetchRaw(url)
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

// fetchReverbIntegration retrieves the Reverb integration config for a site.
// Returns nil if Reverb is not enabled or the endpoint is unavailable.
func (p *Provider) fetchReverbIntegration(serverID, siteID string) (*ForgeReverbIntegration, error) {
	url := fmt.Sprintf("https://forge.laravel.com/api/orgs/%s/servers/%s/sites/%s/integrations/reverb", p.organization, serverID, siteID)
	raw, err := p.fetchRaw(url)
	if err != nil || raw == nil {
		return nil, err
	}
	var resp ForgeReverbResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode reverb response: %w", err)
	}
	if !resp.Data.Attributes.Enabled || resp.Data.Attributes.Host == "" || resp.Data.Attributes.Port == 0 {
		return nil, nil
	}
	return &resp.Data.Attributes, nil
}

// ParseTagConfig parses a tag for configuration directives.
// Format: "traefik:key=value" or "traefik:flag"
// Returns key, value, and whether it's a traefik tag.
func ParseTagConfig(tag string) (string, string, bool) {
	const prefix = "traefik:"
	if !strings.HasPrefix(tag, prefix) {
		return "", "", false
	}

	content := strings.TrimPrefix(tag, prefix)
	if content == "" {
		return "", "", false
	}

	// Check for key=value format
	if idx := strings.Index(content, "="); idx > 0 {
		key := strings.TrimSpace(content[:idx])
		value := strings.TrimSpace(content[idx+1:])
		return key, value, true
	}

	// Just a flag
	return strings.TrimSpace(content), "true", true
}

// ServerConfig holds configuration parsed from server tags.
type ServerConfig struct {
	UpstreamHost string
	UpstreamPort int
	TraefikID    string
}

// ParseServerTags parses server tags to extract configuration (exported for testing).
func ParseServerTags(tags []string) ServerConfig {
	cfg := ServerConfig{
		UpstreamPort: 80, // Default port
	}

	for _, tag := range tags {
		key, value, isTraefikTag := ParseTagConfig(tag)
		if !isTraefikTag {
			continue
		}

		switch key {
		case "upstream-host", "upstreamhost":
			cfg.UpstreamHost = value
		case "upstream-port", "upstreamport":
			var port int
			if n, err := fmt.Sscanf(value, "%d", &port); err == nil && n == 1 {
				cfg.UpstreamPort = port
			}
		case "traefik", "traefik-id":
			cfg.TraefikID = value
		// Keep old aliases for backward compatibility
		case "lb-host", "loadbalancer-host":
			cfg.UpstreamHost = value
		case "lb-port", "loadbalancer-port":
			var port int
			if n, err := fmt.Sscanf(value, "%d", &port); err == nil && n == 1 {
				cfg.UpstreamPort = port
			}
		}
	}

	return cfg
}

// Stop to stop the provider and the related go routines.
func (p *Provider) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

// generateConfiguration fetches data from Forge and generates Traefik configuration.
func (p *Provider) generateConfiguration() (*dynamic.Configuration, error) {
	configuration := &dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{
			Routers:           make(map[string]*dynamic.Router),
			Middlewares:       make(map[string]*dynamic.Middleware),
			Services:          make(map[string]*dynamic.Service),
			ServersTransports: make(map[string]*dynamic.ServersTransport),
		},
		TCP: &dynamic.TCPConfiguration{
			Routers:  make(map[string]*dynamic.TCPRouter),
			Services: make(map[string]*dynamic.TCPService),
		},
		TLS: &dynamic.TLSConfiguration{
			Stores:  make(map[string]tls.Store),
			Options: make(map[string]tls.Options),
		},
		UDP: &dynamic.UDPConfiguration{
			Routers:  make(map[string]*dynamic.UDPRouter),
			Services: make(map[string]*dynamic.UDPService),
		},
	}

	// If httpRedirect is enabled without an explicit middleware name, create one.
	// This makes common.toml unnecessary — the plugin is self-contained.
	redirectMiddlewareName := p.redirectMiddleware
	if p.httpRedirect && redirectMiddlewareName == "" {
		redirectMiddlewareName = "forge-https-redirect"
		configuration.HTTP.Middlewares[redirectMiddlewareName] = &dynamic.Middleware{
			RedirectScheme: &dynamic.RedirectScheme{
				Scheme:    "https",
				Permanent: true,
			},
		}
	}

	// Fetch all servers from Forge
	servers, err := p.fetchForgeServers()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch servers: %w", err)
	}

	fmt.Printf("Fetched %d servers from Forge\n", len(servers))

	// Process each server and its sites
	for _, server := range servers {
		// Parse tags first (highest priority)
		tagConfig := ParseServerTags(server.Attributes.Tags)

		// If traefikID filtering is active, skip servers that don't carry a matching tag.
		// Servers with no traefik:traefik-id tag are also skipped — in multi-LB setups
		// every server should be explicitly assigned.
		if p.traefikID != "" && tagConfig.TraefikID != p.traefikID {
			fmt.Printf("Server '%s' skipped (traefik-id=%q, want %q)\n", server.Attributes.Name, tagConfig.TraefikID, p.traefikID)
			continue
		}

		// Find the mapping for this server (fallback to config file)
		var mapping *ServerMapping
		for i := range p.serverMappings {
			if p.serverMappings[i].ForgeServerName == server.Attributes.Name {
				mapping = &p.serverMappings[i]
				break
			}
		}

		// Determine configuration source: tags > mapping > none
		// If no configuration exists, server will be processed but might have no enabled sites
		var upstreamHost string
		upstreamPort := 80 // default
		var configSource string
		var hasConfig bool

		// Priority 1: Tags
		if tagConfig.UpstreamHost != "" {
			upstreamHost = tagConfig.UpstreamHost
			upstreamPort = tagConfig.UpstreamPort
			configSource = "tags"
			hasConfig = true
		} else if mapping != nil {
			// Priority 2: Config file mapping
			upstreamHost = mapping.UpstreamHost
			upstreamPort = mapping.UpstreamPort
			if upstreamPort == 0 {
				upstreamPort = 80
			}
			configSource = "config mapping"
			hasConfig = true
		}

		// If no explicit config, we can still process if sites are explicitly enabled
		// This allows for "traefik:enabled=true" on a site to pull in the server automatically
		if !hasConfig {
			fmt.Printf("No explicit configuration for server '%s', will check for enabled sites\n", server.Attributes.Name)
		}

		// Fetch sites for this server first to see if any are enabled
		sites, err := p.fetchForgeSites(server.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to fetch sites for server %s: %v\n", server.Attributes.Name, err)
			continue
		}

		fmt.Printf("Found %d sites on server '%s'\n", len(sites), server.Attributes.Name)

		// Count how many sites will actually be processed
		enabledSitesCount := 0
		for _, site := range sites {
			if site.Attributes.Status != "installed" {
				continue
			}
			// Check if site would be enabled
			siteEnabled := p.defaultSitesEnabled
			for _, tag := range site.Attributes.Tags {
				key, value, isTraefikTag := ParseTagConfig(tag)
				if isTraefikTag && key == "enabled" {
					siteEnabled = value == "true"
					break
				}
			}
			if siteEnabled {
				enabledSitesCount++
			}
		}

		// Skip server if no enabled sites
		if enabledSitesCount == 0 {
			fmt.Printf("Server '%s' has no enabled sites, skipping\n", server.Attributes.Name)
			continue
		}

		// Now we know we need this server - determine backend config
		// Auto-detect IP if not explicitly set
		if upstreamHost == "" {
			// Auto-detect from Forge: prefer private IP, fallback to public IP
			switch {
			case server.Attributes.PrivateIPAddress != "":
				upstreamHost = server.Attributes.PrivateIPAddress
				fmt.Printf("Auto-detected private IP for server '%s': %s\n", server.Attributes.Name, upstreamHost)
			case server.Attributes.IPAddress != "":
				upstreamHost = server.Attributes.IPAddress
				fmt.Printf("Using public IP for server '%s': %s\n", server.Attributes.Name, upstreamHost)
			default:
				fmt.Fprintf(os.Stderr, "No IP address found for server '%s' (has %d enabled sites), skipping\n", server.Attributes.Name, enabledSitesCount)
				continue
			}
		}

		backendURL := fmt.Sprintf("http://%s:%d", upstreamHost, upstreamPort)

		if hasConfig {
			fmt.Printf("Processing server '%s' (ID: %s) -> %s (via %s, %d enabled sites)\n", server.Attributes.Name, server.ID, backendURL, configSource, enabledSitesCount)
		} else {
			fmt.Printf("Processing server '%s' (ID: %s) -> %s (auto-detected, %d enabled sites)\n", server.Attributes.Name, server.ID, backendURL, enabledSitesCount)
		}

		// Create router and service for each site
		for _, site := range sites {
			// Skip sites that aren't installed yet
			if site.Attributes.Status != "installed" {
				fmt.Printf("Site '%s' status is '%s', skipping\n", site.Attributes.Name, site.Attributes.Status)
				continue
			}

			routerName := fmt.Sprintf("forge-%s-%s", server.Attributes.Name, site.ID)
			serviceName := fmt.Sprintf("forge-%s-%s-service", server.Attributes.Name, site.ID)

			// Parse site tags for configuration
			certResolver := p.defaultCertResolver
			enableTLS := p.defaultCertResolver != ""
			siteEnabled := p.defaultSitesEnabled
			sitePort := upstreamPort
			httpRedirect := p.httpRedirect
			var entryPoints []string
			var tagAliases []string      // extra hosts from traefik:aliases= tag
			var tagMiddlewares []string  // extra middlewares from traefik:middlewares= tag
			reverbPortOverride := 0      // traefik:reverb-port= tag

			for _, tag := range site.Attributes.Tags {
				key, value, isTraefikTag := ParseTagConfig(tag)
				if !isTraefikTag {
					continue
				}

				switch key {
				case "enabled":
					siteEnabled = value == "true"
					fmt.Printf("Site '%s' explicitly %s via tag\n", site.Attributes.Name, map[bool]string{true: "enabled", false: "disabled"}[siteEnabled])
				case "cert-resolver", "certresolver":
					certResolver = value
					enableTLS = true
					fmt.Printf("Site '%s' using cert resolver from tag: %s\n", site.Attributes.Name, value)
				case "tls":
					enableTLS = value == "true"
					fmt.Printf("Site '%s' TLS %s via tag\n", site.Attributes.Name, map[bool]string{true: "enabled", false: "disabled"}[enableTLS])
				case "port":
					if n, err := fmt.Sscanf(value, "%d", &sitePort); err == nil && n == 1 {
						fmt.Printf("Site '%s' using port %d from tag\n", site.Attributes.Name, sitePort)
					}
				case "http-redirect", "redirect":
					httpRedirect = value == "true"
					fmt.Printf("Site '%s' HTTP redirect %s via tag\n", site.Attributes.Name, map[bool]string{true: "enabled", false: "disabled"}[httpRedirect])
				case "entrypoints", "entry-points":
					entryPoints = strings.Split(value, ",")
					for i := range entryPoints {
						entryPoints[i] = strings.TrimSpace(entryPoints[i])
					}
					fmt.Printf("Site '%s' using custom entry points: %v\n", site.Attributes.Name, entryPoints)
				case "aliases":
					for _, a := range strings.Split(value, ",") {
						if a = strings.TrimSpace(a); a != "" {
							tagAliases = append(tagAliases, a)
						}
					}
					fmt.Printf("Site '%s' tag aliases: %v\n", site.Attributes.Name, tagAliases)
				case "reverb-port":
					if n, err := fmt.Sscanf(value, "%d", &reverbPortOverride); err == nil && n == 1 {
						fmt.Printf("Site '%s' reverb port overridden to %d via tag\n", site.Attributes.Name, reverbPortOverride)
					}
				case "middlewares", "middleware":
					for _, m := range strings.Split(value, ",") {
						if m = strings.TrimSpace(m); m != "" {
							tagMiddlewares = append(tagMiddlewares, m)
						}
					}
					fmt.Printf("Site '%s' extra middlewares: %v\n", site.Attributes.Name, tagMiddlewares)
				}
			}

			// Skip if site is disabled
			if !siteEnabled {
				fmt.Printf("Site '%s' disabled, skipping\n", site.Attributes.Name)
				continue
			}

			// Fetch domain records and reverb integration in parallel context.
			domains, err := p.fetchForgeDomains(server.ID, site.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to fetch domains for site '%s': %v, falling back to site name\n", site.Attributes.Name, err)
			}

			reverb, err := p.fetchReverbIntegration(server.ID, site.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to fetch reverb integration for site '%s': %v\n", site.Attributes.Name, err)
			}

			// reverbHost is the authoritative reverb hostname from the integration.
			// Domains matching this host are routed to the reverb port instead of the main backend.
			reverbHost := ""
			reverbPort := 0
			if reverb != nil {
				reverbHost = reverb.Host
				reverbPort = reverb.Port
			}
			if reverbPortOverride > 0 {
				reverbPort = reverbPortOverride
			}

			// Separate domain records into main hosts and reverb host.
			// Track wildcard-enabled domains separately for HostRegexp rules.
			var mainHosts []string
			var wildcardHosts []string // domains with allow_wildcard_subdomains=true
			var reverbHosts []string

			for _, d := range domains {
				if d.Attributes.Status != "enabled" {
					continue
				}
				if reverbHost != "" && d.Attributes.Name == reverbHost {
					reverbHosts = append(reverbHosts, d.Attributes.Name)
				} else {
					mainHosts = append(mainHosts, d.Attributes.Name)
					if d.Attributes.AllowWildcardSubdomains {
						wildcardHosts = append(wildcardHosts, d.Attributes.Name)
					}
				}
			}

			// If no domain records came back, fall back to the Forge site name.
			if len(mainHosts) == 0 {
				mainHosts = []string{site.Attributes.Name}
			}

			// Append any hosts from traefik:aliases= tag not already present.
			existing := make(map[string]bool)
			for _, h := range mainHosts {
				existing[h] = true
			}
			for _, a := range tagAliases {
				if !existing[a] {
					mainHosts = append(mainHosts, a)
				}
			}

			// Build backend URL with site-specific port
			siteBackendURL := fmt.Sprintf("http://%s:%d", upstreamHost, sitePort)

			// Determine entry points
			if len(entryPoints) == 0 {
				if enableTLS {
					entryPoints = []string{"websecure"}
				} else {
					entryPoints = []string{"web"}
				}
			}

			// Build Host() rule from all main hosts, adding HostRegexp for wildcard domains
			hostRule := buildHostRule(mainHosts, wildcardHosts)

			// Create the main router
			router := &dynamic.Router{
				EntryPoints: entryPoints,
				Service:     serviceName,
				Rule:        hostRule,
				Middlewares: tagMiddlewares,
			}
			if enableTLS {
				router.TLS = &dynamic.RouterTLSConfig{}
				if certResolver != "" {
					router.TLS.CertResolver = certResolver
				}
			}
			configuration.HTTP.Routers[routerName] = router

			// Create HTTP redirect router if needed
			if httpRedirect && enableTLS {
				httpRouterName := fmt.Sprintf("%s-http", routerName)
				httpRouter := &dynamic.Router{
					EntryPoints: []string{"web"},
					Service:     serviceName,
					Rule:        hostRule,
				}
				if redirectMiddlewareName != "" {
					httpRouter.Middlewares = []string{redirectMiddlewareName}
				}
				configuration.HTTP.Routers[httpRouterName] = httpRouter
			}

			// Create the service
			configuration.HTTP.Services[serviceName] = &dynamic.Service{
				LoadBalancer: &dynamic.ServersLoadBalancer{
					Servers:        []dynamic.Server{{URL: siteBackendURL}},
					PassHostHeader: boolPtr(true),
				},
			}

			tlsInfo := "no TLS"
			if enableTLS {
				if certResolver != "" {
					tlsInfo = fmt.Sprintf("TLS with %s", certResolver)
				} else {
					tlsInfo = "TLS enabled"
				}
				if httpRedirect {
					tlsInfo += " + HTTP redirect"
				}
			}
			fmt.Printf("Created router for site '%s' hosts=%v -> %s (%s)\n", site.Attributes.Name, mainHosts, siteBackendURL, tlsInfo)

			// Create separate routers for Reverb (WebSocket) domains
			if len(reverbHosts) > 0 && reverbPort > 0 {
				reverbServiceName := fmt.Sprintf("%s-reverb-service", routerName)
				reverbBackendURL := fmt.Sprintf("http://%s:%d", upstreamHost, reverbPort)
				reverbRule := buildHostRule(reverbHosts, nil)

				reverbRouter := &dynamic.Router{
					EntryPoints: entryPoints,
					Service:     reverbServiceName,
					Rule:        reverbRule,
					Middlewares: tagMiddlewares,
				}
				if enableTLS {
					reverbRouter.TLS = &dynamic.RouterTLSConfig{}
					if certResolver != "" {
						reverbRouter.TLS.CertResolver = certResolver
					}
				}
				configuration.HTTP.Routers[fmt.Sprintf("%s-reverb", routerName)] = reverbRouter

				if httpRedirect && enableTLS {
					reverbHTTPRouter := &dynamic.Router{
						EntryPoints: []string{"web"},
						Service:     reverbServiceName,
						Rule:         reverbRule,
					}
					if redirectMiddlewareName != "" {
						reverbHTTPRouter.Middlewares = []string{redirectMiddlewareName}
					}
					configuration.HTTP.Routers[fmt.Sprintf("%s-reverb-http", routerName)] = reverbHTTPRouter
				}

				configuration.HTTP.Services[reverbServiceName] = &dynamic.Service{
					LoadBalancer: &dynamic.ServersLoadBalancer{
						Servers:        []dynamic.Server{{URL: reverbBackendURL}},
						PassHostHeader: boolPtr(true),
					},
				}
				fmt.Printf("Created Reverb router for site '%s' hosts=%v -> %s\n", site.Attributes.Name, reverbHosts, reverbBackendURL)
			}
		}
	}

	return configuration, nil
}

// GenerateConfiguration is the exported entry point for previewing what the plugin would produce.
// Useful for verification and testing without running Traefik.
func (p *Provider) GenerateConfiguration() (*dynamic.Configuration, error) {
	return p.generateConfiguration()
}

// DumpRaw fetches raw JSON from the Forge API for all servers, their sites,
// individual site details, and any aliases/custom-domain endpoints.
// Useful for inspecting the actual API response shape during development.
func (p *Provider) DumpRaw() ([]byte, error) {
	servers, rawServers, err := p.fetchForgeServersRaw()
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
		sites, raw, err := p.fetchForgeSitesRaw(server.ID)
		if err != nil {
			return nil, fmt.Errorf("sites for server %s: %w", server.Attributes.Name, err)
		}

		dump := siteDump{
			ServerID:   server.ID,
			ServerName: server.Attributes.Name,
			SitesList:  raw,
		}

		for _, site := range sites {
			base := fmt.Sprintf("https://forge.laravel.com/api/orgs/%s/servers/%s/sites/%s", p.organization, server.ID, site.ID)

			if d, _ := p.fetchRaw(base + "/domains"); d != nil {
				dump.Domains = append(dump.Domains, d)
			}

			// Fetch Reverb integration config.
			if r, _ := p.fetchRaw(base + "/integrations/reverb"); r != nil {
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

// fetchRaw performs a GET request and returns the raw response body.
// Returns nil, nil for non-200 responses (used for probing optional endpoints).
func (p *Provider) fetchRaw(url string) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
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

// buildHostRule builds a Traefik v3 routing rule from a list of exact hosts and
// a list of wildcard-enabled hosts. Wildcard hosts get an additional HostRegexp
// clause matching any single-level subdomain (e.g. app.bounceiq.com).
func buildHostRule(hosts []string, wildcardHosts []string) string {
	parts := make([]string, 0, len(hosts)+len(wildcardHosts))
	for _, h := range hosts {
		parts = append(parts, fmt.Sprintf("Host(`%s`)", h))
	}
	for _, h := range wildcardHosts {
		// Escape dots for use in a Go regex, then match any single-level subdomain.
		escaped := strings.ReplaceAll(h, ".", `\.`)
		parts = append(parts, fmt.Sprintf("HostRegexp(`^[^.]+\\.%s$`)", escaped))
	}
	return strings.Join(parts, " || ")
}

func boolPtr(v bool) *bool {
	return &v
}
