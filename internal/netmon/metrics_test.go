package netmon

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsNetworkAndMounts(t *testing.T) {
	metrics := NewMetrics(10)
	metrics.SetRegion("eu")
	metrics.Observe(FlowLabels{
		Workspace:  "alice",
		Region:     "eu",
		Direction:  DirectionEgress,
		Protocol:   "tcp",
		Service:    ServiceS3,
		Endpoint:   "bucket.s3.eu-west-3.amazonaws.com",
		Bucket:     "bucket",
		Confidence: ConfidenceHigh,
		Port:       443,
	}, 123, 1)
	metrics.SetMounts([]MountInfo{{
		Workspace:   "alice",
		PVC:         "poddy-alice",
		Volume:      "pvc-123",
		Server:      "fs-123.efs.eu-west-3.amazonaws.com",
		Path:        "/",
		FSType:      "nfs4",
		Service:     ServiceEFS,
		AccessMode:  "ReadWriteMany",
		Provisioner: "efs.csi.aws.com",
	}})

	expectedBytes := `
# HELP poddy_workspace_network_bytes_total Network bytes attributed to a workspace and external endpoint.
# TYPE poddy_workspace_network_bytes_total counter
poddy_workspace_network_bytes_total{bucket="bucket",confidence="high",direction="egress",port="443",protocol="tcp",region="eu",remote_endpoint="bucket.s3.eu-west-3.amazonaws.com",remote_service="s3",workspace="alice"} 123
`
	if err := testutil.CollectAndCompare(metrics, strings.NewReader(expectedBytes), "poddy_workspace_network_bytes_total"); err != nil {
		t.Fatal(err)
	}

	expectedMounts := `
# HELP poddy_workspace_storage_mount_info Storage mounts attached to a workspace, including NFS, EFS and S3 CSI volumes.
# TYPE poddy_workspace_storage_mount_info gauge
poddy_workspace_storage_mount_info{access_mode="ReadWriteMany",bucket="",fstype="nfs4",path="/",provisioner="efs.csi.aws.com",pvc="poddy-alice",region="eu",server="fs-123.efs.eu-west-3.amazonaws.com",storage_service="efs",volume="pvc-123",workspace="alice"} 1
`
	if err := testutil.CollectAndCompare(metrics, strings.NewReader(expectedMounts), "poddy_workspace_storage_mount_info"); err != nil {
		t.Fatal(err)
	}
}
