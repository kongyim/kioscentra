package ipc

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}
type LogBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
}

func (b *LogBuffer) Snapshot() []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]LogEntry{}, b.entries...)
}

type handler struct {
	next   slog.Handler
	buffer *LogBuffer
}

func (b *LogBuffer) Handler(next slog.Handler) slog.Handler       { return &handler{next, b} }
func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	// Attribute values (credentials, URLs or arbitrary errors) never cross the UI boundary.
	h.buffer.mu.Lock()
	h.buffer.entries = append(h.buffer.entries, LogEntry{r.Time, r.Level.String(), r.Message})
	if len(h.buffer.entries) > 100 {
		h.buffer.entries = h.buffer.entries[len(h.buffer.entries)-100:]
	}
	h.buffer.mu.Unlock()
	return h.next.Handle(ctx, r)
}
func (h *handler) WithAttrs(a []slog.Attr) slog.Handler {
	return &handler{h.next.WithAttrs(a), h.buffer}
}
func (h *handler) WithGroup(g string) slog.Handler { return &handler{h.next.WithGroup(g), h.buffer} }
