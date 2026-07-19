package logger

import (
	"context"
	"log/slog"
	"os"
)

type ctxKeyLogger struct{}

var loggerKey = ctxKeyLogger{}

// New creates a structured logger from the given config.
// Registers it as the slog default.
func New(cfg Config) *slog.Logger {
	level := parseLevel(cfg.Level)

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level == slog.LevelDebug,
	}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	l := slog.New(handler).With(
		"service", cfg.AppName,
		"env", cfg.Env,
		"pod", os.Getenv("HOSTNAME"),
	)
	slog.SetDefault(l)
	return l
}

// WithContext stores logger in ctx for request-scoped logging.
func WithContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// FromContext retrieves the logger from ctx; falls back to slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	return FromContextOrDefault(ctx, slog.Default())
}

// FromContextOrDefault retrieves the logger from ctx, falling back to
// fallback (rather than slog.Default()) when ctx carries none — lets a
// caller that already holds its own service-scoped base logger (e.g.
// middleware.WithLogger's captured log) avoid silently dropping to the
// global default when no per-request logger was set upstream.
func FromContextOrDefault(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if ctx == nil {
		return fallback
	}

	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}

	return fallback
}

// With returns a new logger from ctx with extra key-value pairs attached.
func With(ctx context.Context, args ...any) *slog.Logger {
	return FromContext(ctx).With(args...)
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default: // "info" and anything else
		return slog.LevelInfo
	}
}
