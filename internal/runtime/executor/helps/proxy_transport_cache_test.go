package helps

import (
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// TestCachedProxyTransportReusesPoolPerCredential proves the proxy transport
// is reused across requests of the same credential. Building a transport per
// request forced a fresh TCP plus TLS handshake every time and left the
// previous pool's idle connections behind.
func TestCachedProxyTransportReusesPoolPerCredential(t *testing.T) {
	proxyURL := "http://127.0.0.1:1"
	auth := &cliproxyauth.Auth{ID: "auth-reuse"}

	first := cachedProxyTransport(proxyURL, auth)
	if first == nil {
		t.Fatal("expected a transport for a valid proxy URL")
	}
	second := cachedProxyTransport(proxyURL, auth)
	if first != second {
		t.Fatal("expected the same transport instance for the same credential")
	}
}

// TestCachedProxyTransportSeparatesCredentials proves two credentials never
// share a connection pool. The native clients this project mirrors keep one
// credential per process, so a shared connection would merge their wire
// fingerprints.
func TestCachedProxyTransportSeparatesCredentials(t *testing.T) {
	proxyURL := "http://127.0.0.1:1"

	first := cachedProxyTransport(proxyURL, &cliproxyauth.Auth{ID: "auth-one"})
	second := cachedProxyTransport(proxyURL, &cliproxyauth.Auth{ID: "auth-two"})
	if first == nil || second == nil {
		t.Fatal("expected transports for a valid proxy URL")
	}
	if first == second {
		t.Fatal("expected different transports for different credentials")
	}
}

// TestCachedProxyTransportRejectsEmptyProxy proves the helper does not build a
// transport without a proxy setting.
func TestCachedProxyTransportRejectsEmptyProxy(t *testing.T) {
	if got := cachedProxyTransport("  ", &cliproxyauth.Auth{ID: "auth-empty"}); got != nil {
		t.Fatal("expected no transport for an empty proxy URL")
	}
}

// TestProxyCredentialScopeIsStable proves the scope only depends on the
// credential identity, so a credential keeps its pool across requests.
func TestProxyCredentialScopeIsStable(t *testing.T) {
	auth := &cliproxyauth.Auth{ID: "auth-stable"}
	if first, second := proxyCredentialScope(auth), proxyCredentialScope(auth); first != second {
		t.Fatalf("scope = %q then %q, want a stable value", first, second)
	}
	if proxyCredentialScope(nil) == proxyCredentialScope(auth) {
		t.Fatal("expected an anonymous scope to differ from a credential scope")
	}
}

// TestProxyTransportCacheEvictsIdleConnections proves the cache closes the idle
// connections of an evicted pool instead of leaking them.
func TestProxyTransportCacheEvictsIdleConnections(t *testing.T) {
	proxyURL := "http://127.0.0.1:1"
	auth := &cliproxyauth.Auth{ID: "auth-evict"}
	transport := cachedProxyTransport(proxyURL, auth)
	if transport == nil {
		t.Fatal("expected a transport for a valid proxy URL")
	}

	key := proxyTransportKey{proxy: proxyURL, credential: proxyCredentialScope(auth)}
	if !proxyTransportCache.Contains(key) {
		t.Fatal("expected the transport to be cached")
	}
	if !proxyTransportCache.CloseKey(key) {
		t.Fatal("expected the cached transport to be closed")
	}
	if proxyTransportCache.Contains(key) {
		t.Fatal("expected the cached transport to be removed")
	}
}
