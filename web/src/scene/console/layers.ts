// deck.gl layers for the live console. They read plain data so step 4 can feed them from the socket.
import type { Layer, PickingInfo } from "@deck.gl/core";
import { ColumnLayer, IconLayer, LineLayer, PathLayer, ScatterplotLayer, SolidPolygonLayer, TextLayer } from "@deck.gl/layers";
import { SimpleMeshLayer } from "@deck.gl/mesh-layers";
import { deckColors, LIME, PEARL, type RGBA } from "../../theme/colors";
import { COLUMN_MATERIAL, FLAT, HULL_MATERIAL, clusterSpacingPx, columnHeightPx, columnRadiusPx } from "../layerParams";
import { D2R, lerp, metersPerPixel } from "../math";
import { HULL_MESH } from "../hull";
import { shapeForType, spriteName, type IconMapping } from "../sprites";
import { hullScale, hullShadowPolygon } from "../story/layers";
import type { Fleet } from "../../state/vessels";

export type ConsoleTruth = "live" | "history" | "stale";

export type ConsoleVessels = Fleet;

export interface ConsoleCluster {
  lng: number;
  lat: number;
  count: number;
}

export interface SelectedVessel {
  lng: number;
  lat: number;
  cog: number;
  heading: number;
  aisType: number;
}

export interface ConsoleScene {
  truth: ConsoleTruth;
  zoom: number;
  vessels: ConsoleVessels;
  clusters: ConsoleCluster[] | null;
  selectedMmsi: number;
  selected: SelectedVessel | null;
  /** Oldest first, the newest point is the vessel. */
  track: [number, number][] | null;
  /** History cursor position along the track, 0 to 1, or null in live. */
  trackCursor: number | null;
  /** 0 to 1 while the selection ring settles. */
  selectT: number;
  hoverMmsi: number;
  hover: { lng: number; lat: number } | null;
  /** Temporary cut flag from the performance table. */
  hullEnabled: boolean;
}

export interface ConsoleHandlers {
  onVesselClick(mmsi: number): void;
  onVesselHover(mmsi: number, x: number, y: number): void;
  onClusterClick(c: ConsoleCluster): void;
}

export function consoleLayers(s: ConsoleScene, atlas: { url: string; mapping: IconMapping }, h: ConsoleHandlers): Layer[] {
  if (s.clusters) return clusterLayers(s, s.clusters, h);

  const layers: Layer[] = [];
  const accent: RGBA = s.truth === "live" ? LIME : PEARL;
  const useSilhouettes = s.zoom >= 11;
  const size = useSilhouettes ? Math.min(18, 16 + (s.zoom - 11)) : s.zoom < 7 ? 10 : s.zoom < 9 ? 12 : 14;
  const v = s.vessels;

  // Track under the vessels.
  if (s.track && s.track.length > 1) {
    const ground = s.track;
    const lifted = ground.map(([x, y]) => [x, y, 30] as [number, number, number]);
    const splitAt = s.trackCursor === null ? lifted.length : Math.max(1, Math.round(s.trackCursor * (lifted.length - 1)) + 1);
    const behind = lifted.slice(0, splitAt);
    const ahead = lifted.slice(Math.max(0, splitAt - 1));
    const path = (id: string, data: unknown, color: RGBA, width: number) =>
      new PathLayer({
        id,
        data: [data],
        ...FLAT,
        getPath: (d) => d as [number, number][],
        getColor: color,
        widthUnits: "pixels",
        getWidth: width,
        capRounded: true,
        jointRounded: true,
      });
    layers.push(path("track-shadow", ground, deckColors.trackShadow, 5), path("track", behind, deckColors.trackRibbon, 3));
    if (s.trackCursor !== null && ahead.length > 1) layers.push(path("track-ahead", ahead, deckColors.trackAhead, 3));
    if (s.truth === "live") {
      const tail = lifted.slice(Math.floor(lifted.length * 0.95) - 1);
      if (tail.length > 1) layers.push(path("track-live-end", tail, LIME, 3));
    }
    if (s.trackCursor !== null) {
      const c = ground[Math.round(s.trackCursor * (ground.length - 1))];
      layers.push(
        new ScatterplotLayer({
          id: "track-cursor",
          data: [c],
          ...FLAT,
          radiusUnits: "pixels",
          getPosition: (d) => d,
          getRadius: 5,
          stroked: true,
          lineWidthUnits: "pixels",
          getLineWidth: 2,
          getLineColor: deckColors.clusterLabelOutline,
          getFillColor: PEARL,
        }),
      );
    }
  }

  layers.push(
    new IconLayer({
      id: "fleet",
      data: v.data,
      ...FLAT,
      pickable: true,
      iconAtlas: atlas.url,
      iconMapping: atlas.mapping,
      sizeUnits: "pixels",
      billboard: false,
      getSize: size,
      getIcon: (i: number) => spriteName(useSilhouettes ? shapeForType(v.aisType[i]) : "arrow", "fleet"),
      getAngle: (i: number) => -v.cog[i],
      getPosition: (i: number) => [v.lng[i], v.lat[i]],
      updateTriggers: { getPosition: v.version, getAngle: v.version, getIcon: `${useSilhouettes}:${v.memberVersion}` },
      onClick: (info: PickingInfo) => {
        if (info.index >= 0) h.onVesselClick(v.mmsi[info.index]);
        return true;
      },
      onHover: (info: PickingInfo) => {
        h.onVesselHover(info.index >= 0 ? v.mmsi[info.index] : 0, info.x, info.y);
      },
    }),
  );

  if (s.hover && s.hoverMmsi !== s.selectedMmsi) {
    layers.push(
      new ScatterplotLayer({
        id: "hover-ring",
        data: [s.hover],
        ...FLAT,
        filled: false,
        stroked: true,
        radiusUnits: "pixels",
        getPosition: (d) => [d.lng, d.lat],
        getRadius: 11,
        lineWidthUnits: "pixels",
        getLineWidth: 1,
        getLineColor: deckColors.hoverRing,
      }),
    );
  }

  if (s.selected) {
    const sel = s.selected;
    const mpp = metersPerPixel(s.zoom, sel.lat);
    const showHull = s.hullEnabled && s.zoom >= 11;
    const a = sel.heading * D2R;
    const len = 28 * mpp;
    const bow: [number, number] = [sel.lng + (Math.sin(a) * len) / (111320 * Math.cos(sel.lat * D2R)), sel.lat + (Math.cos(a) * len) / 111320];

    layers.push(
      new LineLayer({
        id: "heading-line",
        data: [sel],
        ...FLAT,
        getSourcePosition: (d) => [d.lng, d.lat],
        getTargetPosition: () => bow,
        getColor: PEARL,
        widthUnits: "pixels",
        getWidth: 2,
      }),
      new ScatterplotLayer({
        id: "selected-ring",
        data: [sel],
        ...FLAT,
        filled: false,
        stroked: true,
        radiusUnits: "pixels",
        getPosition: (d) => [d.lng, d.lat],
        getRadius: lerp(24, 16, s.selectT),
        lineWidthUnits: "pixels",
        getLineWidth: 2,
        getLineColor: accent,
        opacity: s.selectT,
        updateTriggers: { getRadius: s.selectT, getLineColor: s.truth },
      }),
    );

    if (showHull) {
      const scale = hullScale(s.zoom, sel.lat);
      layers.push(
        new SolidPolygonLayer({
          id: "hull-shadow",
          data: [hullShadowPolygon(sel.lng, sel.lat, sel.heading, scale)],
          ...FLAT,
          getPolygon: (d) => d,
          getFillColor: deckColors.hullShadow,
        }),
        new SimpleMeshLayer({
          id: "hull",
          data: [sel],
          mesh: HULL_MESH,
          sizeScale: scale,
          material: HULL_MATERIAL,
          getPosition: (d) => [d.lng, d.lat, 0],
          getOrientation: (d) => [0, -d.heading, 0],
          getColor: accent,
          parameters: { depthCompare: "always", cullMode: "none" },
          updateTriggers: { getColor: s.truth, getPosition: v.version, getOrientation: v.version },
        }),
      );
    } else {
      layers.push(
        new IconLayer({
          id: "selected-sprite",
          data: [sel],
          ...FLAT,
          iconAtlas: atlas.url,
          iconMapping: atlas.mapping,
          sizeUnits: "pixels",
          billboard: false,
          getSize: size * 1.4,
          getIcon: () => spriteName(useSilhouettes ? shapeForType(sel.aisType) : "arrow", s.truth === "live" ? "lime" : "pearl"),
          getAngle: (d) => -d.cog,
          getPosition: (d) => [d.lng, d.lat],
          updateTriggers: { getIcon: `${s.truth}:${useSilhouettes}:${sel.aisType}`, getPosition: v.version, getAngle: v.version },
        }),
      );
    }
  }
  return layers;
}

function clusterLayers(s: ConsoleScene, clusters: ConsoleCluster[], h: ConsoleHandlers): Layer[] {
  const cap: RGBA = s.truth === "live" ? deckColors.clusterCapLive : deckColors.clusterCapRest;
  const radius = columnRadiusPx(s.zoom);
  const spacing = clusterSpacingPx(s.zoom);
  const mpp = metersPerPixel(s.zoom, 38);
  const elevation = (c: ConsoleCluster) => columnHeightPx(c.count, spacing) * mpp;
  const version = `${s.zoom}`;
  const shadowDeg = mpp * 6 * 1e-5;

  return [
    new ScatterplotLayer({
      id: "cluster-shadow",
      data: clusters,
      ...FLAT,
      radiusUnits: "pixels",
      getPosition: (c) => [c.lng + shadowDeg, c.lat - shadowDeg],
      getRadius: radius + 2,
      getFillColor: deckColors.clusterShadow,
      updateTriggers: { getPosition: version },
    }),
    new ColumnLayer({
      id: "cluster",
      data: clusters,
      diskResolution: 12,
      radius,
      radiusUnits: "pixels",
      extruded: true,
      pickable: true,
      material: COLUMN_MATERIAL,
      getPosition: (c) => [c.lng, c.lat],
      getElevation: elevation,
      getFillColor: deckColors.clusterBody,
      updateTriggers: { getElevation: version },
      onClick: (info: PickingInfo<ConsoleCluster>) => {
        if (info.object) h.onClusterClick(info.object);
        return true;
      },
    }),
    new ColumnLayer({
      id: "cluster-cap",
      data: clusters,
      diskResolution: 24,
      radius,
      radiusUnits: "pixels",
      extruded: false,
      parameters: { depthBias: -4, depthBiasSlopeScale: -4 },
      getPosition: (c) => [c.lng, c.lat, elevation(c) + mpp * 3],
      getFillColor: cap,
      updateTriggers: { getPosition: version, getFillColor: s.truth },
    }),
    new TextLayer({
      id: "cluster-count",
      data: radius >= 10 ? clusters : [],
      ...FLAT,
      fontFamily: '"Geist Mono Variable", monospace',
      fontWeight: 500,
      characterSet: "0123456789,",
      fontSettings: { sdf: true },
      getSize: 12,
      getPosition: (c) => [c.lng, c.lat, elevation(c)],
      getText: (c) => c.count.toLocaleString("en-US"),
      getColor: deckColors.clusterLabel,
      getPixelOffset: [0, -16],
      outlineWidth: 2,
      outlineColor: deckColors.clusterLabelOutline,
      updateTriggers: { getPosition: version },
    }),
  ];
}
