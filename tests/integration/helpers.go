package integration

import (
	"database/sql"
	"testing"
)

func OpenDB(driver, dsn string) (*sql.DB, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	return db, nil
}

type testLogger struct {
	t *testing.T
}

func (l *testLogger) Info(msg string, args ...interface{})  {
	l.t.Logf("INFO  "+msg, args...)
}

func (l *testLogger) Warn(msg string, args ...interface{})  {
	l.t.Logf("WARN  "+msg, args...)
}

func (l *testLogger) Error(msg string, args ...interface{}) {
	l.t.Logf("ERROR "+msg, args...)
}
