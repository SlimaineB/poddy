import type { Page, Route } from "@playwright/test";

export const admin = {
  username: "alice-admin",
  name: "Alice Admin",
  email: "alice@example.com",
  groups: [],
  method: "token",
  isAdmin: true,
  isAuditor: false,
  canViewAudit: true,
  ownedTemplates: [],
};

export const regularUser = {
  ...admin,
  username: "bob-user",
  name: "Bob User",
  email: "bob@example.com",
  isAdmin: false,
  canViewAudit: false,
};

export const templates = [
  {
    name: "base",
    displayName: "Base",
    description: "A general-purpose development workspace.",
    image: "ghcr.io/slimaineb/poddy-workspace-base:latest",
    version: "latest",
    parameters: [],
  },
];

export const workspaces = [
  {
    name: "alice-base",
    template: "base",
    phase: "Running",
    owner: "alice@example.com",
    apps: ["vscode"],
    createdAt: "2026-11-16T10:00:00Z",
  },
  {
    name: "bob-python",
    template: "base",
    phase: "Stopped",
    owner: "bob@example.com",
    apps: ["vscode"],
    createdAt: "2026-11-15T09:30:00Z",
  },
];

export const teams = [
  {
    name: "backend",
    members: ["alice@example.com", "group:platform"],
    memberCount: 2,
    groupCount: 1,
    quota: { maxWorkspaces: 2 },
  },
];

export const directory = {
  users: ["alice@example.com", "bob@example.com"],
  groups: ["platform"],
};

export const userQuotas = [
  { identity: "alice@example.com", quota: { maxWorkspaces: 1 } },
];

type Workspace = (typeof workspaces)[number];
type Template = (typeof templates)[number];
type Team = (typeof teams)[number];

type MockOptions = {
  me?: Record<string, unknown>;
  workspaces?: Workspace[];
  templates?: Template[];
  teams?: Team[];
  directory?: typeof directory;
  userQuotas?: typeof userQuotas;
  config?: Record<string, unknown>;
  onWorkspaceCreate?: (body: Record<string, unknown>) => void;
};

const defaultConfig = {
  ssh: false,
  sshCertificates: false,
  localMode: false,
  localVSCode: false,
  localCursor: false,
  regions: [],
  monitoring: {
    enabled: false,
    live: false,
    history: false,
    global: false,
  },
};

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

function matchesQuery(workspace: Workspace, query: string) {
  if (!query) return true;
  return [workspace.name, workspace.template, workspace.owner, workspace.phase]
    .join(" ")
    .toLowerCase()
    .includes(query);
}

export async function mockPortal(page: Page, options: MockOptions = {}) {
  const me = options.me ?? admin;
  const workspaceData = options.workspaces ?? workspaces;
  const templateData = options.templates ?? templates;
  const teamData = options.teams ?? teams;
  const directoryData = options.directory ?? directory;
  const quotaData = options.userQuotas ?? userQuotas;
  const config = options.config ?? defaultConfig;

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;

    if (path === "/api/me") return json(route, me);
    if (path === "/api/config") return json(route, config);
    if (path === "/api/templates") return json(route, templateData);
    if (path === "/api/external-auth") return json(route, { enabled: false, providers: [] });
    if (path === "/api/me/quota") {
      return json(route, { usage: { workspaces: workspaceData.length }, effective: {} });
    }
    if (path === "/api/monitoring/finops") return json(route, { workspaces: [] });

    if (path === "/api/teams" && request.method() === "GET") return json(route, teamData);
    if (path.startsWith("/api/teams/") && request.method() === "GET") {
      const name = decodeURIComponent(path.slice("/api/teams/".length));
      const team = teamData.find((entry) => entry.name === name);
      return team ? json(route, team) : json(route, { error: "team not found" }, 404);
    }
    if (path === "/api/users" && request.method() === "GET") {
      if (url.searchParams.has("page")) {
        return json(route, {
          users: directoryData.users,
          groups: directoryData.groups,
          totalUsers: directoryData.users.length,
          totalGroups: directoryData.groups.length,
          page: 1,
          perPage: 25,
          pages: 1,
          groupPage: 1,
          groupPerPage: 25,
          groupPages: 1,
        });
      }
      return json(route, directoryData);
    }
    if (path === "/api/user-quotas" && request.method() === "GET") return json(route, quotaData);
    if (path.startsWith("/api/user-quotas/") && request.method() === "GET") {
      return json(route, {
        effective: { maxWorkspaces: 2 },
        usage: { workspaces: 1 },
      });
    }

    if (path === "/api/volumes" && request.method() === "GET") {
      return json(route, {
        volumes: [],
        summary: { total: 0, assigned: 0, unassigned: 0, bytes: 0, assignedBytes: 0, unassignedBytes: 0 },
        backup: false,
        total: 0,
        pages: 0,
        page: 1,
        perPage: 25,
      });
    }

    if (path === "/api/workspaces" && request.method() === "GET") {
      const query = (url.searchParams.get("q") ?? "").toLowerCase();
      return json(route, workspaceData.filter((workspace) => matchesQuery(workspace, query)));
    }

    if (path === "/api/workspaces" && request.method() === "POST") {
      const body = request.postDataJSON() as Record<string, unknown>;
      options.onWorkspaceCreate?.(body);
      return json(route, { name: body.name }, 201);
    }

    return json(route, { error: `Unmocked ${request.method()} ${path}` }, 404);
  });
}
