package server

import (
	"net/http"
	"sort"
	"strings"

	"poddy/internal/rbac"
)

// users returns the identities known to the portal. It is a suggestion
// directory for the access and team editors, not an authentication source.
func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	admin := s.isAdmin(id, cfg)
	if !admin && len(cfg.OwnedTemplates(rbacIdentity(id.User))) == 0 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	users := map[string]bool{}
	groups := map[string]bool{}
	add := func(entries []string) {
		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			switch {
			case entry == "", entry == rbac.Wildcard:
			case strings.HasPrefix(entry, "group:"):
				if group := strings.TrimSpace(strings.TrimPrefix(entry, "group:")); group != "" {
					groups[group] = true
				}
			case strings.HasPrefix(entry, "user:"):
				if user := strings.TrimSpace(strings.TrimPrefix(entry, "user:")); user != "" {
					users[user] = true
				}
			case strings.HasPrefix(entry, "email:"):
				if user := strings.TrimSpace(strings.TrimPrefix(entry, "email:")); user != "" {
					users[user] = true
				}
			default:
				users[entry] = true
			}
		}
	}

	add(cfg.Admins)
	for _, team := range cfg.Teams {
		add(team.Members)
	}
	for _, access := range cfg.Templates {
		add(access.Owners)
		add(access.Users)
	}
	for _, group := range id.User.Groups {
		if group = strings.TrimSpace(group); group != "" {
			groups[group] = true
		}
	}
	if id.User.Email != "" {
		users[id.User.Email] = true
	} else if id.User.Username != "" {
		users[id.User.Username] = true
	}

	if s.Users != nil {
		if list, err := s.Users.List(r.Context()); err == nil {
			for _, user := range list {
				if user.Email != "" {
					users[user.Email] = true
				}
				if user.Username != "" {
					users[user.Username] = true
				}
				for _, group := range user.Groups {
					if group = strings.TrimSpace(group); group != "" {
						groups[group] = true
					}
				}
			}
		}
	}

	if admin {
		if list, err := s.Workspaces.List(r.Context()); err == nil {
			for _, workspace := range list {
				if owner := strings.TrimSpace(workspace.Owner); owner != "" {
					users[owner] = true
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"users":  sortedKeys(users),
		"groups": sortedKeys(groups),
	})
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}
