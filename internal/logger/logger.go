package logger

import (
	"log/slog"
)

var Logger *slog.Logger

func init() {
	Logger = slog.Default()
}

// LogInfo logs an informational message with optional key-value pairs
func LogInfo(msg string, args ...any) {
	Logger.Info(msg, args...)
}

// LogError logs an error message with optional key-value pairs
func LogError(msg string, args ...any) {
	Logger.Error(msg, args...)
}
