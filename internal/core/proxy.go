package core

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// forwardingHeaders are the client's own forwarding headers. ReverseProxy deletes
// them before Rewrite runs; the gateway forwards everything that is not hop-by-hop,
// so Rewrite puts them back. It never adds one (research Q5).
var forwardingHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}

// Proxy forwards one adapter's requests to its upstream and streams the response
// back. It is protocol-agnostic: everything protocol-shaped comes from the Adapter.
type Proxy struct {
	rp       *httputil.ReverseProxy
	adapter  Adapter
	identify func(*http.Request) string
}

// NewProxy builds the proxy for adapter a in front of up. identify names the client
// that sent a request.
func NewProxy(a Adapter, up config.Upstream, identify func(*http.Request) string, _ *zap.Logger) *Proxy {
	prefix := a.Prefix()
	base := up.BaseURL
	rp := &httputil.ReverseProxy{
		Transport: newTransport(up),
		Rewrite: func(pr *httputil.ProxyRequest) {
			rewrite(pr, prefix, base)
		},
		ModifyResponse: func(res *http.Response) error {
			stripProxyHeaders(res.Header)
			return nil
		},
	}
	return &Proxy{rp: rp, adapter: a, identify: identify}
}

// ServeHTTP labels the request for the access log, then forwards it.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m := MetaFrom(r.Context()); m != nil {
		m.Protocol = p.adapter.Name()
		m.Client = p.identify(r)
		m.Auth = p.adapter.AuthKind(r.Header)
	}
	p.rp.ServeHTTP(w, r)
}

// newTransport is built field by field rather than cloned from http.DefaultTransport,
// which a test in the same process could have replaced. There is no whole-request
// timeout anywhere: a stream runs as long as upstream sends.
func newTransport(up config.Upstream) *http.Transport {
	dialer := &net.Dialer{Timeout: up.ConnectTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:       http.ProxyFromEnvironment, // research Q9
		DialContext: dialer.DialContext,
		// Accept-Encoding goes upstream as sent, and a compressed body comes back as
		// sent: the gateway never inflates it.
		DisableCompression:    true,
		TLSHandshakeTimeout:   up.TLSHandshakeTimeout,
		ResponseHeaderTimeout: up.ResponseHeaderTimeout,
		// A custom DialContext turns HTTP/2 off unless this is set.
		ForceAttemptHTTP2: true,
		// The stdlib default of 2 idle connections per host would make parallel
		// requests re-handshake TLS constantly.
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// rewrite turns the inbound request into the upstream one, changing only what
// forwarding requires. ReverseProxy has already removed the hop-by-hop headers,
// including those Connection names.
func rewrite(pr *httputil.ProxyRequest, prefix string, base *url.URL) {
	// 1. Strip the adapter prefix. RawPath too, so an escaped path keeps its escapes.
	pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, prefix)
	if pr.Out.URL.RawPath != "" {
		pr.Out.URL.RawPath = strings.TrimPrefix(pr.Out.URL.RawPath, prefix)
	}
	// 2. Scheme, host and base path from base_url; Out.Host is cleared so the upstream
	// host is sent. Never SetXForwarded.
	pr.SetURL(base)
	// 3. ReverseProxy drops query parameters it cannot parse; forward the query as sent.
	pr.Out.URL.RawQuery = pr.In.URL.RawQuery
	// 4. The client's forwarding headers, unless its Connection header made them
	// hop-by-hop.
	listed := connectionTokens(pr.In.Header)
	for _, name := range forwardingHeaders {
		if v, ok := pr.In.Header[name]; ok && !slices.Contains(listed, name) {
			pr.Out.Header[name] = slices.Clone(v)
		}
	}
	// 5. ReverseProxy re-adds Te: trailers, and Connection and Upgrade for a protocol
	// switch, after stripping them; all three are hop-by-hop. So is any Proxy-* header,
	// of which ReverseProxy knows only two.
	pr.Out.Header.Del("Te")
	pr.Out.Header.Del("Connection")
	pr.Out.Header.Del("Upgrade")
	stripProxyHeaders(pr.Out.Header)
}

// connectionTokens are the header names h's Connection header lists, canonicalised.
func connectionTokens(h http.Header) []string {
	var names []string
	for _, v := range h.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if tok = strings.TrimSpace(tok); tok != "" {
				names = append(names, http.CanonicalHeaderKey(tok))
			}
		}
	}
	return names
}

// stripProxyHeaders deletes every Proxy-* header, which the spec lists as hop-by-hop.
func stripProxyHeaders(h http.Header) {
	for name := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "Proxy-") {
			delete(h, name)
		}
	}
}
