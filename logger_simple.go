package spotcontrol

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// simpleLogger implements Logger using fmt output to a configurable writer.
// It provides a ready-made Logger so users don't need to write their own
// adapter for basic console logging.
type simpleLogger struct {
	w      io.Writer
	prefix string
}

// NewSimpleLogger returns a Logger that writes human-readable log lines to w.
// If w is nil, os.Stderr is used. Trace-level messages are suppressed.
//
// This is intended as a convenient default for quick prototyping and examples.
// For production use, consider NewSlogLogger or a custom Logger implementation.
func NewSimpleLogger(w io.Writer) Logger {
	if w == nil {
		w = os.Stderr
	}
	return &simpleLogger{w: w}
}

func (l *simpleLogger) Tracef(format string, args ...interface{}) {
	// Trace is intentionally suppressed in the simple logger.
}

func (l *simpleLogger) Debugf(format string, args ...interface{}) {
	l.logf("DBG", format, args...)
}

func (l *simpleLogger) Infof(format string, args ...interface{}) {
	l.logf("INF", format, args...)
}

func (l *simpleLogger) Warnf(format string, args ...interface{}) {
	l.logf("WRN", format, args...)
}

func (l *simpleLogger) Errorf(format string, args ...interface{}) {
	l.logf("ERR", format, args...)
}

func (l *simpleLogger) Trace(args ...interface{}) {
	// Trace is intentionally suppressed in the simple logger.
}

func (l *simpleLogger) Debug(args ...interface{}) {
	l.log("DBG", args...)
}

func (l *simpleLogger) Info(args ...interface{}) {
	l.log("INF", args...)
}

func (l *simpleLogger) Warn(args ...interface{}) {
	l.log("WRN", args...)
}

func (l *simpleLogger) Error(args ...interface{}) {
	l.log("ERR", args...)
}

func (l *simpleLogger) WithField(key string, value interface{}) Logger {
	newPrefix := fmt.Sprintf("%s[%s=%v]", l.prefix, key, value)
	return &simpleLogger{w: l.w, prefix: newPrefix}
}

func (l *simpleLogger) WithError(err error) Logger {
	newPrefix := fmt.Sprintf("%s[err=%v]", l.prefix, err)
	return &simpleLogger{w: l.w, prefix: newPrefix}
}

func (l *simpleLogger) logf(level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if l.prefix != "" {
		fmt.Fprintf(l.w, "[%s] %s %s\n", level, l.prefix, msg)
	} else {
		fmt.Fprintf(l.w, "[%s] %s\n", level, msg)
	}
}

func (l *simpleLogger) log(level string, args ...interface{}) {
	msg := strings.TrimRight(fmt.Sprintln(args...), "\n")
	if l.prefix != "" {
		fmt.Fprintf(l.w, "[%s] %s %s\n", level, l.prefix, msg)
	} else {
		fmt.Fprintf(l.w, "[%s] %s\n", level, msg)
	}
}
