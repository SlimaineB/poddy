package netmon

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestMountInfoFromPV(t *testing.T) {
	tests := []struct {
		name    string
		pv      *corev1.PersistentVolume
		ok      bool
		service string
		bucket  string
		server  string
		path    string
	}{
		{
			name: "nfs",
			pv: &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					NFS: &corev1.NFSVolumeSource{Server: "nfs.example.com", Path: "/data"},
				},
			}},
			ok:      true,
			service: ServiceNFS,
			server:  "nfs.example.com",
			path:    "/data",
		},
		{
			name: "efs",
			pv: &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "efs.csi.aws.com",
						VolumeHandle: "fs-123::fsap-456",
						VolumeAttributes: map[string]string{
							"server": "fs-123.efs.eu-west-3.amazonaws.com",
							"path":   "/",
						},
					},
				},
			}},
			ok:      true,
			service: ServiceEFS,
			server:  "fs-123.efs.eu-west-3.amazonaws.com",
			path:    "/",
		},
		{
			name: "efs handle only",
			pv: &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "efs.csi.aws.com",
						VolumeHandle: "fs-123",
					},
				},
			}},
			ok:      true,
			service: ServiceEFS,
			server:  "fs-123.efs.amazonaws.com",
			path:    "/",
		},
		{
			name: "s3",
			pv: &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "s3.csi.aws.com",
						VolumeHandle: "my-bucket",
						VolumeAttributes: map[string]string{
							"bucketName": "my-bucket",
						},
					},
				},
			}},
			ok:      true,
			service: ServiceS3,
			bucket:  "my-bucket",
		},
		{
			name: "nfs csi",
			pv: &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver: "nfs.csi.k8s.io",
						VolumeAttributes: map[string]string{
							"server": "nfs.example.com",
							"share":  "/exports",
						},
					},
				},
			}},
			ok:      true,
			service: ServiceNFS,
			server:  "nfs.example.com",
			path:    "/exports",
		},
		{
			name: "hostpath",
			pv:   &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{}},
			ok:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, ok := MountInfoFromPV(tc.pv)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if info.Service != tc.service || info.Bucket != tc.bucket || info.Server != tc.server || info.Path != tc.path {
				t.Fatalf("info = %#v", info)
			}
		})
	}
}

func TestMountIndexRefresh(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "poddy-alice-0",
				Namespace: "poddy-workspaces",
				Labels:    map[string]string{workspaceLabel: "alice"},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-a",
				Volumes: []corev1.Volume{{
					Name: "home",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "poddy-alice"},
					},
				}},
			},
		},
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "poddy-alice", Namespace: "poddy-workspaces"},
			Spec: corev1.PersistentVolumeClaimSpec{
				VolumeName:  "pv-nfs",
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			},
		},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "pv-nfs"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					NFS: &corev1.NFSVolumeSource{Server: "10.0.0.5", Path: "/data"},
				},
			},
		},
	)
	index := NewMountIndex(client, "poddy-workspaces", "node-a")
	if err := index.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	mounts := index.Mounts()
	if len(mounts) != 1 {
		t.Fatalf("mounts = %#v", mounts)
	}
	if mounts[0].Workspace != "alice" || mounts[0].Service != ServiceNFS || mounts[0].PVC != "poddy-alice" {
		t.Fatalf("mount = %#v", mounts[0])
	}
	workspaces := index.WorkspacesForServer("10.0.0.5")
	if len(workspaces) != 1 || workspaces[0] != "alice" {
		t.Fatalf("workspaces = %#v", workspaces)
	}
}
