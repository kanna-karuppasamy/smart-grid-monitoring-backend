// pkg/logger/logger.go
package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger creates a new logger
func NewLogger() *zap.Logger {
	// Determine log level from environment
	logLevel := zap.InfoLevel
	if level, err := zapcore.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil {
		logLevel = level
	}

	// Configure logger
	config := zap.Config{
		Level:       zap.NewAtomicLevelAt(logLevel),
		Development: os.Getenv("GIN_MODE") != "release",
		Encoding:    "json",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := config.Build()
	if err != nil {
		// Fall back to a basic production logger if configuration fails
		logger, _ = zap.NewProduction()
		logger.Error("Failed to initialize custom logger", zap.Error(err))
	}

	return logger
}
