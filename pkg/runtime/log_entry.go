package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Timestamp     time.Time              `json:"ts"`
	Level         string                 `json:"level"`
	RootRequestID string                 `json:"root_request_id"`
	TraceID       string                 `json:"trace_id,omitempty"`
	SpanID        string                 `json:"span_id,omitempty"`
	Operation     string                 `json:"operation,omitempty"`
	Model         string                 `json:"model,omitempty"`
	Block         string                 `json:"block,omitempty"`
	Depth         int                    `json:"depth"`
	LineNo        int                    `json:"line_no,omitempty"`
	Message       string                 `json:"msg"`
	Fields        map[string]interface{} `json:"fields,omitempty"`
	Source        string                 `json:"source"`
}

type LogSink interface {
	Emit(LogEntry)
}

func NewSink() LogSink {
	switch strings.ToLower(os.Getenv("LOG_FORMAT")) {
	case "json":
		return &JSONSink{enc: json.NewEncoder(os.Stdout)}
	default:
		return &PrettySink{}
	}
}

type JSONSink struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (s *JSONSink) Emit(e LogEntry) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.enc.Encode(e)
}

type PrettySink struct {
	mu sync.Mutex
}

const (
	cReset  = "\033[0m"
	cDim    = "\033[2m"
	cCyan   = "\033[36m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cRed    = "\033[31m"
	cBlue   = "\033[34m"
	cMag    = "\033[35m"
)

func (s *PrettySink) Emit(e LogEntry) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	levelColor := cGreen
	switch e.Level {
	case "warn":
		levelColor = cYellow
	case "error":
		levelColor = cRed
	}

	icon := "📝"
	if e.Source == "runtime" {
		icon = "⚙️ "
	}

	shortRoot := e.RootRequestID
	if len(shortRoot) > 8 {
		shortRoot = shortRoot[:8]
	}

	ctxParts := []string{}
	if e.Operation != "" {
		if e.Model != "" {
			ctxParts = append(ctxParts, e.Operation+" "+e.Model)
		} else {
			ctxParts = append(ctxParts, e.Operation)
		}
	}
	if e.Block != "" {
		ctxParts = append(ctxParts, e.Block)
	}
	ctxStr := strings.Join(ctxParts, "/")

	shortSpan := e.SpanID
	if len(shortSpan) > 8 {
		shortSpan = shortSpan[:8]
	}
	rootSpanPart := "[root=" + shortRoot
	if shortSpan != "" {
		rootSpanPart += " span=" + shortSpan
	}
	rootSpanPart += fmt.Sprintf(" d=%d]", e.Depth)

	line := fmt.Sprintf(
		"%s %s%s%s %s%-5s%s %s%s%s %s%s%s",
		icon,
		cDim, e.Timestamp.Format("15:04:05.000"), cReset,
		levelColor, strings.ToUpper(e.Level), cReset,
		cDim, rootSpanPart, cReset,
		cCyan, ctxStr, cReset,
	)

	if e.LineNo > 0 {
		line += fmt.Sprintf(" %sL%d%s", cDim, e.LineNo, cReset)
	}

	if e.Message != "" {
		line += fmt.Sprintf("  %q", e.Message)
	}

	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			line += fmt.Sprintf(" %s%s%s=%v", cMag, k, cReset, e.Fields[k])
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintln(os.Stdout, line)
}

func buildLogPayload(args []interface{}) (string, map[string]interface{}) {
	if len(args) == 0 {
		return "", nil
	}
	var fields map[string]interface{}
	msgArgs := args
	if m, ok := args[len(args)-1].(map[string]interface{}); ok {
		fields = m
		msgArgs = args[:len(args)-1]
	}
	if len(msgArgs) == 0 {
		return "", fields
	}
	if len(msgArgs) == 1 {
		if s, ok := msgArgs[0].(string); ok {
			return s, fields
		}
	}
	parts := make([]string, len(msgArgs))
	for i, a := range msgArgs {
		parts[i] = fmt.Sprintf("%v", a)
	}
	return strings.Join(parts, " "), fields
}
