package proxyutil

import (
	"crypto/tls"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

// TestExpandDefaultTransportIdlePool verifies only the pool sizes change and
// that a second call is a no-op.
func TestExpandDefaultTransportIdlePool(t *testing.T) {
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()

	base := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	http.DefaultTransport = base

	ExpandDefaultTransportIdlePool()

	expanded, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport = %T, want *http.Transport", http.DefaultTransport)
	}
	if expanded == base {
		t.Fatalf("ExpandDefaultTransportIdlePool must install a clone, not mutate the shared default")
	}
	if expanded.MaxIdleConnsPerHost != defaultMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", expanded.MaxIdleConnsPerHost, defaultMaxIdleConnsPerHost)
	}
	if expanded.MaxIdleConns != defaultMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", expanded.MaxIdleConns, defaultMaxIdleConns)
	}
	if base.MaxIdleConnsPerHost != 0 {
		t.Fatalf("the original transport must stay untouched, got MaxIdleConnsPerHost = %d", base.MaxIdleConnsPerHost)
	}

	// Everything that decides how connections are made must be preserved.
	if reflect.ValueOf(expanded.DialContext).Pointer() != reflect.ValueOf(base.DialContext).Pointer() {
		t.Fatalf("DialContext changed")
	}
	if reflect.ValueOf(expanded.Proxy).Pointer() != reflect.ValueOf(base.Proxy).Pointer() {
		t.Fatalf("Proxy changed")
	}
	if expanded.TLSClientConfig == nil || base.TLSClientConfig == nil {
		t.Fatalf("TLSClientConfig must stay set")
	}
	if expanded.TLSClientConfig.MinVersion != base.TLSClientConfig.MinVersion ||
		expanded.TLSClientConfig.InsecureSkipVerify != base.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("TLSClientConfig changed")
	}
	if expanded.ForceAttemptHTTP2 != base.ForceAttemptHTTP2 {
		t.Fatalf("ForceAttemptHTTP2 changed")
	}
	// Transport.Clone copies the HTTP/2 policy and leaves the protocol map and the
	// bundled h2 transport to be configured lazily, so compare the policy fields
	// rather than that lazily initialized state.
	if (expanded.Protocols == nil) != (base.Protocols == nil) {
		t.Fatalf("Protocols presence changed")
	}
	// A TLS dialer would make the transport skip the HTTP/2 upgrade entirely.
	if expanded.DialTLSContext != nil || expanded.DialTLS != nil {
		t.Fatalf("clone must not invent a TLS dialer")
	}
	if expanded.IdleConnTimeout != base.IdleConnTimeout || expanded.TLSHandshakeTimeout != base.TLSHandshakeTimeout {
		t.Fatalf("timeouts changed")
	}

	// Idempotent: a second call must keep the same transport.
	ExpandDefaultTransportIdlePool()
	if http.DefaultTransport != expanded {
		t.Fatalf("second call replaced the transport, want a no-op")
	}
}

// TestExpandDefaultTransportIdlePoolSkipsRaisedTransport verifies a transport
// that already has a larger pool is left alone.
func TestExpandDefaultTransportIdlePoolSkipsRaisedTransport(t *testing.T) {
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()

	base := &http.Transport{MaxIdleConnsPerHost: defaultMaxIdleConnsPerHost * 2}
	http.DefaultTransport = base

	ExpandDefaultTransportIdlePool()
	if http.DefaultTransport != base {
		t.Fatalf("transport with a larger pool must not be replaced")
	}
}
