package main

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// waitEntered waits for a handler to signal that it is running.
func waitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never started")
	}
}

// AC15, in-process: cancelling ctx lets a running request finish with its 200, then
// run logs gateway stopped and exits 0.
func TestRun_GracefulShutdownWaitsForInFlight(t *testing.T) {
	entered := make(chan struct{}, 1)
	g, url := served(t, func(r chi.Router) {
		r.Get("/slow", func(w http.ResponseWriter, _ *http.Request) {
			entered <- struct{}{}
			time.Sleep(200 * time.Millisecond)
			_, _ = w.Write([]byte("done"))
		})
	})

	type result struct {
		status int
		body   string
		err    error
	}
	res := make(chan result, 1)
	go func() {
		resp, err := http.Get(url + "/slow")
		if err != nil {
			res <- result{err: err}
			return
		}
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		res <- result{status: resp.StatusCode, body: string(b), err: err}
	}()
	waitEntered(t, entered)

	if code := g.stop(); code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	r := <-res
	if r.err != nil || r.status != http.StatusOK || r.body != "done" {
		t.Errorf("/slow = %d %q, %v; want 200 \"done\"", r.status, r.body, r.err)
	}
	if n := len(linesWith(g, "gateway stopped")); n != 1 {
		t.Errorf("got %d gateway stopped lines, want 1", n)
	}
	if n := len(linesWith(g, "shutdown timed out")); n != 0 {
		t.Errorf("got %d shutdown timed out lines, want none", n)
	}
}

// AC16: a handler still running when shutdown_timeout passes gives exactly one error
// line, shutdown timed out with in_flight 1, no gateway stopped, and exit 1.
func TestRun_ShutdownTimeout(t *testing.T) {
	entered := make(chan struct{}, 1)
	inTempDir(t, ptr("shutdown_timeout: 100ms\n"))
	g := startGateway(t, options{
		env:        map[string]string{"GATEWAY_LISTEN_ADDR": "127.0.0.1:0"},
		realListen: true,
		mount: []func(chi.Router){func(r chi.Router) {
			r.Get("/hang", func(_ http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				<-r.Context().Done() // until Close cuts the connection
			})
		}},
	})
	addr, _ := g.waitLine("gateway started")["addr"].(string)
	go func() {
		if resp, err := http.Get("http://" + addr + "/hang"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	waitEntered(t, entered)

	start := time.Now()
	if code := g.stop(); code != exitRuntime {
		t.Errorf("exit code = %d, want 1", code)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("shutdown took %v, want about 100ms", took)
	}
	var errs []map[string]any
	for _, l := range g.lines() {
		if l["level"] == "error" {
			errs = append(errs, l)
		}
	}
	if len(errs) != 1 || errs[0]["msg"] != "shutdown timed out" || errs[0]["in_flight"] != float64(1) {
		t.Errorf("error lines = %v, want exactly one shutdown timed out with in_flight 1", errs)
	}
	if n := len(linesWith(g, "gateway stopped")); n != 0 {
		t.Errorf("got %d gateway stopped lines, want none", n)
	}
}
