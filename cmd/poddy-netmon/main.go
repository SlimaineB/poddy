package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"poddy/internal/netmon"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var (
		metricsAddr    = flag.String("metrics-addr", ":9102", "Prometheus metrics listen address")
		namespace      = flag.String("namespace", envDefault("PODDY_WORKSPACE_NAMESPACE", "poddy-workspaces"), "namespace containing workspace pods and PVCs")
		nodeName       = flag.String("node-name", os.Getenv("NODE_NAME"), "only monitor pods scheduled on this node")
		region         = flag.String("region", regionDefault(), "region label for metrics")
		kubeconfig     = flag.String("kubeconfig", "", "kubeconfig path; empty uses in-cluster config")
		iface          = flag.String("interface", "", "capture interface; empty captures all interfaces")
		captureEnabled = flag.Bool("capture", true, "enable packet capture")
		refresh        = flag.Duration("refresh", 15*time.Second, "Kubernetes index refresh interval")
		mountRefresh   = flag.Duration("mount-refresh", 30*time.Second, "storage mount refresh interval")
		statsInterval  = flag.Duration("stats-interval", time.Minute, "log capture statistics interval")
		endpointCap    = flag.Int("endpoint-cap", 8192, "maximum number of network flow label sets kept in memory")
	)
	flag.Parse()

	cfg, err := kubernetesConfig(*kubeconfig)
	if err != nil {
		return err
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}

	pods := netmon.NewPodIndex(client, *namespace, *nodeName)
	mounts := netmon.NewMountIndex(client, *namespace, *nodeName)
	metrics := netmon.NewMetrics(*endpointCap)
	metrics.SetRegion(*region)
	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics)

	collector := netmon.NewCollector(*region, pods, mounts, metrics)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go pods.Run(ctx, *refresh)
	go mounts.Run(ctx, *mountRefresh)
	go syncMounts(ctx, mounts, metrics, *mountRefresh)

	var capture *netmon.Capture
	if *captureEnabled {
		capture, err = netmon.OpenCapture(*iface)
		if err != nil {
			return fmt.Errorf("open capture: %w", err)
		}
		defer capture.Close()
		go captureLoop(ctx, capture, collector)
	}

	var ready atomic.Bool
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				if pods.Synced() && mounts.Synced() {
					ready.Store(true)
				}
			}
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "indexes are not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	server := &http.Server{
		Addr:              *metricsAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go logStats(ctx, collector, *statsInterval)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

func captureLoop(ctx context.Context, capture *netmon.Capture, collector *netmon.Collector) {
	for {
		frame, timestamp, err := capture.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("capture read: %v", err)
			time.Sleep(time.Second)
			continue
		}
		collector.HandleFrame(frame, timestamp)
	}
}

func syncMounts(ctx context.Context, mounts *netmon.MountIndex, metrics *netmon.Metrics, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	time.Sleep(time.Second)
	metrics.SetMounts(mounts.Mounts())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			metrics.SetMounts(mounts.Mounts())
		}
	}
}

func logStats(ctx context.Context, collector *netmon.Collector, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := collector.Stats()
			log.Printf("netmon frames=%d parsed=%d ignored=%d duplicate=%d flows=%d",
				stats.Frames, stats.Parsed, stats.Ignored, stats.Duplicate, stats.Flows)
		}
	}
}

func kubernetesConfig(kubeconfig string) (*rest.Config, error) {
	if strings.TrimSpace(kubeconfig) != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster Kubernetes config: %w", err)
	}
	return cfg, nil
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func regionDefault() string {
	if value := strings.TrimSpace(os.Getenv("PODDY_REGION")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("PODDY_REGION_NAME")); value != "" {
		return value
	}
	return "local"
}
