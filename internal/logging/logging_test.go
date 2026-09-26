package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// lines parses each line of out as a JSON object, failing the test on any line that
// isn't one.
func lines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var parsed []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line is not JSON: %q", line)
		}
		parsed = append(parsed, m)
	}
	return parsed
}

func keys(m map[string]any) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	slices.Sort(k)
	return k
}

func TestNew_JSONShape(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info")
	log.Debug("dropped")
	log.Info("kept")
	log.Error("failed", zap.Error(errors.New("boom")))

	got := lines(t, buf.String())
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2 (debug filtered at info)\n%s", len(got), buf.String())
	}
	if k := keys(got[0]); !slices.Equal(k, []string{"caller", "level", "msg", "ts"}) {
		t.Errorf("info line keys = %v, want caller, level, msg, ts", k)
	}
	if got[0]["level"] != "info" || got[0]["msg"] != "kept" {
		t.Errorf("info line = %v", got[0])
	}
	ts, _ := got[0]["ts"].(string)
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("ts %q is not RFC 3339: %v", ts, err)
	}
	// An error line gets its fields and no stack.
	if k := keys(got[1]); !slices.Equal(k, []string{"caller", "error", "level", "msg", "ts"}) {
		t.Errorf("error line keys = %v, want caller, error, level, msg, ts", k)
	}
}

func TestNew_Level(t *testing.T) {
	for level, want := range map[string][]string{
		"debug": {"debug", "info", "warn", "error"},
		"info":  {"info", "warn", "error"},
		"warn":  {"warn", "error"},
		"error": {"error"},
		"bogus": {"info", "warn", "error"}, // falls back to info
	} {
		var buf bytes.Buffer
		log := New(&buf, level)
		log.Debug("debug")
		log.Info("info")
		log.Warn("warn")
		log.Error("error")
		var got []string
		for _, l := range lines(t, buf.String()) {
			got = append(got, l["msg"].(string))
		}
		if !slices.Equal(got, want) {
			t.Errorf("New(%q) logged %v, want %v", level, got, want)
		}
	}
}

func TestBootstrap_Info(t *testing.T) {
	var buf bytes.Buffer
	log := Bootstrap(&buf)
	log.Debug("dropped")
	log.Info("kept")
	if got := lines(t, buf.String()); len(got) != 1 || got[0]["msg"] != "kept" {
		t.Errorf("Bootstrap logged %v, want only the info line", got)
	}
}

func TestStdLog(t *testing.T) {
	var buf bytes.Buffer
	StdLog(New(&buf, "info")).Printf("http: TLS handshake error from %s", "127.0.0.1:1")

	got := lines(t, buf.String())
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1\n%s", len(got), buf.String())
	}
	want := map[string]any{"level": "warn", "logger": "http", "msg": "http: TLS handshake error from 127.0.0.1:1"}
	for k, v := range want {
		if got[0][k] != v {
			t.Errorf("%s = %v, want %v", k, got[0][k], v)
		}
	}
}

// failingWriter fails every write, so zap reports it on its error output.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestErrorOutput_JSON(t *testing.T) {
	var errOut bytes.Buffer
	newLogger(failingWriter{}, &errOut, "info").Info("lost")

	got := lines(t, errOut.String())
	if len(got) != 1 {
		t.Fatalf("got %d lines on error output, want 1\n%s", len(got), errOut.String())
	}
	if got[0]["level"] != "error" || got[0]["msg"] != "logger error" {
		t.Errorf("line = %v, want level error, msg logger error", got[0])
	}
	if detail, _ := got[0]["detail"].(string); !strings.Contains(detail, "disk full") {
		t.Errorf("detail = %q, want it to carry zap's message", detail)
	}
	ts, _ := got[0]["ts"].(string)
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("ts %q is not RFC 3339: %v", ts, err)
	}
}
