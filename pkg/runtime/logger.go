package runtime

import "github.com/sirupsen/logrus"

type Logger interface {
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

type LoggerWrapper struct {
	*logrus.Logger
}

// Info logs a message with optional structured key-value pairs.
// Caller convention: Info("msg", "key1", val1, "key2", val2, ...)
// NOT printf-style — there are no format verbs in the message.
func (l *LoggerWrapper) Info(msg string, args ...interface{}) {
	if len(args) == 0 {
		l.Logger.Info(msg)
		return
	}
	entry := l.Logger.WithFields(kvsToFields(args))
	entry.Info(msg)
}

func (l *LoggerWrapper) Error(msg string, args ...interface{}) {
	if len(args) == 0 {
		l.Logger.Error(msg)
		return
	}
	entry := l.Logger.WithFields(kvsToFields(args))
	entry.Error(msg)
}

func (l *LoggerWrapper) Warn(msg string, args ...interface{}) {
	if len(args) == 0 {
		l.Logger.Warn(msg)
		return
	}
	entry := l.Logger.WithFields(kvsToFields(args))
	entry.Warn(msg)
}

// kvsToFields converts alternating key-value pairs ["key", val, ...] to logrus.Fields.
func kvsToFields(args []interface{}) map[string]interface{} {
	fields := make(map[string]interface{}, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		key, _ := args[i].(string)
		fields[key] = args[i+1]
	}
	return fields
}

func NewLoggerWrapper(logger *logrus.Logger) Logger {
	return &LoggerWrapper{logger}
}
