package core_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// headersIn builds a header map and the order its names are written in.
func headersIn(pairs ...string) (http.Header, []string) {
	h := http.Header{}
	var names []string
	for i := 0; i < len(pairs); i += 2 {
		name := http.CanonicalHeaderKey(pairs[i])
		if _, ok := h[name]; !ok {
			names = append(names, name)
		}
		h[name] = append(h[name], pairs[i+1])
	}
	return h, names
}

// AC8: method, path with the prefix stripped, query, body and every header reach
// upstream byte-identical. The query holds what ReverseProxy's cleanQueryParams would
// drop (a raw ';' and a bad '%zz'), the path holds an escape that must stay escaped,
// and the client's own forwarding headers must still arrive (Q5).
func TestProxy_ForwardsRequestVerbatim(t *testing.T) {
	up := newUpstream(t, nil)
	gw := startGateway(t, up.URL, identifyWith(testProfile{}))

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	want, names := headersIn(
		"Content-Type", "application/json",
		"Content-Length", strconv.Itoa(len(body)),
		"User-Agent", "some-client/1.2.3",
		"Accept", "*/*",
		"X-Custom", "one",
		"X-Custom", "two",
		"X-Mixed-Case", "  Keep  Spacing ",
		"Authorization", "Bearer not-a-real-token",
		"X-Forwarded-For", "9.9.9.9",
		"X-Forwarded-Host", "client.example",
		"X-Forwarded-Proto", "https",
		"Forwarded", "for=9.9.9.9;proto=https",
		"X-Request-Id", "client-chosen-id",
	)
	target := "/t/v1/things/a%2Fb?beta=true&a=1;b=2&c=%zz&d="
	res, _ := gw.raw(t, rawRequest(http.MethodPost, target, "gateway.local", want, names, body))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	got := up.only(t)
	if got.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.Method)
	}
	if wantURI := "/v1/things/a%2Fb?beta=true&a=1;b=2&c=%zz&d="; got.RequestURI != wantURI {
		t.Errorf("request URI = %q, want %q", got.RequestURI, wantURI)
	}
	if string(got.Body) != body {
		t.Errorf("body = %q, want %q", got.Body, body)
	}
	// Leading and trailing spaces in a value are not part of it on the wire.
	want.Set("X-Mixed-Case", "Keep  Spacing")
	if !reflect.DeepEqual(got.Header, want) {
		t.Errorf("upstream headers differ from what the client sent\n got: %v\nwant: %v", got.Header, want)
	}
}

// The proxy labels the request for the access log: protocol from the adapter, client
// from identify, auth kind from the adapter.
func TestProxy_FillsMeta(t *testing.T) {
	up := newUpstream(t, nil)
	gw := startGateway(t, up.URL, identifyWith(testProfile{}))

	cases := []struct {
		path       string
		headers    []string
		wantClient string
		wantAuth   string
	}{
		{"/t/known", []string{"X-Test-Client", "1", "X-Test-Key", "k"}, "test-client", "api_key"},
		{"/t/unknown", []string{"Authorization", "Bearer b"}, "unknown", "bearer"},
		{"/t/none", nil, "unknown", "none"},
	}
	for _, tc := range cases {
		h, names := headersIn(tc.headers...)
		res, _ := gw.raw(t, rawRequest(http.MethodGet, tc.path, "gateway.local", h, names, ""))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", tc.path, res.StatusCode)
		}
		line := gw.accessLine(t, tc.path)
		if line["protocol"] != "test" || line["client"] != tc.wantClient || line["auth"] != tc.wantAuth {
			t.Errorf("%s: protocol, client, auth = %v, %v, %v; want test, %s, %s",
				tc.path, line["protocol"], line["client"], line["auth"], tc.wantClient, tc.wantAuth)
		}
	}
}

// AC12: a path in base_url is joined in front of the stripped path.
func TestProxy_BaseURLPathPrefixJoined(t *testing.T) {
	for _, basePath := range []string{"/api", "/api/"} {
		t.Run(basePath, func(t *testing.T) {
			up := newUpstream(t, nil)
			base := *up.URL
			base.Path = basePath
			gw := startGateway(t, &base, identifyWith())

			res, _ := gw.raw(t, rawRequest(http.MethodGet, "/t/v1/models?beta=true", "gateway.local", nil, nil, ""))
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", res.StatusCode)
			}
			if got, want := up.only(t).RequestURI, "/api/v1/models?beta=true"; got != want {
				t.Errorf("request URI = %q, want %q", got, want)
			}
		})
	}
}

// AC14: hop-by-hop headers are stripped in both directions: the RFC 7230 list, any
// Proxy-* header, and every header Connection names, a forwarding header included. Te: trailers, which ReverseProxy
// re-adds, and the Upgrade it re-adds for a protocol switch, are stripped too (Q5).
func TestProxy_StripsHopByHopHeaders(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Connection", "X-Resp-Hop")
		h.Set("X-Resp-Hop", "1")
		h.Set("Keep-Alive", "timeout=5")
		h.Set("Proxy-Authenticate", "Basic")
		h.Set("Proxy-Anything", "z")
		h.Set("Upgrade", "h2c")
		h.Set("X-Resp-Keep", "kept")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	gw := startGateway(t, up.URL, identifyWith())

	h, names := headersIn(
		"Connection", "keep-alive, X-Hop, Upgrade",
		"Connection", "x-forwarded-host",
		"X-Hop", "1",
		"X-Forwarded-Host", "named-by-connection.example",
		"Keep-Alive", "timeout=5",
		"Te", "trailers",
		"Trailer", "X-Trailing",
		"Upgrade", "websocket",
		"Proxy-Authorization", "Basic cHJveHk=",
		"Proxy-Connection", "keep-alive",
		"Proxy-Anything", "y",
		"X-Keep", "kept",
	)
	res, body := gw.raw(t, rawRequest(http.MethodGet, "/t/v1/hop", "gateway.local", h, names, ""))
	if res.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("response = %d %q, want 200 \"ok\"", res.StatusCode, body)
	}

	got := up.only(t).Header
	for _, name := range []string{"Connection", "X-Hop", "X-Forwarded-Host", "Keep-Alive", "Te", "Trailer",
		"Upgrade", "Proxy-Authorization", "Proxy-Connection", "Proxy-Anything"} {
		if v, ok := got[name]; ok {
			t.Errorf("request: upstream got hop-by-hop %s: %q", name, v)
		}
	}
	if got.Get("X-Keep") != "kept" {
		t.Errorf("request: X-Keep = %q, want kept", got.Get("X-Keep"))
	}

	for _, name := range []string{"X-Resp-Hop", "Keep-Alive", "Proxy-Authenticate", "Proxy-Anything", "Upgrade"} {
		if v, ok := res.Header[name]; ok {
			t.Errorf("response: client got hop-by-hop %s: %q", name, v)
		}
	}
	if c := res.Header.Get("Connection"); strings.Contains(strings.ToLower(c), "x-resp-hop") {
		t.Errorf("response: Connection = %q still names upstream's hop header", c)
	}
	if res.Header.Get("X-Resp-Keep") != "kept" {
		t.Errorf("response: X-Resp-Keep = %q, want kept", res.Header.Get("X-Resp-Keep"))
	}
}

// AC15: Host is the upstream's, not the gateway's.
func TestProxy_SetsUpstreamHost(t *testing.T) {
	up := newUpstream(t, nil)
	gw := startGateway(t, up.URL, identifyWith())

	res, _ := gw.raw(t, rawRequest(http.MethodGet, "/t/v1/models", "127.0.0.1:7197", nil, nil, ""))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := up.only(t).Host; got != up.URL.Host {
		t.Errorf("upstream Host = %q, want %q", got, up.URL.Host)
	}
}

// AC16: the gateway adds nothing upstream: no X-Forwarded-*, no Forwarded, no
// request ID of its own, no User-Agent or Accept-Encoding the client did not send. A
// client X-Request-Id arrives unchanged.
func TestProxy_AddsNoHeadersUpstream(t *testing.T) {
	up := newUpstream(t, nil)
	gw := startGateway(t, up.URL, identifyWith())

	want, names := headersIn("X-Request-Id", "client-chosen-id")
	res, _ := gw.raw(t, rawRequest(http.MethodGet, "/t/v1/models", "gateway.local", want, names, ""))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	got := up.only(t).Header
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upstream headers = %v, want exactly %v", got, want)
	}
	gatewayID := res.Header.Get("X-Request-Id")
	if gatewayID == "" || gatewayID == "client-chosen-id" {
		t.Fatalf("gateway X-Request-Id = %q, want the gateway's own", gatewayID)
	}
	for name, vs := range got {
		if slices.Contains(vs, gatewayID) {
			t.Errorf("the gateway's request ID reached upstream in %s", name)
		}
	}
}

// AC20: a 64 MiB body is forwarded whole, with no gateway 413. It is generated by a
// reader and hashed on both sides, so no side holds it whole.
func TestProxy_LargeBodyNotLimited(t *testing.T) {
	const size = 64 << 20
	type result struct {
		n   int64
		sum string
	}
	got := make(chan result, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		n, err := io.Copy(h, r.Body)
		if err != nil {
			t.Errorf("upstream: reading body: %v", err)
		}
		got <- result{n, hex.EncodeToString(h.Sum(nil))}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	gw := startGateway(t, base, identifyWith())

	sent := sha256.New()
	src := io.TeeReader(io.LimitReader(rand.NewChaCha8([32]byte{1}), size), sent)
	req, err := http.NewRequest(http.MethodPost, gw.url+"/t/v1/messages", src)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = size
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no gateway limit)", res.StatusCode)
	}
	r := <-got
	if r.n != size {
		t.Errorf("upstream read %d bytes, want %d", r.n, size)
	}
	if want := hex.EncodeToString(sent.Sum(nil)); r.sum != want {
		t.Errorf("upstream body hash = %s, want %s", r.sum, want)
	}
}

// gzipped is s compressed, as an upstream would send it.
func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := io.WriteString(zw, s); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// gzipUpstream answers every request with body, gzip-encoded, with its Content-Length.
func gzipUpstream(t *testing.T, body []byte) *upstream {
	return newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

// AC21: Accept-Encoding reaches upstream as the client sent it, and the compressed
// response reaches the client byte-identical, still compressed.
func TestProxy_CompressionPassThrough(t *testing.T) {
	gz := gzipped(t, `{"type":"message","content":[]}`)
	up := gzipUpstream(t, gz)
	gw := startGateway(t, up.URL, identifyWith())

	h, names := headersIn("Accept-Encoding", "gzip, deflate, br")
	res, body := gw.raw(t, rawRequest(http.MethodPost, "/t/v1/messages", "gateway.local", h, names, ""))
	if got := up.only(t).Header.Values("Accept-Encoding"); !reflect.DeepEqual(got, []string{"gzip, deflate, br"}) {
		t.Errorf("upstream Accept-Encoding = %q, want exactly what the client sent", got)
	}
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", res.Header.Get("Content-Encoding"))
	}
	if !bytes.Equal(body, gz) {
		t.Errorf("client body is not upstream's compressed bytes (%d bytes, want %d)", len(body), len(gz))
	}
}

// AC22: a client that sent no Accept-Encoding gets none added upstream, and a gzip
// response still reaches it compressed, with Content-Encoding and Content-Length.
// Go's transport would otherwise add gzip and inflate the body on the way back.
func TestProxy_TransparentGzipDisabled(t *testing.T) {
	gz := gzipped(t, `{"type":"message","content":[]}`)
	up := gzipUpstream(t, gz)
	gw := startGateway(t, up.URL, identifyWith())

	res, body := gw.raw(t, rawRequest(http.MethodGet, "/t/v1/models", "gateway.local", nil, nil, ""))
	if v, ok := up.only(t).Header["Accept-Encoding"]; ok {
		t.Errorf("upstream got Accept-Encoding %q; the client sent none", v)
	}
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", res.Header.Get("Content-Encoding"))
	}
	if got, want := res.Header.Get("Content-Length"), strconv.Itoa(len(gz)); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if !bytes.Equal(body, gz) {
		t.Errorf("client body is not upstream's compressed bytes (%d bytes, want %d)", len(body), len(gz))
	}
}

// fixedDate is set by upstreams whose response a test compares header for header, so
// a second ticking over between two requests cannot fail it.
const fixedDate = "Sat, 26 Sep 2026 10:00:00 GMT"

// sameResponse asks upstream directly and through the gateway with the same request,
// and fails unless status, headers and body match byte for byte. The only header the
// gateway may change is X-Request-Id, which is its own; the direct answer has no
// hop-by-hop header for the gateway to strip, so nothing else may differ.
func sameResponse(t *testing.T, up *upstream, gw *gateway, req string) *http.Response {
	t.Helper()
	direct, directBody := rawAt(t, up.URL.Host, req)
	via, viaBody := gw.raw(t, req)
	if via.StatusCode != direct.StatusCode {
		t.Errorf("status = %d, want upstream's %d", via.StatusCode, direct.StatusCode)
	}
	if !bytes.Equal(viaBody, directBody) {
		t.Errorf("body = %q, want upstream's %q", viaBody, directBody)
	}
	gotHeader := via.Header.Clone()
	gotHeader.Del("X-Request-Id")
	wantHeader := direct.Header.Clone()
	wantHeader.Del("X-Request-Id")
	if !reflect.DeepEqual(gotHeader, wantHeader) {
		t.Errorf("headers differ from upstream's\n got: %v\nwant: %v", gotHeader, wantHeader)
	}
	return via
}

// AC18: status, headers and body reach the client byte-identical, apart from
// hop-by-hop headers and X-Request-Id. A response with no Content-Type must not gain
// a sniffed one on the way through.
func TestProxy_ResponseVerbatim(t *testing.T) {
	cases := []struct {
		name    string
		respond http.HandlerFunc
	}{
		{"with content type", func(w http.ResponseWriter, _ *http.Request) {
			h := w.Header()
			h.Set("Date", fixedDate)
			h.Set("Content-Type", "application/json")
			h["X-Multi"] = []string{"one", "two"}
			h.Set("X-Spaced", "a  b")
			h.Set("Cache-Control", "no-store")
			h.Set("Set-Cookie", "k=v; Path=/")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("{\"id\":\"m_1\",\"bytes\":\"\x00\xff\"}\n"))
		}},
		{"no content type", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Date", fixedDate)
			w.Header()["Content-Type"] = nil // upstream sends none and sniffs none
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "<html>not really html</html>")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t, tc.respond)
			gw := startGateway(t, up.URL, identifyWith())
			res := sameResponse(t, up, gw, rawRequest(http.MethodGet, "/t/v1/thing", up.URL.Host, nil, nil, ""))
			if tc.name == "no content type" {
				if v, ok := res.Header["Content-Type"]; ok {
					t.Errorf("client got Content-Type %q; upstream sent none", v)
				}
			}
		})
	}
}

// AC19: upstream's X-Request-Id is replaced by the gateway's, leaving exactly one,
// and a differently named request-id passes through untouched (Q6).
func TestProxy_GatewayRequestIDWinsOnResponse(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", "upstream-id")
		w.Header().Set("Request-Id", "req_upstream_1")
		w.WriteHeader(http.StatusOK)
	})
	gw := startGateway(t, up.URL, identifyWith())

	res, _ := gw.raw(t, rawRequest(http.MethodGet, "/t/v1/thing", "gateway.local", nil, nil, ""))
	ids := res.Header.Values("X-Request-Id")
	if len(ids) != 1 || ids[0] == "upstream-id" || ids[0] == "" {
		t.Errorf("X-Request-Id = %q, want exactly one, the gateway's", ids)
	}
	if got := res.Header.Values("Request-Id"); !reflect.DeepEqual(got, []string{"req_upstream_1"}) {
		t.Errorf("Request-Id = %q, want upstream's untouched", got)
	}
}

// sseUpstream sends first (one event, then a ping), flushing each, then waits for
// release before it sends second. contentType is its Content-Type.
func sseUpstream(t *testing.T, contentType string, first []string, second string) (*upstream, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		f.Flush()
		for _, ev := range first {
			_, _ = io.WriteString(w, ev)
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, second)
	})
	// Cleanups run last-in first-out: this runs before upstream closes, so a failed
	// test never leaves the handler waiting.
	t.Cleanup(unblock)
	return up, unblock
}

// AC23: each SSE event, ping included, reaches the client while upstream is still
// holding back the next one, through server.New so 000's middleware is in the path,
// and the content type is upstream's. A read that would wait for the next event
// fails at the deadline instead of hanging.
func TestProxy_StreamsSSEWithoutBuffering(t *testing.T) {
	const contentType = "text/event-stream; charset=utf-8"
	event := "event: message_start\ndata: {\"n\":1}\n\n"
	ping := "event: ping\ndata: {\"type\": \"ping\"}\n\n"
	last := "event: message_stop\ndata: {\"n\":2}\n\n"
	up, release := sseUpstream(t, contentType, []string{event, ping}, last)
	gw := startGateway(t, up.URL, identifyWith())

	conn, err := net.Dial("tcp", gw.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, rawRequest(http.MethodGet, "/t/v1/stream", "gateway.local", nil, nil, "")); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no response headers while upstream holds the stream open: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if got := res.Header.Values("Content-Type"); !reflect.DeepEqual(got, []string{contentType}) {
		t.Errorf("Content-Type = %q, want upstream's %q", got, contentType)
	}
	for _, want := range []string{event, ping} {
		got := make([]byte, len(want))
		if _, err := io.ReadFull(res.Body, got); err != nil {
			t.Fatalf("waiting for %q before upstream sends the next event: %v (got %q)", want, err, got)
		}
		if string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	release()
	rest, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the last event: %v", err)
	}
	if string(rest) != last {
		t.Errorf("last event = %q, want %q", rest, last)
	}
}

// AC26: upstream errors reach the client verbatim: status, body, retry-after,
// x-should-retry and a provider's rate-limit family. Core names no provider, so the
// rate-limit headers here carry a neutral prefix; the proxy treats every header name
// alike.
func TestProxy_UpstreamErrorsVerbatim(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, 529} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			body := `{"type":"error","error":{"type":"overloaded","message":"try later"}}`
			up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
				h := w.Header()
				h.Set("Date", fixedDate)
				h.Set("Content-Type", "application/json")
				h.Set("Retry-After", "17")
				h.Set("X-Should-Retry", "true")
				h.Set("X-Ratelimit-Requests-Limit", "50")
				h.Set("X-Ratelimit-Requests-Remaining", "0")
				h.Set("X-Ratelimit-Requests-Reset", "2026-09-26T10:00:17Z")
				h.Set("X-Ratelimit-Tokens-Remaining", "0")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, body)
			})
			gw := startGateway(t, up.URL, identifyWith())
			req := rawRequest(http.MethodPost, "/t/v1/messages", up.URL.Host, nil, nil, "")
			res := sameResponse(t, up, gw, req)
			if res.StatusCode != status {
				t.Errorf("status = %d, want %d", res.StatusCode, status)
			}
			for name, want := range map[string]string{
				"Retry-After": "17", "X-Should-Retry": "true", "X-Ratelimit-Requests-Remaining": "0",
			} {
				if got := res.Header.Values(name); !reflect.DeepEqual(got, []string{want}) {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

// Risks, Buffering: FlushInterval must stay -1 so every write is flushed at once,
// whatever the response's content type or length.
func TestProxy_FlushIntervalIsImmediate(t *testing.T) {
	base, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	p := core.NewProxy(testAdapter{}, config.Upstream{BaseURL: base}, identifyWith(), logging.New(io.Discard, "info"))
	if got := core.FlushInterval(p); got != -1 {
		t.Errorf("FlushInterval = %v, want -1", got)
	}
}

// AC36: the request line has protocol, client, stream and ttfb_ms, for a streamed
// and a non-streamed response.
func TestAccessLog_ProxyFields(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	})
	gw := startGateway(t, up.URL, identifyWith(testProfile{}))

	cases := []struct {
		path       string
		wantStream bool
	}{
		{"/t/stream", true},
		{"/t/plain", false},
	}
	for _, tc := range cases {
		h, names := headersIn("X-Test-Client", "1")
		res, _ := gw.raw(t, rawRequest(http.MethodGet, tc.path, "gateway.local", h, names, ""))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", tc.path, res.StatusCode)
		}
		line := gw.accessLine(t, tc.path)
		if line["protocol"] != "test" || line["client"] != "test-client" {
			t.Errorf("%s: protocol, client = %v, %v; want test, test-client", tc.path, line["protocol"], line["client"])
		}
		if line["stream"] != tc.wantStream {
			t.Errorf("%s: stream = %v, want %v", tc.path, line["stream"], tc.wantStream)
		}
		ttfb, ok := line["ttfb_ms"].(float64)
		if !ok || ttfb < 0 {
			t.Errorf("%s: ttfb_ms = %v, want a non-negative number", tc.path, line["ttfb_ms"])
		}
		if d, _ := line["duration_ms"].(float64); ok && ttfb > d {
			t.Errorf("%s: ttfb_ms %v exceeds duration_ms %v", tc.path, ttfb, d)
		}
	}
}
