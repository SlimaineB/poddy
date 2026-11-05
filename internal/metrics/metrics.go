package metrics

import (
	"context"
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// WorkspaceInfo is the small subset of a workspace needed for metrics. It keeps
// this package independent from the Kubernetes manager.
type WorkspaceInfo struct {
	Phase    string
	Template string
}

// WorkspaceLister returns the current workspaces. It is called on scrape.
type WorkspaceLister func(context.Context) ([]WorkspaceInfo, error)

type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge

	workspaceOps           *prometheus.CounterVec
	workspaceCreateSeconds *prometheus.HistogramVec
	workspaces             *prometheus.Desc

	authAttempts   *prometheus.CounterVec
	templateReload *prometheus.CounterVec

	workspaceLister WorkspaceLister
}

func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "poddy_http_requests_total",
			Help: "Total HTTP requests handled by the portal.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "poddy_http_request_duration_seconds",
			Help:    "HTTP request duration handled by the portal.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		}, []string{"method", "route"}),
		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "poddy_http_in_flight_requests",
			Help: "HTTP requests currently being handled by the portal.",
		}),
		workspaceOps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "poddy_workspace_operations_total",
			Help: "Workspace lifecycle operations attempted by the portal.",
		}, []string{"operation", "result"}),
		workspaceCreateSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "poddy_workspace_create_duration_seconds",
			Help:    "Time spent creating a workspace.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		}, []string{"template", "result"}),
		workspaces: prometheus.NewDesc(
			"poddy_workspaces",
			"Current workspaces by phase and template.",
			[]string{"phase", "template"},
			nil,
		),
		authAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "poddy_auth_attempts_total",
			Help: "Authentication attempts by method and result.",
		}, []string{"method", "result"}),
		templateReload: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "poddy_template_reloads_total",
			Help: "Template catalog reloads by result.",
		}, []string{"result"}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests,
		m.httpDuration,
		m.httpInFlight,
		m.workspaceOps,
		m.workspaceCreateSeconds,
		m.authAttempts,
		m.templateReload,
		m,
	)
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "poddy_build_info",
		Help: "Build information for the running portal.",
		ConstLabels: prometheus.Labels{
			"version":    version(),
			"go_version": runtime.Version(),
		},
	}, func() float64 { return 1 }))
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

func (m *Metrics) SetWorkspaceLister(lister WorkspaceLister) {
	m.workspaceLister = lister
}

func (m *Metrics) ObserveHTTP(method, route string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	m.httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

func (m *Metrics) HTTPInFlight(delta float64) {
	if m == nil {
		return
	}
	m.httpInFlight.Add(delta)
}

func (m *Metrics) WorkspaceOperation(operation, result string) {
	if m == nil {
		return
	}
	m.workspaceOps.WithLabelValues(operation, result).Inc()
}

func (m *Metrics) WorkspaceCreate(template, result string, duration time.Duration) {
	if m == nil {
		return
	}
	m.workspaceCreateSeconds.WithLabelValues(template, result).Observe(duration.Seconds())
}

func (m *Metrics) AuthAttempt(method, result string) {
	if m == nil {
		return
	}
	m.authAttempts.WithLabelValues(method, result).Inc()
}

func (m *Metrics) TemplateReload(result string) {
	if m == nil {
		return
	}
	m.templateReload.WithLabelValues(result).Inc()
}

// Describe implements prometheus.Collector for the workspace gauge.
func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.workspaces
}

// Collect implements prometheus.Collector. Kubernetes is queried at scrape time
// so the phase gauge reflects pods that changed outside the portal.
func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	if m.workspaceLister == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	list, err := m.workspaceLister(ctx)
	if err != nil {
		return
	}
	counts := map[[2]string]int{}
	for _, workspace := range list {
		phase := workspace.Phase
		if phase == "" {
			phase = "Unknown"
		}
		template := workspace.Template
		if template == "" {
			template = "unknown"
		}
		counts[[2]string{phase, template}]++
	}
	for key, count := range counts {
		ch <- prometheus.MustNewConstMetric(m.workspaces, prometheus.GaugeValue, float64(count), key[0], key[1])
	}
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
