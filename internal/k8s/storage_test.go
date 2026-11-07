package k8s

import (
	"context"
	"testing"
)

func TestListVolumes(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}

	volumes, err := m.ListVolumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 1 {
		t.Fatalf("volumes = %+v", volumes)
	}
	volume := volumes[0]
	if volume.Workspace != "app" || volume.Name != "poddy-app" {
		t.Fatalf("volume = %+v", volume)
	}
	if volume.Requested != "5Gi" || volume.Capacity != "5Gi" {
		t.Fatalf("capacity = %q requested = %q", volume.Capacity, volume.Requested)
	}
	if volume.Bytes != 5*1024*1024*1024 {
		t.Fatalf("bytes = %d", volume.Bytes)
	}
}
