"use strict";

const MAP_WIDTH = 1000;
const MAP_HEIGHT = 500;
const MAP_MIN_WIDTH = 120;
const MAP_MIN_FOCUS_WIDTH = 250;
const MAP_SVG_NS = "http://www.w3.org/2000/svg";

const mapFallbackCoordinates = {
  ap: [1.35, 103.82],
  asia: [30, 105],
  au: [-25.27, 133.77],
  ca: [56.13, -106.35],
  eu: [50.11, 8.68],
  in: [20.59, 78.96],
  jp: [36.2, 138.25],
  sa: [4.57, -74.3],
  uk: [55.38, -3.44],
  us: [39.04, -77.49],
};

let mapView = { x: 0, y: 0, w: MAP_WIDTH, h: MAP_HEIGHT };
let mapInitialized = false;
let mapInteractionsReady = false;
let mapDrag = null;

function mapSvgElement(tag, attributes = {}) {
  const node = document.createElementNS(MAP_SVG_NS, tag);
  for (const [name, value] of Object.entries(attributes)) {
    node.setAttribute(name, String(value));
  }
  return node;
}

function mapProject(lat, lon) {
  return {
    x: ((Number(lon) + 180) / 360) * MAP_WIDTH,
    y: ((90 - Number(lat)) / 180) * MAP_HEIGHT,
  };
}

function mapRegionCoordinates(region, summary) {
  const lat = summary && summary.lat !== undefined ? summary.lat : region.lat;
  const lon = summary && summary.lon !== undefined ? summary.lon : region.lon;
  if (lat !== undefined && lon !== undefined && Number.isFinite(Number(lat)) && Number.isFinite(Number(lon))) {
    return { lat: Number(lat), lon: Number(lon) };
  }
  const fallback = mapFallbackCoordinates[String(region.name || "").toLowerCase()] || [20, 0];
  return { lat: fallback[0], lon: fallback[1] };
}

function mapRegions() {
  const regions = configuredRegions();
  if (regions.length) return regions;
  const name = String(state.config.regionName || "").trim() || "local";
  return [{
    name,
    displayName: name === "local" ? "Local" : regionDisplayName(name),
    default: true,
    sshHost: state.config.sshHost || "",
    sshPort: state.config.sshPort || "",
  }];
}

function mapRegionSummary(region) {
  const summary = (state.regionSummaries || []).find((item) => item.name === region.name) || null;
  if (summary || configuredRegions().length) return summary;
  const workspaces = Array.isArray(state.workspaces) ? state.workspaces : [];
  const owners = new Set();
  let running = 0;
  let stopped = 0;
  for (const workspace of workspaces) {
    if (workspace.owner) owners.add(workspace.owner);
    const phase = String(workspace.phase || "").toLowerCase();
    if (phase === "running") running++;
    else if (phase === "stopped") stopped++;
  }
  return {
    name: region.name,
    available: true,
    workspaces: Number(state.workspaceTotal) || workspaces.length,
    users: owners.size,
    running,
    stopped,
    sshHost: state.config.sshHost,
    sshPort: state.config.sshPort,
  };
}

function mapPrimaryRegion(regions) {
  if (!regions.length) return null;
  const configured = String(state.config.regionName || "").trim();
  return regions.find((region) => region.name === configured)
    || regions.find((region) => region.default)
    || regions[0];
}

function mapRegionMetrics(regions) {
  const metrics = {
    total: regions.length,
    available: 0,
    unavailable: 0,
    loading: 0,
    workspaces: 0,
    users: 0,
    running: 0,
    stopped: 0,
  };
  for (const region of regions) {
    const summary = mapRegionSummary(region);
    if (!summary) {
      metrics.loading++;
      continue;
    }
    if (summary.available) metrics.available++;
    else metrics.unavailable++;
    metrics.workspaces += Number(summary.workspaces) || 0;
    metrics.users += Number(summary.users) || 0;
    metrics.running += Number(summary.running) || 0;
    metrics.stopped += Number(summary.stopped) || 0;
  }
  return metrics;
}

function mapHealthClass(metrics) {
  if (!metrics.total || metrics.loading === metrics.total) return "loading";
  if (metrics.unavailable === metrics.total) return "bad";
  if (metrics.unavailable > 0) return "warn";
  if (metrics.loading > 0) return "loading";
  return "ok";
}

function mapHealthLabel(metrics) {
  if (metrics.loading === metrics.total) return "Waiting for telemetry";
  if (metrics.available === 0 && metrics.unavailable > 0) return "All regions unavailable";
  if (metrics.unavailable > 0) {
    return `${metrics.unavailable} region${metrics.unavailable === 1 ? " needs" : "s need"} attention`;
  }
  if (metrics.loading > 0) {
    return `${metrics.loading} region${metrics.loading === 1 ? " is" : "s are"} loading`;
  }
  return "All regions online";
}

function mapCompactNumber(value) {
  const number = Number(value) || 0;
  if (number < 1000) return String(number);
  if (number < 10000) return `${(number / 1000).toFixed(1).replace(/\.0$/, "")}k`;
  return `${Math.round(number / 1000)}k`;
}

function mapShort(value, max) {
  const text = String(value || "");
  if (text.length <= max) return text;
  return `${text.slice(0, Math.max(1, max - 1)).trimEnd()}…`;
}

function mapScale() {
  return mapView.w / MAP_WIDTH;
}

function mapClampView(view) {
  const w = Math.max(MAP_MIN_WIDTH, Math.min(MAP_WIDTH, Number(view.w) || MAP_WIDTH));
  const h = w / 2;
  const x = Math.max(0, Math.min(MAP_WIDTH - w, Number(view.x) || 0));
  const y = Math.max(0, Math.min(MAP_HEIGHT - h, Number(view.y) || 0));
  return { x, y, w, h };
}

function applyMapView() {
  mapView = mapClampView(mapView);
  els.mapSvg.setAttribute("viewBox", `${mapView.x} ${mapView.y} ${mapView.w} ${mapView.h}`);
  layoutMapMarkers();
  const markerCount = els.mapMarkers ? els.mapMarkers.childElementCount : 0;
  const compact = markerCount > 9 && mapView.w > MAP_WIDTH * 0.58;
  els.mapSvg.classList.toggle("map-compact", compact);
}

function layoutMapMarkers() {
  const scale = mapScale();
  for (const marker of els.mapMarkers.querySelectorAll(".map-marker")) {
    const x = Number(marker.dataset.x) || 0;
    const y = Number(marker.dataset.y) || 0;
    marker.setAttribute("transform", `translate(${x} ${y}) scale(${scale})`);
  }
}

function mapPointer(event) {
  const point = els.mapSvg.createSVGPoint();
  point.x = event.clientX;
  point.y = event.clientY;
  const matrix = els.mapSvg.getScreenCTM();
  return matrix ? point.matrixTransform(matrix.inverse()) : { x: 0, y: 0 };
}

function mapSelectedCenter() {
  if (mapView.w < MAP_WIDTH) return null;
  const region = mapRegions().find((item) => item.name === state.mapSelected);
  if (!region) return null;
  const coordinates = mapRegionCoordinates(region, mapRegionSummary(region));
  return mapProject(coordinates.lat, coordinates.lon);
}

function resetMapView() {
  mapView = { x: 0, y: 0, w: MAP_WIDTH, h: MAP_HEIGHT };
  applyMapView();
}

function focusMapRegion(name) {
  const region = mapRegions().find((item) => item.name === name);
  if (!region) return false;
  const coordinates = mapRegionCoordinates(region, mapRegionSummary(region));
  const point = mapProject(coordinates.lat, coordinates.lon);
  const width = Math.max(MAP_MIN_FOCUS_WIDTH, MAP_WIDTH / 3);
  mapView = mapClampView({
    x: point.x - width / 2,
    y: point.y - width / 4,
    w: width,
    h: width / 2,
  });
  applyMapView();
  return true;
}

function fitMapRegions() {
  const regions = filteredMapRegions();
  if (!regions.length) {
    resetMapView();
    return;
  }
  const points = regions.map((region) => {
    const coordinates = mapRegionCoordinates(region, mapRegionSummary(region));
    return mapProject(coordinates.lat, coordinates.lon);
  });
  if (!points.length) {
    resetMapView();
    return;
  }
  const xs = points.map((point) => point.x);
  const ys = points.map((point) => point.y);
  const minX = Math.min(...xs);
  const maxX = Math.max(...xs);
  const minY = Math.min(...ys);
  const maxY = Math.max(...ys);
  const spanX = Math.max(1, maxX - minX);
  const spanY = Math.max(1, maxY - minY);
  const padding = Math.max(46, Math.min(120, Math.max(spanX, spanY) * 0.22));
  let width = Math.max(MAP_MIN_FOCUS_WIDTH, spanX + padding * 2, (spanY + padding * 2) * 2);
  width = Math.min(MAP_WIDTH, width);
  const height = width / 2;
  mapView = mapClampView({
    x: (minX + maxX) / 2 - width / 2,
    y: (minY + maxY) / 2 - height / 2,
    w: width,
    h: height,
  });
  applyMapView();
}

function zoomMap(factor, center) {
  const target = center || { x: mapView.x + mapView.w / 2, y: mapView.y + mapView.h / 2 };
  const nextWidth = Math.max(MAP_MIN_WIDTH, Math.min(MAP_WIDTH, mapView.w * factor));
  const ratio = nextWidth / mapView.w;
  mapView = mapClampView({
    x: target.x - (target.x - mapView.x) * ratio,
    y: target.y - (target.y - mapView.y) * ratio,
    w: nextWidth,
    h: nextWidth / 2,
  });
  applyMapView();
}

function ensureMapWorld() {
  if (mapInitialized) return;
  const path = typeof WORLD_MAP_PATH === "string" ? WORLD_MAP_PATH : "";
  els.mapLand.setAttribute("d", path);

  if (!els.mapSvg.querySelector("#mapLandGradient")) {
    const defs = mapSvgElement("defs");
    const landGradient = mapSvgElement("linearGradient", {
      id: "mapLandGradient",
      x1: "0",
      y1: "0",
      x2: "1",
      y2: "1",
    });
    for (const [offset, color] of [["0", "#17263c"], ["0.55", "#13233a"], ["1", "#0d1a2d"]]) {
      landGradient.append(mapSvgElement("stop", { offset, "stop-color": color }));
    }
    const arcGradient = mapSvgElement("linearGradient", {
      id: "mapArcGradient",
      x1: "0",
      y1: "0",
      x2: "1",
      y2: "0",
    });
    for (const [offset, color, opacity] of [
      ["0", "#22d3ee", "0.1"],
      ["0.5", "#67e8f9", "0.9"],
      ["1", "#a78bfa", "0.2"],
    ]) {
      arcGradient.append(mapSvgElement("stop", {
        offset,
        "stop-color": color,
        "stop-opacity": opacity,
      }));
    }
    const markerGradient = mapSvgElement("linearGradient", {
      id: "mapMarkerGradient",
      x1: "0",
      y1: "0",
      x2: "1",
      y2: "1",
    });
    markerGradient.append(
      mapSvgElement("stop", { offset: "0", "stop-color": "#67e8f9" }),
      mapSvgElement("stop", { offset: "1", "stop-color": "#6366f1" }),
    );
    defs.append(landGradient, arcGradient, markerGradient);
    els.mapSvg.insertBefore(defs, els.mapSvg.firstChild);
  }
  els.mapLand.style.fill = "url(#mapLandGradient)";

  const grid = mapSvgElement("path", {
    class: "map-grid-line",
    "vector-effect": "non-scaling-stroke",
  });
  let d = "";
  for (let lon = -180; lon <= 180; lon += 30) {
    const x = ((lon + 180) / 360) * MAP_WIDTH;
    d += `M${x},0L${x},${MAP_HEIGHT}`;
  }
  for (let lat = -60; lat <= 60; lat += 30) {
    const y = ((90 - lat) / 180) * MAP_HEIGHT;
    d += `M0,${y}L${MAP_WIDTH},${y}`;
  }
  grid.setAttribute("d", d);
  els.mapGrid.replaceChildren(grid);
  mapInitialized = true;
  applyMapView();
}

function ensureMapInteractions() {
  if (mapInteractionsReady) return;
  mapInteractionsReady = true;

  els.mapSvg.addEventListener("wheel", (event) => {
    if (els.mapView.hidden) return;
    event.preventDefault();
    zoomMap(event.deltaY > 0 ? 1.2 : 1 / 1.2, mapPointer(event));
  }, { passive: false });

  els.mapSvg.addEventListener("pointerdown", (event) => {
    if (event.button !== 0 || els.mapView.hidden) return;
    if (event.target.closest && event.target.closest(".map-marker")) return;
    const matrix = els.mapSvg.getScreenCTM();
    mapDrag = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      view: { ...mapView },
      scale: matrix ? matrix.a : 1,
    };
    els.mapSvg.setPointerCapture(event.pointerId);
    els.mapSvg.classList.add("dragging");
  });

  els.mapSvg.addEventListener("pointermove", (event) => {
    if (!mapDrag || mapDrag.pointerId !== event.pointerId) return;
    const scale = mapDrag.scale || 1;
    mapView = mapClampView({
      x: mapDrag.view.x - (event.clientX - mapDrag.startX) / scale,
      y: mapDrag.view.y - (event.clientY - mapDrag.startY) / scale,
      w: mapDrag.view.w,
      h: mapDrag.view.h,
    });
    applyMapView();
  });

  const endDrag = (event) => {
    if (!mapDrag || (event.pointerId !== undefined && mapDrag.pointerId !== event.pointerId)) return;
    if (els.mapSvg.hasPointerCapture(mapDrag.pointerId)) {
      els.mapSvg.releasePointerCapture(mapDrag.pointerId);
    }
    mapDrag = null;
    els.mapSvg.classList.remove("dragging");
  };
  els.mapSvg.addEventListener("pointerup", endDrag);
  els.mapSvg.addEventListener("pointercancel", endDrag);

  els.mapSvg.addEventListener("dblclick", (event) => {
    event.preventDefault();
    zoomMap(1 / 1.5, mapPointer(event));
  });

  els.mapZoomIn.addEventListener("click", () => zoomMap(1 / 1.35, mapSelectedCenter()));
  els.mapZoomOut.addEventListener("click", () => zoomMap(1.35, mapSelectedCenter()));
  if (els.mapFit) els.mapFit.addEventListener("click", fitMapRegions);
  els.mapReset.addEventListener("click", resetMapView);
}

async function openMap() {
  state.mapActive = true;
  state.topologyActive = false;
  if (els.mapSearch) els.mapSearch.value = state.mapQuery;
  const regions = mapRegions();
  if (state.regionFilter) {
    state.mapSelected = state.regionFilter;
  } else if (!state.mapSelected && regions.length) {
    const defaultRegion = regions.find((region) => region.default) || regions[0];
    if (defaultRegion) state.mapSelected = defaultRegion.name;
  }
  showView("map");
  if (typeof syncWorkspaceViewButtons === "function") syncWorkspaceViewButtons();
  renderMap();
  await loadRegionSummaries({ silent: true });
  renderMap();
  if (state.regionFilter) focusMapRegion(state.regionFilter);
  applyMapView();
}

function renderMapIfVisible() {
  if (!state.mapActive || !els.mapView || els.mapView.hidden) return;
  renderMap();
}

function mapRegionSearchText(region, summary) {
  return [
    region.name,
    region.displayName,
    state.config.sshHost,
    summary && summary.sshHost,
  ].filter(Boolean).join(" ").toLowerCase();
}

function filteredMapRegions() {
  const query = state.mapQuery.trim().toLowerCase();
  return mapRegions().filter((region) => {
    if (state.regionFilter && region.name !== state.regionFilter) return false;
    if (!query) return true;
    return mapRegionSearchText(region, mapRegionSummary(region)).includes(query);
  });
}

function renderMap() {
  if (!els.mapView) return;
  ensureMapWorld();
  ensureMapInteractions();

  const regions = filteredMapRegions();
  const query = state.mapQuery.trim();
  const metrics = mapRegionMetrics(regions);
  const selected = regions.find((region) => region.name === state.mapSelected) || regions[0];
  if (selected && state.mapSelected !== selected.name) state.mapSelected = selected.name;

  if (!regions.length) {
    const emptyTitle = els.mapEmpty.querySelector("h2");
    const emptyText = els.mapEmpty.querySelector("p");
    if (emptyTitle) emptyTitle.textContent = query ? "No matching regions" : "No regions configured";
    if (emptyText) {
      emptyText.textContent = query
        ? `No region matches “${query}”. Try another name or SSH host.`
        : "Add regions to the portal configuration to see the world map.";
    }
    els.mapEmpty.hidden = false;
    els.mapContainer.hidden = true;
    els.mapDetail.hidden = true;
    if (els.mapNetwork) {
      els.mapNetwork.hidden = true;
      els.mapNetwork.replaceChildren();
    }
    els.mapRegionsList.replaceChildren();
    els.mapCount.textContent = query ? "No matching regions" : "No regions";
    els.mapMarkers.replaceChildren();
    if (els.mapConnections) els.mapConnections.replaceChildren();
    if (els.mapHud) els.mapHud.replaceChildren();
    return;
  }

  els.mapEmpty.hidden = true;
  els.mapContainer.hidden = false;

  const matchLabel = query ? " matching" : "";
  const health = metrics.loading === metrics.total
    ? "loading activity…"
    : `${metrics.available}/${metrics.total} online`;
  els.mapCount.textContent = `${regions.length}${matchLabel} region${regions.length === 1 ? "" : "s"} · ${metrics.workspaces} workspace${metrics.workspaces === 1 ? "" : "s"} · ${health}`;

  renderMapHud(regions, metrics);
  renderMapNetwork(regions, metrics);
  renderMapConnections(regions);
  renderMapMarkers(regions);
  renderMapRegionList(regions);
  renderMapDetail(selected);
  applyMapView();
}

function renderMapHud(regions, metrics) {
  if (!els.mapHud) return;
  els.mapHud.replaceChildren();
  const primary = mapPrimaryRegion(regions);
  const healthClass = mapHealthClass(metrics);

  const head = document.createElement("div");
  head.className = "map-hud-head";
  const eyebrow = document.createElement("span");
  eyebrow.className = "map-hud-eyebrow";
  eyebrow.textContent = "Global network";
  const primaryLine = document.createElement("span");
  primaryLine.className = "map-hud-primary";
  primaryLine.textContent = primary ? `Primary · ${primary.displayName || primary.name}` : "No primary region";
  head.append(eyebrow, primaryLine);

  const status = document.createElement("div");
  status.className = `map-hud-status ${healthClass}`;
  status.textContent = mapHealthLabel(metrics);

  const stats = document.createElement("div");
  stats.className = "map-hud-stats";
  for (const [value, label] of [
    [mapCompactNumber(metrics.workspaces), "workspaces"],
    [mapCompactNumber(metrics.users), "users"],
    [mapCompactNumber(metrics.available), "online"],
    [mapCompactNumber(metrics.unavailable), "offline"],
  ]) {
    const item = document.createElement("div");
    item.className = "map-hud-stat";
    const number = document.createElement("strong");
    number.textContent = value;
    const text = document.createElement("span");
    text.textContent = label;
    item.append(number, text);
    stats.append(item);
  }

  const track = document.createElement("div");
  track.className = "map-health-track";
  const fill = document.createElement("span");
  fill.className = `map-health-fill ${healthClass}`;
  const percent = metrics.loading === metrics.total
    ? 100
    : Math.round((metrics.available / metrics.total) * 100);
  fill.style.width = `${percent}%`;
  track.append(fill);

  els.mapHud.append(head, status, stats, track);
}

function renderMapNetwork(regions, metrics) {
  if (!els.mapNetwork) return;
  els.mapNetwork.hidden = false;
  els.mapNetwork.replaceChildren();

  const header = document.createElement("div");
  header.className = "map-network-header";
  const title = document.createElement("h2");
  title.textContent = "Network overview";
  const count = document.createElement("span");
  count.className = "map-network-count";
  count.textContent = metrics.loading === metrics.total
    ? "loading"
    : `${metrics.available}/${metrics.total} online`;
  header.append(title, count);

  const bar = document.createElement("div");
  bar.className = "map-network-bar";
  const barFill = document.createElement("span");
  barFill.className = `map-network-bar-fill ${mapHealthClass(metrics)}`;
  barFill.style.width = `${metrics.loading === metrics.total ? 100 : Math.round((metrics.available / metrics.total) * 100)}%`;
  bar.append(barFill);

  const list = document.createElement("div");
  list.className = "map-network-list";
  const primary = mapPrimaryRegion(regions);
  const sorted = [...regions].sort((left, right) => {
    const leftSummary = mapRegionSummary(left);
    const rightSummary = mapRegionSummary(right);
    const difference = (Number(rightSummary && rightSummary.workspaces) || 0) - (Number(leftSummary && leftSummary.workspaces) || 0);
    return difference || String(left.name).localeCompare(String(right.name));
  });
  const maxWorkspaces = Math.max(1, ...sorted.map((region) => {
    const summary = mapRegionSummary(region);
    return Number(summary && summary.workspaces) || 0;
  }));

  for (const region of sorted.slice(0, 8)) {
    const summary = mapRegionSummary(region);
    const workspaces = Number(summary && summary.workspaces) || 0;
    const health = summary ? (summary.available ? "ok" : "down") : "loading";
    const row = document.createElement("button");
    row.type = "button";
    row.className = `map-network-row${state.mapSelected === region.name ? " selected" : ""}`;
    row.title = `Focus ${region.displayName || region.name}`;
    row.addEventListener("click", () => {
      selectMapRegion(region.name);
      focusMapRegion(region.name);
    });

    const dot = document.createElement("span");
    dot.className = `map-network-dot ${health}`;
    const name = document.createElement("span");
    name.className = "map-network-name";
    name.textContent = region.displayName || region.name;
    if (primary && primary.name === region.name) {
      const badge = document.createElement("span");
      badge.className = "map-network-primary";
      badge.textContent = "hub";
      name.append(badge);
    }
    const value = document.createElement("span");
    value.className = "map-network-value";
    value.textContent = summary ? `${workspaces} ws` : "—";
    const rowBar = document.createElement("span");
    rowBar.className = "map-network-row-track";
    const rowFill = document.createElement("span");
    rowFill.className = `map-network-row-fill ${health}`;
    rowFill.style.width = `${Math.round((workspaces / maxWorkspaces) * 100)}%`;
    rowBar.append(rowFill);
    row.append(dot, name, value, rowBar);
    list.append(row);
  }

  if (sorted.length > 8) {
    const more = document.createElement("p");
    more.className = "map-network-more";
    more.textContent = `+${sorted.length - 8} more regions`;
    list.append(more);
  }

  els.mapNetwork.append(header, bar, list);
}

function renderMapConnections(regions) {
  if (!els.mapConnections) return;
  els.mapConnections.replaceChildren();
  if (regions.length < 2) return;
  const primary = mapPrimaryRegion(regions);
  if (!primary) return;
  const primaryCoordinates = mapRegionCoordinates(primary, mapRegionSummary(primary));
  const primaryPoint = mapProject(primaryCoordinates.lat, primaryCoordinates.lon);
  const reduceMotion = typeof window.matchMedia === "function"
    && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const primaryName = primary.displayName || primary.name;

  regions.forEach((region, index) => {
    if (region.name === primary.name) return;
    const summary = mapRegionSummary(region);
    const coordinates = mapRegionCoordinates(region, summary);
    const point = mapProject(coordinates.lat, coordinates.lon);
    const distance = Math.hypot(point.x - primaryPoint.x, point.y - primaryPoint.y);
    const lift = Math.min(150, 28 + distance * 0.32);
    const controlX = (primaryPoint.x + point.x) / 2;
    const controlY = Math.max(16, Math.min(primaryPoint.y, point.y) - lift);
    const path = mapSvgElement("path", {
      class: `map-arc${summary && !summary.available ? " unavailable" : ""}${state.mapSelected === region.name ? " selected" : ""}`,
      d: `M${primaryPoint.x},${primaryPoint.y} Q${controlX},${controlY} ${point.x},${point.y}`,
      "vector-effect": "non-scaling-stroke",
    });
    if (!reduceMotion) path.style.animationDelay = `${(index % 6) * 0.35}s`;
    const title = mapSvgElement("title");
    title.textContent = `${primaryName} → ${region.displayName || region.name}`;
    path.append(title);
    els.mapConnections.append(path);
  });
}

function renderMapMarkers(regions) {
  els.mapMarkers.replaceChildren();
  const primary = mapPrimaryRegion(regions);

  regions.forEach((region, index) => {
    const summary = mapRegionSummary(region);
    const coordinates = mapRegionCoordinates(region, summary);
    const point = mapProject(coordinates.lat, coordinates.lon);
    const title = region.displayName || region.name;
    const workspaces = summary ? Number(summary.workspaces) || 0 : null;
    const users = summary ? Number(summary.users) || 0 : null;
    const subtitle = summary
      ? `${workspaces} workspace${workspaces === 1 ? "" : "s"} · ${users} user${users === 1 ? "" : "s"}`
      : "Loading activity…";
    const isPrimary = primary && primary.name === region.name;
    const available = summary ? Boolean(summary.available) : null;
    const radius = available === false
      ? 15
      : workspaces === null
        ? 17
        : 16 + Math.min(10, Math.log2(workspaces + 1) * 2.2);
    const displayTitle = mapShort(title, 22);
    const displayMeta = isPrimary
      ? mapShort(`PRIMARY · ${workspaces === null ? "…" : workspaces} ws`, 28)
      : mapShort(summary ? `${workspaces} ws · ${users} users` : "Loading activity…", 28);
    const labelWidth = Math.max(116, Math.min(190, Math.max(displayTitle.length * 7, displayMeta.length * 5.9) + 28));
    const labelAbove = point.y > MAP_HEIGHT * 0.7 || (index % 2 === 1 && point.y > MAP_HEIGHT * 0.26);

    const marker = mapSvgElement("g", {
      class: `map-marker${state.mapSelected === region.name ? " selected" : ""}${available === false ? " unavailable" : ""}${summary ? "" : " loading"}${isPrimary ? " primary" : ""}`,
      tabindex: "0",
      role: "button",
      "aria-pressed": state.mapSelected === region.name ? "true" : "false",
      "aria-label": `${title}: ${subtitle}${isPrimary ? " (primary region)" : ""}`,
    });
    marker.dataset.region = region.name;
    marker.dataset.x = String(point.x);
    marker.dataset.y = String(point.y);
    marker.setAttribute("transform", `translate(${point.x} ${point.y}) scale(${mapScale()})`);
    marker.addEventListener("click", (event) => {
      event.stopPropagation();
      selectMapRegion(region.name);
    });
    marker.addEventListener("dblclick", (event) => {
      event.preventDefault();
      event.stopPropagation();
      selectMapRegion(region.name);
      focusMapRegion(region.name);
    });
    marker.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        selectMapRegion(region.name);
      }
    });

    const titleNode = mapSvgElement("title");
    titleNode.textContent = `${title}: ${subtitle}${isPrimary ? " (primary region)" : ""}`;

    const halo = mapSvgElement("circle", { class: "map-marker-halo", r: radius + 9 });
    const ring = mapSvgElement("circle", { class: "map-marker-ring", r: radius + 4 });
    const dot = mapSvgElement("circle", { class: "map-marker-dot", r: radius });
    const count = mapSvgElement("text", {
      class: "map-marker-count",
      "text-anchor": "middle",
      y: Math.max(4, Math.round(radius * 0.28)),
    });
    count.textContent = workspaces === null ? "…" : mapCompactNumber(workspaces);

    const label = mapSvgElement("g", {
      class: "map-marker-label",
      transform: `translate(0 ${labelAbove ? -radius - 10 : radius + 10})`,
    });
    const labelBg = mapSvgElement("rect", {
      x: -labelWidth / 2,
      y: labelAbove ? -38 : 0,
      width: labelWidth,
      height: 38,
      rx: 8,
    });
    const labelAccent = mapSvgElement("rect", {
      class: "map-marker-label-accent",
      x: -labelWidth / 2,
      y: labelAbove ? -38 : 0,
      width: 3,
      height: 38,
      rx: 1.5,
    });
    const labelTitle = mapSvgElement("text", {
      class: "map-marker-title",
      "text-anchor": "middle",
      x: 2,
      y: labelAbove ? -23 : 15,
    });
    labelTitle.textContent = displayTitle;
    const labelMeta = mapSvgElement("text", {
      class: "map-marker-meta",
      "text-anchor": "middle",
      x: 2,
      y: labelAbove ? -9 : 29,
    });
    labelMeta.textContent = displayMeta;
    label.append(labelBg, labelAccent, labelTitle, labelMeta);

    marker.append(titleNode, halo, ring, dot, count, label);
    els.mapMarkers.append(marker);
  });
}

function renderMapRegionList(regions) {
  els.mapRegionsList.replaceChildren();
  const primary = mapPrimaryRegion(regions);
  for (const region of regions) {
    const summary = mapRegionSummary(region);
    const row = document.createElement("button");
    row.type = "button";
    row.className = `map-region-row${state.mapSelected === region.name ? " selected" : ""}`;
    row.setAttribute("aria-pressed", state.mapSelected === region.name ? "true" : "false");
    row.addEventListener("click", () => selectMapRegion(region.name));
    row.addEventListener("dblclick", () => {
      selectMapRegion(region.name);
      focusMapRegion(region.name);
    });

    const body = document.createElement("span");
    body.className = "map-region-row-body";
    const header = document.createElement("span");
    header.className = "map-region-row-title";
    const name = document.createElement("strong");
    name.textContent = region.displayName || region.name;
    const slug = document.createElement("span");
    slug.className = "badge neutral";
    slug.textContent = region.name;
    header.append(name, slug);
    if (primary && primary.name === region.name) {
      const badge = document.createElement("span");
      badge.className = "badge neutral";
      badge.textContent = "primary";
      header.append(badge);
    } else if (region.default) {
      const badge = document.createElement("span");
      badge.className = "badge neutral";
      badge.textContent = "default";
      header.append(badge);
    }
    const meta = document.createElement("span");
    meta.className = "map-region-row-meta";
    if (summary) {
      meta.textContent = `${summary.workspaces || 0} workspace${summary.workspaces === 1 ? "" : "s"} · ${summary.users || 0} user${summary.users === 1 ? "" : "s"}`;
    } else {
      meta.textContent = "Loading activity…";
    }
    body.append(header, meta);

    const status = document.createElement("span");
    status.className = `region-status ${summary ? (summary.available ? "ok" : "down") : ""}`;
    status.textContent = summary ? (summary.available ? "Available" : "Unavailable") : "Loading";

    row.append(body, status);
    els.mapRegionsList.append(row);
  }
}

function selectMapRegion(name) {
  if (!mapRegions().some((region) => region.name === name)) return;
  state.mapSelected = name;
  renderMap();
}

function renderMapDetail(region) {
  if (!region) {
    els.mapDetail.hidden = true;
    els.mapDetail.replaceChildren();
    return;
  }
  const summary = mapRegionSummary(region);
  const available = summary ? Boolean(summary.available) : null;
  const primary = mapPrimaryRegion(mapRegions());
  els.mapDetail.hidden = false;
  els.mapDetail.replaceChildren();

  const header = document.createElement("div");
  header.className = "map-detail-header";
  const titleWrap = document.createElement("div");
  titleWrap.className = "region-card-title";
  const title = document.createElement("h2");
  title.textContent = region.displayName || region.name;
  const slug = document.createElement("span");
  slug.className = "badge neutral";
  slug.textContent = region.name;
  titleWrap.append(title, slug);
  if (primary && primary.name === region.name) {
    const badge = document.createElement("span");
    badge.className = "badge neutral";
    badge.textContent = "primary";
    titleWrap.append(badge);
  } else if (region.default) {
    const badge = document.createElement("span");
    badge.className = "badge neutral";
    badge.textContent = "default";
    titleWrap.append(badge);
  }
  if (available !== null) {
    const status = document.createElement("span");
    status.className = `region-status ${available ? "ok" : "down"}`;
    status.textContent = available ? "Available" : "Unavailable";
    header.append(titleWrap, status);
  } else {
    header.append(titleWrap);
  }

  const stats = document.createElement("div");
  stats.className = "map-detail-stats";
  for (const [value, label] of [
    [summary ? summary.workspaces || 0 : "—", "workspaces"],
    [summary ? summary.users || 0 : "—", "users"],
    [summary ? summary.running || 0 : "—", "running"],
    [summary ? summary.stopped || 0 : "—", "stopped"],
  ]) {
    const item = document.createElement("div");
    const number = document.createElement("strong");
    number.textContent = String(value);
    const text = document.createElement("span");
    text.textContent = label;
    item.append(number, text);
    stats.append(item);
  }

  const host = summary && summary.sshHost ? summary.sshHost : state.config.sshHost;
  const port = summary && summary.sshPort ? summary.sshPort : state.config.sshPort;
  const endpoint = host ? `SSH ${host}:${port || "22"}` : "";
  const meta = document.createElement("p");
  meta.className = "map-detail-meta";
  meta.textContent = endpoint || "SSH endpoint not configured";

  const actions = document.createElement("div");
  actions.className = "map-detail-actions";
  const focus = document.createElement("button");
  focus.type = "button";
  focus.className = "button ghost small";
  focus.textContent = "Focus on map";
  focus.addEventListener("click", () => {
    selectMapRegion(region.name);
    focusMapRegion(region.name);
  });
  const view = document.createElement("button");
  view.type = "button";
  view.className = "button primary small";
  view.textContent = "View workspaces";
  view.disabled = available === false;
  view.addEventListener("click", () => {
    state.mapActive = false;
    state.topologyActive = false;
    setRegionFilter(region.name);
    showView("workspaces");
    if (typeof syncWorkspaceViewButtons === "function") syncWorkspaceViewButtons();
  });
  actions.append(focus, view);

  els.mapDetail.append(header, stats, meta, actions);
}
