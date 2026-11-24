package netmon

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		host    string
		port    uint16
		service string
		bucket  string
		conf    string
	}{
		{"bucket.s3.us-east-1.amazonaws.com", 443, ServiceS3, "bucket", ConfidenceHigh},
		{"bucket.s3.amazonaws.com", 443, ServiceS3, "bucket", ConfidenceHigh},
		{"s3.us-east-1.amazonaws.com", 443, ServiceS3, "", ConfidenceHigh},
		{"bucket.s3-accelerate.amazonaws.com", 443, ServiceS3, "bucket", ConfidenceHigh},
		{"fs-12345678.efs.us-east-1.amazonaws.com", 2049, ServiceEFS, "", ConfidenceHigh},
		{"nfs.example.com", 2049, ServiceNFS, "", ConfidenceMedium},
		{"example.com", 443, ServiceHTTPS, "", ConfidenceHigh},
		{"", 443, ServiceHTTPS, "", ConfidenceLow},
		{"", 2049, ServiceNFS, "", ConfidenceMedium},
		{"8.8.8.8", 53, ServiceDNS, "", ConfidenceHigh},
	}
	for _, tc := range tests {
		service, bucket, conf := Classify(tc.host, tc.port)
		if service != tc.service || bucket != tc.bucket || conf != tc.conf {
			t.Fatalf("Classify(%q, %d) = (%q, %q, %q), want (%q, %q, %q)",
				tc.host, tc.port, service, bucket, conf, tc.service, tc.bucket, tc.conf)
		}
	}
}
