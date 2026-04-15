// Package traefik_laravel_forge provides a Traefik provider plugin for Laravel Forge.
package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/traefik/genconf/dynamic"
	"github.com/traefik/genconf/dynamic/tls"
)

// ServerMapping maps a Forge server to an upstream backend.
type ServerMapping struct {
	ForgeServerName string `json:"forgeServerName,omitempty"`
	UpstreamHost    string `json:"upstreamHost,omitempty"`
	UpstreamPort    int    `json:"upstreamPort,omitempty"`
}

// Config is the plugin configuration.
type Config struct {
	APIToken            string          `json:"apiToken,omitempty"`
	Organization        string          `json:"organization,omitempty"`
	PollInterval        string          `json:"pollInterval,omitempty"`
	DefaultCertResolver string          `json:"defaultCertResolver,omitempty"`
	DefaultSitesEnabled bool            `json:"defaultSitesEnabled,omitempty"`
	HTTPRedirect        bool            `json:"httpRedirect,omitempty"`
	RedirectMiddleware  string          `json:"redirectMiddleware,omitempty"`
	TraefikID           string          `json:"traefikID,omitempty"`
	ServerMappings      []ServerMapping `json:"serverMappings,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		PollInterval:        "30s",
		DefaultSitesEnabled: true,
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
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

// ForgeDomain represents a domain record attached to a Forge site.
type ForgeDomain struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"`
	Attributes ForgeDomainAttributes `json:"attributes"`
}

// ForgeDomainAttributes holds domain record details.
// DomainType is one of: "primary", "alias", "reverb".
type ForgeDomainAttributes struct {
	Name                    string `json:"name"`
	DomainType              string `json:"type"`
	Status                  string `json:"status"`
	AllowWildcardSubdomains bool   `json:"allow_wildcard_subdomains"`
	WWWRedirectType         string `json:"www_redirect_type"` // "none", "from-www", or "to-www"
}

// ForgeClient abstracts all Forge API calls. Extracted as an interface to allow
// testing generateConfiguration without live HTTP calls.
//
//go:generate mockery --name ForgeClient --filename mock_forge_client.go --outpkg mocks --dir mocks
type ForgeClient interface {
	FetchServers() ([]ForgeServer, error)
	FetchSites(serverID string) ([]ForgeSite, error)
	FetchDomains(serverID, siteID string) ([]ForgeDomain, error)
	FetchReverbIntegration(serverID, siteID string) (*ForgeReverbIntegration, error)
}

// Provider is the Laravel Forge provider plugin.
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
	client              ForgeClient

	cancel func()
}

// NewProviderWithClient creates a Provider from the given config but uses client
// for all Forge API calls instead of the default HTTP client. Unlike New, it
// does not require APIToken or Organization since the supplied client handles
// all API communication. Intended for integration tests and tooling.
func NewProviderWithClient(config *Config, client ForgeClient) (*Provider, error) {
	pi, err := time.ParseDuration(config.PollInterval)
	if err != nil {
		return nil, err
	}
	return &Provider{
		name:                "custom",
		pollInterval:        pi,
		defaultCertResolver: config.DefaultCertResolver,
		defaultSitesEnabled: config.DefaultSitesEnabled,
		httpRedirect:        config.HTTPRedirect,
		redirectMiddleware:  config.RedirectMiddleware,
		traefikID:           config.TraefikID,
		serverMappings:      config.ServerMappings,
		client:              client,
	}, nil
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
		client:              newForgeHTTPClient(config.APIToken, config.Organization),
	}, nil
}

// Init validates the provider configuration.
func (p *Provider) Init() error {
	if p.pollInterval <= 0 {
		return fmt.Errorf("poll interval must be greater than 0")
	}
	if p.pollInterval < 10*time.Second {
		return fmt.Errorf("poll interval must be at least 10s, got %s", p.pollInterval)
	}
	return nil
}

// Provide starts emitting dynamic configuration.
func (p *Provider) Provide(cfgChan chan<- json.Marshaler) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Print(r)
			}
		}()
		p.loadConfiguration(ctx, cfgChan)
	}()

	return nil
}

// Stop halts the provider and its background goroutine.
func (p *Provider) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

func (p *Provider) loadConfiguration(ctx context.Context, cfgChan chan<- json.Marshaler) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

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
		log.Printf("Error generating configuration: %v", err)
		return
	}
	cfgChan <- &dynamic.JSONPayload{Configuration: configuration}
}

// GenerateConfiguration is the exported entry point used by the verify tool.
func (p *Provider) GenerateConfiguration() (*dynamic.Configuration, error) {
	return p.generateConfiguration()
}

// DumpRaw fetches and returns raw Forge API responses for debugging.
// Only available when using the default HTTP client.
func (p *Provider) DumpRaw() ([]byte, error) {
	c, ok := p.client.(*forgeHTTPClient)
	if !ok {
		return nil, fmt.Errorf("DumpRaw is only supported with the HTTP Forge client")
	}
	return c.dumpRaw()
}

// generateConfiguration fetches data from Forge via the client and builds Traefik config.
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

	// Auto-create the redirect middleware when httpRedirect is on and no external one is named.
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

	servers, err := p.client.FetchServers()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch servers: %w", err)
	}

	log.Printf("Fetched %d servers from Forge", len(servers))

	for _, server := range servers {
		p.processServer(server, configuration, redirectMiddlewareName)
	}

	return configuration, nil
}

func (p *Provider) processServer(
	server ForgeServer,
	configuration *dynamic.Configuration,
	redirectMiddlewareName string,
) {
	tagConfig := ParseServerTags(server.Attributes.Tags)

	// In multi-LB mode, skip servers not assigned to this instance.
	if p.traefikID != "" && tagConfig.TraefikID != p.traefikID {
		log.Printf("Server %q skipped (traefik-id=%q, want %q)",
			server.Attributes.Name, tagConfig.TraefikID, p.traefikID)
		return
	}

	// Look up an explicit server mapping from plugin config (lowest priority override).
	var mapping *ServerMapping
	for i := range p.serverMappings {
		if p.serverMappings[i].ForgeServerName == server.Attributes.Name {
			mapping = &p.serverMappings[i]
			break
		}
	}

	// Resolve upstream host: tags > mapping > auto-detect from Forge.
	upstreamHost, upstreamPort := p.resolveUpstream(server, tagConfig, mapping)
	if upstreamHost == "" {
		// Checked again after site filtering; log deferred to resolveUpstream.
		log.Printf("Server %q has no upstream host — will skip if any sites need routing",
			server.Attributes.Name)
	}

	sites, err := p.client.FetchSites(server.ID)
	if err != nil {
		log.Printf("Failed to fetch sites for server %q: %v", server.Attributes.Name, err)
		return
	}

	log.Printf("Found %d sites on server %q", len(sites), server.Attributes.Name)

	// Skip the server entirely if none of its sites are enabled.
	if !hasEnabledSites(sites, p.defaultSitesEnabled) {
		log.Printf("Server %q has no enabled sites, skipping", server.Attributes.Name)
		return
	}

	if upstreamHost == "" {
		log.Printf("Server %q has enabled sites but no IP address — skipping", server.Attributes.Name)
		return
	}

	log.Printf("Processing server %q -> http://%s:%d", server.Attributes.Name, upstreamHost, upstreamPort)

	for _, site := range sites {
		p.processSite(site, server.ID, upstreamHost, upstreamPort, configuration, redirectMiddlewareName)
	}
}

// resolveUpstream determines the upstream host and port for a server using the
// priority chain: server tag > server mapping > auto-detected Forge IP.
// Returns empty string for host if no IP is available.
func (p *Provider) resolveUpstream(
	server ForgeServer,
	tagConfig ServerConfig,
	mapping *ServerMapping,
) (host string, port int) {
	port = 80

	switch {
	case tagConfig.UpstreamHost != "":
		return tagConfig.UpstreamHost, tagConfig.UpstreamPort
	case mapping != nil:
		mp := mapping.UpstreamPort
		if mp == 0 {
			mp = 80
		}
		return mapping.UpstreamHost, mp
	case server.Attributes.PrivateIPAddress != "":
		return server.Attributes.PrivateIPAddress, port
	case server.Attributes.IPAddress != "":
		return server.Attributes.IPAddress, port
	default:
		return "", port
	}
}

// hasEnabledSites reports whether any installed site in the list would be enabled
// given the default sites-enabled policy.
func hasEnabledSites(sites []ForgeSite, defaultEnabled bool) bool {
	for _, site := range sites {
		if site.Attributes.Status != "installed" {
			continue
		}
		if isSiteEnabled(site.Attributes.Tags, defaultEnabled) {
			return true
		}
	}
	return false
}

// isSiteEnabled returns the effective enabled state for a site, considering
// the default policy and any traefik:enabled tag override.
func isSiteEnabled(tags []string, defaultEnabled bool) bool {
	for _, tag := range tags {
		key, value, ok := ParseTagConfig(tag)
		if ok && key == "enabled" {
			return value == "true"
		}
	}
	return defaultEnabled
}

func (p *Provider) processSite(
	site ForgeSite,
	serverID, upstreamHost string,
	upstreamPort int,
	configuration *dynamic.Configuration,
	redirectMiddlewareName string,
) {
	if site.Attributes.Status != "installed" {
		log.Printf("Site %q status=%q, skipping", site.Attributes.Name, site.Attributes.Status)
		return
	}

	defaults := siteDefaults{
		CertResolver: p.defaultCertResolver,
		SitesEnabled: p.defaultSitesEnabled,
		HTTPRedirect: p.httpRedirect,
		Port:         upstreamPort,
	}
	tags := parseSiteTags(site.Attributes.Tags, defaults)

	if !tags.Enabled {
		log.Printf("Site %q disabled, skipping", site.Attributes.Name)
		return
	}

	domains, err := p.client.FetchDomains(serverID, site.ID)
	if err != nil {
		log.Printf("Failed to fetch domains for site %q: %v — falling back to site name",
			site.Attributes.Name, err)
	}

	reverb, err := p.client.FetchReverbIntegration(serverID, site.ID)
	if err != nil {
		log.Printf("Failed to fetch Reverb integration for site %q: %v", site.Attributes.Name, err)
	}

	reverbHost, reverbPort := "", 0
	if reverb != nil {
		reverbHost = reverb.Host
		reverbPort = reverb.Port
	}
	if tags.ReverbPortOverride > 0 {
		reverbPort = tags.ReverbPortOverride
	}

	mainHosts, wildcardHosts, reverbHosts := classifyDomains(domains, reverbHost)

	if len(mainHosts) == 0 {
		mainHosts = []string{site.Attributes.Name}
	}

	// Filter .on-forge.com domains and apply TLS rules.
	mainHosts, enableTLS, certResolver := applyForgeDomainPolicy(
		mainHosts, tags.IncludeForgeDomain, tags.EnableTLS, tags.CertResolver,
		site.Attributes.Name,
	)
	if len(mainHosts) == 0 {
		return // site skipped — only .on-forge.com domains and not opted in
	}

	// Append tag aliases not already present.
	mainHosts = appendAliases(mainHosts, tags.Aliases)

	routerName := fmt.Sprintf("forge-%s-%s", site.Attributes.Name, site.ID)
	serviceName := fmt.Sprintf("%s-service", routerName)
	siteBackendURL := fmt.Sprintf("http://%s:%d", upstreamHost, tags.Port)

	entryPoints := tags.EntryPoints
	if len(entryPoints) == 0 {
		if enableTLS {
			entryPoints = []string{"websecure"}
		} else {
			entryPoints = []string{"web"}
		}
	}

	hostRule := buildHostRule(mainHosts, wildcardHosts)

	router := &dynamic.Router{
		EntryPoints: entryPoints,
		Service:     serviceName,
		Rule:        hostRule,
		Middlewares: tags.Middlewares,
	}
	if enableTLS {
		router.TLS = &dynamic.RouterTLSConfig{}
		if certResolver != "" {
			router.TLS.CertResolver = certResolver
		}
	}
	configuration.HTTP.Routers[routerName] = router

	if tags.HTTPRedirect && enableTLS {
		httpRouter := &dynamic.Router{
			EntryPoints: []string{"web"},
			Service:     serviceName,
			Rule:        hostRule,
		}
		if redirectMiddlewareName != "" {
			httpRouter.Middlewares = []string{redirectMiddlewareName}
		}
		configuration.HTTP.Routers[routerName+"-http"] = httpRouter
	}

	configuration.HTTP.Services[serviceName] = &dynamic.Service{
		LoadBalancer: &dynamic.ServersLoadBalancer{
			Servers:        []dynamic.Server{{URL: siteBackendURL}},
			PassHostHeader: boolPtr(true),
		},
	}

	log.Printf("Created router for site %q hosts=%v -> %s", site.Attributes.Name, mainHosts, siteBackendURL)

	if len(reverbHosts) > 0 && reverbPort > 0 {
		p.addReverbRouter(routerName, reverbHosts, upstreamHost, reverbPort, entryPoints,
			enableTLS, certResolver, tags.Middlewares, configuration)
		log.Printf("Created Reverb router for site %q hosts=%v port=%d",
			site.Attributes.Name, reverbHosts, reverbPort)
	}
}

func (p *Provider) addReverbRouter(
	baseRouterName string,
	reverbHosts []string,
	upstreamHost string,
	reverbPort int,
	entryPoints []string,
	enableTLS bool,
	certResolver string,
	middlewares []string,
	configuration *dynamic.Configuration,
) {
	reverbRouterName := baseRouterName + "-reverb"
	reverbServiceName := reverbRouterName + "-service"
	reverbBackendURL := fmt.Sprintf("http://%s:%d", upstreamHost, reverbPort)
	reverbRule := buildHostRule(reverbHosts, nil)

	reverbRouter := &dynamic.Router{
		EntryPoints: entryPoints,
		Service:     reverbServiceName,
		Rule:        reverbRule,
		Middlewares: middlewares,
	}
	if enableTLS {
		reverbRouter.TLS = &dynamic.RouterTLSConfig{}
		if certResolver != "" {
			reverbRouter.TLS.CertResolver = certResolver
		}
	}
	configuration.HTTP.Routers[reverbRouterName] = reverbRouter

	// No HTTP redirect router for Reverb — WebSocket clients don't follow
	// HTTP redirects during the upgrade handshake, so it would never be used.

	configuration.HTTP.Services[reverbServiceName] = &dynamic.Service{
		LoadBalancer: &dynamic.ServersLoadBalancer{
			Servers:        []dynamic.Server{{URL: reverbBackendURL}},
			PassHostHeader: boolPtr(true),
		},
	}
}

// classifyDomains partitions enabled domain records into main hosts, wildcard hosts,
// and Reverb hosts. The reverbHost argument is the authoritative host from the
// Reverb integration API; domain type alone is not sufficient.
func classifyDomains(domains []ForgeDomain, reverbHost string) (main, wildcard, reverb []string) {
	for _, d := range domains {
		if d.Attributes.Status != "enabled" {
			continue
		}
		name := d.Attributes.Name
		if reverbHost != "" && name == reverbHost {
			reverb = append(reverb, name)
			continue
		}
		main = append(main, name)
		if d.Attributes.AllowWildcardSubdomains {
			wildcard = append(wildcard, name)
		}
		// If Forge manages a www redirect for this domain, Traefik must accept
		// traffic on both apex and www — the redirect itself is handled by Nginx.
		if d.Attributes.WWWRedirectType != "" && d.Attributes.WWWRedirectType != "none" &&
			!strings.HasPrefix(name, "www.") {
			main = append(main, "www."+name)
		}
	}
	return main, wildcard, reverb
}

// applyForgeDomainPolicy filters .on-forge.com hosts according to the forge-domain
// opt-in flag, adjusts TLS settings if only forge domains remain, and logs skipped sites.
func applyForgeDomainPolicy(
	hosts []string,
	includeForgeDomain bool,
	enableTLS bool,
	certResolver string,
	siteName string,
) (filtered []string, outTLS bool, outResolver string) {
	outTLS = enableTLS
	outResolver = certResolver

	hasReal := false
	for _, h := range hosts {
		if strings.HasSuffix(h, ".on-forge.com") {
			if includeForgeDomain {
				filtered = append(filtered, h)
			}
		} else {
			filtered = append(filtered, h)
			hasReal = true
		}
	}

	if len(filtered) == 0 {
		log.Printf("Site %q has only .on-forge.com domains — skipping (use traefik:forge-domain=true to include)",
			siteName)
		return nil, false, ""
	}

	if !hasReal {
		// Only forge domains opted in — disable TLS, Forge controls those certs.
		log.Printf("Site %q uses only .on-forge.com domains — disabling TLS", siteName)
		outTLS = false
		outResolver = ""
	}

	return filtered, outTLS, outResolver
}

// appendAliases adds any tag aliases not already present in hosts.
func appendAliases(hosts, aliases []string) []string {
	existing := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		existing[h] = true
	}
	for _, a := range aliases {
		if !existing[a] {
			hosts = append(hosts, a)
		}
	}
	return hosts
}

// siteDefaults holds the provider-level defaults applied before site tags are evaluated.
type siteDefaults struct {
	CertResolver string
	SitesEnabled bool
	HTTPRedirect bool
	Port         int
}

// siteTagConfig is the resolved configuration for a single site after tag processing.
type siteTagConfig struct {
	Enabled            bool
	CertResolver       string
	EnableTLS          bool
	Port               int
	HTTPRedirect       bool
	EntryPoints        []string
	Aliases            []string
	Middlewares        []string
	ReverbPortOverride int
	IncludeForgeDomain bool
}

// parseSiteTags resolves site-level tag overrides on top of provider defaults.
func parseSiteTags(tags []string, defaults siteDefaults) siteTagConfig {
	cfg := siteTagConfig{
		Enabled:      defaults.SitesEnabled,
		CertResolver: defaults.CertResolver,
		EnableTLS:    defaults.CertResolver != "",
		Port:         defaults.Port,
		HTTPRedirect: defaults.HTTPRedirect,
	}

	for _, tag := range tags {
		key, value, ok := ParseTagConfig(tag)
		if !ok {
			continue
		}
		switch key {
		case "enabled":
			cfg.Enabled = value == "true"
		case "cert-resolver", "certresolver":
			cfg.CertResolver = value
			cfg.EnableTLS = true
		case "tls":
			cfg.EnableTLS = value == "true"
		case "port":
			var port int
			if n, _ := fmt.Sscanf(value, "%d", &port); n == 1 {
				cfg.Port = port
			}
		case "http-redirect", "redirect":
			cfg.HTTPRedirect = value == "true"
		case "entrypoints", "entry-points":
			for _, ep := range strings.Split(value, ",") {
				if ep = strings.TrimSpace(ep); ep != "" {
					cfg.EntryPoints = append(cfg.EntryPoints, ep)
				}
			}
		case "aliases":
			for _, a := range strings.Split(value, ",") {
				if a = strings.TrimSpace(a); a != "" {
					cfg.Aliases = append(cfg.Aliases, a)
				}
			}
		case "reverb-port":
			var port int
			if n, _ := fmt.Sscanf(value, "%d", &port); n == 1 {
				cfg.ReverbPortOverride = port
			}
		case "forge-domain":
			cfg.IncludeForgeDomain = value == "true"
		case "middlewares", "middleware":
			for _, m := range strings.Split(value, ",") {
				if m = strings.TrimSpace(m); m != "" {
					cfg.Middlewares = append(cfg.Middlewares, m)
				}
			}
		}
	}

	return cfg
}

// ParseTagConfig parses a Forge tag for configuration directives.
// Format: "traefik:key=value" or "traefik:flag" (treated as key=true).
// Returns key, value, and ok=true if it is a traefik tag.
func ParseTagConfig(tag string) (key, value string, ok bool) {
	const prefix = "traefik:"
	if !strings.HasPrefix(tag, prefix) {
		return "", "", false
	}
	content := strings.TrimPrefix(tag, prefix)
	if content == "" {
		return "", "", false
	}
	if idx := strings.Index(content, "="); idx > 0 {
		return strings.TrimSpace(content[:idx]), strings.TrimSpace(content[idx+1:]), true
	}
	return strings.TrimSpace(content), "true", true
}

// ServerConfig holds configuration parsed from server-level tags.
type ServerConfig struct {
	UpstreamHost string
	UpstreamPort int
	TraefikID    string
}

// ParseServerTags parses server tags and returns the resolved ServerConfig.
func ParseServerTags(tags []string) ServerConfig {
	cfg := ServerConfig{UpstreamPort: 80}

	for _, tag := range tags {
		key, value, ok := ParseTagConfig(tag)
		if !ok {
			continue
		}
		switch key {
		case "upstream-host", "upstreamhost", "lb-host", "loadbalancer-host":
			cfg.UpstreamHost = value
		case "upstream-port", "upstreamport", "lb-port", "loadbalancer-port":
			var port int
			if n, _ := fmt.Sscanf(value, "%d", &port); n == 1 {
				cfg.UpstreamPort = port
			}
		case "traefik", "traefik-id":
			cfg.TraefikID = value
		}
	}

	return cfg
}

// buildHostRule builds a Traefik v3 routing rule from a list of exact hosts and
// wildcard-enabled hosts. Wildcard hosts get an additional HostRegexp clause
// matching any single-level subdomain (e.g. app.example.com).
func buildHostRule(hosts, wildcardHosts []string) string {
	parts := make([]string, 0, len(hosts)+len(wildcardHosts))
	for _, h := range hosts {
		parts = append(parts, fmt.Sprintf("Host(`%s`)", h))
	}
	for _, h := range wildcardHosts {
		escaped := strings.ReplaceAll(h, ".", `\.`)
		parts = append(parts, fmt.Sprintf("HostRegexp(`^[^.]+\\.%s$`)", escaped))
	}
	return strings.Join(parts, " || ")
}

func boolPtr(v bool) *bool { return &v }
