package traefik_laravel_forge

import (
	"fmt"
	"os"
	"time"
)

// defaultCacheTTL is used when cacheTTL is omitted from the plugin config.
// Any positive value keeps each site's last good answer as a fallback for a
// refused fetch; zero turns the fallback off.
const defaultCacheTTL = 10 * time.Minute

// siteLookup is a site's domain records and Reverb host as Forge last returned
// them.
//
// It is only ever a fallback for a fetch Forge refused. Every poll asks Forge
// again: serving a stored list while Forge is answering would keep a hostname
// that was removed from a site, or moved to another one, routed to the old
// backend and covered by its cert resolver until the entry aged out.
//
// poll is the generation of the last poll that routed the site. Entries a
// completed poll did not touch are dropped, so a site that stops being routed
// cannot later come back on records from before it left.
type siteLookup struct {
	domains    []ForgeDomain
	reverbHost string
	fetchedAt  time.Time
	poll       uint64
}

// parseCacheTTL turns the configured cacheTTL into a duration. An empty value
// means "not configured" and takes the default, so a traefik.toml written
// before this option existed keeps working.
func parseCacheTTL(raw string) (time.Duration, error) {
	if raw == "" {
		return defaultCacheTTL, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid cacheTTL %q: %w", raw, err)
	}
	return ttl, nil
}

// cacheKey identifies one site's lookup entry.
func cacheKey(serverID, siteID string) string { return serverID + "/" + siteID }

// siteLookupFor fetches a site's domain records and Reverb host from Forge.
//
// Error policy, which mirrors the surrounding poll semantics:
//   - domains fetch fails and we hold this site's last good answer: serve it
//     and let the poll succeed. Traefik keeps routing the site exactly as the
//     previous poll did, which is also what aborting the poll would leave.
//   - domains fetch fails with nothing held: return the error so the caller
//     aborts the poll rather than emitting a config missing this site.
//   - Reverb fetch fails: never fatal. It only identifies which of the fresh
//     domains belongs to Reverb, and that traffic reaches the same Nginx
//     backend regardless, so the last Reverb host Forge returned stands in.
func (p *Provider) siteLookupFor(serverID string, site ForgeSite) (siteLookup, error) {
	key := cacheKey(serverID, site.ID)
	fallback := p.cacheTTL > 0
	previous, hadPrevious := p.cachedLookup(key)

	domains, err := p.client.FetchDomains(serverID, site.ID)
	if err != nil {
		// With the fallback off there is nothing legitimate to serve, so the
		// error propagates.
		if fallback && hadPrevious {
			fmt.Fprintf(os.Stderr,
				"forge: domains fetch failed for site %q, reusing its records from %s ago: %v\n",
				site.Attributes.Name, p.clock().Sub(previous.fetchedAt).Round(time.Second), err)
			p.storeLookup(key, previous)
			return previous, nil
		}
		return siteLookup{}, fmt.Errorf("failed to fetch domains for site %q: %w",
			site.Attributes.Name, err)
	}

	reverbHost, reverbResolved := p.fetchReverbHost(serverID, site)
	if !reverbResolved && hadPrevious {
		reverbHost = previous.reverbHost
	}

	entry := siteLookup{domains: domains, reverbHost: reverbHost, fetchedAt: p.clock()}
	if fallback {
		p.storeLookup(key, entry)
	}
	return entry, nil
}

// fetchReverbHost returns the site's Reverb host and whether the call answered.
// We only need the host to identify which domain record belongs to Reverb —
// traffic routes to Nginx, which proxies the WebSocket internally.
func (p *Provider) fetchReverbHost(serverID string, site ForgeSite) (string, bool) {
	reverb, err := p.client.FetchReverbIntegration(serverID, site.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge: failed to fetch Reverb integration for site %q: %v\n",
			site.Attributes.Name, err)
		return "", false
	}
	if reverb == nil {
		return "", true // answered: this site has no Reverb integration
	}
	return reverb.Host, true
}

// beginPoll starts a new generation for the entries the coming poll touches.
func (p *Provider) beginPoll() {
	p.lookupMu.Lock()
	defer p.lookupMu.Unlock()
	p.pollGen++
}

// pruneLookups drops every entry the poll that just completed did not route.
// Call it only after a successful poll: an aborted one has not visited every
// site, and the entries it skipped are still the fallback for the next poll.
func (p *Provider) pruneLookups() {
	p.lookupMu.Lock()
	defer p.lookupMu.Unlock()
	var stale []string
	for key, entry := range p.lookups {
		if entry.poll != p.pollGen {
			stale = append(stale, key)
		}
	}
	for _, key := range stale {
		delete(p.lookups, key)
	}
}

func (p *Provider) cachedLookup(key string) (siteLookup, bool) {
	p.lookupMu.Lock()
	defer p.lookupMu.Unlock()
	entry, ok := p.lookups[key]
	return entry, ok
}

func (p *Provider) storeLookup(key string, entry siteLookup) {
	p.lookupMu.Lock()
	defer p.lookupMu.Unlock()
	if p.lookups == nil {
		p.lookups = map[string]siteLookup{}
	}
	entry.poll = p.pollGen
	p.lookups[key] = entry
}

// clock returns the provider's time source, defaulting to time.Now so that
// callers constructed without one still work.
func (p *Provider) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}
