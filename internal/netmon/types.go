package netmon

import "time"

const (
	ServiceS3       = "s3"
	ServiceEFS      = "efs"
	ServiceNFS      = "nfs"
	ServiceHTTPS    = "https"
	ServiceHTTP     = "http"
	ServiceDNS      = "dns"
	ServiceUnknown  = "unknown"
	ServiceExternal = "external"

	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"

	DirectionEgress  = "egress"
	DirectionIngress = "ingress"
)

type PodInfo struct {
	Workspace string
	Namespace string
	Node      string
	UID       string
	IPs       []string
}

type MountInfo struct {
	Workspace   string
	Namespace   string
	PVC         string
	Volume      string
	Server      string
	ServerIPs   []string
	Path        string
	FSType      string
	Service     string
	Bucket      string
	AccessMode  string
	Provisioner string
}

type Endpoint struct {
	Host       string
	Service    string
	Bucket     string
	Confidence string
}

type FlowLabels struct {
	Workspace  string
	Region     string
	Direction  string
	Protocol   string
	Service    string
	Endpoint   string
	Bucket     string
	Confidence string
	Port       uint16
}

type Packet struct {
	SrcIP     string
	DstIP     string
	SrcPort   uint16
	DstPort   uint16
	Protocol  string
	TCPFlags  uint8
	TCPSeq    uint32
	IPID      uint16
	Payload   []byte
	Length    int
	Timestamp time.Time
}
