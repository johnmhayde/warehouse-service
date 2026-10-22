// Package telemetry builds the request's wide event: one set of attributes
// per request that middleware and handlers add to, emitted once when the
// request completes.
//
// For now the event is emitted on the server's single per-request log line.
// OpenTelemetry spans come with WHS-201.
package telemetry

import (
	"context"
	"log/slog"
	"sync"
)

// Event collects the attributes for one request. It is safe for concurrent
// use, so a handler may add to it from goroutines it starts.
type Event struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

type contextKey struct{}

// NewContext returns a copy of ctx carrying a new, empty Event, and the
// Event itself so the caller can emit it.
func NewContext(ctx context.Context) (context.Context, *Event) {
	e := &Event{}
	return context.WithValue(ctx, contextKey{}, e), e
}

// Add appends attrs to the Event in ctx. It does nothing if ctx has no
// Event, so code that enriches the event works outside a request too.
func Add(ctx context.Context, attrs ...slog.Attr) {
	e, ok := ctx.Value(contextKey{}).(*Event)
	if !ok {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attrs = append(e.attrs, attrs...)
}

// Attrs returns a copy of the attributes added so far, in the order they
// were added.
func (e *Event) Attrs() []slog.Attr {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]slog.Attr(nil), e.attrs...)
}
