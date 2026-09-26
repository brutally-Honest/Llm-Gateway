package core

import "io"

// Test-only doors to the unexported body watchers, for package core_test. The proxy
// wraps the bodies itself; tests reach the same code through these.

func WatchRequestBody(m *Meta, rc io.ReadCloser) io.ReadCloser  { return m.watchRequestBody(rc) }
func WatchResponseBody(m *Meta, rc io.ReadCloser) io.ReadCloser { return m.watchResponseBody(rc) }
func RequestBodyErr(m *Meta) error                              { return m.requestBodyErr() }
func ResponseBodyErr(m *Meta) error                             { return m.responseBodyErr() }
