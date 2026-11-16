package monitoring

import (
	"math"
	"time"
)

// FinOps signals attached to a workspace so the UI can explain why it is
// considered wasteful.
const (
	SignalIdleCPU          = "idle-cpu"
	SignalIdleMemory       = "idle-memory"
	SignalOversized        = "oversized"
	SignalContended        = "contended"
	SignalMissingRequests  = "missing-requests"
	SignalNoMetrics        = "no-metrics"
	SignalNotRunning       = "not-running"
	SignalRestarts         = "restarts"
	SignalOOM              = "oom"
	SignalMissingWorkspace = "missing-workspace"
)

const (
	gib = float64(1024 * 1024 * 1024)
	mib = float64(1024 * 1024)
)

// Pricing holds the configurable unit prices used by the FinOps report. A zero
// value disables the corresponding cost estimate, which is the expected setup
// on-prem where only resource waste is meaningful.
type Pricing struct {
	CPUCoreHour    float64 `json:"cpuCoreHour"`
	MemoryGBHour   float64 `json:"memoryGBHour"`
	StorageGBMonth float64 `json:"storageGBMonth"`
	Currency       string  `json:"currency,omitempty"`
}

// Configured reports whether at least one unit price is set.
func (p Pricing) Configured() bool {
	return p.CPUCoreHour > 0 || p.MemoryGBHour > 0 || p.StorageGBMonth > 0
}

// FinOpsWorkspace is one workspace in the FinOps report.
type FinOpsWorkspace struct {
	Name              string        `json:"name"`
	Template          string        `json:"template,omitempty"`
	Owner             string        `json:"owner,omitempty"`
	Teams             []string      `json:"teams,omitempty"`
	Region            string        `json:"region,omitempty"`
	Phase             string        `json:"phase,omitempty"`
	Available         bool          `json:"available"`
	Reason            string        `json:"reason,omitempty"`
	Usage             ResourceUsage `json:"usage"`
	Requests          ResourceUsage `json:"requests"`
	Limits            ResourceUsage `json:"limits"`
	CPUUtilization    *float64      `json:"cpuUtilization,omitempty"`
	MemoryUtilization *float64      `json:"memoryUtilization,omitempty"`
	IdleCPU           *float64      `json:"idleCpuCores,omitempty"`
	IdleMemory        *int64        `json:"idleMemoryBytes,omitempty"`
	StorageBytes      int64         `json:"storageBytes,omitempty"`
	ReservedHourly    *float64      `json:"reservedHourly,omitempty"`
	UsedHourly        *float64      `json:"usedHourly,omitempty"`
	WastedHourly      *float64      `json:"wastedHourly,omitempty"`
	StorageMonthly    *float64      `json:"storageMonthly,omitempty"`
	Restarts          int32         `json:"restarts,omitempty"`
	WasteScore        float64       `json:"wasteScore"`
	Signals           []string      `json:"signals,omitempty"`
}

// FinOpsTotals aggregates the report.
type FinOpsTotals struct {
	Workspaces      int     `json:"workspaces"`
	Idle            int     `json:"idle"`
	Oversized       int     `json:"oversized"`
	MissingRequests int     `json:"missingRequests"`
	Restarts        int64   `json:"restarts"`
	CPUUsage        float64 `json:"cpuUsageCores"`
	CPURequests     float64 `json:"cpuRequestsCores"`
	CPUIdle         float64 `json:"cpuIdleCores"`
	MemoryUsage     int64   `json:"memoryUsageBytes"`
	MemoryRequests  int64   `json:"memoryRequestsBytes"`
	MemoryIdle      int64   `json:"memoryIdleBytes"`
	StorageBytes    int64   `json:"storageBytes"`
	ReservedHourly  float64 `json:"reservedHourly"`
	UsedHourly      float64 `json:"usedHourly"`
	WastedHourly    float64 `json:"wastedHourly"`
	StorageMonthly  float64 `json:"storageMonthly"`
	WastePercent    float64 `json:"wastePercent"`
}

// FinOpsReport is the payload consumed by the FinOps view.
type FinOpsReport struct {
	Available   bool              `json:"available"`
	Reason      string            `json:"reason,omitempty"`
	Source      string            `json:"source,omitempty"`
	ObservedAt  *time.Time        `json:"observedAt,omitempty"`
	Admin       bool              `json:"admin"`
	CostVisible bool              `json:"costVisible"`
	Pricing     Pricing           `json:"pricing"`
	Totals      FinOpsTotals      `json:"totals"`
	Workspaces  []FinOpsWorkspace `json:"workspaces"`
}

// ComputeFinOps turns live monitoring rows into a waste and cost report. When
// pricing is not configured only resource waste is computed.
func ComputeFinOps(rows []WorkspaceOverview, pricing Pricing) FinOpsReport {
	report := FinOpsReport{
		Pricing:    pricing,
		Workspaces: make([]FinOpsWorkspace, 0, len(rows)),
	}
	costed := pricing.Configured()
	for _, row := range rows {
		item := computeFinOpsWorkspace(row, pricing, costed)
		report.Workspaces = append(report.Workspaces, item)

		report.Totals.Workspaces++
		report.Totals.Restarts += int64(item.Restarts)
		if item.Usage.CPU != nil {
			report.Totals.CPUUsage += *item.Usage.CPU
		}
		if item.Requests.CPU != nil {
			report.Totals.CPURequests += *item.Requests.CPU
		}
		if item.IdleCPU != nil {
			report.Totals.CPUIdle += *item.IdleCPU
		}
		if item.Usage.Memory != nil {
			report.Totals.MemoryUsage += *item.Usage.Memory
		}
		if item.Requests.Memory != nil {
			report.Totals.MemoryRequests += *item.Requests.Memory
		}
		if item.IdleMemory != nil {
			report.Totals.MemoryIdle += *item.IdleMemory
		}
		report.Totals.StorageBytes += item.StorageBytes
		if item.StorageMonthly != nil {
			report.Totals.StorageMonthly += *item.StorageMonthly
		}
		if item.ReservedHourly != nil {
			report.Totals.ReservedHourly += *item.ReservedHourly
		}
		if item.UsedHourly != nil {
			report.Totals.UsedHourly += *item.UsedHourly
		}
		if item.WastedHourly != nil {
			report.Totals.WastedHourly += *item.WastedHourly
		}
	}
	// Idle and oversized can both be present on the same workspace, so count
	// them from the signal set instead of accumulating per signal.
	report.Totals.Idle = 0
	report.Totals.Oversized = 0
	report.Totals.MissingRequests = 0
	for _, item := range report.Workspaces {
		hasIdle, hasOversized, hasMissing := false, false, false
		for _, signal := range item.Signals {
			switch signal {
			case SignalIdleCPU, SignalIdleMemory:
				hasIdle = true
			case SignalOversized:
				hasOversized = true
			case SignalMissingRequests:
				hasMissing = true
			}
		}
		if hasIdle {
			report.Totals.Idle++
		}
		if hasOversized {
			report.Totals.Oversized++
		}
		if hasMissing {
			report.Totals.MissingRequests++
		}
	}
	if report.Totals.ReservedHourly > 0 {
		report.Totals.WastePercent = math.Round(report.Totals.WastedHourly/report.Totals.ReservedHourly*1000) / 10
	}
	return report
}

func computeFinOpsWorkspace(row WorkspaceOverview, pricing Pricing, costed bool) FinOpsWorkspace {
	item := FinOpsWorkspace{
		Name:         row.Name,
		Template:     row.Template,
		Owner:        row.Owner,
		Region:       row.Region,
		Phase:        row.Phase,
		Available:    row.Available,
		Reason:       row.Reason,
		Usage:        row.Usage,
		Requests:     row.Requests,
		Limits:       row.Limits,
		Restarts:     row.Restarts,
		StorageBytes: row.StorageBytes,
	}

	signals := make([]string, 0, 5)
	if row.Phase != "" && row.Phase != "Running" {
		signals = append(signals, SignalNotRunning)
	}
	if !row.Available {
		signals = append(signals, SignalNoMetrics)
	}
	if row.OOMKilled {
		signals = append(signals, SignalOOM)
	}
	if row.Restarts > 0 {
		signals = append(signals, SignalRestarts)
	}

	cpuRequest := row.Requests.CPU
	cpuUsage := row.Usage.CPU
	memRequest := row.Requests.Memory
	memUsage := row.Usage.Memory

	if cpuRequest == nil && memRequest == nil {
		signals = append(signals, SignalMissingRequests)
	}

	var idleCPU *float64
	var idleMemory *int64
	idleScore := 0.0

	if cpuRequest != nil && cpuUsage != nil && *cpuRequest > 0 {
		util := *cpuUsage / *cpuRequest
		item.CPUUtilization = roundedPtr(util * 100)
		if util <= 0.20 {
			signals = append(signals, SignalIdleCPU)
		}
		if *cpuUsage > *cpuRequest*1.10 {
			signals = append(signals, SignalContended)
		}
		if *cpuRequest >= *cpuUsage*3 && *cpuRequest-*cpuUsage >= 0.25 {
			signals = append(signals, SignalOversized)
		}
		idle := math.Max(0, *cpuRequest-*cpuUsage)
		idleCPU = &idle
		idleScore += idle
	}
	if memRequest != nil && memUsage != nil && *memRequest > 0 {
		util := float64(*memUsage) / float64(*memRequest)
		item.MemoryUtilization = roundedPtr(util * 100)
		if util <= 0.25 {
			signals = append(signals, SignalIdleMemory)
		}
		if *memUsage > int64(float64(*memRequest)*1.10) {
			signals = append(signals, SignalContended)
		}
		if float64(*memRequest) >= float64(*memUsage)*3 && float64(*memRequest-*memUsage) >= 256*mib {
			signals = append(signals, SignalOversized)
		}
		idle := *memRequest - *memUsage
		if idle < 0 {
			idle = 0
		}
		idleMemory = &idle
		idleScore += float64(idle) / gib
	}
	item.IdleCPU = idleCPU
	item.IdleMemory = idleMemory
	item.WasteScore = math.Round(idleScore*1000) / 1000

	if costed {
		reserved := 0.0
		used := 0.0
		wasted := 0.0
		if cpuRequest != nil {
			reserved += *cpuRequest * pricing.CPUCoreHour
		}
		if memRequest != nil {
			reserved += float64(*memRequest) / gib * pricing.MemoryGBHour
		}
		if cpuUsage != nil {
			used += *cpuUsage * pricing.CPUCoreHour
		}
		if memUsage != nil {
			used += float64(*memUsage) / gib * pricing.MemoryGBHour
		}
		if idleCPU != nil {
			wasted += *idleCPU * pricing.CPUCoreHour
		}
		if idleMemory != nil {
			wasted += float64(*idleMemory) / gib * pricing.MemoryGBHour
		}
		item.ReservedHourly = roundedPtr(reserved)
		item.UsedHourly = roundedPtr(used)
		item.WastedHourly = roundedPtr(wasted)
		if row.StorageBytes > 0 && pricing.StorageGBMonth > 0 {
			storage := float64(row.StorageBytes) / gib * pricing.StorageGBMonth
			item.StorageMonthly = roundedPtr(storage)
		}
	}

	item.Signals = uniqueSignals(signals)
	return item
}

func uniqueSignals(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func roundedPtr(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	rounded := math.Round(value*10000) / 10000
	return &rounded
}
