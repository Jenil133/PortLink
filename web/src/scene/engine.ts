// The render loop. It lives outside React so nothing in the component tree re-renders at 60 Hz.
// One map, one overlay, one WebGL context: the story hands the same canvas over to the console.
import { AmbientLight, DirectionalLight, LightingEffect } from "@deck.gl/core";
import { MapboxOverlay } from "@deck.gl/mapbox";
import type { Map as MapLibreMap, GeoJSONSource } from "maplibre-gl";
import { gridLines, gridStep } from "./basemap";
import { clamp, range, smoothstep } from "./math";
import { getAtlas } from "./sprites";
import { buildFixture, STORY_VESSEL, type Fixture } from "./story/fixture";
import { fleet } from "../state/vessels";
import { StoryState, storyLayers } from "./story/layers";
import { cameraAt, consoleFrame, DEFAULT_CAMERA, lensSweepX, reducedFrame, type Camera } from "./story/timeline";
import { consoleLayers, type ConsoleScene, type ConsoleTruth } from "./console/layers";

export type SceneMode = "story" | "console";

const RAIL_H = 64;
const PHONE_W = 768;

export type StoryListener = (p: number) => void;
export interface EngineView {
  zoom: number;
  lng: number;
  lat: number;
  pitch: number;
}

export interface SceneElements {
  frame: HTMLElement | null;
  frameBorder: HTMLElement | null;
  frameLabel: HTMLElement | null;
}

export interface EngineListeners {
  onMode?(mode: SceneMode): void;
  onHover?(mmsi: number, x: number, y: number): void;
  onSelect?(mmsi: number): void;
  onView?(view: EngineView): void;
  onUserPan?(): void;
  /** The camera or the window changed, so what the server should send has too. */
  onCameraChange?(): void;
}

/** Console pitch from zoom, design spec v3, sheet 03. */
export function pitchForZoom(zoom: number, selected: boolean): number {
  if (selected) return 45;
  if (zoom < 6) return 40;
  if (zoom < 9) return 0;
  if (zoom < 11) return (zoom - 9) * 15;
  return Math.min(40, 30 + (zoom - 11) * 5);
}

export class SceneEngine {
  private overlay: MapboxOverlay;
  private atlas = getAtlas();
  private raf = 0;
  private running = false;
  private lastNow = 0;

  mode: SceneMode;
  private pSmooth = 0;
  private pRaw = 0;
  private lastP = -1;
  private lastRawChange = 0;
  private forceDraw = true;
  private driftClock = 0;

  private fixture: Fixture;
  private story: StoryState;
  private vw = 0;
  private vh = 0;
  private isPhone = false;
  private readonly reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
  private lastGridKey = "";

  private scene: ConsoleScene;
  private selectedAt = 0;
  private consoleEnteredAt = 0;
  private rightInset = 0;

  /** Frame timing, read by the performance overlay. */
  readonly stats = { frames: 0, lastFrameMs: 0, fps: 0, windowStart: 0, windowFrames: 0, longFrames: 0 };

  private storyListeners = new Set<StoryListener>();
  private lastViewAt = 0;

  constructor(
    readonly map: MapLibreMap,
    private readonly els: SceneElements,
    private readonly listeners: EngineListeners,
    initialMode: SceneMode,
  ) {
    this.mode = initialMode;
    this.measureViewport();

    const phone = this.isPhone;
    this.fixture = buildFixture(phone ? 600 : 2000, phone ? 120 : 300);
    this.story = new StoryState({ fixture: this.fixture, atlas: this.atlas, vw: this.vw, vh: this.vh, isPhone: phone, reduced: this.reduced });
    this.scene = {
      truth: "live",
      zoom: DEFAULT_CAMERA.zoom,
      vessels: fleet,
      clusters: null,
      selectedMmsi: 0,
      selected: null,
      track: null,
      trackCursor: null,
      selectT: 1,
      hoverMmsi: 0,
      hover: null,
      hullEnabled: true,
    };

    const ambient = new AmbientLight({ color: [200, 215, 230], intensity: 0.85 });
    const sun = new DirectionalLight({ color: [255, 250, 235], intensity: 1.4, direction: [-0.6, -1.0, -1.2] });
    this.overlay = new MapboxOverlay({ interleaved: true, layers: [], effects: [new LightingEffect({ ambient, sun })] });
    map.addControl(this.overlay as unknown as Parameters<MapLibreMap["addControl"]>[0]);

    map.on("move", this.onMapMove);
    map.on("dragend", this.onDragEnd);
    map.on("moveend", this.emitView);
    addEventListener("resize", this.onResize);
    this.applyInteraction();
    if (this.mode === "console") this.enterConsole(true);
    else listeners.onMode?.("story");
    this.updateFrameLabel();
  }

  start() {
    if (this.running) return;
    this.running = true;
    this.raf = requestAnimationFrame(this.tick);
  }

  destroy() {
    this.running = false;
    cancelAnimationFrame(this.raf);
    removeEventListener("resize", this.onResize);
    this.map.off("move", this.onMapMove);
    this.map.off("dragend", this.onDragEnd);
    this.map.off("moveend", this.emitView);
    if (this.map.hasControl(this.overlay as never)) this.map.removeControl(this.overlay as never);
    document.documentElement.style.overflow = "";
  }

  // ---- public controls ----

  /** Story overlays read progress through this, without any React state. */
  subscribeStory(fn: StoryListener) {
    this.storyListeners.add(fn);
    fn(this.lastP < 0 ? 0 : this.lastP);
    return () => {
      this.storyListeners.delete(fn);
    };
  }

  /** Scene 5's instruments hang off the hull, so they need its position on screen. */
  storyVesselScreen() {
    const pt = this.map.project([STORY_VESSEL.lng, STORY_VESSEL.lat]);
    return { x: pt.x, y: pt.y };
  }

  bbox(): [number, number, number, number] {
    const b = this.map.getBounds();
    return [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()];
  }

  clearSelection() {
    this.scene.selectedMmsi = 0;
    this.scene.selected = null;
    this.scene.track = null;
    this.scene.trackCursor = null;
    this.forceDraw = true;
    this.map.easeTo({ padding: { top: 0, left: 0, right: 0, bottom: 0 }, duration: this.reduced ? 0 : 500 });
    this.setRightInset(0);
  }

  setTrackCursor(cursor: number | null) {
    this.scene.trackCursor = cursor;
    this.forceDraw = true;
  }

  fitTrack() {
    const track = this.scene.track;
    if (!track || track.length < 2) return;
    const lngs = track.map((p) => p[0]);
    const lats = track.map((p) => p[1]);
    const inset = this.panelInset();
    this.map.fitBounds(
      [
        [Math.min(...lngs), Math.min(...lats)],
        [Math.max(...lngs), Math.max(...lats)],
      ],
      { padding: { top: 96, left: this.vw < PHONE_W ? 32 : 528, right: inset + 24, bottom: RAIL_H + 48 }, pitch: 45, duration: this.reduced ? 0 : 800, maxZoom: 13 },
    );
  }

  setHoverListener(fn: (mmsi: number, x: number, y: number) => void) {
    this.listeners.onHover = fn;
  }

  setSubscribing(on: boolean) {
    this.els.frame?.classList.toggle("is-subscribing", on);
  }

  /** Return from the console to the top of the story. */
  backToStory() {
    this.mode = "story";
    document.documentElement.style.overflow = "";
    this.map.setPadding({ top: 0, left: 0, right: 0, bottom: 0 });
    this.applyInteraction();
    this.listeners.onMode?.("story");
    scrollTo({ top: 0 });
    this.pSmooth = 0;
    this.forceDraw = true;
  }

  setRightInset(px: number) {
    this.rightInset = px;
    this.syncConsoleFrame();
  }

  setTruth(truth: ConsoleTruth) {
    this.scene.truth = truth;
    if (this.els.frame) this.els.frame.dataset.truth = truth;
    this.forceDraw = true;
  }

  /** A new track from the series, or a cleared one. */
  setTrack(track: [number, number][] | null, cursor: number | null) {
    this.scene.track = track;
    this.scene.trackCursor = cursor;
    this.forceDraw = true;
  }

  /** Selects a vessel from the map: the panel opens, and the camera flies to it. */
  private handleSelect(mmsi: number) {
    const m = this.map;
    const i = fleet.indexOf(mmsi);
    if (i < 0) return;
    if (mmsi !== this.scene.selectedMmsi) this.selectedAt = performance.now();
    this.scene.selectedMmsi = mmsi;
    this.scene.track = null;
    this.scene.trackCursor = null;
    this.syncSelected();
    this.listeners.onSelect?.(mmsi);
    const inset = this.panelInset();
    this.setRightInset(inset);
    m.flyTo({
      center: [fleet.lng[i], fleet.lat[i]],
      zoom: Math.max(m.getZoom(), 12),
      pitch: 45,
      duration: this.reduced ? 0 : 800,
      padding: { top: 0, left: 0, right: inset, bottom: this.vw < PHONE_W ? 140 : RAIL_H },
      easing: (t) => 1 - Math.pow(1 - t, 3),
    });
    this.forceDraw = true;
  }

  /** The selected vessel glides with the fleet. Off screen it has no marker, but stays selected. */
  private syncSelected() {
    const s = this.scene;
    const i = s.selectedMmsi ? fleet.indexOf(s.selectedMmsi) : -1;
    s.selected = i < 0 ? null : { lng: fleet.lng[i], lat: fleet.lat[i], cog: fleet.cog[i], heading: fleet.heading[i], aisType: fleet.aisType[i] };
  }

  // ---- input ----

  private onResize = () => {
    this.measureViewport();
    this.story.ctx.vw = this.vw;
    this.story.ctx.vh = this.vh;
    this.story.ctx.isPhone = this.isPhone;
    this.story.measure();
    this.updateFrameLabel();
    if (this.mode === "console") this.syncConsoleFrame();
    this.forceDraw = true;
    this.map.resize();
    if (this.mode === "console") this.listeners.onCameraChange?.();
  };

  private onMapMove = () => {
    if (this.mode === "console") {
      this.forceDraw = true;
      this.listeners.onCameraChange?.();
    }
  };

  private onDragEnd = () => {
    if (this.mode === "console") this.listeners.onUserPan?.();
  };

  private emitView = () => {
    const c = this.map.getCenter();
    this.listeners.onView?.({ zoom: this.map.getZoom(), lng: c.lng, lat: c.lat, pitch: this.map.getPitch() });
  };

  private panelInset() {
    if (this.vw < PHONE_W) return 0;
    return this.vw < 1280 ? 336 : 384;
  }

  private measureViewport() {
    this.vw = innerWidth;
    this.vh = innerHeight;
    this.isPhone = this.vw < PHONE_W;
  }

  private applyInteraction() {
    const m = this.map;
    const enable = this.mode === "console";
    const handlers = [m.scrollZoom, m.boxZoom, m.dragPan, m.keyboard, m.doubleClickZoom, m.touchZoomRotate];
    for (const handler of handlers) (enable ? handler.enable() : handler.disable());
    if (enable) m.dragRotate.enable();
    else m.dragRotate.disable();
    m.touchPitch.disable();
    // Without this a touch on the canvas would never reach the page's scroll in the story.
    m.getCanvas().style.touchAction = enable ? "none" : "pan-y";
  }

  private enterConsole(instant: boolean) {
    this.mode = "console";
    if (instant) {
      // Deep link entry move. MapLibre cancels an ease on any user input, so it can always be skipped.
      const home = { center: [DEFAULT_CAMERA.lng, DEFAULT_CAMERA.lat] as [number, number], zoom: DEFAULT_CAMERA.zoom, pitch: 0, bearing: 0 };
      if (this.reduced) {
        this.map.jumpTo(home);
      } else {
        this.map.jumpTo({ center: [-122.9, 37.4], zoom: 7.2, pitch: 0, bearing: -6 });
        this.map.easeTo({ ...home, duration: 2200, easing: (t) => 1 - Math.pow(1 - t, 3) });
      }
    }
    if (!instant) this.consoleEnteredAt = performance.now();
    document.documentElement.style.overflow = "hidden";
    this.applyInteraction();
    this.syncConsoleFrame();
    this.listeners.onMode?.("console");
    this.forceDraw = true;
  }

  // ---- lens frame DOM ----

  private updateFrameLabel() {
    const label = this.els.frameLabel;
    if (label) label.firstChild!.textContent = `${this.story.insideCount.toLocaleString("en-US")} of ${this.fixture.count.toLocaleString("en-US")}`;
  }

  private placeFrame(x: number, y: number, w: number, h: number, opacity: number, edgeOpacity: number, labelOpacity: number) {
    const { frame, frameBorder, frameLabel } = this.els;
    if (!frame) return;
    frame.style.transform = `translate(${x}px, ${y}px)`;
    frame.style.width = `${w}px`;
    frame.style.height = `${h}px`;
    frame.style.opacity = String(opacity);
    frame.style.visibility = opacity > 0 ? "visible" : "hidden";
    if (frameBorder) frameBorder.style.opacity = String(edgeOpacity);
    if (frameLabel) frameLabel.style.opacity = String(labelOpacity);
  }

  private syncConsoleFrame() {
    const f = consoleFrame(this.vw, this.vh, RAIL_H, this.rightInset);
    this.placeFrame(f.x, f.y, f.w, f.h, 1, 0, 0);
  }

  private storyFrame(p: number) {
    const rest = this.story.rest;
    if (p < 0.94) {
      const opacity = range(p, 0.37, 0.39) * (1 - range(p, 0.52, 0.54));
      this.placeFrame(lensSweepX(p, rest), rest.y, rest.w, rest.h, opacity, 1, opacity);
      return;
    }
    // Handoff: the lens grows until its corners are the console's viewport ticks.
    const f = consoleFrame(this.vw, this.vh, RAIL_H, 0);
    const startW = this.isPhone ? rest.w : Math.min(720, this.vw * 0.5);
    const startH = this.isPhone ? rest.h : Math.min(440, this.vh * 0.5);
    const start = { x: (this.vw - startW) / 2, y: (this.vh - startH) / 2, w: startW, h: startH };
    const g = this.reduced ? 1 : smoothstep(range(p, 0.96, 0.99));
    this.placeFrame(
      start.x + (f.x - start.x) * g,
      start.y + (f.y - start.y) * g,
      start.w + (f.w - start.w) * g,
      start.h + (f.h - start.h) * g,
      range(p, 0.94, 0.96),
      1 - range(p, 0.98, 0.99),
      0,
    );
  }

  // ---- the loop ----

  private tick = (now: number) => {
    if (!this.running) return;
    this.raf = requestAnimationFrame(this.tick);
    const started = performance.now();
    const dt = this.lastNow ? now - this.lastNow : 16;
    this.lastNow = now;

    if (this.mode === "story") this.tickStory(now, dt);
    else this.tickConsole(now);

    this.recordStats(now, performance.now() - started);
  };

  private tickStory(now: number, dt: number) {
    const travel = 6 * this.vh;
    const raw = clamp(scrollY / travel, 0, 1);
    if (raw !== this.pRaw) {
      this.pRaw = raw;
      this.lastRawChange = now;
    }

    // Frame rate independent version of the spec's 0.18 per frame at 60 Hz.
    if (this.reduced) {
      this.pSmooth = this.pRaw >= 0.999 ? 1 : reducedFrame(this.pRaw);
    } else {
      this.pSmooth += (this.pRaw - this.pSmooth) * (1 - Math.pow(1 - 0.18, dt / 16.67));
      if (Math.abs(this.pRaw - this.pSmooth) < 0.0004) this.pSmooth = this.pRaw;
    }
    const p = this.pSmooth;

    const moved = p !== this.lastP;
    let dirty = moved || this.forceDraw;

    // Idle drift, at 10 Hz because 0.4 px per second needs no more.
    if (!this.reduced && !moved && now - this.lastRawChange > 400 && p > 0.2 && p < 0.88) {
      this.driftClock += dt;
      if (this.driftClock >= 100) {
        this.story.drift((this.isPhone ? 0.3 : 0.4) * (this.driftClock / 1000), cameraAt(p, this.isPhone, this.vw / this.vh).zoom);
        this.driftClock = 0;
        dirty = true;
      }
    }
    if (!dirty) return;

    const camera = cameraAt(p, this.isPhone, this.vw / this.vh, this.reduced);
    this.applyCamera(camera);
    this.story.update(p, camera);
    this.overlay.setProps({ layers: storyLayers(p, camera, this.story) });
    this.storyFrame(p);
    this.updateGrid(camera.zoom);
    for (const fn of this.storyListeners) fn(p);

    this.lastP = p;
    this.forceDraw = false;

    if (p >= 1) this.enterConsole(false);
  }

  private tickConsole(now: number) {
    const m = this.map;
    const zoom = m.getZoom();
    const s = this.scene;

    // Pitch follows zoom, easing over about 500 ms. Never touched during a flyTo.
    const target = pitchForZoom(zoom, s.selectedMmsi !== 0);
    const pitch = m.getPitch();
    if (!m.isEasing() && Math.abs(pitch - target) > 0.01) {
      m.setPitch(Math.abs(target - pitch) < 0.05 ? target : pitch + (target - pitch) * (this.reduced ? 1 : 0.16));
      this.forceDraw = true;
    }

    if (fleet.step(now) || fleet.dirty) {
      fleet.dirty = false;
      this.forceDraw = true;
    }

    const selectT = this.reduced ? 1 : clamp((now - this.selectedAt) / 300, 0, 1);
    const animating = smoothstep(selectT) !== s.selectT;
    if (now - this.lastViewAt >= 500) {
      this.lastViewAt = now;
      this.emitView();
    }
    // Arriving from the story, its fleet holds the frame for a moment until real vessels arrive.
    const holding = this.consoleEnteredAt > 0 && fleet.count === 0 && !fleet.clusters && now - this.consoleEnteredAt < 1200;
    if (!this.forceDraw && !animating && !holding) return;
    s.selectT = smoothstep(selectT);
    s.zoom = zoom;
    s.clusters = fleet.clusters;
    this.syncSelected();
    if (s.hoverMmsi) {
      const i = fleet.indexOf(s.hoverMmsi);
      s.hover = i >= 0 ? { lng: fleet.lng[i], lat: fleet.lat[i] } : null;
    }

    if (holding) {
      this.overlay.setProps({ layers: storyLayers(1, DEFAULT_CAMERA, this.story) });
    } else {
      this.consoleEnteredAt = 0;
      this.overlay.setProps({
        layers: consoleLayers(s, this.atlas, {
          onVesselClick: (mmsi) => this.handleSelect(mmsi),
          onVesselHover: (mmsi, x, y) => this.handleHover(mmsi, x, y),
          onClusterClick: (c) => m.easeTo({ center: [c.lng, c.lat], zoom: 7, pitch: 0, duration: this.reduced ? 0 : 500 }),
        }),
      });
    }
    this.updateGrid(zoom);
    this.forceDraw = false;
  }

  private handleHover(mmsi: number, x: number, y: number) {
    const s = this.scene;
    if (mmsi === s.hoverMmsi) return;
    s.hoverMmsi = mmsi;
    const i = mmsi ? fleet.indexOf(mmsi) : -1;
    s.hover = i >= 0 ? { lng: fleet.lng[i], lat: fleet.lat[i] } : null;
    this.map.getCanvas().style.cursor = i >= 0 ? "pointer" : "";
    this.listeners.onHover?.(mmsi, x, y);
    this.forceDraw = true;
  }

  private applyCamera(c: Camera) {
    this.map.jumpTo({ center: [c.lng, c.lat], zoom: c.zoom, pitch: c.pitch, bearing: c.bearing });
  }

  private updateGrid(zoom: number) {
    const step = gridStep(zoom);
    const key = String(step);
    if (key === this.lastGridKey) return;
    this.lastGridKey = key;
    const source = this.map.getSource("chart-grid") as GeoJSONSource | undefined;
    source?.setData(gridLines(step, step >= 1 ? [-180, 10, -100, 62] : [-130, 30, -114, 50]));
  }

  private recordStats(now: number, ms: number) {
    const st = this.stats;
    st.frames++;
    st.lastFrameMs = ms;
    st.windowFrames++;
    if (ms > 16.7) st.longFrames++;
    if (!st.windowStart) st.windowStart = now;
    if (now - st.windowStart >= 1000) {
      st.fps = Math.round((st.windowFrames * 1000) / (now - st.windowStart));
      st.windowStart = now;
      st.windowFrames = 0;
    }
  }
}
