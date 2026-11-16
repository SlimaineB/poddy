package monitoring

import "time"

// ResourceUsage uses base units so callers can format or compare values
// without parsing Kubernetes quantities.
type ResourceUsage struct {
	CPU    *float64 `json:"cpuCores,omitempty"`
	Memory *int64   `json:"memoryBytes,omitempty"`
}

// Add accumulates another resource usage. Nil values are ignored so callers
// can distinguish missing metrics from real zero usage.
func (u *ResourceUsage) Add(other ResourceUsage) {
	if other.CPU != nil {
		if u.CPU == nil {
			zero := 0.0
			u.CPU = &zero
		}
		*u.CPU += *other.CPU
	}
	if other.Memory != nil {
		if u.Memory == nil {
			zero := int64(0)
			u.Memory = &zero
		}
		*u.Memory += *other.Memory
	}
}

// ContainerMetrics contains live usage, declared resources and pod status for
// one workspace container.
type ContainerMetrics struct {
	Name      string         `json:"name"`
	Usage     *ResourceUsage `json:"usage,omitempty"`
	Requests  *ResourceUsage `json:"requests,omitempty"`
	Limits    *ResourceUsage `json:"limits,omitempty"`
	Ready     bool           `json:"ready"`
	Restarts  int32          `json:"restarts"`
	State     string         `json:"state,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	OOMKilled bool           `json:"oomKilled,omitempty"`
}

// WorkspaceMetrics is the live monitoring payload for one workspace.
type WorkspaceMetrics struct {
	Available     bool               `json:"available"`
	Reason        string             `json:"reason,omitempty"`
	Source        string             `json:"source,omitempty"`
	ObservedAt    *time.Time         `json:"observedAt,omitempty"`
	WindowSeconds int64              `json:"windowSeconds,omitempty"`
	Phase         string             `json:"phase,omitempty"`
	Node          string             `json:"node,omitempty"`
	StartedAt     *time.Time         `json:"startedAt,omitempty"`
	Containers    []ContainerMetrics `json:"containers"`
	Usage         ResourceUsage      `json:"usage"`
	Requests      ResourceUsage      `json:"requests"`
	Limits        ResourceUsage      `json:"limits"`
}

// WorkspaceOverview is one row in the portal-wide monitoring table.
type WorkspaceOverview struct {
	Name         string        `json:"name"`
	Template     string        `json:"template,omitempty"`
	Owner        string        `json:"owner,omitempty"`
	Phase        string        `json:"phase,omitempty"`
	Region       string        `json:"region,omitempty"`
	Available    bool          `json:"available"`
	Reason       string        `json:"reason,omitempty"`
	Usage        ResourceUsage `json:"usage"`
	Requests     ResourceUsage `json:"requests"`
	Limits       ResourceUsage `json:"limits"`
	Restarts     int32         `json:"restarts"`
	Ready        bool          `json:"ready"`
	OOMKilled    bool          `json:"oomKilled,omitempty"`
	StorageBytes int64         `json:"storageBytes,omitempty"`
	ObservedAt   *time.Time    `json:"observedAt,omitempty"`
}

// Overview is the portal-wide live monitoring payload.
type Overview struct {
	Available     bool                `json:"available"`
	Reason        string              `json:"reason,omitempty"`
	Source        string              `json:"source,omitempty"`
	ObservedAt    *time.Time          `json:"observedAt,omitempty"`
	WindowSeconds int64               `json:"windowSeconds,omitempty"`
	Usage         ResourceUsage       `json:"usage"`
	Requests      ResourceUsage       `json:"requests"`
	Limits        ResourceUsage       `json:"limits"`
	Workspaces    []WorkspaceOverview `json:"workspaces"`
}

// Point is one sample in a history series.
type Point struct {
	Timestamp float64 `json:"t"`
	Value     float64 `json:"v"`
}

// Series is one named history series returned by Prometheus.
type Series struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Unit   string  `json:"unit"`
	Points []Point `json:"points"`
}

// History is the historical monitoring payload for one workspace or for the
// whole portal.
type History struct {
	Available   bool      `json:"available"`
	Reason      string    `json:"reason,omitempty"`
	Source      string    `json:"source,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	StepSeconds int64     `json:"stepSeconds"`
	Series      []Series  `json:"series"`
}

// UsageSnapshot is the minimal live usage for one pod.
type UsageSnapshot struct {
	CPU           *float64
	Memory        *int64
	ObservedAt    *time.Time
	WindowSeconds int64
}
