"use strict";

let topologyRequest = 0;
let topologyPending = false;
let topologyQueued = false;
let topologyCy = null;
let topologySignature = "";
let topologyWorkspaceMap = new Map();

const topologyCardSize = {
  user: { nodeWidth: 244, nodeHeight: 96, cardWidth: 240, cardHeight: 92 },
  workspace: { nodeWidth: 364, nodeHeight: 100, cardWidth: 360, cardHeight: 96 },
  resource: { nodeWidth: 284, nodeHeight: 76, cardWidth: 280, cardHeight: 72 },
};

async function openTopology() {
  state.topologyActive = true;
  showView("topology");
  if (typeof syncWorkspaceViewButtons === "function") syncWorkspaceViewButtons();
  if (topologyCy) topologyCy.resize();
  await loadTopology();
}

function refreshTopologyIfVisible() {
  if (!els.topologyView.hidden) loadTopology({ silent: true });
}

async function loadTopology({ silent = false } = {}) {
  if (topologyPending) {
    topologyQueued = true;
    return;
  }
  topologyPending = true;
  const request = ++topologyRequest;
  if (!silent) {
    els.topologyLoading.hidden = false;
    els.topologyEmpty.hidden = true;
    els.topologyGraph.hidden = true;
    els.topologyHint.hidden = true;
  }
  try {
    const data = await api(pageURL("/api/topology", state.topologyPage, state.topologyPerPage));
    if (request !== topologyRequest) return;
    state.topology = data && typeof data === "object" ? data : { totals: {}, users: [] };
    state.topologyPage = Number(data.page) || 1;
    state.topologyPages = Number(data.pages) || 0;
    state.topologyTotal = Number(data.total) || 0;
    renderTopology();
    renderPagination(els.topologyPagination, {
      page: state.topologyPage,
      pages: state.topologyPages,
      total: state.topologyTotal,
      summary: `${state.topologyTotal} user${state.topologyTotal === 1 ? "" : "s"} · page ${state.topologyPage} of ${Math.max(1, state.topologyPages)}`,
    }, (page) => {
      state.topologyPage = page;
      loadTopology({ silent: true });
    });
  } catch (err) {
    if (!silent) {
      els.topologyLoading.hidden = true;
      els.topologyEmpty.hidden = false;
      toast(err.message || "Failed to load topology.");
    }
  } finally {
    topologyPending = false;
    if (topologyQueued) {
      topologyQueued = false;
      loadTopology({ silent: true });
    }
  }
}

function renderTopology() {
  const totals = state.topology.totals || {};
  const users = Array.isArray(state.topology.users) ? state.topology.users : [];
  els.topologyLoading.hidden = true;

  const parts = [
    `${totals.users || 0} user${totals.users === 1 ? "" : "s"}`,
    `${totals.workspaces || 0} workspace${totals.workspaces === 1 ? "" : "s"}`,
    `${totals.volumes || 0} volume${totals.volumes === 1 ? "" : "s"}`,
  ];
  if (totals.storageBytes) parts.push(formatStorage(totals.storageBytes));
  const regionLabel = state.regionFilter ? ` · ${regionDisplayName(state.regionFilter)}` : "";
  els.topologyCount.textContent = parts.join(" · ") + regionLabel;

  const workspaces = users.reduce((sum, user) => sum + (user.workspaceCount || 0), 0);
  if (!users.length || (!workspaces && !users.some((user) => user.current))) {
    els.topologyEmpty.hidden = false;
    els.topologyGraph.hidden = true;
    els.topologyHint.hidden = true;
    return;
  }

  els.topologyEmpty.hidden = true;
  els.topologyGraph.hidden = false;
  els.topologyHint.hidden = false;
  syncTopologyGraph(users);
}

function buildTopologyModel(users) {
  const nodes = [];
  const edges = [];
  const workspaces = new Map();

  for (const user of users) {
    const userId = `user:${user.identity}`;
    nodes.push({
      data: {
        id: userId,
        type: "user",
        name: user.name || user.identity,
        identity: user.identity,
        current: Boolean(user.current),
        workspaceCount: user.workspaceCount || 0,
        volumeCount: user.volumeCount || 0,
        storageBytes: user.storageBytes || 0,
      },
    });

    for (const workspace of user.workspaces || []) {
      const workspaceId = `workspace:${workspace.name}`;
      const resources = Array.isArray(workspace.resources) ? workspace.resources : [];
      workspaces.set(workspaceId, workspace);
      nodes.push({
        data: {
          id: workspaceId,
          type: "workspace",
          name: workspace.name,
          phase: workspace.phase || "Unknown",
          template: workspace.template || "",
          image: workspace.image || "",
          createdAt: workspace.createdAt || "",
          browserVSCode: Boolean(workspace.browserVSCode),
          region: workspace.region || "",
          pvcCount: resources.filter((resource) => resource.kind !== "Ephemeral").length,
        },
      });
      edges.push({
        data: {
          id: `edge:${userId}->${workspaceId}`,
          source: userId,
          target: workspaceId,
          type: "owner",
        },
      });

      for (const resource of resources) {
        const resourceId = `resource:${workspace.name}:${resource.name}`;
        nodes.push({
          data: {
            id: resourceId,
            type: "resource",
            kind: resource.kind || "PersistentVolumeClaim",
            name: resource.name || resource.kind || "volume",
            phase: resource.phase || "",
            capacity: resource.capacity || "",
            requested: resource.requested || "",
            storageClass: resource.storageClass || "",
            volumeName: resource.volumeName || "",
          },
        });
        edges.push({
          data: {
            id: `edge:${workspaceId}->${resourceId}`,
            source: workspaceId,
            target: resourceId,
            type: "storage",
          },
        });
      }
    }
  }

  return { nodes, edges, workspaces };
}

function ensureTopologyCy() {
  if (topologyCy) return topologyCy;
  if (typeof cytoscape !== "function") return null;

  topologyCy = cytoscape({
    container: els.topologyGraph,
    elements: [],
    style: [
      {
        selector: "node",
        style: {
          "background-opacity": 0,
          "border-width": 0,
          "border-opacity": 0,
          shape: "round-rectangle",
          label: "",
          "overlay-opacity": 0,
        },
      },
      {
        selector: 'node[type="user"]',
        style: {
          width: topologyCardSize.user.nodeWidth,
          height: topologyCardSize.user.nodeHeight,
        },
      },
      {
        selector: 'node[type="workspace"]',
        style: {
          width: topologyCardSize.workspace.nodeWidth,
          height: topologyCardSize.workspace.nodeHeight,
        },
      },
      {
        selector: 'node[type="resource"]',
        style: {
          width: topologyCardSize.resource.nodeWidth,
          height: topologyCardSize.resource.nodeHeight,
        },
      },
      {
        selector: "node:selected",
        style: {
          "border-width": 2,
          "border-opacity": 1,
          "border-color": "#2563eb",
        },
      },
      {
        selector: "edge",
        style: {
          "curve-style": "bezier",
          "bezier-control-point-step-size": 46,
          width: 1.8,
          "line-color": "#cbd5e1",
          "line-opacity": 0.9,
          "target-arrow-shape": "triangle",
          "target-arrow-color": "#cbd5e1",
          "arrow-scale": 0.72,
          "overlay-opacity": 0,
        },
      },
      {
        selector: 'edge[type="owner"]',
        style: {
          "line-color": "#94a3b8",
          "target-arrow-color": "#94a3b8",
        },
      },
      {
        selector: "node.dimmed",
        style: {
          "border-opacity": 0,
        },
      },
      {
        selector: "edge.dimmed",
        style: {
          opacity: 0.12,
        },
      },
      {
        selector: "edge.highlighted",
        style: {
          width: 2.6,
          "line-color": "#2563eb",
          "target-arrow-color": "#2563eb",
          opacity: 1,
        },
      },
    ],
    layout: { name: "preset" },
    minZoom: 0.22,
    maxZoom: 2.5,
    wheelSensitivity: 0.18,
    boxSelectionEnabled: false,
    selectionType: "single",
  });

  if (typeof topologyCy.nodeHtmlLabel === "function") {
    topologyCy.nodeHtmlLabel([
      { query: 'node[type="user"]', tpl: topologyCard, cssClass: "topology-label" },
      { query: 'node[type="workspace"]', tpl: topologyCard, cssClass: "topology-label" },
      { query: 'node[type="resource"]', tpl: topologyCard, cssClass: "topology-label" },
    ]);
  }

  topologyCy.on("mouseover", "node", (event) => {
    topologyFocus(event.target);
    els.topologyGraph.style.cursor = event.target.data("type") === "workspace" ? "pointer" : "grab";
  });
  topologyCy.on("mouseout", "node", () => {
    topologyClearFocus();
    els.topologyGraph.style.cursor = "";
  });
  topologyCy.on("tap", 'node[type="workspace"]', (event) => {
    const workspace = topologyWorkspaceMap.get(event.target.id());
    if (workspace) showDetails(workspace);
  });
  topologyCy.on("tap", (event) => {
    if (event.target === topologyCy) topologyClearFocus();
  });

  return topologyCy;
}

function syncTopologyGraph(users) {
  const cy = ensureTopologyCy();
  if (!cy) return;

  const model = buildTopologyModel(users);
  const incoming = new Set();
  for (const element of model.nodes) incoming.add(element.data.id);
  for (const element of model.edges) incoming.add(element.data.id);

  cy.batch(() => {
    cy.elements().forEach((element) => {
      if (!incoming.has(element.id())) element.remove();
    });
    for (const element of model.nodes) {
      const existing = cy.getElementById(element.data.id);
      if (existing.empty()) cy.add(element);
      else existing.data(element.data);
    }
    for (const element of model.edges) {
      const existing = cy.getElementById(element.data.id);
      if (existing.empty()) cy.add(element);
      else existing.data(element.data);
    }
  });

  topologyWorkspaceMap = model.workspaces;
  const signature = [
    ...model.nodes.map((element) => element.data.id),
    ...model.edges.map((element) => element.data.id),
  ].sort().join("|");

  if (signature !== topologySignature) {
    topologySignature = signature;
    runTopologyLayout();
  }
}

function runTopologyLayout() {
  if (!topologyCy) return;
  let layout;
  try {
    layout = topologyCy.layout({
      name: "dagre",
      rankDir: "LR",
      nodeSep: 28,
      rankSep: 96,
      edgeSep: 18,
      ranker: "network-simplex",
      animate: true,
      animationDuration: 480,
      animationEasing: "ease-out",
      fit: true,
      padding: 42,
    });
  } catch {
    layout = topologyCy.layout({
      name: "breadthfirst",
      directed: true,
      spacingFactor: 1.25,
      animate: true,
      animationDuration: 480,
      fit: true,
      padding: 42,
    });
  }
  layout.run();
}

function topologyFocus(node) {
  if (!topologyCy) return;
  const neighborhood = node.closedNeighborhood();
  topologyCy.elements().addClass("dimmed").removeClass("highlighted");
  neighborhood.addClass("highlighted").removeClass("dimmed");
  const visible = new Set(neighborhood.nodes().map((item) => item.id()));
  for (const card of els.topologyGraph.querySelectorAll(".topology-card")) {
    card.classList.toggle("dimmed", !visible.has(card.dataset.id));
  }
}

function topologyClearFocus() {
  if (!topologyCy) return;
  topologyCy.elements().removeClass("dimmed highlighted");
  for (const card of els.topologyGraph.querySelectorAll(".topology-card.dimmed")) {
    card.classList.remove("dimmed");
  }
}

function topologyCard(data) {
  if (data.type === "user") return topologyUserCard(data);
  if (data.type === "workspace") return topologyWorkspaceCard(data);
  return topologyResourceCard(data);
}

function topologyUserCard(data) {
  const identity = escapeTopologyHTML(data.identity);
  const name = escapeTopologyHTML(data.name || data.identity);
  const initial = escapeTopologyHTML(String(data.name || data.identity || "?").slice(0, 1).toUpperCase());
  const badges = [];
  if (data.current) badges.push('<span class="badge owner">you</span>');
  badges.push(`<span class="badge neutral">${Number(data.workspaceCount) || 0} ws</span>`);
  badges.push(`<span class="badge neutral">${Number(data.volumeCount) || 0} vol</span>`);
  if (data.storageBytes) badges.push(`<span class="badge neutral">${escapeTopologyHTML(formatStorage(data.storageBytes))}</span>`);
  return `<div class="topology-card topology-card-user${data.current ? " is-current" : ""}" data-id="${escapeTopologyHTML(data.id)}">
    <span class="topology-avatar">${initial}</span>
    <span class="topology-card-body">
      <span class="topology-card-title">${name}</span>
      <span class="topology-card-sub" title="${identity}">${identity}</span>
      <span class="topology-card-badges">${badges.join("")}</span>
    </span>
  </div>`;
}

function topologyWorkspaceCard(data) {
  const phase = data.phase || "Unknown";
  const phaseClass = topologyPhaseClass(phase);
  const meta = [`<span class="status"><span class="dot ${phaseClass}"></span>${escapeTopologyHTML(phase)}</span>`];
  if (data.region) meta.push(`<span class="badge neutral">${escapeTopologyHTML(data.region)}</span>`);
  if (data.template) meta.push(`<span class="badge neutral">${escapeTopologyHTML(data.template)}</span>`);
  if (data.pvcCount) meta.push(`<span class="badge neutral">${Number(data.pvcCount)} PVC</span>`);
  if (data.browserVSCode) meta.push('<span class="badge neutral">VS Code Web</span>');
  if (data.createdAt) meta.push(`<span class="topology-age">${escapeTopologyHTML(formatAge(data.createdAt))}</span>`);
  return `<div class="topology-card topology-card-workspace phase-${phaseClass}" data-id="${escapeTopologyHTML(data.id)}">
    <span class="topology-node-icon">${icon("box")}</span>
    <span class="topology-card-body">
      <span class="topology-card-title" title="${escapeTopologyHTML(data.name)}">${escapeTopologyHTML(data.name)}</span>
      <span class="topology-card-meta">${meta.join("")}</span>
    </span>
  </div>`;
}

function topologyResourceCard(data) {
  const ephemeral = data.kind === "Ephemeral";
  const phaseClass = topologyPhaseClass(data.phase);
  const bits = [];
  if (data.phase) bits.push(data.phase);
  const capacity = data.capacity || data.requested;
  if (capacity) bits.push(capacity);
  if (data.storageClass) bits.push(data.storageClass);
  if (ephemeral) bits.length = 0;
  const meta = bits.join(" · ") || (ephemeral ? "ephemeral storage" : "persistent volume");
  const pv = data.volumeName
    ? `<span class="topology-card-sub mono" title="${escapeTopologyHTML(data.volumeName)}">PV ${escapeTopologyHTML(data.volumeName)}</span>`
    : "";
  return `<div class="topology-card topology-card-resource${ephemeral ? " is-ephemeral" : ""}${phaseClass ? ` phase-${phaseClass}` : ""}" data-id="${escapeTopologyHTML(data.id)}">
    <span class="topology-resource-icon">${icon(ephemeral ? "box" : "database")}</span>
    <span class="topology-card-body">
      <span class="topology-card-title mono" title="${escapeTopologyHTML(data.name)}">${escapeTopologyHTML(data.name)}</span>
      <span class="topology-card-sub">${escapeTopologyHTML(meta)}</span>
      ${pv}
    </span>
  </div>`;
}

function topologyPhaseClass(phase) {
  const value = String(phase || "").toLowerCase();
  if (value === "bound" || value === "running" || value === "active") return "running";
  if (value === "pending" || value === "containercreating" || value === "waiting" || value === "creating") return "pending";
  if (value === "failed" || value === "error" || value === "crashloopbackoff" || value === "terminating" || value === "lost") return "failed";
  return "";
}

function escapeTopologyHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]));
}

function formatStorage(bytes) {
  const value = Number(bytes);
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let index = 0;
  let size = value;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  const rounded = size >= 10 || index === 0 ? Math.round(size) : size.toFixed(1);
  return `${rounded} ${units[index]}`;
}

function topologyZoom(factor) {
  if (!topologyCy) return;
  const level = Math.max(0.22, Math.min(2.5, topologyCy.zoom() * factor));
  topologyCy.zoom({
    level,
    renderedPosition: { x: topologyCy.width() / 2, y: topologyCy.height() / 2 },
  });
}

els.topologyFit?.addEventListener("click", () => {
  if (topologyCy) topologyCy.fit(undefined, 42);
});
els.topologyRelayout?.addEventListener("click", () => {
  if (!topologyCy) return;
  topologySignature = "";
  syncTopologyGraph(state.topology.users || []);
});
els.topologyZoomIn?.addEventListener("click", () => topologyZoom(1.22));
els.topologyZoomOut?.addEventListener("click", () => topologyZoom(1 / 1.22));

window.addEventListener("resize", () => {
  if (topologyCy) topologyCy.resize();
});
