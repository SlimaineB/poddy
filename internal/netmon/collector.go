package netmon

import (
	"strings"
	"sync/atomic"
	"time"
)

type Stats struct {
	Frames    uint64
	Parsed    uint64
	Ignored   uint64
	Duplicate uint64
	Flows     uint64
}

type Collector struct {
	Region  string
	Pods    *PodIndex
	Mounts  *MountIndex
	DNS     *DNSCache
	SNI     *SNICache
	Metrics *Metrics
	dedupe  *deduper

	frames    atomic.Uint64
	parsed    atomic.Uint64
	ignored   atomic.Uint64
	duplicate atomic.Uint64
	flows     atomic.Uint64
}

func NewCollector(region string, pods *PodIndex, mounts *MountIndex, metrics *Metrics) *Collector {
	if metrics == nil {
		metrics = NewMetrics(8192)
	}
	return &Collector{
		Region:  region,
		Pods:    pods,
		Mounts:  mounts,
		DNS:     NewDNSCache(0),
		SNI:     NewSNICache(0),
		Metrics: metrics,
		dedupe:  newDeduper(2*time.Second, 65536),
	}
}

func (c *Collector) HandleFrame(frame []byte, timestamp time.Time) {
	c.frames.Add(1)
	c.Metrics.ObservePacket("seen")
	packet, ok := ParsePacket(frame, timestamp)
	if !ok {
		c.ignored.Add(1)
		c.Metrics.ObservePacket("ignored")
		return
	}
	c.parsed.Add(1)
	if c.dedupe != nil && c.dedupe.Seen(packet) {
		c.duplicate.Add(1)
		c.Metrics.ObservePacket("duplicate")
		return
	}
	if !c.handlePacket(packet) {
		c.ignored.Add(1)
		c.Metrics.ObservePacket("ignored")
	}
}

func (c *Collector) Stats() Stats {
	return Stats{
		Frames:    c.frames.Load(),
		Parsed:    c.parsed.Load(),
		Ignored:   c.ignored.Load(),
		Duplicate: c.duplicate.Load(),
		Flows:     c.flows.Load(),
	}
}

func (c *Collector) handlePacket(packet Packet) bool {
	srcPod, srcOK := c.Pods.Lookup(packet.SrcIP)
	dstPod, dstOK := c.Pods.Lookup(packet.DstIP)
	switch {
	case srcOK && dstOK:
		return false
	case srcOK:
		return c.observePodFlow(srcPod, packet, DirectionEgress)
	case dstOK:
		return c.observePodFlow(dstPod, packet, DirectionIngress)
	default:
		return c.observeMountFlow(packet)
	}
}

func (c *Collector) observePodFlow(pod PodInfo, packet Packet, direction string) bool {
	var remoteIP string
	var remotePort uint16
	if direction == DirectionEgress {
		remoteIP = packet.DstIP
		remotePort = packet.DstPort
	} else {
		remoteIP = packet.SrcIP
		remotePort = packet.SrcPort
	}

	if packet.Protocol == "udp" && (packet.SrcPort == 53 || packet.DstPort == 53) {
		c.DNS.Observe(packet.Payload)
	}
	if packet.Protocol == "tcp" && direction == DirectionEgress && remotePort == 443 && len(packet.Payload) > 0 {
		c.SNI.Observe(remoteIP, remotePort, packet.Payload)
	}

	endpoint := c.endpoint(remoteIP, remotePort)
	labels := FlowLabels{
		Workspace:  pod.Workspace,
		Region:     c.Region,
		Direction:  direction,
		Protocol:   packet.Protocol,
		Service:    endpoint.Service,
		Endpoint:   endpoint.Host,
		Bucket:     endpoint.Bucket,
		Confidence: endpoint.Confidence,
		Port:       remotePort,
	}
	connections := uint64(0)
	if packet.Protocol == "tcp" && direction == DirectionEgress && packet.TCPFlags&0x02 != 0 && packet.TCPFlags&0x10 == 0 {
		connections = 1
	}
	c.Metrics.Observe(labels, uint64(len(packet.Payload)), connections)
	c.flows.Add(1)
	return true
}

func (c *Collector) observeMountFlow(packet Packet) bool {
	if packet.SrcPort != 2049 && packet.DstPort != 2049 {
		return false
	}
	serverIP := packet.DstIP
	direction := DirectionEgress
	if packet.SrcPort == 2049 {
		serverIP = packet.SrcIP
		direction = DirectionIngress
	}
	workspaces := c.Mounts.WorkspacesForServer(serverIP)
	if len(workspaces) != 1 {
		return false
	}
	endpoint := c.endpoint(serverIP, 2049)
	labels := FlowLabels{
		Workspace:  workspaces[0],
		Region:     c.Region,
		Direction:  direction,
		Protocol:   packet.Protocol,
		Service:    endpoint.Service,
		Endpoint:   endpoint.Host,
		Bucket:     endpoint.Bucket,
		Confidence: ConfidenceMedium,
		Port:       2049,
	}
	c.Metrics.Observe(labels, uint64(len(packet.Payload)), 0)
	c.flows.Add(1)
	return true
}

func (c *Collector) endpoint(ip string, port uint16) Endpoint {
	host := ""
	confidence := ""
	if name, ok := c.SNI.Lookup(ip, port); ok {
		host = name
		confidence = ConfidenceHigh
	}
	if host == "" {
		if name, ok := c.DNS.Lookup(ip); ok {
			host = name
			confidence = ConfidenceHigh
		}
	}
	if host == "" {
		if name, ok := c.Mounts.HostForServer(ip); ok {
			host = name
			confidence = ConfidenceMedium
		}
	}
	service, bucket, classified := Classify(host, port)
	if confidence == "" || confidence == ConfidenceLow {
		confidence = classified
	}
	if host == "" {
		host = ip
	}
	return Endpoint{
		Host:       strings.TrimSpace(host),
		Service:    service,
		Bucket:     bucket,
		Confidence: confidence,
	}
}
