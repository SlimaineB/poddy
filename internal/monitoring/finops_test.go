package monitoring

import (
	"math"
	"testing"
)

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func TestComputeFinOpsWasteAndCost(t *testing.T) {
	rows := []WorkspaceOverview{{
		Name:         "dev",
		Owner:        "alice@example.com",
		Phase:        "Running",
		Available:    true,
		Usage:        ResourceUsage{CPU: f64(0.2), Memory: i64(512 * 1024 * 1024)},
		Requests:     ResourceUsage{CPU: f64(2), Memory: i64(4 * 1024 * 1024 * 1024)},
		Restarts:     1,
		StorageBytes: 10 * 1024 * 1024 * 1024,
	}}
	pricing := Pricing{CPUCoreHour: 0.03, MemoryGBHour: 0.004, StorageGBMonth: 0.08, Currency: "USD"}
	report := ComputeFinOps(rows, pricing)
	if len(report.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(report.Workspaces))
	}
	item := report.Workspaces[0]
	if item.IdleCPU == nil || math.Abs(*item.IdleCPU-1.8) > 0.0001 {
		t.Fatalf("idle CPU = %v, want 1.8", item.IdleCPU)
	}
	if item.IdleMemory == nil || *item.IdleMemory != 3584*1024*1024 {
		t.Fatalf("idle memory = %v, want 3.5GiB", item.IdleMemory)
	}
	if item.CPUUtilization == nil || math.Abs(*item.CPUUtilization-10) > 0.01 {
		t.Fatalf("CPU utilization = %v, want 10", item.CPUUtilization)
	}
	wantWaste := 1.8*0.03 + 3.5*0.004
	if item.WastedHourly == nil || math.Abs(*item.WastedHourly-wantWaste) > 0.0001 {
		t.Fatalf("wasted hourly = %v, want %v", item.WastedHourly, wantWaste)
	}
	for _, want := range []string{SignalIdleCPU, SignalIdleMemory, SignalOversized, SignalRestarts} {
		if !hasSignal(item.Signals, want) {
			t.Fatalf("missing signal %q in %v", want, item.Signals)
		}
	}
	if report.Totals.Idle != 1 || report.Totals.Oversized != 1 {
		t.Fatalf("totals idle=%d oversized=%d, want 1/1", report.Totals.Idle, report.Totals.Oversized)
	}
	if report.Totals.WastePercent <= 0 {
		t.Fatalf("waste percent = %v, want > 0", report.Totals.WastePercent)
	}
	if item.StorageMonthly == nil || math.Abs(*item.StorageMonthly-0.8) > 0.0001 {
		t.Fatalf("storage monthly = %v, want 0.8", item.StorageMonthly)
	}
	if report.Totals.StorageMonthly < 0.79 {
		t.Fatalf("total storage monthly = %v, want 0.8", report.Totals.StorageMonthly)
	}
}

func TestComputeFinOpsWithoutPricing(t *testing.T) {
	rows := []WorkspaceOverview{{
		Name:      "dev",
		Phase:     "Running",
		Available: true,
		Usage:     ResourceUsage{CPU: f64(0)},
		Requests:  ResourceUsage{CPU: f64(1)},
	}}
	report := ComputeFinOps(rows, Pricing{})
	item := report.Workspaces[0]
	if item.WastedHourly != nil || item.ReservedHourly != nil {
		t.Fatalf("cost fields should be absent when pricing is disabled: %+v", item)
	}
	if item.IdleCPU == nil || *item.IdleCPU != 1 {
		t.Fatalf("idle CPU = %v, want 1", item.IdleCPU)
	}
}

func TestComputeFinOpsMissingRequests(t *testing.T) {
	report := ComputeFinOps([]WorkspaceOverview{{
		Name:      "orphan",
		Phase:     "Running",
		Available: true,
		Usage:     ResourceUsage{CPU: f64(0.1)},
	}}, Pricing{CPUCoreHour: 1})
	item := report.Workspaces[0]
	if !hasSignal(item.Signals, SignalMissingRequests) {
		t.Fatalf("missing requests signal: %v", item.Signals)
	}
	if report.Totals.MissingRequests != 1 {
		t.Fatalf("missing requests total = %d, want 1", report.Totals.MissingRequests)
	}
}

func hasSignal(signals []string, want string) bool {
	for _, signal := range signals {
		if signal == want {
			return true
		}
	}
	return false
}
