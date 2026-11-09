package rbac

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

func quotaIntPtr(value int) *int {
	return &value
}

func TestQuotaValidate(t *testing.T) {
	valid := Quota{MaxWorkspaces: quotaIntPtr(0), CPU: "500m", Memory: "1Gi", Storage: "5Gi"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid quota rejected: %v", err)
	}
	if valid.IsZero() {
		t.Fatal("quota with zero workspaces should not be empty")
	}
	if !(Quota{}).IsZero() {
		t.Fatal("empty quota should be zero")
	}

	for name, quota := range map[string]Quota{
		"negative workspaces": {MaxWorkspaces: quotaIntPtr(-1)},
		"invalid cpu":         {CPU: "nope"},
		"negative memory":     {Memory: "-1Gi"},
		"invalid storage":     {Storage: "1GB!"},
	} {
		if err := quota.Validate(); err == nil {
			t.Fatalf("%s quota accepted", name)
		}
	}
}

func TestEffectiveQuotaUsesUserOverrideAndMostPermissiveTeam(t *testing.T) {
	cfg := Config{
		Teams: map[string]Team{
			"backend": {
				Members: []string{"alice@example.com"},
				Quota: &Quota{
					MaxWorkspaces: quotaIntPtr(2),
					CPU:           "2",
					Memory:        "4Gi",
					Storage:       "10Gi",
				},
			},
			"frontend": {
				Members: []string{"alice@example.com"},
				Quota: &Quota{
					MaxWorkspaces: quotaIntPtr(3),
					CPU:           "1",
					Memory:        "8Gi",
					Storage:       "5Gi",
				},
			},
		},
		UserQuotas: map[string]Quota{
			"ALICE@example.com": {CPU: "4"},
		},
	}

	got, err := cfg.EffectiveQuota(Identity{Username: "alice", Email: "alice@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkspaces == nil || *got.MaxWorkspaces != 3 {
		t.Fatalf("max workspaces = %v", got.MaxWorkspaces)
	}
	if got.CPU != "4" {
		t.Fatalf("cpu = %q", got.CPU)
	}
	if got.Memory != "8Gi" {
		t.Fatalf("memory = %q", got.Memory)
	}
	if got.Storage != "10Gi" {
		t.Fatalf("storage = %q", got.Storage)
	}
}

func TestEffectiveQuotaUserOverrideWinsEvenWhenLower(t *testing.T) {
	cfg := Config{
		Teams: map[string]Team{
			"backend": {
				Members: []string{"alice"},
				Quota:   &Quota{MaxWorkspaces: quotaIntPtr(5), CPU: "8"},
			},
		},
		UserQuotas: map[string]Quota{
			"user:alice": {MaxWorkspaces: quotaIntPtr(1), CPU: "1"},
		},
	}
	got, err := cfg.EffectiveQuota(Identity{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkspaces == nil || *got.MaxWorkspaces != 1 || got.CPU != "1" {
		t.Fatalf("effective quota = %+v", got)
	}
}

func TestEffectiveQuotaWithoutTeamOrOverrideIsUnlimited(t *testing.T) {
	cfg := Config{
		Teams: map[string]Team{
			"backend": {Members: []string{"bob"}, Quota: &Quota{CPU: "2"}},
		},
	}
	got, err := cfg.EffectiveQuota(Identity{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsZero() {
		t.Fatalf("expected unlimited quota, got %+v", got)
	}
}

func TestQuotaCheck(t *testing.T) {
	quota := Quota{
		MaxWorkspaces: quotaIntPtr(2),
		CPU:           "2",
		Memory:        "4Gi",
		Storage:       "10Gi",
	}
	usage := Usage{
		Workspaces: 2,
		CPU:        resource.MustParse("2"),
		Memory:     resource.MustParse("4Gi"),
		Storage:    resource.MustParse("10Gi"),
	}
	if err := quota.Check(usage); err != nil {
		t.Fatalf("usage at limit rejected: %v", err)
	}

	cases := []struct {
		name  string
		usage Usage
		want  string
	}{
		{"workspaces", Usage{Workspaces: 3}, "workspace quota"},
		{"cpu", Usage{CPU: resource.MustParse("3")}, "CPU quota"},
		{"memory", Usage{Memory: resource.MustParse("5Gi")}, "memory quota"},
		{"storage", Usage{Storage: resource.MustParse("11Gi")}, "storage quota"},
	}
	for _, tc := range cases {
		err := quota.Check(tc.usage)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s check = %v", tc.name, err)
		}
	}
}

func TestConfigValidateQuotas(t *testing.T) {
	valid := Config{
		Teams: map[string]Team{
			"backend": {Quota: &Quota{CPU: "2"}},
		},
		UserQuotas: map[string]Quota{
			"alice@example.com": {Storage: "10Gi"},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	for name, cfg := range map[string]Config{
		"invalid team quota": {
			Teams: map[string]Team{"backend": {Quota: &Quota{CPU: "nope"}}},
		},
		"invalid user quota": {
			UserQuotas: map[string]Quota{"alice@example.com": {Memory: "-1Gi"}},
		},
		"empty identity": {
			UserQuotas: map[string]Quota{" ": {CPU: "1"}},
		},
	} {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
