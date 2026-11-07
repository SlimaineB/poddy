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

	userList := sortedKeys(users)
	groupList := sortedKeys(groups)
	query := searchQuery(r)
	if query != "" {
		filteredUsers := make([]string, 0, len(userList))
		for _, user := range userList {
			if matchesSearch(query, user) {
				filteredUsers = append(filteredUsers, user)
			}
		}
		userList = filteredUsers
		filteredGroups := make([]string, 0, len(groupList))
		for _, group := range groupList {
			if matchesSearch(query, group) {
				filteredGroups = append(filteredGroups, group)
			}
		}
		groupList = filteredGroups
	}
	if !pagedRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{
			"users":  userList,
			"groups": groupList,
		})
		return
	}
	page, perPage := pageRequest(r)
	groupPage, groupPerPage := pageFromRequest(r, "group_page", "group_per_page")
	pages := pageCount(len(userList), perPage)
	if pages > 0 && page > pages {
		page = pages
	}
	if page < 1 {
		page = 1
	}
	groupPages := pageCount(len(groupList), groupPerPage)
	if groupPages > 0 && groupPage > groupPages {
		groupPage = groupPages
	}
	if groupPage < 1 {
		groupPage = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":        pageSlice(userList, page, perPage),
		"groups":       pageSlice(groupList, groupPage, groupPerPage),
		"totalUsers":   len(userList),
		"totalGroups":  len(groupList),
		"page":         page,
		"perPage":      perPage,
		"pages":        pages,
		"groupPage":    groupPage,
		"groupPerPage": groupPerPage,
		"groupPages":   groupPages,
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
