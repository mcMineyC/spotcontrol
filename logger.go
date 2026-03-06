package spotcontrol

// Logger is the interface used throughout spotcontrol for structured logging.
// It is compatible with logrus.Entry and similar structured loggers.
type Logger interface {
	Tracef(format string, args ...interface{})
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})

	Trace(args ...interface{})
	Debug(args ...interface{})
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})

	WithField(key string, value interface{}) Logger
	WithError(err error) Logger
}

// NullLogger is a Logger implementation that discards all output.
type NullLogger struct{}

func (l *NullLogger) Tracef(string, ...interface{}) {}
func (l *NullLogger) Debugf(string, ...interface{}) {}
func (l *NullLogger) Infof(string, ...interface{})  {}
func (l *NullLogger) Warnf(string, ...interface{})  {}
func (l *NullLogger) Errorf(string, ...interface{}) {}

func (l *NullLogger) Trace(...interface{}) {}
func (l *NullLogger) Debug(...interface{}) {}
func (l *NullLogger) Info(...interface{})  {}
func (l *NullLogger) Warn(...interface{})  {}
func (l *NullLogger) Error(...interface{}) {}

func (l *NullLogger) WithField(string, interface{}) Logger { return l }
func (l *NullLogger) WithError(error) Logger               { return l }

// ObfuscateUsername returns an obfuscated version of a username for logging.
func ObfuscateUsername(username string) string {
	if len(username) <= 3 {
		return "***"
	}
	return username[:3] + "***"
}
