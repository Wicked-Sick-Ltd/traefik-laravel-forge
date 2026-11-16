// Package traefik_laravel_forge provides a Traefik provider plugin for Laravel Forge.
package traefik_laravel_forge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
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
	client              ForgeClient

	cacheTTL time.Duration
	now      func() time.Time

	lookupMu sync.Mutex
	lookups  map[string]siteLookup

	// hostOwners maps each canonical hostname in the last generated
	// configuration to the cacheKey of the site that served it.
	ownersMu   sync.Mutex
	hostOwners map[string]string

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

	pluginLog.Printf("Fetched %d servers from Forge", len(servers))

	var plans []sitePlan
	for _, server := range servers {
		serverPlans, err := p.planServer(server)
		if err != nil {
			return nil, err
		}
		plans = append(plans, serverPlans...)
	}

	// Every site's hostnames are known before any router is written, so which
	// site serves a contested hostname cannot depend on the order Forge lists
	// servers and sites in.
	owners := p.resolveHostOwners(plans)
	published := make(map[string]string)
	for i := range plans {
		emitSite(&plans[i], owners, published, configuration, redirectMiddlewareName)
	}
	p.storeHostOwners(published)

	return configuration, nil
}

// planServer collects the routing plan for every enabled site on one Forge server.
// A non-nil error means the server's routing state could not be determined —
// the caller must abandon the whole poll rather than emit a partial config,
// which Traefik would apply by deleting the missing routers.
func (p *Provider) planServer(server ForgeServer) ([]sitePlan, error) {
	tagConfig := ParseServerTags(server.Attributes.Tags)

	// In multi-LB mode, skip servers not assigned to this instance.
	if p.traefikID != "" && tagConfig.TraefikID != p.traefikID {
		pluginLog.Printf("Server %q skipped (traefik-id=%q, want %q)",
			server.Attributes.Name, tagConfig.TraefikID, p.traefikID)
		return nil, nil
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
		return nil, fmt.Errorf("failed to fetch sites for server %q: %w", server.Attributes.Name, err)
	}

	pluginLog.Printf("Found %d sites on server %q", len(sites), server.Attributes.Name)

	// Skip the server entirely if none of its sites are enabled.
	if !hasEnabledSites(sites, p.defaultSitesEnabled) {
		pluginLog.Printf("Server %q has no enabled sites, skipping", server.Attributes.Name)
		return nil, nil
	}

	if upstreamHost == "" {
		pluginLog.Printf("Server %q has enabled sites but no IP address — skipping", server.Attributes.Name)
		return nil, nil
	}

	pluginLog.Printf("Processing server %q -> http://%s:%d", server.Attributes.Name, upstreamHost, upstreamPort)

	var plans []sitePlan
	for _, site := range sites {
		plan, err := p.planSite(site, server.ID, upstreamHost, upstreamPort)
		if err != nil {
			return nil, err
		}
		if plan != nil {
			plans = append(plans, *plan)
		}
	}

	return plans, nil
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
	case mapping != nil && mapping.UpstreamHost != "":
		return mapping.UpstreamHost, mappingPort(mapping)
	}

	// A mapping may override only the port and leave the host to auto-detection.
	if mapping != nil {
		port = mappingPort(mapping)
	}

	switch {
	case server.Attributes.PrivateIPAddress != "":
		return server.Attributes.PrivateIPAddress, port
	case server.Attributes.IPAddress != "":
		return server.Attributes.IPAddress, port
	default:
		return "", port
	}
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

// hostRoute is one router a site asks for, before ownership of its hostnames
// across sites has been decided. host and wwwHost are canonical (see canonicalHost).
type hostRoute struct {
	routerName   string
	host         string
	wwwHost      string
	wildcard     bool
	exact        bool // the Forge name or tag already was the canonical spelling
	fromDomain   bool // a Forge domain record, not a traefik:aliases entry
	tags         siteTagConfig
	enableTLS    bool
	certResolver string
}

// sitePlan is everything one site needs routed. claims maps each canonical
// hostname the site asks for to whether any of its requests spelled it exactly.
type sitePlan struct {
	key         string
	name        string
	serviceName string
	backendURL  string
	routes      []hostRoute
	reverb      []hostRoute
	claims      map[string]bool
	explicit    map[string]bool // a domain record or alias named the host, exact or not
	domainNamed map[string]bool // a domain record named the host, exact or not
	domainExact map[string]bool // exact claim came from a domain record, not an alias
}

func (s *sitePlan) claim(r hostRoute) {
	if r.host != "" {
		// An explicit record or alias can upgrade a weaker claim. It must not
		// downgrade one that was already exact.
		s.claims[r.host] = s.claims[r.host] || r.exact
		if s.explicit == nil {
			s.explicit = map[string]bool{}
		}
		s.explicit[r.host] = true
		if r.fromDomain {
			if s.domainNamed == nil {
				s.domainNamed = map[string]bool{}
			}
			s.domainNamed[r.host] = true
		}
		if r.fromDomain && r.exact {
			if s.domainExact == nil {
				s.domainExact = map[string]bool{}
			}
			s.domainExact[r.host] = true
		}
	}
	if r.wwwHost != "" {
		// A www host implied by a redirect is not an exact claim. An explicit
		// www record, claimed as r.host on its own route, outranks it.
		if _, ok := s.claims[r.wwwHost]; !ok {
			s.claims[r.wwwHost] = false
		}
	}
}

func (s *sitePlan) hasRoute(routerName string) bool {
	for _, r := range s.routes {
		if r.routerName == routerName {
			return true
		}
	}
	return false
}

// planSite builds the routing plan for a single Forge site. As with planServer, a
// non-nil error means routing state is unknown and the poll must be abandoned.
// A nil plan with a nil error means the site is not routed.
func (p *Provider) planSite(
	site ForgeSite,
	serverID, upstreamHost string,
	upstreamPort int,
) (*sitePlan, error) {
	if site.Attributes.Status != "installed" {
		pluginLog.Printf("Site %q status=%q, skipping", site.Attributes.Name, site.Attributes.Status)
		return nil, nil
	}

	defaults := siteDefaults{
		CertResolver: p.defaultCertResolver,
		SitesEnabled: p.defaultSitesEnabled,
		HTTPRedirect: p.httpRedirect,
		Port:         upstreamPort,
	}
	tags := parseSiteTags(site.Attributes.Tags, defaults)

	if !tags.Enabled {
		pluginLog.Printf("Site %q disabled, skipping", site.Attributes.Name)
		return nil, nil
	}

	// Domains and the Reverb integration come from the cache. These two calls are
	// the bulk of a poll's request volume (two per site), and caching them keeps
	// the burst small enough to stay inside Forge's 60 requests/minute. A domains
	// failure with nothing cached is still fatal to the poll: falling back to the
	// site name would silently swap every real customer hostname for the Forge one.
	lookup, err := p.siteLookupFor(serverID, site)
	if err != nil {
		return nil, err
	}
	domains, reverbHost := lookup.domains, lookup.reverbHost

	mainDomains, reverbDomains := classifyDomains(domains, reverbHost)

	// Fallback: no Forge domain records → synthesize one from the site name.
	if len(mainDomains) == 0 {
		mainDomains = []ForgeDomain{{
			Attributes: ForgeDomainAttributes{
				Name:   site.Attributes.Name,
				Status: "enabled",
			},
		}}
	}

	plan := &sitePlan{
		key:         cacheKey(serverID, site.ID),
		name:        site.Attributes.Name,
		serviceName: fmt.Sprintf("forge-%s-%s-service", site.Attributes.Name, site.ID),
		backendURL:  fmt.Sprintf("http://%s:%d", upstreamHost, tags.Port),
		claims:      map[string]bool{},
		explicit:    map[string]bool{},
		domainNamed: map[string]bool{},
		domainExact: map[string]bool{},
	}

	// One router per Forge domain record.
	for _, d := range mainDomains {
		if r, ok := domainRoute(site.ID, d, tags); ok {
			plan.claim(r)
			if !plan.hasRoute(r.routerName) {
				plan.routes = append(plan.routes, r)
			}
		}
	}

	// traefik:aliases → one router per alias, skipping any already covered by a domain record.
	for _, alias := range tags.Aliases {
		host, ok := canonicalHost(alias)
		if !ok {
			pluginLog.Printf("Ignoring traefik:aliases entry %q on site %q: not a valid hostname",
				alias, site.Attributes.Name)
			continue
		}
		r := hostRoute{
			routerName:   routerNameFor(site.ID, host),
			host:         host,
			exact:        alias == host,
			tags:         tags,
			enableTLS:    tags.EnableTLS,
			certResolver: tags.CertResolver,
		}
		plan.claim(r)
		if !plan.hasRoute(r.routerName) {
			plan.routes = append(plan.routes, r)
		}
	}

	// Reverb domains route through the same Nginx backend as the main site.
	// Nginx handles the WebSocket proxy internally via its Forge-configured location.
	// HTTP redirect is suppressed — WebSocket clients don't follow redirects.
	noRedirectTags := tags
	noRedirectTags.HTTPRedirect = false
	for _, rd := range reverbDomains {
		if r, ok := domainRoute(site.ID, rd, noRedirectTags); ok {
			plan.claim(r)
			plan.reverb = append(plan.reverb, r)
		}
	}

	return plan, nil
}

// domainRoute describes the router for a single Forge domain record,
// incorporating any www redirect and wildcard subdomain variants. Returns false
// if the domain is filtered out.
func domainRoute(siteID string, domain ForgeDomain, tags siteTagConfig) (hostRoute, bool) {
	name := domain.Attributes.Name
	host, ok := canonicalHost(name)
	if !ok {
		pluginLog.Printf("Ignoring domain %q: not a valid hostname", name)
		return hostRoute{}, false
	}

	r := hostRoute{
		routerName:   routerNameFor(siteID, host),
		host:         host,
		exact:        name == host,
		fromDomain:   true,
		tags:         tags,
		enableTLS:    tags.EnableTLS,
		certResolver: tags.CertResolver,
	}

	// .on-forge.com domains are skipped unless the site opted in.
	if strings.HasSuffix(host, ".on-forge.com") {
		if !tags.IncludeForgeDomain {
			pluginLog.Printf("Skipping .on-forge.com domain %q (use traefik:forge-domain=true to include)", host)
			return hostRoute{}, false
		}
		// Opted in but Forge controls TLS — disable cert management for this domain.
		pluginLog.Printf("Domain %q is .on-forge.com — disabling TLS", host)
		r.tags.EnableTLS = false
		r.tags.CertResolver = ""
		r.enableTLS = false
		r.certResolver = ""
		return r, true
	}

	r.wildcard = domain.Attributes.AllowWildcardSubdomains
	if domain.Attributes.WWWRedirectType != "" && domain.Attributes.WWWRedirectType != "none" &&
		!strings.HasPrefix(host, "www.") {
		if www, ok := canonicalHost("www." + host); ok {
			r.wwwHost = www
		}
	}
	return r, true
}

func routerNameFor(siteID, host string) string {
	return fmt.Sprintf("forge-%s-%s", siteID, host)
}

// resolveHostOwners decides which site serves each canonical hostname. Traefik
// matches Host rules case-insensitively and ignores one trailing dot, so two
// sites asking for the same canonical name would otherwise both match, and the
// higher priority or earlier router would take the other site's traffic.
//
// A name asked for by one site belongs to it. When several sites ask, the site
// that served the name after the previous poll keeps it. Failing that, the one
// site whose Forge record or tag spells it exactly gets it. Failing that, an
// exact tie goes to the lowest site key so the name stays online, and that
// choice is what the next poll remembers. A tie in which nobody spelled it
// exactly goes to no site, so a case or trailing-dot variant cannot capture it.
func (p *Provider) resolveHostOwners(plans []sitePlan) map[string]string {
	claimants := make(map[string][]int)
	for i := range plans {
		for host := range plans[i].claims {
			claimants[host] = append(claimants[host], i)
		}
	}

	previous := p.loadHostOwners()
	owners := make(map[string]string, len(claimants))
	for host, idx := range claimants {
		if owner, ok := pickHostOwner(host, idx, plans, previous[host]); ok {
			owners[host] = owner
		}
	}
	// An implied www host follows the apex winner only when nobody else named
	// that www host in a record or alias. One explicit claim, even spelled
	// non-canonically, wins instead. Two explicit non-canonical claims stay
	// unpublished, which is the same rule as any other inexact tie.
	for host, idx := range claimants {
		if _, ok := owners[host]; ok || !strings.HasPrefix(host, "www.") {
			continue
		}
		var explicit []int
		for _, i := range idx {
			// An alias, including a case variant, does not count. A domain
			// record does, even when its spelling is not canonical.
			if plans[i].domainNamed[host] {
				explicit = append(explicit, i)
			}
		}
		if len(explicit) == 1 {
			owners[host] = plans[explicit[0]].key
			fmt.Fprintf(os.Stderr, "forge: host %q is spelled non-canonically only by site %q\n",
				host, plans[explicit[0]].name)
			continue
		}
		if len(explicit) > 1 {
			continue
		}
		apexOwner, ok := owners[strings.TrimPrefix(host, "www.")]
		if !ok {
			continue
		}
		for _, i := range idx {
			if plans[i].key != apexOwner {
				continue
			}
			owners[host] = apexOwner
			fmt.Fprintf(os.Stderr, "forge: host %q follows apex owner site %q\n",
				host, plans[i].name)
			break
		}
	}
	return owners
}

func pickHostOwner(host string, idx []int, plans []sitePlan, incumbent string) (string, bool) {
	if len(idx) == 1 {
		return plans[idx[0]].key, true
	}

	names := make([]string, 0, len(idx))
	var exact []int
	for _, i := range idx {
		if incumbent != "" && plans[i].key == incumbent {
			return incumbent, true
		}
		if plans[i].claims[host] {
			exact = append(exact, i)
		}
		names = append(names, fmt.Sprintf("%q", plans[i].name))
	}
	sort.Strings(names)

	if len(exact) == 1 {
		fmt.Fprintf(os.Stderr, "forge: host %q is requested by sites %s; routing it only to site %q\n",
			host, strings.Join(names, ", "), plans[exact[0]].name)
		return plans[exact[0]].key, true
	}
	if len(exact) > 1 {
		// A Forge domain record beats an alias-only claim, so adding an alias
		// cannot take a name a domain record already spells exactly.
		pool := exact
		var fromDomain []int
		for _, i := range exact {
			if plans[i].domainExact[host] {
				fromDomain = append(fromDomain, i)
			}
		}
		if len(fromDomain) > 0 && len(fromDomain) < len(exact) {
			pool = fromDomain
		}
		winner := pool[0]
		for _, i := range pool[1:] {
			if plans[i].key < plans[winner].key {
				winner = i
			}
		}
		fmt.Fprintf(os.Stderr, "forge: host %q is requested by sites %s; routing the exact tie to site %q\n",
			host, strings.Join(names, ", "), plans[winner].name)
		return plans[winner].key, true
	}
	fmt.Fprintf(os.Stderr, "forge: host %q is requested by sites %s; routing it to none of them\n",
		host, strings.Join(names, ", "))
	return "", false
}

// emitSite writes the routers and service for one site, leaving out every
// hostname the site does not own, and records each published hostname.
func emitSite(
	plan *sitePlan,
	owners, published map[string]string,
	configuration *dynamic.Configuration,
	redirectMiddlewareName string,
) {
	routersCreated := 0
	for _, r := range plan.routes {
		if emitRoute(plan, r, owners, published, configuration, redirectMiddlewareName) {
			routersCreated++
		}
	}
	for _, r := range plan.reverb {
		if emitRoute(plan, r, owners, published, configuration, redirectMiddlewareName) {
			routersCreated++
			pluginLog.Printf("Created Reverb router for site %q domain=%q -> %s (via Nginx)",
				plan.name, r.host, plan.backendURL)
		}
	}
	if routersCreated == 0 {
		return
	}

	configuration.HTTP.Services[plan.serviceName] = &dynamic.Service{
		LoadBalancer: &dynamic.ServersLoadBalancer{
			Servers:        []dynamic.Server{{URL: plan.backendURL}},
			PassHostHeader: boolPtr(true),
		},
	}

	pluginLog.Printf("Created %d router(s) for site %q -> %s", routersCreated, plan.name, plan.backendURL)
}

func emitRoute(
	plan *sitePlan,
	r hostRoute,
	owners, published map[string]string,
	configuration *dynamic.Configuration,
	redirectMiddlewareName string,
) bool {
	ownsHost := owners[r.host] == plan.key
	ownsWWW := r.wwwHost != "" && owners[r.wwwHost] == plan.key
	if !ownsHost && !ownsWWW {
		return false
	}
	hosts := make([]string, 0, 2)
	if ownsHost {
		hosts = append(hosts, r.host)
	}
	if ownsWWW {
		hosts = append(hosts, r.wwwHost)
	}
	var wildcardHosts []string
	if r.wildcard && ownsHost {
		wildcardHosts = []string{r.host}
	}
	priorityHost := r.host
	if !ownsHost {
		priorityHost = r.wwwHost
	}

	createRouter(r.routerName, buildHostRule(hosts, wildcardHosts), plan.serviceName, r.tags,
		r.enableTLS, r.certResolver, domainPriority(priorityHost), configuration, redirectMiddlewareName)
	for _, h := range hosts {
		published[h] = plan.key
	}
	return true
}

func (p *Provider) loadHostOwners() map[string]string {
	p.ownersMu.Lock()
	defer p.ownersMu.Unlock()
	return p.hostOwners
}

func (p *Provider) storeHostOwners(owners map[string]string) {
	p.ownersMu.Lock()
	defer p.ownersMu.Unlock()
	p.hostOwners = owners
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
			entryPoints = []string{"websecure"}
		} else {
			entryPoints = []string{"web"}
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

	if tags.HTTPRedirect && enableTLS {
		httpRouter := &dynamic.Router{
			EntryPoints: []string{"web"},
			Service:     serviceName,
			Rule:        rule,
			Priority:    priority,
		}
		if redirectMiddlewareName != "" {
			httpRouter.Middlewares = []string{redirectMiddlewareName}
		}
		configuration.HTTP.Routers[routerName+"-http"] = httpRouter
	}
}

// classifyDomains partitions enabled domain records into main domains and Reverb
// domains. The reverbHost argument is authoritative — domain type alone is not
// sufficient (see CLAUDE.md for details).
func classifyDomains(domains []ForgeDomain, reverbHost string) (main, reverb []ForgeDomain) {
	reverbCanonical, hasReverb := canonicalHost(reverbHost)
	for _, d := range domains {
		if d.Attributes.Status != "enabled" {
			continue
		}
		if name, ok := canonicalHost(d.Attributes.Name); hasReverb && ok && name == reverbCanonical {
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
			if port, ok := parsePort(value); ok {
				cfg.Port = port
			} else {
				pluginLog.Printf("Ignoring invalid traefik:port=%q (want 1-65535)", value)
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
			if port, ok := parsePort(value); ok {
				cfg.UpstreamPort = port
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

// canonicalHost returns the form of a hostname that Traefik's Host matcher
// treats as equal to it: lowercased, with one trailing dot removed. It reports
// false for anything that is not a dotted DNS hostname, which also keeps
// backticks and other rule syntax out of generated rules.
func canonicalHost(raw string) (string, bool) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" || len(host) > 253 {
		return "", false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", false
			}
		}
	}
	return host, true
}

// domainPriority returns the routing priority for a domain-based router. More
// specific (deeper) domains get higher priority so subdomain-specific rules
// always outrank parent wildcard rules regardless of rule string length.
// Callers pass the canonical host, so a trailing dot cannot add a level.
func domainPriority(domain string) int {
	return (strings.Count(domain, ".") + 1) * 100
}

func boolPtr(v bool) *bool { return &v }
