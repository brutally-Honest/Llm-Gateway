package server

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// requestIDHeader is the response header that carries the request ID.
const requestIDHeader = "X-Request-Id"

type requestIDKey struct{}

// RequestID is the ID the requestID middleware gave this request, or "" outside one.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// requestID gives every request a server-generated ID: 128 random bits, 26 base32
// characters. An incoming X-Request-Id is untrusted and ignored, but left in
// r.Header, because what passthrough forwards is 001's call. chi's
// middleware.RequestID is not used because it trusts the incoming header.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := rand.Text()
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// accessLog logs one line per request. It logs r.URL.Path only, never the query,
// because some providers put keys there, and no headers or body.
func accessLog(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			// Keeps http.Flusher, which 001's streams need.
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("request",
				zap.String("request_id", RequestID(r.Context())),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Int("bytes", ww.BytesWritten()),
				zap.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				zap.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// recoverer turns a handler panic into one error line and, if nothing has been
// written yet, a 500. http.ErrAbortHandler is re-panicked so net/http aborts the
// response silently, as it does by default.
func recoverer(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww, ok := w.(middleware.WrapResponseWriter)
			if !ok {
				ww = middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			}
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				log.Error("panic recovered",
					zap.String("request_id", RequestID(r.Context())),
					zap.Any("panic", v),
					zap.String("stack", string(debug.Stack())),
				)
				if ww.Status() == 0 {
					ww.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
