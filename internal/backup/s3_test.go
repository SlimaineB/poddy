package backup

import "testing"

func TestNewAcceptsHostPortEndpoint(t *testing.T) {
	store, err := New(Config{
		Endpoint:  "minio.poddy-system.svc.cluster.local:9000",
		Bucket:    "poddy-backups",
		AccessKey: "access",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.client.EndpointURL()
	if endpoint.Host != "minio.poddy-system.svc.cluster.local:9000" || endpoint.Scheme != "http" {
		t.Fatalf("endpoint = %s", endpoint)
	}
}

func TestNewAcceptsURLWithScheme(t *testing.T) {
	store, err := New(Config{
		Endpoint:  "https://minio.example.com:9000/",
		Bucket:    "poddy-backups",
		AccessKey: "access",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.client.EndpointURL()
	if endpoint.Host != "minio.example.com:9000" || endpoint.Scheme != "https" {
		t.Fatalf("endpoint = %s", endpoint)
	}
}
