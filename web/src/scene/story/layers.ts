// All deck.gl layers for the scroll story, as a function of progress p.
import type { Layer } from "@deck.gl/core";
import { WebMercatorViewport } from "@deck.gl/core";
import { ArcLayer, ColumnLayer, IconLayer, PathLayer, ScatterplotLayer, SolidPolygonLayer, TextLayer } from "@deck.gl/layers";
import { TripsLayer } from "@deck.gl/geo-layers";
import { SimpleMeshLayer } from "@deck.gl/mesh-layers";
import { deckColors, LIME, PEARL } from "../../theme/colors";
import { HULL_MATERIAL, FLAT, COLUMN_MATERIAL, columnHeightPx, columnRadiusPx, clusterSpacingPx } from "../layerParams";
import { D2R, lerp, metersPerPixel, range, smoothstep, srange } from "../math";
import { HULL_MESH, HULL_OUTLINE } from "../hull";
import { spriteName, type IconMapping } from "../sprites";
import { STORY_TRACK, STORY_VESSEL, type Fixture } from "./fixture";
import { cameraAt, copyRect, lensRest, lensSweepX, type Camera, type Rect } from "./timeline";

export interface StoryContext {
  fixture: Fixture;
  atlas: { url: string; mapping: IconMapping };
  vw: number;
  vh: number;
  isPhone: boolean;
  reduced: boolean;
}

/** Screen space state that is expensive to rebuild: per vessel state, updated only when something changes. */
export class StoryState {
  readonly lensX: Float32Array;
  readonly lensInside: Uint8Array;
  readonly alpha: Uint8Array;
  readonly lime: Uint8Array;
  readonly fall: Float32Array;
  readonly driftLng: Float32Array;
  readonly driftLat: Float32Array;
  /** Every third vessel carries an arc: all 2,000 read as a white sheet that hides the fleet. */
  readonly arcIndices: number[];
  rest: Rect = { x: 0, y: 0, w: 0, h: 0 };
  insideCount = 0;
  markerA: [number, number] = [0, 0];
  markerB: [number, number] = [0, 0];
  colorVersion = 0;
  fallVersion = 0;
  iconVersion = 0;
  driftVersion = 0;

  constructor(readonly ctx: StoryContext) {
    const n = ctx.fixture.count;
    this.lensX = new Float32Array(n);
    this.lensInside = new Uint8Array(n);
    this.alpha = new Uint8Array(n).fill(255);
    this.lime = new Uint8Array(n);
    this.fall = new Float32Array(n).fill(1);
    this.driftLng = new Float32Array(n);
    this.driftLat = new Float32Array(n);
    this.arcIndices = ctx.fixture.indices.filter((i) => i % 3 === 0);
    this.measure();
  }

  /** Scene anchors and the set of vessels inside the resting lens. Rerun on resize. */
  measure() {
    const { fixture, vw, vh, isPhone } = this.ctx;
    const aspect = vw / vh;
    const viewportAt = (p: number) => {
      const c = cameraAt(p, isPhone, aspect);
      return new WebMercatorViewport({ width: vw, height: vh, longitude: c.lng, latitude: c.lat, zoom: c.zoom, pitch: c.pitch, bearing: c.bearing });
    };
    this.rest = lensRest(vw, vh, isPhone);
    const atRest = viewportAt(0.46);
    const r = this.rest;
    let inside = 0;
    for (let i = 0; i < fixture.count; i++) {
      const [x, y] = atRest.project([fixture.lng[i], fixture.lat[i]]);
      this.lensX[i] = x;
      const hit = x >= r.x && x <= r.x + r.w && y >= r.y && y <= r.y + r.h ? 1 : 0;
      this.lensInside[i] = hit;
      inside += hit;
    }
    this.insideCount = inside;

    // The browser marker is a geographic point per scene, so moving the camera never re-uploads 2,000 arc targets.
    const a = viewportAt(0.33).unproject(isPhone ? [vw * 0.5, vh * 0.55] : [vw * 0.36, vh * 0.71]);
    const b = atRest.unproject([r.x + r.w * 0.5, r.y + r.h * 0.84]);
    this.markerA = [a[0], a[1]];
    this.markerB = [b[0], b[1]];
  }

  /** Updates per vessel alpha, lime and arc fall for p. Bumps a version only for what actually changed. */
  update(p: number, camera: Camera) {
    const { fixture, vw, vh, isPhone } = this.ctx;
    const inLens = p >= 0.36 && p < 0.56;
    const frameRight = lensSweepX(p, this.rest) + this.rest.w + vw * range(p, 0.42, 0.47);
    const copy = copyRect(p, vw, vh, isPhone);
    const viewport = copy
      ? new WebMercatorViewport({ width: vw, height: vh, longitude: camera.lng, latitude: camera.lat, zoom: camera.zoom, pitch: camera.pitch, bearing: camera.bearing })
      : null;
    const outsideReturn = range(p, 0.52, 0.56);

    let colorChanged = false;
    let fallChanged = false;
    let iconChanged = false;
    for (let i = 0; i < fixture.count; i++) {
      let a = 255;
      let lime = 0;
      let fall = 1;
      if (inLens) {
        const x = this.lensX[i];
        // Vessels far off screen are never reached by the sweep, so everything settles by p 0.47.
        const crossed = Math.max(range(frameRight - x, 0, this.rest.w * 0.5), range(p, 0.44, 0.47));
        if (crossed > 0) {
          if (this.lensInside[i]) {
            lime = p < 0.54 ? 1 : 0;
          } else {
            fall = 1 - crossed;
            a = Math.round(lerp(lerp(255, 64, crossed), 255, outsideReturn));
          }
        }
      }
      if (viewport && a > 40) {
        const [sx, sy] = viewport.project([fixture.lng[i], fixture.lat[i]]);
        if (sx >= copy!.x && sx <= copy!.x + copy!.w && sy >= copy!.y && sy <= copy!.y + copy!.h) a = 40;
      }
      if (this.alpha[i] !== a) {
        this.alpha[i] = a;
        colorChanged = true;
      }
      if (this.lime[i] !== lime) {
        this.lime[i] = lime;
        iconChanged = true;
        colorChanged = true;
      }
      if (this.fall[i] !== fall) {
        this.fall[i] = fall;
        fallChanged = true;
      }
    }
    if (colorChanged) this.colorVersion++;
    if (fallChanged) this.fallVersion++;
    if (iconChanged) this.iconVersion++;
  }

  /** Slow drift along each vessel's own course while the user is not scrolling. */
  drift(pixels: number, zoom: number) {
    const { fixture } = this.ctx;
    for (let i = 0; i < fixture.count; i++) {
      const meters = pixels * metersPerPixel(zoom, fixture.lat[i]);
      const a = fixture.cog[i] * D2R;
      this.driftLat[i] += (Math.cos(a) * meters) / 111320;
      this.driftLng[i] += (Math.sin(a) * meters) / (111320 * Math.cos(fixture.lat[i] * D2R));
    }
    this.driftVersion++;
  }
}

/** Point along the track, t = 0 at the vessel and 1 six hours back. */
function trackAt(t: number): [number, number] {
  const f = (1 - t) * (STORY_TRACK.length - 1);
  const i = Math.min(STORY_TRACK.length - 2, Math.floor(f));
  const k = f - i;
  return [lerp(STORY_TRACK[i][0], STORY_TRACK[i + 1][0], k), lerp(STORY_TRACK[i][1], STORY_TRACK[i + 1][1], k)];
}

export function trackCursorT(p: number) {
  return range(p, 0.58, 0.68);
}

export function storyLayers(p: number, camera: Camera, s: StoryState): Layer[] {
  const { fixture, atlas } = s.ctx;
  const layers: Layer[] = [];
  const zoom = camera.zoom;

  // ---- Scene 1: density columns rising out of the water ----
  const riseT = range(p, 0.03, 0.13);
  const sink = 1 - srange(p, 0.16, 0.22);
  if (riseT > 0 && sink > 0) {
    const mpp = metersPerPixel(zoom, 38);
    const spacing = clusterSpacingPx(zoom);
    const radius = columnRadiusPx(zoom);
    // Smallest columns start first, largest finish last.
    const heightOf = (c: Fixture["clusters"][number]) => {
      const t = smoothstep(range(riseT, c.rank * 0.5, c.rank * 0.5 + 0.5));
      return columnHeightPx(c.count, spacing) * mpp * t * sink;
    };
    const version = `${riseT}:${sink}:${zoom}`;
    layers.push(
      new ScatterplotLayer({
        id: "story-column-shadow",
        data: fixture.clusters,
        ...FLAT,
        getPosition: (c) => [c.lng + 8 * mpp * 1e-5, c.lat - 6 * mpp * 1e-5],
        radiusUnits: "pixels",
        getRadius: radius + 2,
        getFillColor: deckColors.clusterShadow,
        opacity: riseT,
        updateTriggers: { getPosition: zoom },
      }),
      new ColumnLayer({
        id: "story-column",
        data: fixture.clusters,
        diskResolution: 12,
        radius,
        radiusUnits: "pixels",
        extruded: true,
        material: COLUMN_MATERIAL,
        getPosition: (c) => [c.lng, c.lat],
        getElevation: heightOf,
        getFillColor: deckColors.clusterBody,
        updateTriggers: { getElevation: version },
      }),
      new ColumnLayer({
        id: "story-column-cap",
        data: fixture.clusters,
        diskResolution: 24,
        radius,
        radiusUnits: "pixels",
        extruded: false,
        parameters: { depthBias: -4, depthBiasSlopeScale: -4 },
        getPosition: (c) => [c.lng, c.lat, heightOf(c) + mpp * 3],
        getFillColor: LIME,
        updateTriggers: { getPosition: version },
      }),
      new TextLayer({
        id: "story-column-count",
        data: fixture.clusters,
        ...FLAT,
        fontFamily: '"Geist Mono Variable", monospace',
        fontWeight: 500,
        characterSet: "0123456789,",
        getSize: 12,
        sizeUnits: "pixels",
        getPosition: (c) => [c.lng, c.lat, heightOf(c)],
        getText: (c) => c.count.toLocaleString("en-US"),
        getColor: PEARL,
        getPixelOffset: [0, -16],
        fontSettings: { sdf: true },
        outlineWidth: 2,
        outlineColor: deckColors.clusterLabelOutline,
        opacity: range(p, 0.11, 0.15) * sink,
        updateTriggers: { getPosition: version },
      }),
    );
  }

  // ---- Scene 2 and 3: every vessel, then only those inside the lens ----
  const fleetOpacity = srange(p, 0.2, 0.26);
  const arcGrow = srange(p, 0.22, 0.32);
  const arcOpacity = 1 - srange(p, 0.52, 0.54);
  if (arcGrow > 0 && arcOpacity > 0) {
    const m = range(p, 0.34, 0.38);
    const target: [number, number] = [lerp(s.markerA[0], s.markerB[0], m), lerp(s.markerA[1], s.markerB[1], m)];
    layers.push(
      new ArcLayer({
        id: "story-arcs",
        data: s.arcIndices,
        ...FLAT,
        widthUnits: "pixels",
        getWidth: 1,
        numSegments: 28,
        opacity: arcOpacity,
        getSourcePosition: (i: number) => [fixture.lng[i] + s.driftLng[i], fixture.lat[i] + s.driftLat[i]],
        getTargetPosition: () => target,
        getHeight: (i: number) => 0.5 * arcGrow * s.fall[i],
        getSourceColor: (i: number) => arcColor(s, i),
        getTargetColor: (i: number) => arcColor(s, i),
        updateTriggers: {
          getSourcePosition: s.driftVersion,
          getTargetPosition: m,
          getHeight: `${arcGrow}:${s.fallVersion}`,
          getSourceColor: s.colorVersion,
          getTargetColor: s.colorVersion,
        },
      }),
      new ScatterplotLayer({
        id: "story-browser-marker",
        data: [target],
        ...FLAT,
        radiusUnits: "pixels",
        getPosition: (d) => d,
        getRadius: 6,
        stroked: true,
        lineWidthUnits: "pixels",
        getLineWidth: 1,
        getLineColor: deckColors.clusterLabelOutline,
        getFillColor: PEARL,
        opacity: arcOpacity * arcGrow,
      }),
      new TextLayer({
        id: "story-browser-label",
        data: [target],
        ...FLAT,
        fontFamily: '"Geist Variable", sans-serif',
        fontWeight: 500,
        getPosition: (d) => d,
        getText: () => "browser",
        getSize: 12,
        getColor: PEARL,
        getTextAnchor: "start",
        getPixelOffset: [12, 0],
        fontSettings: { sdf: true },
        outlineWidth: 2,
        outlineColor: deckColors.clusterLabelOutline,
        opacity: arcOpacity * arcGrow,
      }),
    );
  }

  // ---- Scene 4: wakes ----
  const wakeT = range(p, 0.58, 0.68);
  const wakeOpacity = 1 - srange(p, 0.68, 0.7);
  if (wakeT > 0 && wakeOpacity > 0 && !s.ctx.reduced) {
    layers.push(
      new TripsLayer({
        id: "story-wakes",
        data: fixture.trails,
        ...FLAT,
        getPath: (d: Fixture["trails"][number]) => d.path,
        getTimestamps: (d: Fixture["trails"][number]) => d.timestamps,
        getColor: deckColors.storyWake,
        widthUnits: "pixels",
        widthMinPixels: 2,
        getWidth: 2,
        currentTime: 40,
        trailLength: 40 * wakeT,
        fadeTrail: true,
        opacity: wakeOpacity,
      }),
    );
  } else if (s.ctx.reduced && p >= 0.54 && p < 0.7) {
    layers.push(
      new PathLayer({
        id: "story-wakes-static",
        data: fixture.trails,
        ...FLAT,
        getPath: (d: Fixture["trails"][number]) => d.path,
        getColor: [220, 230, 226, 90],
        widthUnits: "pixels",
        getWidth: 1,
      }),
    );
  }

  // ---- The fleet itself ----
  if (fleetOpacity > 0) {
    // Other vessels step back for scenes 4 and 5, then return behind the pearl section.
    const dim = lerp(lerp(1, 120 / 255, srange(p, 0.58, 0.62)), 1, srange(p, 0.88, 0.92));
    layers.push(
      new IconLayer({
        id: "story-fleet",
        data: fixture.indices,
        ...FLAT,
        iconAtlas: atlas.url,
        iconMapping: atlas.mapping,
        sizeUnits: "pixels",
        billboard: false,
        sizeScale: lerp(1.3, 1, srange(p, 0.3, 0.38)),
        getSize: 12,
        getIcon: (i: number) => (s.lime[i] ? spriteName("arrow", "lime") : spriteName("arrow", "fleet")),
        getAngle: (i: number) => -fixture.cog[i],
        getPosition: (i: number) => [fixture.lng[i] + s.driftLng[i], fixture.lat[i] + s.driftLat[i]],
        getColor: (i: number) => [255, 255, 255, s.alpha[i]],
        opacity: fleetOpacity * dim,
        updateTriggers: { getPosition: s.driftVersion, getColor: s.colorVersion, getIcon: s.iconVersion },
      }),
    );
  }

  // ---- Scene 4 and 5: the selected vessel, its raised track and hull ----
  const selectedT = srange(p, 0.54, 0.58) * (1 - srange(p, 0.88, 0.9));
  if (selectedT > 0) {
    const cursor = trackCursorT(p);
    const n = 40;
    const revealed: [number, number][] = [];
    for (let k = 0; k <= n; k++) revealed.push(trackAt((k / n) * cursor));
    const lifted = revealed.map(([x, y]) => [x, y, 30]);
    const liveEnd = lifted.slice(0, 3);

    const hullT = srange(p, 0.76, 0.8);
    // Scene 5 is about this one vessel, so its hull is drawn larger than the console's.
    const scale = hullScale(zoom, STORY_VESSEL.lat) * 2.4;

    layers.push(
      new PathLayer({
        id: "story-track-shadow",
        data: [revealed],
        ...FLAT,
        getPath: (d) => d,
        getColor: deckColors.trackShadow,
        widthUnits: "pixels",
        getWidth: 5,
        capRounded: true,
        jointRounded: true,
        opacity: selectedT,
      }),
      new PathLayer({
        id: "story-track",
        data: [lifted],
        ...FLAT,
        getPath: (d) => d as unknown as [number, number][],
        getColor: [242, 240, 233, 200],
        widthUnits: "pixels",
        getWidth: 3,
        capRounded: true,
        jointRounded: true,
        opacity: selectedT,
      }),
      new PathLayer({
        id: "story-track-live",
        data: [liveEnd],
        ...FLAT,
        getPath: (d) => d as unknown as [number, number][],
        getColor: LIME,
        widthUnits: "pixels",
        getWidth: 3,
        capRounded: true,
        opacity: selectedT,
      }),
      new ScatterplotLayer({
        id: "story-track-cursor",
        data: [trackAt(cursor)],
        ...FLAT,
        radiusUnits: "pixels",
        getPosition: (d) => d,
        getRadius: 5,
        stroked: true,
        lineWidthUnits: "pixels",
        getLineWidth: 2,
        getLineColor: deckColors.clusterLabelOutline,
        getFillColor: PEARL,
        opacity: selectedT * (1 - srange(p, 0.7, 0.74)),
      }),
    );
    if (hullT > 0) {
      layers.push(
        new SolidPolygonLayer({
          id: "story-hull-shadow",
          data: [hullShadowPolygon(STORY_VESSEL.lng, STORY_VESSEL.lat, STORY_VESSEL.cog, scale)],
          ...FLAT,
          getPolygon: (d) => d,
          getFillColor: deckColors.hullShadow,
          opacity: hullT * selectedT,
        }),
        new SimpleMeshLayer({
          id: "story-hull",
          data: [STORY_VESSEL],
          mesh: HULL_MESH,
          sizeScale: scale,
          material: HULL_MATERIAL,
          getPosition: (d) => [d.lng, d.lat, 0],
          getOrientation: (d) => [0, -d.cog, 0],
          getColor: LIME,
          opacity: hullT * selectedT,
          parameters: { depthCompare: "always", cullMode: "none" },
        }),
      );
    }
    if (hullT < 1) {
      layers.push(
        new IconLayer({
          id: "story-selected",
          data: [STORY_VESSEL],
          ...FLAT,
          iconAtlas: atlas.url,
          iconMapping: atlas.mapping,
          sizeUnits: "pixels",
          billboard: false,
          getSize: 17,
          getIcon: () => spriteName("arrow", "lime"),
          getAngle: (d) => -d.cog,
          getPosition: (d) => [d.lng, d.lat],
          opacity: selectedT * (1 - hullT),
        }),
      );
    }
  }

  return layers;
}

function arcColor(s: StoryState, i: number): [number, number, number, number] {
  const a = s.alpha[i] / 255;
  if (s.lime[i]) return [deckColors.storyArcLens[0], deckColors.storyArcLens[1], deckColors.storyArcLens[2], deckColors.storyArcLens[3] * a];
  return [deckColors.storyArc[0], deckColors.storyArc[1], deckColors.storyArc[2], deckColors.storyArc[3] * a * s.fall[i]];
}

/** Hull scale: 40 px long at zoom 13, never under 24 px, so it reads larger than a sprite. Hull units are 2 long. */
export function hullScale(zoom: number, lat: number) {
  const mpp = metersPerPixel(zoom, lat);
  const px = Math.max(24, 40 * 2 ** (zoom - 13));
  return (px * mpp) / 2;
}

/** Deck outline on the ground, offset 8 m along the light's away vector, as metres at this latitude. */
export function hullShadowPolygon(lng: number, lat: number, cog: number, scale: number): [number, number][] {
  const a = cog * D2R;
  const cos = Math.cos(a);
  const sin = Math.sin(a);
  const mPerDegLng = 111320 * Math.cos(lat * D2R);
  const offset = scale * 0.25;
  return HULL_OUTLINE.map(([x, y]) => {
    const ex = (x * cos + y * sin) * scale + offset;
    const ny = (-x * sin + y * cos) * scale - offset;
    return [lng + ex / mPerDegLng, lat + ny / 111320] as [number, number];
  });
}
