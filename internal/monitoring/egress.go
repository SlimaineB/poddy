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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EgressNetwork is one external endpoint observed for a workspace. Bytes and
// Connections are totals over the requested window; BytesPerSecond is their
// average rate over that window.
type EgressNetwork struct {
	Service        string  `json:"service"`
	Endpoint       string  `json:"endpoint"`
	Bucket         string  `json:"bucket,omitempty"`
	Direction      string  `json:"direction"`
	Protocol       string  `json:"protocol"`
	Port           string  `json:"port"`
	Confidence     string  `json:"confidence"`
	Bytes          float64 `json:"bytes"`
	BytesPerSecond float64 `json:"bytesPerSecond"`
	Connections    float64 `json:"connections"`
}

// EgressMount is a storage volume attached to a workspace. It is sourced from
// the collector's Kubernetes PVC/PV index, so it remains visible even when the
// CSI driver keeps the network traffic outside the workspace netns.
type EgressMount struct {
	PVC         string `json:"pvc,omitempty"`
	Volume      string `json:"volume,omitempty"`
	Server      string `json:"server,omitempty"`
	Path        string `json:"path,omitempty"`
	FSType      string `json:"fstype,omitempty"`
	Service     string `json:"service"`
	Bucket      string `json:"bucket,omitempty"`
	AccessMode  string `json:"accessMode,omitempty"`
	Provisioner string `json:"provisioner,omitempty"`
}

// Egress is the workspace egress and storage attribution payload.
type Egress struct {
	Available     bool            `json:"available"`
	Reason        string          `json:"reason,omitempty"`
	Source        string          `json:"source,omitempty"`
	Start         time.Time       `json:"start,omitempty"`
	End           time.Time       `json:"end,omitempty"`
	WindowSeconds int64           `json:"windowSeconds,omitempty"`
	Network       []EgressNetwork `json:"network"`
	Mounts        []EgressMount   `json:"mounts"`
}

type vectorSample struct {
	labels map[string]string
	value  float64
}

// WorkspaceEgress returns external endpoint and storage mount attribution for
// one workspace. PromQL is built from fixed templates; workspace and range
// values are never interpolated as raw PromQL.
func (p *PrometheusClient) WorkspaceEgress(ctx context.Context, workspace, rangeValue string) (Egress, error) {
	if p == nil {
		return Egress{}, ErrPrometheusNotConfigured
	}
	start, end, _, err := historyWindow(p.now(), rangeValue, "")
	if err != nil {
		return Egress{}, err
	}
	window := end.Sub(start)
	if window <= 0 {
		window = defaultHistoryRange
	}
	seconds := strconv.FormatInt(int64(window.Seconds()), 10) + "s"
	selector := "workspace=" + promQuote(workspace)
	networkSelector := selector + ",remote_service!=\"\""
	mountSelector := selector + ",storage_service!=\"\""
	result := Egress{
		Source:        "prometheus",
		Start:         start,
		End:           end,
		WindowSeconds: int64(window.Seconds()),
		Network:       []EgressNetwork{},
		Mounts:        []EgressMount{},
	}

	bytesQuery := "sum by (remote_service,remote_endpoint,bucket,direction,protocol,port,confidence) (increase(poddy_workspace_network_bytes_total{" + networkSelector + "}[" + seconds + "]))"
	connectionsQuery := "sum by (remote_service,remote_endpoint,bucket,protocol,port,confidence) (increase(poddy_workspace_network_connections_total{" + networkSelector + "}[" + seconds + "]))"
	mountsQuery := "poddy_workspace_storage_mount_info{" + mountSelector + "}"

	type queryResult struct {
		samples []vectorSample
		err     error
	}
	results := make([]queryResult, 3)
	var wg sync.WaitGroup
	queries := []string{bytesQuery, connectionsQuery, mountsQuery}
	for index, query := range queries {
		wg.Add(1)
		go func(index int, query string) {
			defer wg.Done()
			samples, err := p.queryVector(ctx, query, end)
			results[index] = queryResult{samples: samples, err: err}
		}(index, query)
	}
	wg.Wait()

	var problems []error
	for _, item := range results {
		if item.err != nil {
			problems = append(problems, item.err)
		}
	}
	if len(problems) == len(results) {
		result.Reason = "Prometheus is unavailable"
		return result, errors.Join(problems...)
	}

	result.Network = mergeEgressNetwork(results[0].samples, results[1].samples, window)
	result.Mounts = egressMounts(results[2].samples)
	result.Available = len(result.Network) > 0 || len(result.Mounts) > 0
	if !result.Available {
		result.Reason = "No egress data for this workspace in the selected window"
	}
	return result, nil
}

func mergeEgressNetwork(bytesSamples, connectionSamples []vectorSample, window time.Duration) []EgressNetwork {
	byKey := make(map[string]*EgressNetwork)
	for _, sample := range bytesSamples {
		key := egressNetworkKey(sample.labels, sample.labels["direction"])
		item := &EgressNetwork{
			Service:    sample.labels["remote_service"],
			Endpoint:   sample.labels["remote_endpoint"],
			Bucket:     sample.labels["bucket"],
			Direction:  sample.labels["direction"],
			Protocol:   sample.labels["protocol"],
			Port:       sample.labels["port"],
			Confidence: sample.labels["confidence"],
			Bytes:      sample.value,
		}
		if window > 0 {
			item.BytesPerSecond = sample.value / window.Seconds()
		}
		byKey[key] = item
	}
	for _, sample := range connectionSamples {
		key := egressNetworkKey(sample.labels, "egress")
		item := byKey[key]
		if item == nil {
			item = &EgressNetwork{
				Service:    sample.labels["remote_service"],
				Endpoint:   sample.labels["remote_endpoint"],
				Bucket:     sample.labels["bucket"],
				Direction:  "egress",
				Protocol:   sample.labels["protocol"],
				Port:       sample.labels["port"],
				Confidence: sample.labels["confidence"],
			}
			byKey[key] = item
		}
		item.Connections = sample.value
	}
	out := make([]EgressNetwork, 0, len(byKey))
	for _, item := range byKey {
		if item.Bytes == 0 && item.Connections == 0 {
			continue
		}
		out = append(out, *item)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Bytes != out[b].Bytes {
			return out[a].Bytes > out[b].Bytes
		}
		if out[a].Connections != out[b].Connections {
			return out[a].Connections > out[b].Connections
		}
		if out[a].Service != out[b].Service {
			return out[a].Service < out[b].Service
		}
		return out[a].Endpoint < out[b].Endpoint
	})
	return out
}

func egressNetworkKey(labels map[string]string, direction string) string {
	return labels["remote_service"] + "|" + labels["remote_endpoint"] + "|" + labels["bucket"] + "|" + labels["protocol"] + "|" + labels["port"] + "|" + labels["confidence"] + "|" + direction
}

func egressMounts(samples []vectorSample) []EgressMount {
	out := make([]EgressMount, 0, len(samples))
	seen := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		item := EgressMount{
			PVC:         sample.labels["pvc"],
			Volume:      sample.labels["volume"],
			Server:      sample.labels["server"],
			Path:        sample.labels["path"],
			FSType:      sample.labels["fstype"],
			Service:     sample.labels["storage_service"],
			Bucket:      sample.labels["bucket"],
			AccessMode:  sample.labels["access_mode"],
			Provisioner: sample.labels["provisioner"],
		}
		key := item.PVC + "|" + item.Volume + "|" + item.Service + "|" + item.Bucket + "|" + item.Server + "|" + item.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Service != out[b].Service {
			return out[a].Service < out[b].Service
		}
		if out[a].PVC != out[b].PVC {
			return out[a].PVC < out[b].PVC
		}
		return out[a].Volume < out[b].Volume
	})
	return out
}

func (p *PrometheusClient) queryVector(ctx context.Context, query string, at time.Time) ([]vectorSample, error) {
	values := url.Values{}
	values.Set("query", query)
	values.Set("time", strconv.FormatInt(at.Unix(), 10))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/v1/query?"+values.Encode(), nil)
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
				Metric map[string]string  `json:"metric"`
				Value  [2]json.RawMessage `json:"value"`
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
	samples := make([]vectorSample, 0, len(payload.Data.Result))
	for _, item := range payload.Data.Result {
		value, err := rawFloat(item.Value[1])
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		samples = append(samples, vectorSample{labels: item.Metric, value: value})
	}
	return samples, nil
}
