package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type lokiBatch struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

type LokiHook struct {
	url     string
	service string
	client  *http.Client
	mu      sync.Mutex
	buf     [][]string
}

func NewLokiHook(pushURL, service string) *LokiHook {
	if pushURL == "" || service == "" {
		return nil
	}
	h := &LokiHook{
		url:     pushURL,
		service: service,
		client:  &http.Client{Timeout: 4 * time.Second},
	}
	go h.flushLoop()
	return h
}

func RegisterLokiHookIfConfigured(logger *logrus.Logger, service string) {
	u := os.Getenv("LOKI_PUSH_URL")
	if u == "" {
		return
	}
	if service == "" {
		service = "fookie"
	}
	h := NewLokiHook(u, service)
	if h == nil {
		return
	}
	logger.AddHook(h)
}

func (h *LokiHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel,
		logrus.WarnLevel, logrus.InfoLevel, logrus.DebugLevel, logrus.TraceLevel,
	}
}

func (h *LokiHook) Fire(e *logrus.Entry) error {
	if h == nil {
		return nil
	}
	line := e.Message
	if len(e.Data) > 0 {
		line = fmt.Sprintf("%s %v", line, e.Data)
	}
	ts := strconv.FormatInt(time.Now().UnixNano(), 10)
	level := e.Level.String()
	h.mu.Lock()
	h.buf = append(h.buf, []string{ts, line, level})
	if len(h.buf) >= 80 {
		b := h.buf
		h.buf = nil
		h.mu.Unlock()
		h.pushBatch(b)
		return nil
	}
	h.mu.Unlock()
	return nil
}

func (h *LokiHook) flushLoop() {
	t := time.NewTicker(750 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		h.mu.Lock()
		if len(h.buf) == 0 {
			h.mu.Unlock()
			continue
		}
		b := h.buf
		h.buf = nil
		h.mu.Unlock()
		h.pushBatch(b)
	}
}

func (h *LokiHook) pushBatch(rows [][]string) {
	if len(rows) == 0 {
		return
	}
	byLevel := map[string][][]string{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		lvl := r[2]
		byLevel[lvl] = append(byLevel[lvl], []string{r[0], r[1]})
	}
	var streams []lokiStream
	for lvl, vals := range byLevel {
		if len(vals) == 0 {
			continue
		}
		streams = append(streams, lokiStream{
			Stream: map[string]string{
				"job":     h.service,
				"service": h.service,
				"level":   lvl,
			},
			Values: vals,
		})
	}
	if len(streams) == 0 {
		return
	}
	body, err := json.Marshal(lokiBatch{Streams: streams})
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	_, _ = h.client.Do(req)
}
