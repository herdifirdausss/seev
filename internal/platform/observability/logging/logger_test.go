package logger

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew_JSONFormat(t *testing.T) {
	l := New(Config{Level: "info", Format: "json"})
	assert.NotNil(t, l)
	assert.Equal(t, l, slog.Default())
}

func TestNew_TextFormat(t *testing.T) {
	l := New(Config{Level: "info", Format: "text"})
	assert.NotNil(t, l)
}

func TestNew_AllLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error", "unknown"}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			l := New(Config{Level: level, Format: "json"})
			assert.NotNil(t, l)
		})
	}
}

func TestParseLevel(t *testing.T) {
	assert.Equal(t, slog.LevelDebug, parseLevel("debug"))
	assert.Equal(t, slog.LevelInfo, parseLevel("info"))
	assert.Equal(t, slog.LevelWarn, parseLevel("warn"))
	assert.Equal(t, slog.LevelError, parseLevel("error"))
	assert.Equal(t, slog.LevelInfo, parseLevel(""))
	assert.Equal(t, slog.LevelInfo, parseLevel("unknown"))
}

func TestWithContext_AndFromContext(t *testing.T) {
	base := New(Config{Level: "info", Format: "json"})
	child := base.With("request_id", "abc-123")

	ctx := context.Background()
	ctx = WithContext(ctx, child)

	retrieved := FromContext(ctx)
	assert.Equal(t, child, retrieved)
}

func TestFromContext_FallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	l := FromContext(ctx)
	// Should return slog.Default(), not nil
	assert.NotNil(t, l)
}

func TestFromContext_NilValue(t *testing.T) {
	// Storing nil explicitly should still return the default
	ctx := context.WithValue(context.Background(), loggerKey, (*slog.Logger)(nil))
	l := FromContext(ctx)
	assert.NotNil(t, l)
	assert.Equal(t, slog.Default(), l)
}

func TestWith_ReturnsChildLogger(t *testing.T) {
	base := New(Config{Level: "info", Format: "json"})
	ctx := WithContext(context.Background(), base)

	child := With(ctx, "user_id", "u-1", "trace_id", "t-1")
	assert.NotNil(t, child)
}

func TestWith_NoLoggerInContext(t *testing.T) {
	// Should use default logger and not panic
	child := With(context.Background(), "key", "val")
	assert.NotNil(t, child)
}
