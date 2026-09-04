package traefik_laravel_forge

import (
	"fmt"
	"os"
	"time"
)

// defaultCacheTTL is used when cacheTTL is omitted from the plugin config.
// Domain records and Reverb integrations change rarely, so a long TTL is safe.
const defaultCacheTTL = 10 * time.Minute

// siteLookup is the cached result of the two per-site Forge calls.
//
// reverbResolved records whether the Reverb half actually answered. A Reverb
// failure is not fatal, so the entry is still cached for its domains — but the
// failure itself must not be cached for a full TTL, or one transient 429 would
// misclassify the Reverb domain for up to 2*TTL. Before this cache existed that
// self-corrected on the next poll, and it still must.
type siteLookup struct {
	domains        []ForgeDomain
	reverbHost     string
	reverbResolved bool
	expiresAt      time.Time
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

// jitterFraction derives a stable value in [0,1000) from a key using FNV-1a.
//
// Expiry is jittered because the whole point of the cache is to shrink the
// per-poll request burst. If every entry were written with the same TTL they
// would all fall due on the same cycle and rebuild the very burst that trips
// Forge's 60 requests/minute limit. Deriving the offset from the key rather
// than a random source keeps it deterministic, and so testable.
func jitterFraction(key string) int64 {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return int64(h % 1000)
}

// expiryFor spreads an entry's expiry across [TTL, 2*TTL).
func (p *Provider) expiryFor(key string, from time.Time) time.Time {
	spread := time.Duration(jitterFraction(key) * int64(p.cacheTTL) / 1000)
	return from.Add(p.cacheTTL + spread)
}

// siteLookupFor returns a site's domain records and Reverb host, serving a
// cached entry while it is still fresh.
//
// Error policy, which mirrors the surrounding poll semantics:
//   - domains fetch fails and we hold any previous value, even an expired one:
//     serve it and let the poll succeed. Traefik keeps routing the site.
//   - domains fetch fails with nothing cached: return the error so the caller
//     aborts the poll rather than emitting a config missing this site.
//   - Reverb fetch fails: never fatal. It only identifies which domain belongs
//     to Reverb, and that traffic reaches the same Nginx backend regardless.
func (p *Provider) siteLookupFor(serverID string, site ForgeSite) (siteLookup, error) {
	key := cacheKey(serverID, site.ID)
	now := p.clock()
	caching := p.cacheTTL > 0

	cached, hadCached := p.cachedLookup(key)
	if caching && hadCached && now.Before(cached.expiresAt) {
		if cached.reverbResolved {
			return cached, nil
		}
		// Domains are still fresh, but Reverb never answered. Retry just that
		// half rather than waiting out the whole TTL.
		return p.refreshReverb(key, serverID, site, cached), nil
	}

	domains, err := p.client.FetchDomains(serverID, site.ID)
	if err != nil {
		// Only a usable cached value lets the poll continue. With caching off
		// there is nothing legitimate to fall back on, so the error propagates.
		if caching && hadCached {
			fmt.Fprintf(os.Stderr,
				"forge: domains fetch failed for site %q, reusing cached records: %v\n",
				site.Attributes.Name, err)
			return cached, nil
		}
		return siteLookup{}, fmt.Errorf("failed to fetch domains for site %q: %w",
			site.Attributes.Name, err)
	}

	reverbHost, reverbResolved := p.fetchReverbHost(serverID, site)
	if !reverbResolved && hadCached {
		reverbHost = cached.reverbHost
	}

	entry := siteLookup{
		domains:        domains,
		reverbHost:     reverbHost,
		reverbResolved: reverbResolved,
		expiresAt:      p.expiryFor(key, now),
	}
	if caching {
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

// refreshReverb retries only the Reverb half of an otherwise-fresh entry,
// leaving the cached domains and expiry untouched.
func (p *Provider) refreshReverb(
	key, serverID string, site ForgeSite, cached siteLookup,
) siteLookup {
	host, resolved := p.fetchReverbHost(serverID, site)
	if !resolved {
		return cached
	}
	cached.reverbHost = host
	cached.reverbResolved = true
	p.storeLookup(key, cached)
	return cached
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
