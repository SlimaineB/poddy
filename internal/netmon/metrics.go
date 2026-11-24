package netmon

import (
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	networkBytesDesc = prometheus.NewDesc(
		"poddy_workspace_network_bytes_total",
		"Network bytes attributed to a workspace and external endpoint.",
		[]string{"workspace", "region", "direction", "protocol", "remote_service", "remote_endpoint", "port", "bucket", "confidence"},
		nil,
	)
	networkConnectionsDesc = prometheus.NewDesc(
		"poddy_workspace_network_connections_total",
		"TCP connections attributed to a workspace and external endpoint.",
		[]string{"workspace", "region", "protocol", "remote_service", "remote_endpoint", "port", "bucket", "confidence"},
		nil,
	)
	mountInfoDesc = prometheus.NewDesc(
		"poddy_workspace_storage_mount_info",
		"Storage mounts attached to a workspace, including NFS, EFS and S3 CSI volumes.",
		[]string{"workspace", "region", "pvc", "volume", "server", "path", "fstype", "storage_service", "bucket", "access_mode", "provisioner"},
		nil,
	)
	packetsDesc = prometheus.NewDesc(
		"poddy_netmon_packets_total",
		"Packets observed by the egress monitor.",
		[]string{"result"},
		nil,
	)
)

type Metrics struct {
	mu       sync.RWMutex
	bytes    map[FlowLabels]uint64
	conns    map[FlowLabels]uint64
	mounts   []MountInfo
	packets  map[string]uint64
	maxFlows int
	region   string
}

func NewMetrics(maxFlows int) *Metrics {
	if maxFlows <= 0 {
		maxFlows = 8192
	}
	return &Metrics{
		bytes:    make(map[FlowLabels]uint64),
		conns:    make(map[FlowLabels]uint64),
		packets:  make(map[string]uint64),
		maxFlows: maxFlows,
	}
}

func (m *Metrics) SetRegion(region string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.region = region
	m.mu.Unlock()
}

func (m *Metrics) Observe(labels FlowLabels, bytes, connections uint64) {
	if m == nil {
		return
	}
	if bytes == 0 && connections == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bytes[labels]; !ok && len(m.bytes)+len(m.conns) >= m.maxFlows {
		labels.Endpoint = "other"
		labels.Bucket = ""
		labels.Confidence = ConfidenceLow
	}
	if bytes > 0 {
		m.bytes[labels] += bytes
	}
	if connections > 0 {
		conn := labels
		m.conns[conn] += connections
	}
}

func (m *Metrics) SetMounts(mounts []MountInfo) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.mounts = append([]MountInfo(nil), mounts...)
	m.mu.Unlock()
}

func (m *Metrics) ObservePacket(result string) {
	if m == nil || result == "" {
		return
	}
	m.mu.Lock()
	m.packets[result]++
	m.mu.Unlock()
}

func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- networkBytesDesc
	ch <- networkConnectionsDesc
	ch <- mountInfoDesc
	ch <- packetsDesc
}

func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	if m == nil {
		return
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for labels, value := range m.bytes {
		ch <- prometheus.MustNewConstMetric(
			networkBytesDesc,
			prometheus.CounterValue,
			float64(value),
			labels.Workspace,
			labels.Region,
			labels.Direction,
			labels.Protocol,
			labels.Service,
			labels.Endpoint,
			strconv.Itoa(int(labels.Port)),
			labels.Bucket,
			labels.Confidence,
		)
	}
	for labels, value := range m.conns {
		ch <- prometheus.MustNewConstMetric(
			networkConnectionsDesc,
			prometheus.CounterValue,
			float64(value),
			labels.Workspace,
			labels.Region,
			labels.Protocol,
			labels.Service,
			labels.Endpoint,
			strconv.Itoa(int(labels.Port)),
			labels.Bucket,
			labels.Confidence,
		)
	}
	for _, mount := range m.mounts {
		ch <- prometheus.MustNewConstMetric(
			mountInfoDesc,
			prometheus.GaugeValue,
			1,
			mount.Workspace,
			m.region,
			mount.PVC,
			mount.Volume,
			mount.Server,
			mount.Path,
			mount.FSType,
			mount.Service,
			mount.Bucket,
			mount.AccessMode,
			mount.Provisioner,
		)
	}
	for result, value := range m.packets {
		ch <- prometheus.MustNewConstMetric(
			packetsDesc,
			prometheus.CounterValue,
			float64(value),
			result,
		)
	}
}
