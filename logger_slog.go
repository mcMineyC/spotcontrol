package spotcontrol

import (
	"context"
	"fmt"
	"log/slog"
)

// slogLogger adapts a *slog.Logger to the spotcontrol Logger interface.
type slogLogger struct {
	l *slog.Logger
}

// NewSlogLogger returns a Logger that delegates to the provided *slog.Logger.
// Trace-level messages are mapped to slog.LevelDebug-4 (below Debug). Debug,
// Info, Warn, and Error map to their slog equivalents. WithField and WithError
// return new loggers with the additional attributes.
//
// If l is nil, slog.Default() is used.
func NewSlogLogger(l *slog.Logger) Logger {
	if l == nil {
		l = slog.Default()
	}
	return &slogLogger{l: l}
}

// slogLevelTrace is a custom level below slog.LevelDebug for Trace messages.
const slogLevelTrace = slog.LevelDebug - 4

func (s *slogLogger) Tracef(format string, args ...interface{}) {
	s.l.Log(context.Background(), slogLevelTrace, fmt.Sprintf(format, args...))
}

func (s *slogLogger) Debugf(format string, args ...interface{}) {
	s.l.Debug(fmt.Sprintf(format, args...))
}

func (s *slogLogger) Infof(format string, args ...interface{}) {
	s.l.Info(fmt.Sprintf(format, args...))
}

func (s *slogLogger) Warnf(format string, args ...interface{}) {
	s.l.Warn(fmt.Sprintf(format, args...))
}

func (s *slogLogger) Errorf(format string, args ...interface{}) {
	s.l.Error(fmt.Sprintf(format, args...))
}

func (s *slogLogger) Trace(args ...interface{}) {
	s.l.Log(context.Background(), slogLevelTrace, fmt.Sprint(args...))
}

func (s *slogLogger) Debug(args ...interface{}) {
	s.l.Debug(fmt.Sprint(args...))
}

func (s *slogLogger) Info(args ...interface{}) {
	s.l.Info(fmt.Sprint(args...))
}

func (s *slogLogger) Warn(args ...interface{}) {
	s.l.Warn(fmt.Sprint(args...))
}

func (s *slogLogger) Error(args ...interface{}) {
	s.l.Error(fmt.Sprint(args...))
}

func (s *slogLogger) WithField(key string, value interface{}) Logger {
	return &slogLogger{l: s.l.With(key, value)}
}

func (s *slogLogger) WithError(err error) Logger {
	return &slogLogger{l: s.l.With("error", err)}
}
