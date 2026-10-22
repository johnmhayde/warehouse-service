package server

import (
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
// next should be the ServeMux itself: ServeMux sets r.Pattern on the request
// it is given, which is how the matched route reaches http.route. A
// middleware in between that passes on a copy of the request (WithContext)
// would hide it.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		ctx, event := telemetry.NewContext(r.Context())
		r2 := r.WithContext(ctx)

		next.ServeHTTP(rec, r2)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		attrs := []slog.Attr{
			slog.String("http.method", r.Method),
			// The matched route, e.g. "GET /healthz". It is empty when
			// nothing matched (404).
			slog.String("http.route", r2.Pattern),
			slog.String("http.path", r.URL.Path),
			slog.Int("http.status", rec.status),
			slog.Int("http.response_bytes", rec.bytes),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
		}
		logger.LogAttrs(r.Context(), slog.LevelInfo, "http request", append(attrs, event.Attrs()...)...)
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
