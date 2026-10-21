package server

import (
	"log/slog"
	"net/http"
	"time"
)

// logRequests emits one structured log line per request once it completes.
// It is the seed of the wide-event middleware: later it will create an event
// in the request context that handlers enrich, and emit that instead.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		logger.LogAttrs(r.Context(), slog.LevelInfo, "http request",
			slog.String("http.method", r.Method),
			// ServeMux sets r.Pattern to the matched route, e.g.
			// "GET /healthz". It is empty when nothing matched (404).
			slog.String("http.route", r.Pattern),
			slog.String("http.path", r.URL.Path),
			slog.Int("http.status", rec.status),
			slog.Int("http.response_bytes", rec.bytes),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
		)
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
