package netmon

import (
	"testing"
	"time"
)

func testPodIndex() *PodIndex {
	return &PodIndex{pods: map[string]PodInfo{
		"10.244.0.2": {Workspace: "alice", Namespace: "poddy-workspaces", Node: "node-a"},
	}}
}

func testMountIndex() *MountIndex {
	return &MountIndex{byServer: map[string][]MountInfo{
		"10.0.0.5": {{
			Workspace: "bob",
			Server:    "fs-123.efs.eu-west-3.amazonaws.com",
			Service:   ServiceEFS,
		}},
	}}
}

func TestCollectorS3DNS(t *testing.T) {
	collector := NewCollector("eu", testPodIndex(), testMountIndex(), NewMetrics(100))
	collector.DNS.Observe(dnsResponse(t, "bucket.s3.eu-west-3.amazonaws.com", [4]byte{203, 0, 113, 10}))
	frame := ipv4Frame([4]byte{10, 244, 0, 2}, [4]byte{203, 0, 113, 10}, 6, 1,
		tcpSegment(12345, 443, 1, 0x18, []byte("hello")))
	collector.HandleFrame(frame, time.Now())

	found := false
	for labels, value := range collector.Metrics.bytes {
		if labels.Workspace == "alice" && labels.Service == ServiceS3 && labels.Bucket == "bucket" && value == 5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("s3 metric not found: %#v", collector.Metrics.bytes)
	}
}

func TestCollectorS3SNI(t *testing.T) {
	collector := NewCollector("eu", testPodIndex(), testMountIndex(), NewMetrics(100))
	payload := clientHello("bucket.s3.eu-west-3.amazonaws.com")
	frame := ipv4Frame([4]byte{10, 244, 0, 2}, [4]byte{203, 0, 113, 11}, 6, 2,
		tcpSegment(12345, 443, 1, 0x18, payload))
	collector.HandleFrame(frame, time.Now())

	found := false
	for labels := range collector.Metrics.bytes {
		if labels.Workspace == "alice" && labels.Service == ServiceS3 && labels.Bucket == "bucket" {
			found = true
		}
	}
	if !found {
		t.Fatalf("s3 SNI metric not found: %#v", collector.Metrics.bytes)
	}
}

func TestCollectorNFSMount(t *testing.T) {
	collector := NewCollector("eu", testPodIndex(), testMountIndex(), NewMetrics(100))
	frame := ipv4Frame([4]byte{10, 244, 0, 2}, [4]byte{10, 0, 0, 5}, 6, 3,
		tcpSegment(12345, 2049, 1, 0x18, []byte("nfs")))
	collector.HandleFrame(frame, time.Now())

	found := false
	for labels, value := range collector.Metrics.bytes {
		if labels.Workspace == "alice" && labels.Service == ServiceEFS && labels.Endpoint == "fs-123.efs.eu-west-3.amazonaws.com" && value == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("efs metric not found: %#v", collector.Metrics.bytes)
	}
}
