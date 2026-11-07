"use strict";

const MAP_WIDTH = 1000;
const MAP_HEIGHT = 500;
const MAP_MIN_WIDTH = 120;

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

function mapRegionSummary(region) {
  return (state.regionSummaries || []).find((item) => item.name === region.name) || null;
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
  const region = configuredRegions().find((item) => item.name === state.mapSelected);
  if (!region) return null;
  const coordinates = mapRegionCoordinates(region, mapRegionSummary(region));
  return mapProject(coordinates.lat, coordinates.lon);
}

function resetMapView() {
  mapView = { x: 0, y: 0, w: MAP_WIDTH, h: MAP_HEIGHT };
  applyMapView();
}

function focusMapRegion(name) {
  const region = configuredRegions().find((item) => item.name === name);
  if (!region) return false;
  const coordinates = mapRegionCoordinates(region, mapRegionSummary(region));
  const point = mapProject(coordinates.lat, coordinates.lon);
  const width = Math.max(MAP_MIN_WIDTH, MAP_WIDTH / 3);
  mapView = mapClampView({
    x: point.x - width / 2,
    y: point.y - width / 4,
    w: width,
    h: width / 2,
  });
  applyMapView();
  return true;
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

  const grid = document.createElementNS("http://www.w3.org/2000/svg", "path");
  grid.setAttribute("class", "map-grid-line");
  grid.setAttribute("vector-effect", "non-scaling-stroke");
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
  els.mapReset.addEventListener("click", resetMapView);
}

async function openMap() {
  state.mapActive = true;
  state.topologyActive = false;
  if (state.regionFilter) {
    state.mapSelected = state.regionFilter;
  } else if (!state.mapSelected && configuredRegions().length) {
    const defaultRegion = configuredRegions().find((region) => region.default) || configuredRegions()[0];
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

function renderMap() {
  if (!els.mapView) return;
  ensureMapWorld();
  ensureMapInteractions();

  const allRegions = configuredRegions();
  const regions = state.regionFilter
    ? allRegions.filter((region) => region.name === state.regionFilter)
    : allRegions;
  const hasActivity = regions.some((region) => mapRegionSummary(region));
  const selected = regions.find((region) => region.name === state.mapSelected) || regions[0];
  if (selected && state.mapSelected !== selected.name) state.mapSelected = selected.name;

  if (!regions.length) {
    els.mapEmpty.hidden = false;
    els.mapContainer.hidden = true;
    els.mapDetail.hidden = true;
    els.mapRegionsList.replaceChildren();
    els.mapCount.textContent = "No regions";
    els.mapMarkers.replaceChildren();
    return;
  }

  els.mapEmpty.hidden = true;
  els.mapContainer.hidden = false;

  const totalWorkspaces = regions.reduce((sum, region) => {
    const summary = mapRegionSummary(region);
    return sum + (summary ? Number(summary.workspaces) || 0 : 0);
  }, 0);
  els.mapCount.textContent = hasActivity
    ? `${regions.length} region${regions.length === 1 ? "" : "s"} · ${totalWorkspaces} workspace${totalWorkspaces === 1 ? "" : "s"}`
    : `${regions.length} region${regions.length === 1 ? "" : "s"} · loading activity…`;

  renderMapMarkers(regions);
  renderMapRegionList(regions);
  renderMapDetail(selected);
  applyMapView();
}

function renderMapMarkers(regions) {
  els.mapMarkers.replaceChildren();
  for (const region of regions) {
    const summary = mapRegionSummary(region);
    const coordinates = mapRegionCoordinates(region, summary);
    const point = mapProject(coordinates.lat, coordinates.lon);
    const title = region.displayName || region.name;
    const workspaces = summary ? Number(summary.workspaces) || 0 : null;
    const users = summary ? Number(summary.users) || 0 : null;
    const subtitle = summary
      ? `${workspaces} workspace${workspaces === 1 ? "" : "s"} · ${users} user${users === 1 ? "" : "s"}`
      : "Loading activity…";
    const labelWidth = Math.max(112, Math.max(title.length * 6.8, subtitle.length * 5.8) + 22);

    const marker = document.createElementNS("http://www.w3.org/2000/svg", "g");
    marker.setAttribute("class", `map-marker${state.mapSelected === region.name ? " selected" : ""}${summary && !summary.available ? " unavailable" : ""}`);
    marker.dataset.region = region.name;
    marker.dataset.x = String(point.x);
    marker.dataset.y = String(point.y);
    marker.setAttribute("tabindex", "0");
    marker.setAttribute("role", "button");
    marker.setAttribute("aria-label", `${title}: ${subtitle}`);
    marker.addEventListener("click", (event) => {
      event.stopPropagation();
      selectMapRegion(region.name);
    });
    marker.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        selectMapRegion(region.name);
      }
    });

    const titleNode = document.createElementNS("http://www.w3.org/2000/svg", "title");
    titleNode.textContent = `${title}: ${subtitle}`;

    const halo = document.createElementNS("http://www.w3.org/2000/svg", "circle");
    halo.setAttribute("class", "map-marker-halo");
    halo.setAttribute("r", "24");

    const dot = document.createElementNS("http://www.w3.org/2000/svg", "circle");
    dot.setAttribute("class", "map-marker-dot");
    dot.setAttribute("r", "17");

    const count = document.createElementNS("http://www.w3.org/2000/svg", "text");
    count.setAttribute("class", "map-marker-count");
    count.setAttribute("text-anchor", "middle");
    count.setAttribute("y", "5");
    count.textContent = workspaces === null ? "…" : String(workspaces);

    const label = document.createElementNS("http://www.w3.org/2000/svg", "g");
    label.setAttribute("class", "map-marker-label");
    label.setAttribute("transform", "translate(0 28)");
    const labelBg = document.createElementNS("http://www.w3.org/2000/svg", "rect");
    labelBg.setAttribute("x", String(-labelWidth / 2));
    labelBg.setAttribute("y", "0");
    labelBg.setAttribute("width", String(labelWidth));
    labelBg.setAttribute("height", "38");
    labelBg.setAttribute("rx", "8");
    const labelTitle = document.createElementNS("http://www.w3.org/2000/svg", "text");
    labelTitle.setAttribute("class", "map-marker-title");
    labelTitle.setAttribute("text-anchor", "middle");
    labelTitle.setAttribute("y", "15");
    labelTitle.textContent = title;
    const labelMeta = document.createElementNS("http://www.w3.org/2000/svg", "text");
    labelMeta.setAttribute("class", "map-marker-meta");
    labelMeta.setAttribute("text-anchor", "middle");
    labelMeta.setAttribute("y", "29");
    labelMeta.textContent = subtitle;
    label.append(labelBg, labelTitle, labelMeta);

    marker.append(titleNode, halo, dot, count, label);
    els.mapMarkers.append(marker);
  }
}

function renderMapRegionList(regions) {
  els.mapRegionsList.replaceChildren();
  for (const region of regions) {
    const summary = mapRegionSummary(region);
    const row = document.createElement("button");
    row.type = "button";
    row.className = `map-region-row${state.mapSelected === region.name ? " selected" : ""}`;
    row.addEventListener("click", () => selectMapRegion(region.name));

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
    if (region.default) {
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
  if (!configuredRegions().some((region) => region.name === name)) return;
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
  if (region.default) {
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

  const endpoint = summary && (summary.sshHost || region.sshHost)
    ? `SSH ${summary.sshHost || region.sshHost}:${summary.sshPort || region.sshPort || "22"}`
    : "";
  const meta = document.createElement("p");
  meta.className = "map-detail-meta";
  meta.textContent = endpoint;

  const actions = document.createElement("div");
  actions.className = "map-detail-actions";
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
  actions.append(view);

  els.mapDetail.append(header, stats, meta);
  if (actions.children.length) els.mapDetail.append(actions);
}
