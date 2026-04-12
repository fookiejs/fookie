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

func (l *LoggerWrapper) Info(msg string, args ...interface{}) {
	if len(args) > 0 {
		l.Logger.Infof(msg, args...)
	} else {
		l.Logger.Info(msg)
	}
}

func (l *LoggerWrapper) Error(msg string, args ...interface{}) {
	if len(args) > 0 {
		l.Logger.Errorf(msg, args...)
	} else {
		l.Logger.Error(msg)
	}
}

func (l *LoggerWrapper) Warn(msg string, args ...interface{}) {
	if len(args) > 0 {
		l.Logger.Warnf(msg, args...)
	} else {
		l.Logger.Warn(msg)
	}
}

func NewLoggerWrapper(logger *logrus.Logger) Logger {
	return &LoggerWrapper{logger}
}
