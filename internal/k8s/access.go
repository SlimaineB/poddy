package k8s

import "poddy/internal/access"

// Access state types live in the access package so the storage backend
// (PostgreSQL or in-memory) is independent from Kubernetes. These aliases keep
// the existing workspace, region, and topology APIs stable.
type (
	AccessRequest   = access.AccessRequest
	AccessGrant     = access.AccessGrant
	Presence        = access.Presence
	AccessState     = access.AccessState
	PresenceSummary = access.PresenceSummary
	WorkspaceAccess = access.WorkspaceAccess
	AccessStore     = access.Store
)

const (
	// DefaultAccessGrantTTL is how long an owner approval lasts.
	DefaultAccessGrantTTL = access.DefaultAccessGrantTTL
	// DefaultPresenceTTL is how long a presence entry is considered fresh.
	DefaultPresenceTTL = access.DefaultPresenceTTL
)

// NewPresenceID returns an opaque, unguessable interactive session identifier.
func NewPresenceID() (string, error) { return access.NewPresenceID() }

// NewMemoryAccessStore returns an in-process access store. It is intended for
// tests and single-replica deployments without a database.
func NewMemoryAccessStore() AccessStore { return access.NewMemory() }
