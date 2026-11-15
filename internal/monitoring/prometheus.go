package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHistoryRange = time.Hour
	maxHistoryRange     = 7 * 24 * time.Hour
	minHistoryStep      = 15 * time.Second
	maxHistoryPoints    = 720
)

var (
	// ErrPrometheusNotConfigured is returned when history is disabled or the
	// Prometheus URL is empty.
	ErrPrometheusNotConfigured = errors.New("Prometheus history is not configured")
)

// PrometheusClient queries a Prometheus-compatible HTTP API. Queries are built
// server-side from fixed templates; user input never becomes PromQL.
type PrometheusClient struct {
	baseURL string
	token   string
	http    *http.Client
	now     func() time.Time
}

func NewPrometheusClient(rawURL, token string) (*PrometheusClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if baseURL == "" {
		return nil, ErrPrometheusNotConfigured
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("invalid Prometheus URL")
	}
	return &PrometheusClient{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
		now: time.Now,
	}, nil
}

type seriesQuery struct {
	key   string
	label string
	unit  string
	query func(selector string) string
}

var workspaceSeries = []seriesQuery{
	{
		key:   "cpu",
		label: "CPU",
		unit:  "cores",
		query: func(selector string) string {
			return `sum(rate(container_cpu_usage_seconds_total{` + selector + `,container!="",container!="POD"}[2m]))`
		},
	},
	{
		key:   "memory",
		label: "Memory",
		unit:  "bytes",
		query: func(selector string) string {
			return `sum(container_memory_working_set_bytes{` + selector + `,container!="",container!="POD"})`
		},
	},
	{
		key:   "networkRx",
		label: "Network in",
		unit:  "bytes/s",
		query: func(selector string) string {
			return `sum(rate(container_network_receive_bytes_total{` + selector + `}[2m]))`
		},
	},
	{
		key:   "networkTx",
		label: "Network out",
		unit:  "bytes/s",
		query: func(selector string) string {
			return `sum(rate(container_network_transmit_bytes_total{` + selector + `}[2m]))`
		},
	},
	{
		key:   "fsRead",
		label: "Disk read",
		unit:  "bytes/s",
		query: func(selector string) string {
			return `sum(rate(container_fs_reads_bytes_total{` + selector + `,container!=""}[2m]))`
		},
	},
	{
		key:   "fsWrite",
		label: "Disk write",
		unit:  "bytes/s",
		query: func(selector string) string {
			return `sum(rate(container_fs_writes_bytes_total{` + selector + `,container!=""}[2m]))`
		},
	},
	{
		key:   "fsReadIOPS",
		label: "Disk read IOPS",
		unit:  "iops",
		query: func(selector string) string {
			return `sum(rate(container_fs_reads_total{` + selector + `,container!=""}[2m]))`
		},
	},
	{
		key:   "fsWriteIOPS",
		label: "Disk write IOPS",
		unit:  "iops",
		query: func(selector string) string {
			return `sum(rate(container_fs_writes_total{` + selector + `,container!=""}[2m]))`
		},
	},
	{
		key:   "networkErrors",
		label: "Network errors",
		unit:  "pps",
		query: func(selector string) string {
			return `sum(rate(container_network_receive_errors_total{` + selector + `}[2m])) + sum(rate(container_network_transmit_errors_total{` + selector + `}[2m]))`
		},
	},
	{
		key:   "networkDrops",
		label: "Network drops",
		unit:  "pps",
		query: func(selector string) string {
			return `sum(rate(container_network_receive_packets_dropped_total{` + selector + `}[2m])) + sum(rate(container_network_transmit_packets_dropped_total{` + selector + `}[2m]))`
		},
	},
	{
		key:   "cpuThrottle",
		label: "CPU throttled",
		unit:  "ratio",
		query: func(selector string) string {
			return `sum(rate(container_cpu_cfs_throttled_periods_total{` + selector + `,container!=""}[2m])) / clamp_min(sum(rate(container_cpu_cfs_periods_total{` + selector + `,container!=""}[2m])), 1)`
		},
	},
	{
		key:   "restarts",
		label: "Restarts",
		unit:  "count",
		query: func(selector string) string {
			return `sum(kube_pod_container_status_restarts_total{` + selector + `})`
		},
	},
}

var storageSeries = []seriesQuery{
	{
		key:   "storageUsed",
		label: "Disk used",
		unit:  "bytes",
		query: func(selector string) string {
			return `sum(kubelet_volume_stats_used_bytes{` + selector + `})`
		},
	},
	{
		key:   "storageCapacity",
		label: "Disk capacity",
		unit:  "bytes",
		query: func(selector string) string {
			return `sum(kube_persistentvolumeclaim_resource_requests_storage_bytes{` + selector + `}) or sum(kubelet_volume_stats_capacity_bytes{` + selector + `})`
		},
	},
}

var namespaceSeries = []seriesQuery{
	{
		key:   "cpu",
		label: "CPU",
		unit:  "cores",
		query: func(selector string) string {
			return `sum(rate(container_cpu_usage_seconds_total{` + selector + `,container!="",container!="POD"}[2m]))`
		},
	},
	{
		key:   "memory",
		label: "Memory",
		unit:  "bytes",
		query: func(selector string) string {
			return `sum(container_memory_working_set_bytes{` + selector + `,container!="",container!="POD"})`
		},
	},
	{
		key:   "networkRx",
		label: "Network in",
		unit:  "bytes/s",
		query: func(selector string) string {
			return `sum(rate(container_network_receive_bytes_total{` + selector + `}[2m]))`
		},
	},
	{
		key:   "networkTx",
		label: "Network out",
		unit:  "bytes/s",
		query: func(selector string) string {
			return `sum(rate(container_network_transmit_bytes_total{` + selector + `}[2m]))`
		},
	},
	{
		key:   "networkErrors",
		label: "Network errors",
		unit:  "pps",
		query: func(selector string) string {
			return `sum(rate(container_network_receive_errors_total{` + selector + `}[2m])) + sum(rate(container_network_transmit_errors_total{` + selector + `}[2m]))`
		},
	},
	{
		key:   "networkDrops",
		label: "Network drops",
		unit:  "pps",
		query: func(selector string) string {
			return `sum(rate(container_network_receive_packets_dropped_total{` + selector + `}[2m])) + sum(rate(container_network_transmit_packets_dropped_total{` + selector + `}[2m]))`
		},
	},
	{
		key:   "fsRead",
		label: "Disk read",
		unit:  "bytes/s",
		query: func(selector string) string {
			return `sum(rate(container_fs_reads_bytes_total{` + selector + `,container!=""}[2m]))`
		},
	},
	{
		key:   "fsWrite",
		label: "Disk write",
		unit:  "bytes/s",
		query: func(selector string) string {
			return `sum(rate(container_fs_writes_bytes_total{` + selector + `,container!=""}[2m]))`
		},
	},
	{
		key:   "fsReadIOPS",
		label: "Disk read IOPS",
		unit:  "iops",
		query: func(selector string) string {
			return `sum(rate(container_fs_reads_total{` + selector + `,container!=""}[2m]))`
		},
	},
	{
		key:   "fsWriteIOPS",
		label: "Disk write IOPS",
		unit:  "iops",
		query: func(selector string) string {
			return `sum(rate(container_fs_writes_total{` + selector + `,container!=""}[2m]))`
		},
	},
	{
		key:   "restarts",
		label: "Restarts",
		unit:  "count",
		query: func(selector string) string {
			return `sum(kube_pod_container_status_restarts_total{` + selector + `})`
		},
	},
}

// WorkspaceHistory returns historical series for one workspace pod.
func (p *PrometheusClient) WorkspaceHistory(ctx context.Context, namespace, podName, pvcName, rangeValue, stepValue string) (History, error) {
	if p == nil {
		return History{}, ErrPrometheusNotConfigured
	}
	start, end, step, err := historyWindow(p.now(), rangeValue, stepValue)
	if err != nil {
		return History{}, err
	}
	selector := fmt.Sprintf("namespace=%s,pod=%s", promQuote(namespace), promQuote(podName))
	result, err := p.querySeries(ctx, workspaceSeries, selector, start, end, step)
	if err != nil {
		return result, err
	}
	if pvcName != "" {
		storageSelector := fmt.Sprintf("namespace=%s,persistentvolumeclaim=%s", promQuote(namespace), promQuote(pvcName))
		storage, storageErr := p.querySeries(ctx, storageSeries, storageSelector, start, end, step)
		if storageErr == nil {
			result.Series = append(result.Series, storage.Series...)
		}
	}
	return result, nil
}

// NamespaceHistory returns historical series for all workspace pods in a
// namespace.
func (p *PrometheusClient) NamespaceHistory(ctx context.Context, namespace, rangeValue, stepValue string) (History, error) {
	if p == nil {
		return History{}, ErrPrometheusNotConfigured
	}
	start, end, step, err := historyWindow(p.now(), rangeValue, stepValue)
	if err != nil {
		return History{}, err
	}
	selector := fmt.Sprintf("namespace=%s,pod=~%s", promQuote(namespace), promQuote(`poddy-.+-0`))
	result, err := p.querySeries(ctx, namespaceSeries, selector, start, end, step)
	if err != nil {
		return result, err
	}
	storageSelector := fmt.Sprintf("namespace=%s,persistentvolumeclaim=~%s", promQuote(namespace), promQuote(`poddy-.+`))
	storage, storageErr := p.querySeries(ctx, storageSeries, storageSelector, start, end, step)
	if storageErr == nil {
		result.Series = append(result.Series, storage.Series...)
	}
	return result, nil
}

func historyWindow(now time.Time, rangeValue, stepValue string) (time.Time, time.Time, time.Duration, error) {
	window := defaultHistoryRange
	if strings.TrimSpace(rangeValue) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(rangeValue))
		if err != nil || parsed <= 0 {
			return time.Time{}, time.Time{}, 0, fmt.Errorf("invalid range")
		}
		window = parsed
	}
	if window > maxHistoryRange {
		window = maxHistoryRange
	}
	step := window / maxHistoryPoints
	if strings.TrimSpace(stepValue) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(stepValue))
		if err != nil || parsed <= 0 {
			return time.Time{}, time.Time{}, 0, fmt.Errorf("invalid step")
		}
		step = parsed
	}
	if step < minHistoryStep {
		step = minHistoryStep
	}
	minStepForPoints := window / maxHistoryPoints
	if step < minStepForPoints {
		step = minStepForPoints
	}
	step = step.Round(time.Second)
	if step <= 0 {
		step = minHistoryStep
	}
	end := now.UTC()
	start := end.Add(-window)
	return start, end, step, nil
}

func (p *PrometheusClient) querySeries(ctx context.Context, queries []seriesQuery, selector string, start, end time.Time, step time.Duration) (History, error) {
	result := History{
		Source:      "prometheus",
		Start:       start,
		End:         end,
		StepSeconds: int64(step.Seconds()),
		Series:      []Series{},
	}

	type queryResult struct {
		index  int
		points []Point
		err    error
	}
	results := make([]queryResult, len(queries))
	var wg sync.WaitGroup
	for i, item := range queries {
		wg.Add(1)
		go func(index int, item seriesQuery) {
			defer wg.Done()
			points, err := p.queryRange(ctx, item.query(selector), start, end, step)
			results[index] = queryResult{index: index, points: points, err: err}
		}(i, item)
	}
	wg.Wait()

	var problems []error
	for i, item := range queries {
		queryResult := results[i]
		if queryResult.err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", item.key, queryResult.err))
			continue
		}
		if len(queryResult.points) == 0 {
			continue
		}
		result.Series = append(result.Series, Series{
			Key:    item.key,
			Label:  item.label,
			Unit:   item.unit,
			Points: queryResult.points,
		})
	}
	if len(problems) == len(queries) {
		result.Reason = "Prometheus is unavailable"
		return result, errors.Join(problems...)
	}
	result.Available = true
	return result, nil
}

func (p *PrometheusClient) queryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Point, error) {
	values := url.Values{}
	values.Set("query", query)
	values.Set("start", strconv.FormatInt(start.Unix(), 10))
	values.Set("end", strconv.FormatInt(end.Unix(), 10))
	values.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/v1/query_range?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("Prometheus returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Values [][2]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
		Error string `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Status != "success" {
		if payload.Error != "" {
			return nil, fmt.Errorf("Prometheus query failed: %s", payload.Error)
		}
		return nil, fmt.Errorf("Prometheus query failed")
	}
	points := []Point{}
	for _, series := range payload.Data.Result {
		for _, pair := range series.Values {
			timestamp, err := rawFloat(pair[0])
			if err != nil {
				continue
			}
			value, err := rawFloat(pair[1])
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			points = append(points, Point{Timestamp: timestamp, Value: value})
		}
	}
	return points, nil
}

func rawFloat(raw json.RawMessage) (float64, error) {
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(text, 64)
}

func promQuote(value string) string {
	return strconv.Quote(value)
}
