package logger

import (
	"context"
	"log/slog"
)

type MultiHandler struct {
	stdoutHandler      slog.Handler
	victoriaLogsWriter *VictoriaLogsWriter
}

func NewMultiHandler(
	stdoutHandler slog.Handler,
	victoriaLogsWriter *VictoriaLogsWriter,
) *MultiHandler {
	return &MultiHandler{
		stdoutHandler:      stdoutHandler,
		victoriaLogsWriter: victoriaLogsWriter,
	}
}

func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.victoriaLogsWriter != nil {
		return level >= slog.LevelDebug
	}

	return h.stdoutHandler.Enabled(ctx, level)
}

func (h *MultiHandler) Handle(ctx context.Context, record slog.Record) error {
	// Send to stdout handler (only if level is enabled for stdout)
	if h.stdoutHandler.Enabled(ctx, record.Level) {
		if err := h.stdoutHandler.Handle(ctx, record); err != nil {
			return err
		}
	}

	// Send to VictoriaLogs if configured
	if h.victoriaLogsWriter != nil {
		attrs := make(map[string]any)
		record.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})

		h.victoriaLogsWriter.Write(record.Level.String(), record.Message, attrs)
	}

	return nil
}

func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &MultiHandler{
		stdoutHandler:      h.stdoutHandler.WithAttrs(attrs),
		victoriaLogsWriter: h.victoriaLogsWriter,
	}
}

func (h *MultiHandler) WithGroup(name string) slog.Handler {
	return &MultiHandler{
		stdoutHandler:      h.stdoutHandler.WithGroup(name),
		victoriaLogsWriter: h.victoriaLogsWriter,
	}
}
