package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/johnmhayde/warehouse-service/api/internal/telemetry"
)

// logRequests emits one structured log line per request once it completes:
// the request's wide event. It puts a telemetry.Event in the request context
// before calling next, and the attributes handlers add to it with
// telemetry.Add are appended to that line.
//
// The ServeMux inside next must be wrapped in recordRoute, or http.route is
// empty.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		ctx, event := telemetry.NewContext(r.Context())
		route := new(string)
		ctx = context.WithValue(ctx, routeKey{}, route)

		next.ServeHTTP(rec, r.WithContext(ctx))

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		attrs := []slog.Attr{
			slog.String("http.method", r.Method),
			// The matched route, e.g. "GET /healthz". It is empty when
			// nothing matched (404).
			slog.String("http.route", *route),
			slog.String("http.path", r.URL.Path),
			slog.Int("http.status", rec.status),
			slog.Int("http.response_bytes", rec.bytes),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
		}
		logger.LogAttrs(r.Context(), slog.LevelInfo, "http request", append(attrs, event.Attrs()...)...)
	})
}

type routeKey struct{}

// recordRoute wraps the ServeMux and reports the route it matched back to
// logRequests. ServeMux sets r.Pattern only on the request it is given, and
// middleware in between (identity.DevHeader) passes it a copy made by
// WithContext, so logRequests can't read r.Pattern from its own request.
func recordRoute(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		if route, ok := r.Context().Value(routeKey{}).(*string); ok {
			*route = r.Pattern
		}
	})
}

// statusRecorder wraps a ResponseWriter to capture the status code and
// response size for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer (for
// Flush, deadlines and so on).
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
