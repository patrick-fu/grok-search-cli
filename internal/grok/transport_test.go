package grok

import (
	"crypto/tls"
	"net/http"
	"testing"
)

func TestProductionTransport_NoProxy(t *testing.T) {
	rt := productionTransport()
	transport, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", rt)
	}
	if transport.Proxy != nil {
		t.Error("Proxy must be nil to disable environment proxy routing")
	}
}

func TestProductionTransport_TLSMinVersion(t *testing.T) {
	rt := productionTransport()
	transport := rt.(*http.Transport)
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig must not be nil")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want %d", transport.TLSClientConfig.MinVersion, tls.VersionTLS12)
	}
}

func TestNewClient_NoRedirects(t *testing.T) {
	c := NewClient("tok", "1.0", &fakeRoundTripper{})
	inner := c.(*client)
	err := inner.httpClient.CheckRedirect(nil, nil)
	if err != http.ErrUseLastResponse {
		t.Errorf("CheckRedirect should return http.ErrUseLastResponse, got %v", err)
	}
}

func TestNewClient_DefaultVersion(t *testing.T) {
	c := NewClient("tok", "", nil)
	inner := c.(*client)
	if inner.cliVersion != "dev" {
		t.Errorf("cliVersion = %q, want dev for empty input", inner.cliVersion)
	}
}

func productionTransport() http.RoundTripper {
	c := NewClient("tok", "1.0", nil)
	inner := c.(*client)
	return inner.httpClient.Transport
}
