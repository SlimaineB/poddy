package k8s

import (
	"strings"
	"testing"
)

func TestAgentTokenSecretNameFitsDNSLabel(t *testing.T) {
	long := strings.Repeat("a", 63)
	name := AgentTokenSecretName(long)
	if len(name) > 63 {
		t.Fatalf("secret name length = %d", len(name))
	}
	if name == AgentTokenSecretName(long+"b") {
		t.Fatal("long workspace names share a secret name")
	}
}
