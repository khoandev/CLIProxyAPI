package proxyutil

import "net/http"

const (
	// defaultMaxIdleConnsPerHost keeps enough idle connections per upstream that a
	// proxy serving many concurrent requests reuses them instead of re-dialing.
	defaultMaxIdleConnsPerHost = 64
	// defaultMaxIdleConns bounds the idle pool across all upstreams.
	defaultMaxIdleConns = 256
)

// ExpandDefaultTransportIdlePool raises the idle-connection limits of the shared
// default transport. The Go default keeps only 2 idle connections per host, so a
// proxy serving more than two concurrent requests per upstream closes and
// re-dials (including the TLS handshake) on almost every request.
//
// Only the pool sizes change: proxy, dialer, TLS config and HTTP/2 policy stay
// identical. The call is idempotent and safe to repeat from several entrypoints.
func ExpandDefaultTransportIdlePool() {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return
	}
	if base.MaxIdleConnsPerHost >= defaultMaxIdleConnsPerHost {
		return
	}
	// Clone copies proxy, dialer, TLS config and HTTP/2 policy, and leaves the
	// clone's TLSNextProto for the transport to configure lazily, exactly like a
	// fresh default transport.
	clone := base.Clone()
	clone.MaxIdleConns = defaultMaxIdleConns
	clone.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	http.DefaultTransport = clone
}
