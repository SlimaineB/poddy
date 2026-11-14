package envstore

import (
	"context"
	"strings"

	"poddy/internal/rbac"
	"poddy/internal/userstore"
)

// IdentityForOwner builds an RBAC identity from the workspace owner. The OIDC
// directory supplies group memberships when the owner is a known user.
func IdentityForOwner(ctx context.Context, users userstore.Directory, owner string) rbac.Identity {
	owner = strings.TrimSpace(owner)
	id := rbac.Identity{Username: owner}
	if strings.Contains(owner, "@") {
		id.Email = owner
	}
	if users == nil || owner == "" {
		return id
	}
	list, err := users.List(ctx)
	if err != nil {
		return id
	}
	for _, user := range list {
		switch {
		case strings.EqualFold(strings.TrimSpace(user.Email), owner),
			strings.EqualFold(strings.TrimSpace(user.Username), owner),
			strings.EqualFold(strings.TrimSpace(user.Subject), owner):
			return rbac.Identity{
				Username: user.Username,
				Email:    user.Email,
				Groups:   user.Groups,
			}
		}
	}
	return id
}

// TeamsForIdentity returns the RBAC team names the identity belongs to.
func TeamsForIdentity(cfg rbac.Config, id rbac.Identity) []string {
	names := cfg.TeamNames()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if cfg.InTeam(name, id) {
			out = append(out, name)
		}
	}
	return out
}

// ResolveForOwner loads RBAC and the user directory, then resolves the
// environment for a workspace owner.
func ResolveForOwner(ctx context.Context, store Store, rbacStore rbac.ConfigStore, users userstore.Directory, owner, template, workspace string) (Resolved, error) {
	cfg := rbac.Config{}
	if rbacStore != nil {
		loaded, err := rbacStore.Load(ctx)
		if err != nil {
			return Resolved{}, err
		}
		cfg = loaded
	}
	id := IdentityForOwner(ctx, users, owner)
	return store.Resolve(ctx, owner, TeamsForIdentity(cfg, id), template, workspace)
}
