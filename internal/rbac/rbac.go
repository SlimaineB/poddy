// Package rbac stores poddy roles in a Kubernetes ConfigMap.
//
// Roles are intentionally simple:
//   - global admins see and manage every workspace and template;
//   - template owners manage the users allowed to use their templates;
//   - regular users see their own workspaces and the templates assigned to them.
//
// The ConfigMap is the source of truth. No external database is required.
package rbac

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"
)

const (
	// DefaultConfigMap is the ConfigMap read by the portal when no name is set.
	DefaultConfigMap = "poddy-rbac"
	// DataKey is the ConfigMap key holding the YAML document.
	DataKey = "rbac.yaml"
	// Wildcard matches every authenticated user in an access list.
	Wildcard = "*"
)

// Identity is the authenticated user used for role matching.
type Identity struct {
	Username string
	Email    string
	Groups   []string
}

// Quota is an optional resource budget. A nil or empty field means unlimited.
// Team quotas are defaults for their members; a user quota overrides the
// corresponding team field.
type Quota struct {
	MaxWorkspaces *int   `json:"maxWorkspaces,omitempty" yaml:"maxWorkspaces,omitempty"`
	CPU           string `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory        string `json:"memory,omitempty" yaml:"memory,omitempty"`
	Storage       string `json:"storage,omitempty" yaml:"storage,omitempty"`
}

// Usage is the projected resource usage checked against a quota.
type Usage struct {
	Workspaces int
	CPU        resource.Quantity
	Memory     resource.Quantity
	Storage    resource.Quantity
}

// Team is a named group of users managed by the portal.
type Team struct {
	Members []string `json:"members,omitempty"`
	Quota   *Quota   `json:"quota,omitempty" yaml:"quota,omitempty"`
}

// Access describes who can use and manage a template.
type Access struct {
	Owners []string `json:"owners,omitempty"`
	Users  []string `json:"users,omitempty"`
	Teams  []string `json:"teams,omitempty"`
}

// Config is the RBAC document stored in the ConfigMap.
type Config struct {
	Admins     []string          `json:"admins,omitempty"`
	Auditors   []string          `json:"auditors,omitempty"`
	Teams      map[string]Team   `json:"teams,omitempty"`
	UserQuotas map[string]Quota  `json:"userQuotas,omitempty" yaml:"userQuotas,omitempty"`
	Templates  map[string]Access `json:"templates,omitempty"`
}

var teamName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// ValidateTeamName checks that a team name is safe to use as a YAML key.
func ValidateTeamName(name string) error {
	name = strings.TrimSpace(name)
	if !teamName.MatchString(name) {
		return fmt.Errorf("invalid team name %q: use letters, numbers, dots, dashes or underscores", name)
	}
	return nil
}

// IsZero reports whether the quota has no configured limit.
func (q Quota) IsZero() bool {
	return q.MaxWorkspaces == nil &&
		strings.TrimSpace(q.CPU) == "" &&
		strings.TrimSpace(q.Memory) == "" &&
		strings.TrimSpace(q.Storage) == ""
}

// Normalize trims whitespace from resource fields.
func (q *Quota) Normalize() {
	if q == nil {
		return
	}
	q.CPU = strings.TrimSpace(q.CPU)
	q.Memory = strings.TrimSpace(q.Memory)
	q.Storage = strings.TrimSpace(q.Storage)
}

// Validate checks that quota values are non-negative Kubernetes quantities.
func (q Quota) Validate() error {
	if q.MaxWorkspaces != nil && *q.MaxWorkspaces < 0 {
		return fmt.Errorf("maxWorkspaces must be zero or greater")
	}
	for name, value := range map[string]string{
		"cpu":     q.CPU,
		"memory":  q.Memory,
		"storage": q.Storage,
	} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return fmt.Errorf("invalid %s %q: %w", name, value, err)
		}
		if quantity.Sign() < 0 {
			return fmt.Errorf("%s must be zero or greater", name)
		}
	}
	return nil
}

// Check returns an error when the projected usage exceeds the quota.
func (q Quota) Check(usage Usage) error {
	if q.MaxWorkspaces != nil && usage.Workspaces > *q.MaxWorkspaces {
		return fmt.Errorf("workspace quota exceeded: %d of %d", usage.Workspaces, *q.MaxWorkspaces)
	}
	checks := []struct {
		name  string
		limit string
		used  resource.Quantity
	}{
		{"CPU", q.CPU, usage.CPU},
		{"memory", q.Memory, usage.Memory},
		{"storage", q.Storage, usage.Storage},
	}
	for _, check := range checks {
		limit := strings.TrimSpace(check.limit)
		if limit == "" {
			continue
		}
		quantity, err := resource.ParseQuantity(limit)
		if err != nil {
			return fmt.Errorf("invalid %s quota %q: %w", check.name, limit, err)
		}
		if check.used.Cmp(quantity) > 0 {
			return fmt.Errorf("%s quota exceeded: %s of %s", check.name, check.used.String(), limit)
		}
	}
	return nil
}

// Validate checks team and user quota entries.
func (c Config) Validate() error {
	for name, team := range c.Teams {
		if err := ValidateTeamName(name); err != nil {
			return err
		}
		if team.Quota == nil {
			continue
		}
		quota := *team.Quota
		quota.Normalize()
		if err := quota.Validate(); err != nil {
			return fmt.Errorf("team %q quota: %w", name, err)
		}
	}
	for identity, quota := range c.UserQuotas {
		if strings.TrimSpace(identity) == "" {
			return fmt.Errorf("user quota identity is empty")
		}
		quota.Normalize()
		if err := quota.Validate(); err != nil {
			return fmt.Errorf("user %q quota: %w", identity, err)
		}
	}
	return nil
}

// UserQuota returns the explicit user override, if any. Keys may be a plain
// username/email, user:<username>, or email:<email>.
func (c Config) UserQuota(id Identity) (Quota, bool) {
	candidates := []string{id.Username, id.Email}
	if id.Username != "" {
		candidates = append(candidates, "user:"+id.Username)
	}
	if id.Email != "" {
		candidates = append(candidates, "email:"+id.Email)
	}
	keys := make([]string, 0, len(c.UserQuotas))
	for key := range c.UserQuotas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, candidate := range candidates {
			if candidate != "" && strings.EqualFold(strings.TrimSpace(key), candidate) {
				return c.UserQuotas[key], true
			}
		}
	}
	return Quota{}, false
}

// EffectiveQuota resolves a user override field by field, then fills missing
// fields from the most permissive team quota. No quota means unlimited.
func (c Config) EffectiveQuota(id Identity) (Quota, error) {
	user, hasUser := c.UserQuota(id)
	user.Normalize()
	out := user
	names := c.TeamNames()
	for _, name := range names {
		team := c.Teams[name]
		if team.Quota == nil || !c.InTeam(name, id) {
			continue
		}
		quota := *team.Quota
		quota.Normalize()
		if err := out.fillMostPermissive(quota, user, hasUser); err != nil {
			return Quota{}, fmt.Errorf("team %q quota: %w", name, err)
		}
	}
	return out, nil
}

func (q *Quota) fillMostPermissive(team, user Quota, hasUser bool) error {
	if !hasUser || user.MaxWorkspaces == nil {
		if q.MaxWorkspaces == nil {
			q.MaxWorkspaces = team.MaxWorkspaces
		} else if team.MaxWorkspaces != nil && *team.MaxWorkspaces > *q.MaxWorkspaces {
			q.MaxWorkspaces = team.MaxWorkspaces
		}
	}
	fields := []struct {
		name  string
		value *string
		user  string
		team  string
	}{
		{"cpu", &q.CPU, user.CPU, team.CPU},
		{"memory", &q.Memory, user.Memory, team.Memory},
		{"storage", &q.Storage, user.Storage, team.Storage},
	}
	for _, field := range fields {
		if hasUser && strings.TrimSpace(field.user) != "" {
			continue
		}
		if strings.TrimSpace(field.team) == "" {
			continue
		}
		if strings.TrimSpace(*field.value) == "" {
			*field.value = field.team
			continue
		}
		current, err := resource.ParseQuantity(*field.value)
		if err != nil {
			return fmt.Errorf("invalid %s %q: %w", field.name, *field.value, err)
		}
		candidate, err := resource.ParseQuantity(field.team)
		if err != nil {
			return fmt.Errorf("invalid %s %q: %w", field.name, field.team, err)
		}
		if candidate.Cmp(current) > 0 {
			*field.value = field.team
		}
	}
	return nil
}

// Store reads and writes the RBAC ConfigMap.
type Store struct {
	client    kubernetes.Interface
	namespace string
	name      string
	mu        sync.Mutex
}

// NewStore returns a ConfigMap-backed RBAC store.
func NewStore(client kubernetes.Interface, namespace, name string) *Store {
	if name == "" {
		name = DefaultConfigMap
	}
	return &Store{client: client, namespace: namespace, name: name}
}

// Load returns the current RBAC configuration. A missing ConfigMap is not an
// error: it means no admins, no owners and public templates.
func (s *Store) Load(ctx context.Context) (Config, error) {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	return Parse(cm.Data[DataKey])
}

// Update applies mutate to the latest RBAC document. The write is serialized
// per process and retried on conflicts so concurrent changes on different
// replicas do not lose updates.
func (s *Store) Update(ctx context.Context, mutate func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out Config
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
		notFound := apierrors.IsNotFound(err)
		if err != nil && !notFound {
			return err
		}
		cfg := Config{}
		if err == nil {
			cfg, err = Parse(cm.Data[DataKey])
			if err != nil {
				return err
			}
		}
		if err := mutate(&cfg); err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		raw, err := cfg.Marshal()
		if err != nil {
			return err
		}
		if notFound {
			_, err = s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: s.name},
				Data:       map[string]string{DataKey: raw},
			}, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				return apierrors.NewConflict(corev1.Resource("configmaps"), s.name, err)
			}
		} else {
			if cm.Data == nil {
				cm.Data = map[string]string{}
			}
			cm.Data[DataKey] = raw
			_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
		}
		if err == nil {
			out = cfg
		}
		return err
	})
	if err != nil {
		return Config{}, fmt.Errorf("update rbac configmap %s: %w", s.name, err)
	}
	return out, nil
}

// Save replaces the RBAC document.
func (s *Store) Save(ctx context.Context, cfg Config) error {
	_, err := s.Update(ctx, func(target *Config) error {
		*target = cfg
		return nil
	})
	return err
}

// Parse decodes the YAML document. An empty document is valid.
func Parse(raw string) (Config, error) {
	var cfg Config
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse rbac: %w", err)
	}
	return cfg, nil
}

// Marshal encodes the document as YAML.
func (c Config) Marshal() (string, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal rbac: %w", err)
	}
	return string(data), nil
}

// IsAdmin reports whether the identity is a global admin.
func (c Config) IsAdmin(id Identity) bool {
	return matchesAny(c.Admins, id)
}

// IsAuditor reports whether the identity can read the audit trail.
func (c Config) IsAuditor(id Identity) bool {
	return matchesAny(c.Auditors, id)
}

// CanViewAudit reports whether the identity can read the audit trail.
func (c Config) CanViewAudit(id Identity) bool {
	return c.IsAdmin(id) || c.IsAuditor(id)
}

// Access returns the access entry for a template.
func (c Config) Access(template string) (Access, bool) {
	a, ok := c.Templates[template]
	return a, ok
}

// IsOwner reports whether the identity owns the template.
func (c Config) IsOwner(template string, id Identity) bool {
	a, ok := c.Templates[template]
	return ok && matchesAny(a.Owners, id)
}

// CanManage reports whether the identity can edit the template access list.
func (c Config) CanManage(template string, id Identity) bool {
	return c.IsAdmin(id) || c.IsOwner(template, id)
}

// CanUse reports whether the identity can create workspaces from the template.
//
// A template without an access entry is public. Once an entry exists, only the
// admins, the owners and the listed users/groups can use it. Use "*" in Users
// to keep a template with owners public.
func (c Config) CanUse(template string, id Identity) bool {
	if c.IsAdmin(id) || c.IsOwner(template, id) {
		return true
	}
	a, ok := c.Templates[template]
	if !ok {
		return true
	}
	if matchesAny(a.Users, id) {
		return true
	}
	for _, name := range a.Teams {
		if c.InTeam(name, id) {
			return true
		}
	}
	return false
}

// InTeam reports whether the identity is a member of the team.
func (c Config) InTeam(name string, id Identity) bool {
	team, ok := c.Teams[name]
	if !ok {
		return false
	}
	return matchesAny(team.Members, id)
}

// TeamNames returns the team names, sorted.
func (c Config) TeamNames() []string {
	out := make([]string, 0, len(c.Teams))
	for name := range c.Teams {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// OwnedTemplates returns the templates owned by the identity, sorted.
func (c Config) OwnedTemplates(id Identity) []string {
	out := make([]string, 0)
	for name, access := range c.Templates {
		if matchesAny(access.Owners, id) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Normalize trims, drops empty entries and removes case-insensitive duplicates.
func Normalize(entries []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key := strings.ToLower(entry)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	return out
}

func matchesAny(rules []string, id Identity) bool {
	for _, rule := range rules {
		if match(rule, id) {
			return true
		}
	}
	return false
}

func match(rule string, id Identity) bool {
	rule = strings.TrimSpace(rule)
	switch {
	case rule == "":
		return false
	case rule == Wildcard:
		return true
	case strings.HasPrefix(rule, "group:"):
		return containsFold(id.Groups, strings.TrimPrefix(rule, "group:"))
	case strings.HasPrefix(rule, "user:"):
		return equalFold(id.Username, strings.TrimPrefix(rule, "user:"))
	case strings.HasPrefix(rule, "email:"):
		return equalFold(id.Email, strings.TrimPrefix(rule, "email:"))
	default:
		return equalFold(id.Username, rule) || equalFold(id.Email, rule)
	}
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if equalFold(value, want) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
