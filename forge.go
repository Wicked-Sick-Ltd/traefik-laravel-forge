// Package traefik_laravel_forge provides a Traefik provider plugin for Laravel Forge.
package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/traefik/genconf/dynamic"
	"github.com/traefik/genconf/dynamic/tls"
)

// pluginLog writes to stdout so Traefik captures the output below ERROR level.
// Go's default logger writes to stderr, which Traefik surfaces as level=error —
// incorrect for informational polling messages.
var pluginLog = log.New(os.Stdout, "", 0) //nolint:gochecknoglobals

const (
	// httpEntryPoint is the cleartext entrypoint HTTP→HTTPS redirect routers listen on.
	httpEntryPoint = "web"
	// httpsEntryPoint is the default entrypoint for TLS routers.
	httpsEntryPoint = "websecure"
	// defaultRedirectMiddleware is the redirect middleware the plugin defines itself.
	defaultRedirectMiddleware = "forge-https-redirect"
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
	CacheTTL            string          `json:"cacheTTL,omitempty"`
	DefaultCertResolver string          `json:"defaultCertResolver,omitempty"`
	DefaultSitesEnabled bool            `json:"defaultSitesEnabled,omitempty"`
	HTTPRedirect        bool            `json:"httpRedirect,omitempty"`
	RedirectMiddleware  string          `json:"redirectMiddleware,omitempty"`
	TraefikID           string          `json:"traefikID,omitempty"`
	ServerMappings      []ServerMapping `json:"serverMappings,omitempty"`
	AllowedSitePorts    []int           `json:"allowedSitePorts,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		PollInterval:        "30s",
		CacheTTL:            "10m",
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
	ID            string                `json:"id"`
	Type          string                `json:"type"`
	Attributes    ForgeServerAttributes `json:"attributes"`
	Relationships ForgeRelationships    `json:"relationships"`
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
	ID            string              `json:"id"`
	Type          string              `json:"type"`
	Attributes    ForgeSiteAttributes `json:"attributes"`
	Relationships ForgeRelationships  `json:"relationships"`
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
	ID         string                `json:"id"`
	Type       string                `json:"type"`
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
	allowedSitePorts    []int
	client              ForgeClient

	cacheTTL time.Duration
	now      func() time.Time

	lookupMu sync.Mutex
	lookups  map[string]siteLookup
	pollGen  uint64

	// recordRouters names routers built from a Forge domain or Reverb record
	// during this poll. Alias routers are absent, so a tag cannot outrank the
	// site that holds the hostname.
	recordRouters map[string]map[string]struct{}

	mu     sync.Mutex
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
	ttl, err := parseCacheTTL(config.CacheTTL)
	if err != nil {
		return nil, err
	}
	return &Provider{
		name:                "custom",
		pollInterval:        pi,
		cacheTTL:            ttl,
		lookups:             map[string]siteLookup{},
		defaultCertResolver: config.DefaultCertResolver,
		defaultSitesEnabled: config.DefaultSitesEnabled,
		httpRedirect:        config.HTTPRedirect,
		redirectMiddleware:  config.RedirectMiddleware,
		traefikID:           config.TraefikID,
		serverMappings:      config.ServerMappings,
		allowedSitePorts:    config.AllowedSitePorts,
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

	ttl, err := parseCacheTTL(config.CacheTTL)
	if err != nil {
		return nil, err
	}

	return &Provider{
		name:                name,
		apiToken:            config.APIToken,
		cacheTTL:            ttl,
		lookups:             map[string]siteLookup{},
		organization:        config.Organization,
		pollInterval:        pi,
		defaultCertResolver: config.DefaultCertResolver,
		defaultSitesEnabled: config.DefaultSitesEnabled,
		httpRedirect:        config.HTTPRedirect,
		redirectMiddleware:  config.RedirectMiddleware,
		traefikID:           config.TraefikID,
		serverMappings:      config.ServerMappings,
		allowedSitePorts:    config.AllowedSitePorts,
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
	if p.cacheTTL < 0 {
		return fmt.Errorf("cacheTTL must not be negative, got %s", p.cacheTTL)
	}
	for _, port := range p.allowedSitePorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("allowedSitePorts entries must be 1-65535, got %d", port)
		}
	}
	return nil
}

// Provide starts emitting dynamic configuration.
func (p *Provider) Provide(cfgChan chan<- json.Marshaler) error {
	ctx, cancel := context.WithCancel(context.Background())

	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "forge plugin panic: %v\n", r)
			}
		}()
		p.loadConfiguration(ctx, cfgChan)
	}()

	return nil
}

// Stop halts the provider and its background goroutine.
func (p *Provider) Stop() error {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

func (p *Provider) loadConfiguration(ctx context.Context, cfgChan chan<- json.Marshaler) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	p.sendConfiguration(ctx, cfgChan)

	for {
		select {
		case <-ticker.C:
			p.sendConfiguration(ctx, cfgChan)
		case <-ctx.Done():
			return
		}
	}
}

// sendConfiguration pushes a freshly generated config to Traefik. On error it
// sends nothing, so Traefik keeps the last configuration it received rather than
// tearing down routers because of a transient Forge API failure.
func (p *Provider) sendConfiguration(ctx context.Context, cfgChan chan<- json.Marshaler) {
	configuration, err := p.generateConfiguration()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"forge: error generating configuration (keeping previous config): %v\n", err)
		return
	}

	// Don't push a config that Stop() has already made irrelevant.
	//
	// This deliberately is NOT a `select` on ctx.Done() around the send. Traefik
	// interprets this plugin with yaegi, which drives a select's send case through
	// reflect.Select; converting *dynamic.JSONPayload to json.Marshaler there is
	// not supported and panics at runtime. A bare send is the form every Traefik
	// provider plugin uses, so keep it. The cost is that a send can still park if
	// Traefik stops draining the channel — acceptable, since Traefik drains it for
	// the life of the process.
	if ctx.Err() != nil {
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
		redirectMiddlewareName = ensureRedirectMiddleware(configuration)
	}

	servers, err := p.client.FetchServers()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch servers: %w", err)
	}

	pluginLog.Printf("Fetched %d servers from Forge", len(servers))

	p.beginPoll()
	p.recordRouters = map[string]map[string]struct{}{}
	for _, server := range servers {
		if err := p.processServer(server, configuration, redirectMiddlewareName); err != nil {
			return nil, err
		}
	}
	dropContestedHosts(configuration, p.recordRouters)
	p.pruneLookups()

	return configuration, nil
}

// processServer adds routers for every enabled site on one Forge server.
// A non-nil error means the server's routing state could not be determined —
// the caller must abandon the whole poll rather than emit a partial config,
// which Traefik would apply by deleting the missing routers.
func (p *Provider) processServer(
	server ForgeServer,
	configuration *dynamic.Configuration,
	redirectMiddlewareName string,
) error {
	tagConfig := ParseServerTags(server.Attributes.Tags)

	// In multi-LB mode, skip servers not assigned to this instance.
	if p.traefikID != "" && tagConfig.TraefikID != p.traefikID {
		pluginLog.Printf("Server %q skipped (traefik-id=%q, want %q)",
			server.Attributes.Name, tagConfig.TraefikID, p.traefikID)
		return nil
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
		pluginLog.Printf("Server %q has no upstream host — will skip if any sites need routing",
			server.Attributes.Name)
	}

	sites, err := p.client.FetchSites(server.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch sites for server %q: %w", server.Attributes.Name, err)
	}

	pluginLog.Printf("Found %d sites on server %q", len(sites), server.Attributes.Name)

	// Skip the server entirely if none of its sites are enabled.
	if !hasEnabledSites(sites, p.defaultSitesEnabled) {
		pluginLog.Printf("Server %q has no enabled sites, skipping", server.Attributes.Name)
		return nil
	}

	if upstreamHost == "" {
		pluginLog.Printf("Server %q has enabled sites but no IP address — skipping", server.Attributes.Name)
		return nil
	}

	pluginLog.Printf("Processing server %q -> %q", server.Attributes.Name,
		fmt.Sprintf("http://%s:%d", upstreamHost, upstreamPort))

	for _, site := range sites {
		if err := p.processSite(
			site, server.ID, upstreamHost, upstreamPort, configuration, redirectMiddlewareName,
		); err != nil {
			return err
		}
	}

	return nil
}

// resolveUpstream determines the upstream host and port for a server using the
// priority chain: server tag > server mapping > auto-detected Forge IP.
// Host and port are resolved independently: a traefik:upstream-port tag applies
// to whichever host wins, including a mapping host or the auto-detected IP.
// Returns empty string for host if no IP is available.
func (p *Provider) resolveUpstream(
	server ForgeServer,
	tagConfig ServerConfig,
	mapping *ServerMapping,
) (host string, port int) {
	// Separate assignments, not `host, port = a, f()`: Yaegi drops the first
	// value of a tuple assignment whose right-hand side includes a call.
	switch {
	case tagConfig.UpstreamHost != "":
		// A server that names its own host does not inherit the mapping's port.
		host = tagConfig.UpstreamHost
		port = tagConfig.UpstreamPort
	case mapping != nil && mapping.UpstreamHost != "":
		host = mapping.UpstreamHost
		port = mappingPort(mapping)
	default:
		// A mapping may override only the port and leave the host to auto-detection.
		port = 80
		if mapping != nil {
			port = mappingPort(mapping)
		}
		switch {
		case server.Attributes.PrivateIPAddress != "":
			host = server.Attributes.PrivateIPAddress
		case server.Attributes.IPAddress != "":
			host = server.Attributes.IPAddress
		}
	}

	if tagConfig.UpstreamPortSet {
		port = tagConfig.UpstreamPort
	}
	return host, port
}

// mappingPort returns the mapping's upstream port, defaulting to 80 when unset.
func mappingPort(mapping *ServerMapping) int {
	if mapping.UpstreamPort == 0 {
		return 80
	}
	return mapping.UpstreamPort
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

// processSite adds the routers for a single Forge site. As with processServer, a
// non-nil error means routing state is unknown and the poll must be abandoned.
func (p *Provider) processSite(
	site ForgeSite,
	serverID, upstreamHost string,
	upstreamPort int,
	configuration *dynamic.Configuration,
	redirectMiddlewareName string,
) error {
	if site.Attributes.Status != "installed" {
		pluginLog.Printf("Site %q status=%q, skipping", site.Attributes.Name, site.Attributes.Status)
		return nil
	}

	defaults := siteDefaults{
		CertResolver: p.defaultCertResolver,
		SitesEnabled: p.defaultSitesEnabled,
		HTTPRedirect: p.httpRedirect,
		Port:         upstreamPort,
		AllowedPorts: p.allowedSitePorts,
	}
	tags := parseSiteTags(site.Attributes.Tags, defaults)

	if !tags.Enabled {
		pluginLog.Printf("Site %q disabled, skipping", site.Attributes.Name)
		return nil
	}

	// Domains and the Reverb integration are fetched on every poll, so a hostname
	// removed from or moved off this site in Forge leaves on the next poll. The
	// previous answer is only used when Forge refuses the fetch. A domains failure
	// with no previous answer is still fatal to the poll: falling back to the site
	// name would silently swap every real customer hostname for the Forge one.
	lookup, err := p.siteLookupFor(serverID, site)
	if err != nil {
		return err
	}
	domains, reverbHost := lookup.domains, lookup.reverbHost
	// An accepted traefik:port sends main routers to the app and Reverb routers
	// to Nginx. If Forge did not say which hostname is Reverb, and no previous
	// answer is held, that hostname would follow the app port. Keep this site
	// on the upstream port for this poll instead of abandoning every other site.
	if tags.Port != upstreamPort && !lookup.reverbResolved {
		pluginLog.Printf("Reverb integration for site %q did not answer; keeping upstream port %d for this poll instead of traefik:port=%d",
			site.Attributes.Name, upstreamPort, tags.Port)
		tags.Port = upstreamPort
	}

	mainDomains, reverbDomains := classifyDomains(domains, reverbHost)

	// The site-name fallback runs only when Forge returned no main domain
	// records. Dropping .on-forge.com names must not invent a router for the
	// site name: that name may be a hostname the site no longer holds.
	hadMainRecords := len(mainDomains) > 0
	if !tags.IncludeForgeDomain {
		mainDomains = withoutForgeDefaultDomains(mainDomains)
	}

	// Fallback: no main domain records at all → synthesize one from the site name.
	if !hadMainRecords {
		mainDomains = []ForgeDomain{{
			Attributes: ForgeDomainAttributes{
				Name:   site.Attributes.Name,
				Status: "enabled",
			},
		}}
	}

	serviceName := fmt.Sprintf("forge-%s-%s-service", site.Attributes.Name, site.ID)
	siteBackendURL := fmt.Sprintf("http://%s:%d", upstreamHost, tags.Port)

	// One router per Forge domain record.
	routersCreated := 0
	for _, d := range mainDomains {
		// The synthesized site-name domain is not a Forge record: it must not
		// outrank a real record another site holds for that hostname.
		if p.createDomainRouter(site.ID, d, serviceName, tags, configuration, redirectMiddlewareName, hadMainRecords) {
			routersCreated++
		}
	}

	// traefik:aliases → one router per alias, skipping any already covered by a domain record.
	for _, alias := range tags.Aliases {
		if !plainHostname(alias) {
			pluginLog.Printf("Ignoring traefik:aliases entry %q on site %q: not a plain hostname",
				alias, site.Attributes.Name)
			continue
		}
		routerName := fmt.Sprintf("forge-%s-%s", site.ID, alias)
		if _, exists := configuration.HTTP.Routers[routerName]; !exists {
			createRouter(routerName, fmt.Sprintf("Host(`%s`)", alias), serviceName, tags,
				tags.EnableTLS, tags.CertResolver, domainPriority(alias), configuration, redirectMiddlewareName)
			routersCreated++
		}
	}

	// Reverb domains route to the server's Nginx, which proxies WebSockets to Reverb
	// via its Forge-configured location. That is the server upstream port, not a
	// traefik:port override, so an overridden site gets a separate Nginx service.
	// HTTP redirect is suppressed — WebSocket clients don't follow redirects.
	// They are built even when no main router was: a poll that omits the Reverb
	// host makes Traefik delete its router and frees the name for another site.
	reverbServiceName, reverbBackendURL := serviceName, siteBackendURL
	if tags.Port != upstreamPort {
		reverbServiceName = fmt.Sprintf("forge-%s-%s-nginx-service", site.Attributes.Name, site.ID)
		reverbBackendURL = fmt.Sprintf("http://%s:%d", upstreamHost, upstreamPort)
	}
	noRedirectTags := tags
	noRedirectTags.HTTPRedirect = false
	reverbRouters := 0
	for _, rd := range reverbDomains {
		if p.createDomainRouter(site.ID, rd, reverbServiceName, noRedirectTags, configuration, redirectMiddlewareName, true) {
			reverbRouters++
			pluginLog.Printf("Created Reverb router for site %q domain=%q -> %q (via Nginx)",
				site.Attributes.Name, rd.Attributes.Name, reverbBackendURL)
		}
	}
	if reverbRouters > 0 && reverbServiceName != serviceName {
		configuration.HTTP.Services[reverbServiceName] = &dynamic.Service{
			LoadBalancer: &dynamic.ServersLoadBalancer{
				Servers:        []dynamic.Server{{URL: reverbBackendURL}},
				PassHostHeader: boolPtr(true),
			},
		}
	} else {
		routersCreated += reverbRouters
	}

	if routersCreated == 0 {
		return nil
	}

	configuration.HTTP.Services[serviceName] = &dynamic.Service{
		LoadBalancer: &dynamic.ServersLoadBalancer{
			Servers:        []dynamic.Server{{URL: siteBackendURL}},
			PassHostHeader: boolPtr(true),
		},
	}

	pluginLog.Printf("Created %d router(s) for site %q -> %q", routersCreated, site.Attributes.Name, siteBackendURL)

	return nil
}

// isForgeDefaultDomain reports whether name is a Forge-managed *.on-forge.com host.
func isForgeDefaultDomain(name string) bool {
	return strings.HasSuffix(name, ".on-forge.com")
}

// withoutForgeDefaultDomains returns domains with every *.on-forge.com record removed.
func withoutForgeDefaultDomains(domains []ForgeDomain) []ForgeDomain {
	kept := make([]ForgeDomain, 0, len(domains))
	for _, d := range domains {
		if !isForgeDefaultDomain(d.Attributes.Name) {
			kept = append(kept, d)
		}
	}
	return kept
}

// createDomainRouter creates a Traefik router for a single Forge domain record,
// incorporating any www redirect and wildcard subdomain variants. Returns true if
// a router was created (false if the domain was filtered out). record is true
// when domain is a real Forge domain or Reverb record; only that record's own
// hostname is noted as a record claim, not a synthesized www. variant.
func (p *Provider) createDomainRouter(
	siteID string,
	domain ForgeDomain,
	serviceName string,
	tags siteTagConfig,
	configuration *dynamic.Configuration,
	redirectMiddlewareName string,
	record bool,
) bool {
	name := domain.Attributes.Name

	// .on-forge.com domains are skipped unless the site opted in.
	if isForgeDefaultDomain(name) {
		if !tags.IncludeForgeDomain {
			pluginLog.Printf("Skipping .on-forge.com domain %q (use traefik:forge-domain=true to include)", name)
			return false
		}
		// Opted in but Forge controls TLS — disable cert management for this domain.
		pluginLog.Printf("Domain %q is .on-forge.com — disabling TLS", name)
		noTLSTags := tags
		noTLSTags.EnableTLS = false
		noTLSTags.CertResolver = ""
		routerName := fmt.Sprintf("forge-%s-%s", siteID, name)
		createRouter(routerName, fmt.Sprintf("Host(`%s`)", name), serviceName, noTLSTags,
			false, "", domainPriority(name), configuration, redirectMiddlewareName)
		if record {
			p.noteRecordRouter(routerName, name)
		}
		return true
	}

	// Build the rule, adding www and wildcard variants that belong to this domain.
	hosts := []string{name}
	var wildcardHosts []string
	if domain.Attributes.AllowWildcardSubdomains {
		wildcardHosts = []string{name}
	}
	if domain.Attributes.WWWRedirectType != "" && domain.Attributes.WWWRedirectType != "none" &&
		!strings.HasPrefix(name, "www.") {
		hosts = append(hosts, "www."+name)
	}

	routerName := fmt.Sprintf("forge-%s-%s", siteID, name)
	createRouter(routerName, buildHostRule(hosts, wildcardHosts), serviceName, tags,
		tags.EnableTLS, tags.CertResolver, domainPriority(name), configuration, redirectMiddlewareName)
	if record {
		p.noteRecordRouter(routerName, name)
	}
	return true
}

// noteRecordRouter records that router publishes host from a Forge record.
func (p *Provider) noteRecordRouter(router, host string) {
	if p.recordRouters == nil {
		p.recordRouters = map[string]map[string]struct{}{}
	}
	if p.recordRouters[router] == nil {
		p.recordRouters[router] = map[string]struct{}{}
	}
	p.recordRouters[router][claimHost(host)] = struct{}{}
}

// createRouter writes a router (and optional HTTP redirect router) into configuration.
func createRouter(
	routerName, rule, serviceName string,
	tags siteTagConfig,
	enableTLS bool,
	certResolver string,
	priority int,
	configuration *dynamic.Configuration,
	redirectMiddlewareName string,
) {
	entryPoints := tags.EntryPoints
	if len(entryPoints) == 0 {
		if enableTLS {
			entryPoints = []string{httpsEntryPoint}
		} else {
			entryPoints = []string{httpEntryPoint}
		}
	}

	router := &dynamic.Router{
		EntryPoints: entryPoints,
		Service:     serviceName,
		Rule:        rule,
		Middlewares: tags.Middlewares,
		Priority:    priority,
	}
	if enableTLS {
		router.TLS = &dynamic.RouterTLSConfig{}
		if certResolver != "" {
			router.TLS.CertResolver = certResolver
		}
	}
	configuration.HTTP.Routers[routerName] = router

	if !tags.HTTPRedirect || !enableTLS {
		return
	}
	// The redirect router listens on the public HTTP entrypoint. Skip it only
	// when the site is not on a public entrypoint at all. websecure is the
	// public TLS entrypoint (and the default), so a tag that lists it still
	// gets the redirect. A tag that names only something else, such as an
	// internal entrypoint, keeps the hostname off port 80.
	if len(tags.EntryPoints) > 0 &&
		!slices.Contains(tags.EntryPoints, httpEntryPoint) &&
		!slices.Contains(tags.EntryPoints, httpsEntryPoint) {
		pluginLog.Printf("Skipping HTTP redirect router for %q: entrypoints %v include neither %q nor %q",
			routerName, tags.EntryPoints, httpEntryPoint, httpsEntryPoint)
		return
	}
	// A site-level http-redirect tag can enable this router when the global
	// redirect middleware was never created. Without a middleware the router
	// would proxy the origin in cleartext, so always attach one.
	if redirectMiddlewareName == "" {
		redirectMiddlewareName = ensureRedirectMiddleware(configuration)
	}
	configuration.HTTP.Routers[routerName+"-http"] = &dynamic.Router{
		EntryPoints: []string{httpEntryPoint},
		Service:     serviceName,
		Rule:        rule,
		Middlewares: []string{redirectMiddlewareName},
		Priority:    priority,
	}
}

// ensureRedirectMiddleware defines the plugin's own HTTP→HTTPS redirect
// middleware in configuration (if not already present) and returns its name.
func ensureRedirectMiddleware(configuration *dynamic.Configuration) string {
	if _, ok := configuration.HTTP.Middlewares[defaultRedirectMiddleware]; !ok {
		configuration.HTTP.Middlewares[defaultRedirectMiddleware] = &dynamic.Middleware{
			RedirectScheme: &dynamic.RedirectScheme{
				Scheme:    "https",
				Permanent: true,
			},
		}
	}
	return defaultRedirectMiddleware
}

// classifyDomains partitions enabled domain records into main domains and Reverb
// domains. The reverbHost argument is authoritative — domain type alone is not
// sufficient (see CLAUDE.md for details).
func classifyDomains(domains []ForgeDomain, reverbHost string) (main, reverb []ForgeDomain) {
	for _, d := range domains {
		if d.Attributes.Status != "enabled" {
			continue
		}
		if reverbHost != "" && d.Attributes.Name == reverbHost {
			reverb = append(reverb, d)
		} else {
			main = append(main, d)
		}
	}
	return main, reverb
}

// siteDefaults holds the provider-level defaults applied before site tags are evaluated.
type siteDefaults struct {
	CertResolver string
	SitesEnabled bool
	HTTPRedirect bool
	Port         int
	// AllowedPorts lists the ports a traefik:port tag may select besides Port.
	AllowedPorts []int
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
			port, ok := parsePort(value)
			switch {
			case !ok:
				pluginLog.Printf("Ignoring invalid traefik:port=%q (want 1-65535)", value)
			case port != defaults.Port && !slices.Contains(defaults.AllowedPorts, port):
				pluginLog.Printf("Ignoring traefik:port=%d: not in allowedSitePorts, keeping port %d",
					port, cfg.Port)
			default:
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

// parsePort parses a TCP port from a tag value. Unlike fmt.Sscanf it rejects
// trailing junk ("80x") and out-of-range values ("-1", "999999") outright rather
// than silently routing traffic to a nonsense port.
func parsePort(value string) (int, bool) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

// validUpstreamHost reports whether value can stand alone as the host in
// "http://host:port". Tag values are set by any Forge member who can edit a
// server, so anything outside hostname characters is refused: a newline would
// split plugin log records, and ":", "/", "@" or "#" would change what the
// backend URL points at. A bracketed IPv6 literal is the one exception, because
// that is the form the backend URL already uses.
func validUpstreamHost(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	if bracketedIPv6(value) {
		return true
	}
	for _, r := range value {
		if !isHostnameRune(r) {
			return false
		}
	}
	return true
}

// bracketedIPv6 reports whether value is an IPv6 address wrapped in [] so it
// can be placed in a URL. A bare IPv6 address, a host:port pair and a zone ID
// are refused: the first two do not build a usable backend URL, and a zone ID
// is not a stable address to dial.
func bracketedIPv6(value string) bool {
	if len(value) < 4 || value[0] != '[' || value[len(value)-1] != ']' {
		return false
	}
	inner := value[1 : len(value)-1]
	if strings.Contains(inner, "%") {
		return false
	}
	ip := net.ParseIP(inner)
	return ip != nil && ip.To4() == nil
}

// isHostnameRune uses plain ifs on purpose: under Yaegi, a tagless switch with
// empty multi-expression cases fell through to default and rejected valid hosts.
func isHostnameRune(r rune) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	return r == '.' || r == '-' || r == '_'
}

// ServerConfig holds configuration parsed from server-level tags.
type ServerConfig struct {
	UpstreamHost string
	UpstreamPort int
	// UpstreamPortSet distinguishes a valid traefik:upstream-port tag from the
	// default of 80, so an explicit port 80 still outranks a mapping's port.
	UpstreamPortSet bool
	TraefikID       string
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
			if validUpstreamHost(value) {
				cfg.UpstreamHost = value
			} else {
				pluginLog.Printf("Ignoring invalid traefik:%s=%q (want a hostname, an IPv4 address, or a bracketed IPv6 address)", key, value)
			}
		case "upstream-port", "upstreamport", "lb-port", "loadbalancer-port":
			if port, ok := parsePort(value); ok {
				cfg.UpstreamPort = port
				cfg.UpstreamPortSet = true
			} else {
				pluginLog.Printf("Ignoring invalid traefik:upstream-port=%q (want 1-65535)", value)
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

// domainPriority returns the routing priority for a domain-based router. More
// specific (deeper) domains get higher priority so subdomain-specific rules
// always outrank parent wildcard rules regardless of rule string length.
func domainPriority(domain string) int {
	return (strings.Count(domain, ".") + 1) * 100
}

// hostClaim is one site's exact Host() on one router.
// record is true when that router came from a Forge domain or Reverb record.
type hostClaim struct {
	siteID string
	router string
	eps    []string
	record bool
}

// dropContestedHosts gives a hostname to the site that holds its domain or
// Reverb record. A traefik:aliases router cannot remove that site's router or
// its HTTP redirect. Two record holders are not split across entry points: the
// lowest numeric site id that publishes TLS keeps every entry point it already
// has, and the others lose the host. Alias-only overlaps, with no record, stay
// per entry point. A site's own second router is not a contest. Only a rule
// part that is exactly Host(`name`) counts. Hostnames are compared
// case-insensitively, with one trailing dot removed.
// records maps a router name to the hostnames it holds a Forge record for; any
// other host on that router (a www. variant, an alias) is not a record claim.
func dropContestedHosts(configuration *dynamic.Configuration, records map[string]map[string]struct{}) {
	if configuration == nil || configuration.HTTP == nil {
		return
	}
	claims := map[string][]hostClaim{}
	for name, router := range configuration.HTTP.Routers {
		if strings.HasSuffix(name, "-http") || router == nil {
			continue
		}
		siteID := siteIDFromRouter(name, router.Rule)
		eps := append([]string{}, router.EntryPoints...)
		if httpRouter := configuration.HTTP.Routers[name+"-http"]; httpRouter != nil {
			for _, ep := range httpRouter.EntryPoints {
				if !slices.Contains(eps, ep) {
					eps = append(eps, ep)
				}
			}
		}
		for _, host := range exactHosts(router.Rule) {
			claims[claimHost(host)] = append(claims[claimHost(host)], hostClaim{
				siteID: siteID,
				router: name,
				eps:    eps,
				record: isRecordHost(records, name, host),
			})
		}
	}
	// drop[router] is the set of hosts to remove from that router.
	// split is the alias-only contests still settled per entry point.
	drop := map[string]map[string]bool{}
	split := map[string][]hostClaim{}
	for host, list := range claims {
		if preferred, ok := preferredRecordOwner(list); ok {
			var kept, lost []string
			for _, c := range list {
				if c.siteID == preferred {
					kept = append(kept, c.siteID)
					continue
				}
				lost = append(lost, c.siteID)
				markHostDrop(drop, c.router, host)
			}
			if len(lost) == 0 {
				continue
			}
			sort.Strings(lost)
			sort.Strings(kept)
			pluginLog.Printf("Host %q stays with site %s; not publishing it for %s",
				host, strings.Join(uniqueStrings(kept), ", "), strings.Join(uniqueStrings(lost), ", "))
			continue
		}
		// A site that still won another entry point is not a loser, but it must
		// lose the entry points it did not win. Trim those even when nobody
		// lost the hostname outright.
		losers := losingSites(list)
		split[host] = list
		if len(losers) == 0 {
			continue
		}
		var kept, lost []string
		for _, c := range list {
			if losers[c.siteID] {
				lost = append(lost, c.siteID)
				markHostDrop(drop, c.router, host)
				continue
			}
			kept = append(kept, c.siteID)
		}
		sort.Strings(lost)
		sort.Strings(kept)
		pluginLog.Printf("Host %q stays with site %s; not publishing it for %s",
			host, strings.Join(uniqueStrings(kept), ", "), strings.Join(uniqueStrings(lost), ", "))
	}
	if len(drop) == 0 && len(split) == 0 {
		return
	}
	for name, router := range configuration.HTTP.Routers {
		if router == nil {
			continue
		}
		base := name
		if rest, ok := strings.CutSuffix(name, "-http"); ok {
			base = rest
		}
		hosts := drop[base]
		if len(hosts) == 0 {
			continue
		}
		rule, changed := ruleWithoutHosts(router.Rule, hosts)
		if !changed {
			continue
		}
		if rule == "" {
			delete(configuration.HTTP.Routers, name)
			continue
		}
		router.Rule = rule
	}
	trimLostEntryPoints(configuration, split)
}

func isRecordHost(records map[string]map[string]struct{}, router, host string) bool {
	_, ok := records[router][claimHost(host)]
	return ok
}

func markHostDrop(drop map[string]map[string]bool, router, host string) {
	if drop[router] == nil {
		drop[router] = map[string]bool{}
	}
	drop[router][host] = true
}

// preferredRecordOwner is the site that holds the domain or Reverb record.
// The lowest numeric id that publishes TLS wins, so web and websecure stay on
// one upstream and the HTTP redirect stays with that TLS router. With no TLS
// record, the lowest numeric id wins. ok is false when no claim is a record.
func preferredRecordOwner(list []hostClaim) (string, bool) {
	var tlsOwner, anyOwner string
	hasTLS, hasAny := false, false
	for _, c := range list {
		if !c.record {
			continue
		}
		if !hasAny || siteIDLess(c.siteID, anyOwner) {
			anyOwner = c.siteID
			hasAny = true
		}
		if !slices.Contains(c.eps, httpsEntryPoint) {
			continue
		}
		if !hasTLS || siteIDLess(c.siteID, tlsOwner) {
			tlsOwner = c.siteID
			hasTLS = true
		}
	}
	if !hasAny {
		return "", false
	}
	if hasTLS {
		return tlsOwner, true
	}
	return anyOwner, true
}

// trimLostEntryPoints removes entry points a site lost while still winning
// another one, and deletes its HTTP redirect router when it lost `web`.
func trimLostEntryPoints(configuration *dynamic.Configuration, claims map[string][]hostClaim) {
	for host, list := range claims {
		if len(list) < 2 {
			continue
		}
		winnerOf := entryPointWinners(list)
		for _, c := range list {
			var wonEPs []string
			for _, ep := range c.eps {
				if winnerOf[ep] == c.siteID {
					wonEPs = append(wonEPs, ep)
				}
			}
			if len(wonEPs) == 0 || len(wonEPs) == len(c.eps) {
				continue
			}
			router := configuration.HTTP.Routers[c.router]
			if router == nil {
				continue
			}
			mainKept := make([]string, 0, len(router.EntryPoints))
			for _, ep := range router.EntryPoints {
				if slices.Contains(wonEPs, ep) {
					mainKept = append(mainKept, ep)
				}
			}
			raw := hostSpelling(router.Rule, host)
			httpName := c.router + "-http"
			if ruleIsOnlyHost(router.Rule, host) {
				// One host. Never leave the router with an empty entry-point
				// list: Traefik would attach it to every default entry point.
				if len(mainKept) == 0 {
					delete(configuration.HTTP.Routers, c.router)
				} else {
					router.EntryPoints = mainKept
				}
				if !slices.Contains(wonEPs, "web") {
					delete(configuration.HTTP.Routers, httpName)
				}
				continue
			}
			// Several hosts share this router. Take only the contested host
			// off it, and leave the other hosts and their redirect router.
			src := *router
			var httpCopy *dynamic.Router
			if httpRouter := configuration.HTTP.Routers[httpName]; httpRouter != nil {
				cp := *httpRouter
				httpCopy = &cp
			}
			stripHost(configuration, c.router, host)
			stripHost(configuration, httpName, host)
			split := c.router + "--" + strings.ReplaceAll(host, ".", "-")
			if len(mainKept) > 0 {
				nr := src
				nr.Rule = "Host(`" + raw + "`)"
				nr.EntryPoints = mainKept
				configuration.HTTP.Routers[split] = &nr
			}
			// The copied main router still has TLS, so it does not answer on web.
			// The redirect sibling does. Build it whenever this site won web.
			if httpCopy != nil && slices.Contains(wonEPs, "web") {
				hr := *httpCopy
				hr.Rule = "Host(`" + raw + "`)"
				hr.EntryPoints = []string{"web"}
				configuration.HTTP.Routers[split+"-http"] = &hr
			}
		}
	}
}

func stripHost(configuration *dynamic.Configuration, routerName, host string) {
	router := configuration.HTTP.Routers[routerName]
	if router == nil {
		return
	}
	rule, changed := ruleWithoutHosts(router.Rule, map[string]bool{host: true})
	if !changed {
		return
	}
	if rule == "" {
		delete(configuration.HTTP.Routers, routerName)
		return
	}
	router.Rule = rule
}

func ruleIsOnlyHost(rule, canonical string) bool {
	parts := strings.Split(rule, " || ")
	if len(parts) != 1 {
		return false
	}
	host, ok := exactHostPart(parts[0])
	return ok && claimHost(host) == canonical
}

func hostSpelling(rule, canonical string) string {
	for _, host := range exactHosts(rule) {
		if claimHost(host) == canonical {
			return host
		}
	}
	return canonical
}

func entryPointWinners(list []hostClaim) map[string]string {
	winnerOf := map[string]string{}
	for _, c := range list {
		for _, ep := range c.eps {
			cur, ok := winnerOf[ep]
			if !ok || siteIDLess(c.siteID, cur) {
				winnerOf[ep] = c.siteID
			}
		}
	}
	return winnerOf
}

func losingSites(list []hostClaim) map[string]bool {
	if len(list) < 2 {
		return nil
	}
	sites := map[string]struct{}{}
	for _, c := range list {
		sites[c.siteID] = struct{}{}
	}
	if len(sites) < 2 {
		return nil
	}
	// Each entry point belongs to the lowest numeric site id that listens on it.
	// A site loses the hostname only when it wins none of its entry points, so
	// sharing `web` does not take the host off `websecure`.
	winnerOf := entryPointWinners(list)
	losers := map[string]bool{}
	for _, c := range list {
		won := false
		for _, ep := range c.eps {
			if winnerOf[ep] == c.siteID {
				won = true
				break
			}
		}
		if !won {
			losers[c.siteID] = true
		}
	}
	return losers
}

func siteIDLess(a, b string) bool {
	ai, aerr := strconv.ParseInt(a, 10, 64)
	bi, berr := strconv.ParseInt(b, 10, 64)
	if aerr == nil && berr == nil && ai != bi {
		return ai < bi
	}
	return a < b
}

func claimHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func siteIDFromRouter(name, rule string) string {
	for _, host := range exactHosts(rule) {
		suffix := "-" + host
		if strings.HasPrefix(name, "forge-") && strings.HasSuffix(name, suffix) {
			id := strings.TrimSuffix(strings.TrimPrefix(name, "forge-"), suffix)
			if id != "" {
				return id
			}
		}
	}
	return name
}

func exactHosts(rule string) []string {
	parts := strings.Split(rule, " || ")
	hosts := make([]string, 0, len(parts))
	for _, part := range parts {
		host, ok := exactHostPart(part)
		if ok {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	sort.Strings(values)
	out := []string{values[0]}
	for _, v := range values[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func plainHostname(value string) bool {
	if value == "" || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") {
		return false
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if c >= 'a' && c <= 'z' {
				continue
			}
			if c >= 'A' && c <= 'Z' {
				continue
			}
			if c >= '0' && c <= '9' {
				continue
			}
			if c == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func ruleWithoutHosts(rule string, drop map[string]bool) (string, bool) {
	parts := strings.Split(rule, " || ")
	kept := make([]string, 0, len(parts))
	changed := false
	for _, part := range parts {
		host, ok := exactHostPart(part)
		if ok && drop[claimHost(host)] {
			changed = true
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, " || "), changed
}

func exactHostPart(part string) (string, bool) {
	part = strings.TrimSpace(part)
	if !strings.HasPrefix(part, "Host(`") || !strings.HasSuffix(part, "`)") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(part, "Host(`"), "`)"), true
}

func boolPtr(v bool) *bool { return &v }
