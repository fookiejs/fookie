package telemetry

import (
	"net/http"
	"os"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	promMu       sync.Mutex
	promReg      *prometheus.Registry
	promService  string
	execCounter  *prometheus.CounterVec
	execDuration *prometheus.HistogramVec
	execInFlight *prometheus.GaugeVec
)

func InitPrometheus(service string) {
	promMu.Lock()
	defer promMu.Unlock()
	if promReg != nil {
		return
	}
	if service == "" {
		service = "fookie"
	}
	promService = service
	promReg = prometheus.NewRegistry()

	execCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fookie_executor_operations_total",
			Help: "Fookie executor operations by model, operation, and result.",
		},
		[]string{"service", "model", "operation", "result"},
	)
	execDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fookie_executor_operation_duration_seconds",
			Help:    "Wall time for executor operations (includes both ok and error).",
			Buckets: []float64{.0005, .001, .002, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"service", "model", "operation", "result"},
	)
	execInFlight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "fookie_executor_in_flight",
			Help: "In-flight executor operations by model and operation.",
		},
		[]string{"service", "model", "operation"},
	)
	promReg.MustRegister(execCounter, execDuration, execInFlight)
}

func RecordExecutorOperation(model, operation, result string, seconds float64) {
	promMu.Lock()
	reg := promReg
	svc := promService
	c := execCounter
	h := execDuration
	promMu.Unlock()
	if reg == nil || c == nil {
		return
	}
	c.WithLabelValues(svc, model, operation, result).Inc()
	if h != nil && seconds >= 0 {
		h.WithLabelValues(svc, model, operation, result).Observe(seconds)
	}
}

// BeginExecutorOp increments the in-flight gauge for the given model/operation
// and returns a function that decrements it. Use with defer at the top of every
// executor entry point:
//
//	defer telemetry.BeginExecutorOp(modelName, "create")()
func BeginExecutorOp(model, operation string) func() {
	promMu.Lock()
	reg := promReg
	svc := promService
	g := execInFlight
	promMu.Unlock()
	if reg == nil || g == nil {
		return func() {}
	}
	g.WithLabelValues(svc, model, operation).Inc()
	return func() {
		g.WithLabelValues(svc, model, operation).Dec()
	}
}

func MetricsHandler() http.Handler {
	promMu.Lock()
	reg := promReg
	promMu.Unlock()
	if reg == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("metrics not initialised"))
		})
	}
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
}

func ServiceName() string {
	if s := os.Getenv("FOOKEE_SERVICE_NAME"); s != "" {
		return s
	}
	return promService
}
