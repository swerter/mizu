package dns

import (
	"context"
	"fmt"
	"net"

	"migadu/mizu/pkg/metrics"
)

// CachingWrapper wraps a net.Resolver and adds application-level caching
type CachingWrapper struct {
	resolver *net.Resolver
	rr       *ResilientResolver // For cache access
}

// WrapWithCache wraps a net.Resolver with caching capabilities
func WrapWithCache(resolver *net.Resolver, rr *ResilientResolver) *CachingWrapper {
	return &CachingWrapper{
		resolver: resolver,
		rr:       rr,
	}
}

// LookupHost performs a cached DNS lookup for a hostname
func (c *CachingWrapper) LookupHost(ctx context.Context, host string) ([]string, error) {
	if c.rr != nil {
		// Check cache first
		if addrs, found := c.rr.getCached(host); found {
			// Record cache hit
			if c.rr.metrics != nil {
				c.rr.metrics.DNSCacheHits.WithLabelValues("A").Inc()
			}
			return addrs, nil
		}
		// Record cache miss
		if c.rr.metrics != nil {
			c.rr.metrics.DNSCacheMisses.WithLabelValues("A").Inc()
		}
	}

	// Perform actual lookup
	addrs, err := c.resolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}

	// Cache the result
	if c.rr != nil {
		c.rr.putCache(host, addrs)
		// Update cache size metric
		if c.rr.metrics != nil {
			size, _ := c.rr.GetCacheStats()
			c.rr.metrics.DNSCacheSize.WithLabelValues("A").Set(float64(size))
		}
	}

	return addrs, nil
}

// LookupAddr performs a cached reverse DNS lookup
func (c *CachingWrapper) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	cacheKey := fmt.Sprintf("reverse:%s", addr)

	if c.rr != nil {
		// Check cache first
		if names, found := c.rr.getCached(cacheKey); found {
			// Record cache hit
			if c.rr.metrics != nil {
				c.rr.metrics.DNSCacheHits.WithLabelValues("PTR").Inc()
			}
			return names, nil
		}
		// Record cache miss
		if c.rr.metrics != nil {
			c.rr.metrics.DNSCacheMisses.WithLabelValues("PTR").Inc()
		}
	}

	// Perform actual lookup
	names, err := c.resolver.LookupAddr(ctx, addr)
	if err != nil {
		return nil, err
	}

	// Cache the result
	if c.rr != nil {
		c.rr.putCache(cacheKey, names)
		// Update cache size metric
		if c.rr.metrics != nil {
			size, _ := c.rr.GetCacheStats()
			c.rr.metrics.DNSCacheSize.WithLabelValues("PTR").Set(float64(size))
		}
	}

	return names, nil
}

// LookupMX performs a cached MX record lookup
func (c *CachingWrapper) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	cacheKey := fmt.Sprintf("mx:%s", name)

	if c.rr != nil {
		// Check cache first
		if cached, found := c.rr.getCached(cacheKey); found {
			// Reconstruct MX records from cache (format: "pref:host")
			mxRecords := make([]*net.MX, 0, len(cached))
			for _, entry := range cached {
				var pref uint16
				var host string
				if _, err := fmt.Sscanf(entry, "%d:%s", &pref, &host); err == nil {
					mxRecords = append(mxRecords, &net.MX{Host: host, Pref: pref})
				}
			}
			if len(mxRecords) > 0 {
				// Record cache hit
				if c.rr.metrics != nil {
					c.rr.metrics.DNSCacheHits.WithLabelValues("MX").Inc()
				}
				return mxRecords, nil
			}
		}
		// Record cache miss
		if c.rr.metrics != nil {
			c.rr.metrics.DNSCacheMisses.WithLabelValues("MX").Inc()
		}
	}

	// Perform actual lookup
	mxRecords, err := c.resolver.LookupMX(ctx, name)
	if err != nil {
		return nil, err
	}

	// Cache the result
	if c.rr != nil {
		cached := make([]string, len(mxRecords))
		for i, mx := range mxRecords {
			cached[i] = fmt.Sprintf("%d:%s", mx.Pref, mx.Host)
		}
		c.rr.putCache(cacheKey, cached)
		// Update cache size metric
		if c.rr.metrics != nil {
			size, _ := c.rr.GetCacheStats()
			c.rr.metrics.DNSCacheSize.WithLabelValues("MX").Set(float64(size))
		}
	}

	return mxRecords, nil
}

// LookupTXT performs a cached TXT record lookup
func (c *CachingWrapper) LookupTXT(ctx context.Context, name string) ([]string, error) {
	cacheKey := fmt.Sprintf("txt:%s", name)

	if c.rr != nil {
		// Check cache first
		if txts, found := c.rr.getCached(cacheKey); found {
			// Record cache hit
			if c.rr.metrics != nil {
				c.rr.metrics.DNSCacheHits.WithLabelValues("TXT").Inc()
			}
			return txts, nil
		}
		// Record cache miss
		if c.rr.metrics != nil {
			c.rr.metrics.DNSCacheMisses.WithLabelValues("TXT").Inc()
		}
	}

	// Perform actual lookup
	txts, err := c.resolver.LookupTXT(ctx, name)
	if err != nil {
		return nil, err
	}

	// Cache the result
	if c.rr != nil {
		c.rr.putCache(cacheKey, txts)
		// Update cache size metric
		if c.rr.metrics != nil {
			size, _ := c.rr.GetCacheStats()
			c.rr.metrics.DNSCacheSize.WithLabelValues("TXT").Set(float64(size))
		}
	}

	return txts, nil
}

// GetResolver returns the underlying net.Resolver for methods that don't need caching
func (c *CachingWrapper) GetResolver() *net.Resolver {
	return c.resolver
}

// GetCacheStats returns cache statistics
func (c *CachingWrapper) GetCacheStats() (size int, ttl string) {
	if c.rr != nil {
		s, t := c.rr.GetCacheStats()
		return s, t.String()
	}
	return 0, "0s"
}

// FlushCache clears all cached DNS responses
func (c *CachingWrapper) FlushCache() {
	if c.rr != nil {
		c.rr.FlushCache()
	}
}

// SetMetrics sets the metrics instance for DNS cache monitoring
func (c *CachingWrapper) SetMetrics(m *metrics.Metrics) {
	if c.rr != nil {
		c.rr.SetMetrics(m)
	}
}
