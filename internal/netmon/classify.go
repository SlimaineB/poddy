package netmon

import (
	"net"
	"strconv"
	"strings"
)

var s3Markers = []string{
	".s3.",
	".s3-accelerate.",
	".s3-accesspoint.",
	".s3-control.",
	".s3-object-lambda.",
	".s3-website",
	".s3express",
	".s3.dualstack.",
	".s3-accelerate.dualstack.",
}

func Classify(host string, port uint16) (service, bucket, confidence string) {
	host = normalizeHost(host)
	if host != "" {
		switch {
		case isS3Host(host):
			return ServiceS3, s3Bucket(host), ConfidenceHigh
		case isEFSHost(host):
			return ServiceEFS, "", ConfidenceHigh
		}
	}

	switch port {
	case 53:
		return ServiceDNS, "", ConfidenceHigh
	case 2049:
		return ServiceNFS, "", ConfidenceMedium
	case 443:
		if host != "" {
			return ServiceHTTPS, "", ConfidenceHigh
		}
		return ServiceHTTPS, "", ConfidenceLow
	case 80:
		if host != "" {
			return ServiceHTTP, "", ConfidenceHigh
		}
		return ServiceHTTP, "", ConfidenceLow
	}
	if host != "" {
		return ServiceExternal, "", ConfidenceMedium
	}
	return ServiceUnknown, "", ConfidenceLow
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}

func isS3Host(host string) bool {
	if host == "s3.amazonaws.com" || strings.HasPrefix(host, "s3.") {
		return true
	}
	for _, marker := range s3Markers {
		if strings.Contains(host, marker) {
			return true
		}
	}
	return false
}

func s3Bucket(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return ""
	}
	if parts[0] == "s3" || strings.HasPrefix(parts[0], "s3-") {
		return ""
	}
	for _, marker := range s3Markers {
		if strings.Contains(host, marker) {
			return parts[0]
		}
	}
	if host == "s3.amazonaws.com" {
		return ""
	}
	return ""
}

func isEFSHost(host string) bool {
	if !strings.Contains(host, ".efs.") {
		return false
	}
	return strings.HasSuffix(host, ".amazonaws.com") || strings.Contains(host, ".amazonaws.com.")
}

func portLabel(port uint16) string {
	return strconv.Itoa(int(port))
}
