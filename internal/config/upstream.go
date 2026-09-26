package config

import (
	"net/url"
	"time"
)

// Upstream is one adapter's upstream: where it lives and how long to wait for it.
// There is deliberately no timeout for the request as a whole.
type Upstream struct {
	BaseURL               *url.URL
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}

// UpstreamSpec names one upstream and its default base URL. The adapters supply the
// list, so this package names no provider.
type UpstreamSpec struct {
	Name           string
	DefaultBaseURL string
}

// The fields under upstreams.<name>, in the order they are applied.
const (
	fieldBaseURL               = "base_url"
	fieldConnectTimeout        = "connect_timeout"
	fieldTLSHandshakeTimeout   = "tls_handshake_timeout"
	fieldResponseHeaderTimeout = "response_header_timeout"
)

const (
	defaultConnectTimeout        = 10 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 10 * time.Minute
)

// upstreamKey is the dotted key of one upstream field.
func upstreamKey(name, field string) string {
	return "upstreams." + name + "." + field
}

// defaultUpstream is the spec's upstream with no file and no env. It returns false if
// the spec's own default URL does not parse.
func defaultUpstream(spec UpstreamSpec) (Upstream, bool) {
	u, err := url.Parse(spec.DefaultBaseURL)
	if err != nil {
		return Upstream{}, false
	}
	return Upstream{
		BaseURL:               u,
		ConnectTimeout:        defaultConnectTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
	}, true
}

// upstreamSettings is one setting per field of the named upstream.
func upstreamSettings(name string) []setting {
	duration := func(field string, set func(*Upstream, time.Duration)) setting {
		return setting{upstreamKey(name, field), func(c *Config, v string) string {
			d, ok := parseDuration(v)
			if !ok {
				return reasonInvalidDuration
			}
			up := c.Upstreams[name]
			set(&up, d)
			c.Upstreams[name] = up
			return ""
		}}
	}
	return []setting{
		{upstreamKey(name, fieldBaseURL), func(c *Config, v string) string {
			u, err := url.Parse(v)
			if err != nil {
				return reasonInvalidURL
			}
			up := c.Upstreams[name]
			up.BaseURL = u
			c.Upstreams[name] = up
			return ""
		}},
		duration(fieldConnectTimeout, func(u *Upstream, d time.Duration) { u.ConnectTimeout = d }),
		duration(fieldTLSHandshakeTimeout, func(u *Upstream, d time.Duration) { u.TLSHandshakeTimeout = d }),
		duration(fieldResponseHeaderTimeout, func(u *Upstream, d time.Duration) { u.ResponseHeaderTimeout = d }),
	}
}
