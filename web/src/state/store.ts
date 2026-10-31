// UI state for the 2D overlays. Sampled at 2 Hz, never per frame: vessel positions do not live here.
import { create } from "zustand";

export type Truth = "live" | "history" | "stale";
export type ConnectionState = "connecting" | "connected" | "reconnecting" | "offline";
export type TierName = "live" | "1s" | "1m" | "10m";
export type MetricKey = "sog" | "rpm" | "fuel" | "temp" | "level";

export interface Telemetry {
  bytesPerSec: number;
  framesPerSec: number;
  renderFps: number;
  lastFrameBytes: number;
  inView: number;
  dropped: number;
  droppedRising: boolean;
  seekMs: number | null;
  lagMs: number | null;
}

export interface FirehoseState {
  on: boolean;
  /** Seconds left before the firehose value counts as measured, or 0 when settled. */
  settlingS: number;
  value: number | null;
  lastLens: number | null;
}

export interface MetricSeries {
  key: MetricKey;
  label: string;
  unit: string;
  times: number[];
  values: number[];
  min: number;
  max: number;
}

export interface SeriesData {
  tier: TierName;
  rawPoints: number;
  startMs: number;
  endMs: number;
  metrics: MetricSeries[];
}

export interface SelectedInfo {
  mmsi: number;
  name: string;
  aisType: number;
  sog: number | null;
  cog: number | null;
  heading: number | null;
  rpm: number | null;
  fuelRate: number | null;
  temp: number | null;
  level: number | null;
}

export interface LogRow {
  time: number;
  vessels: number;
  bytes: number;
  snapshot: boolean;
}

export interface ContractState {
  bbox: [number, number, number, number] | null;
  viewId: number;
  seq: number;
  renderMode: "vessels" | "clusters" | "firehose";
  vesselsInFrame: number;
  removed: number;
  log: LogRow[];
}

export interface TimelineState {
  historyStartMs: number;
  nowMs: number;
  /** Null while live. */
  cursorMs: number | null;
  paused: boolean;
  rate: 1 | 10 | 60;
  hasHistory: boolean;
}

export interface ViewState {
  zoom: number;
  lng: number;
  lat: number;
  pitch: number;
}

export interface HoverInfo {
  mmsi: number;
  x: number;
  y: number;
  name: string;
  aisType: number;
  sog: number | null;
  cog: number | null;
}

export interface LensState {
  sceneMode: "story" | "console";
  truth: Truth;
  connection: ConnectionState;
  attempt: number;
  nextRetryS: number;
  staleAgeS: number;
  firstFrame: boolean;
  tier: TierName;
  clustered: boolean;
  subscribing: boolean;
  error: string | null;
  telemetry: Telemetry;
  firehose: FirehoseState;
  bytesHistory: number[];
  fpsHistory: number[];
  contract: ContractState;
  timeline: TimelineState;
  view: ViewState;
  selected: SelectedInfo | null;
  series: SeriesData | null;
  hover: HoverInfo | null;
  inspectorOpen: boolean;
  shortcutsOpen: boolean;
  cardExpanded: boolean;
  guide: { pan: boolean; scrub: boolean; firehose: boolean; dismissed: boolean };
  set(partial: Partial<LensState>): void;
}

export const useLens = create<LensState>((set) => ({
  sceneMode: "story",
  truth: "live",
  connection: "connecting",
  attempt: 0,
  nextRetryS: 0,
  staleAgeS: 0,
  firstFrame: false,
  tier: "live",
  clustered: false,
  subscribing: false,
  error: null,
  telemetry: { bytesPerSec: 0, framesPerSec: 0, renderFps: 0, lastFrameBytes: 0, inView: 0, dropped: 0, droppedRising: false, seekMs: null, lagMs: null },
  firehose: { on: false, settlingS: 0, value: null, lastLens: null },
  bytesHistory: [],
  fpsHistory: [],
  contract: { bbox: null, viewId: 0, seq: 0, renderMode: "vessels", vesselsInFrame: 0, removed: 0, log: [] },
  timeline: { historyStartMs: 0, nowMs: 0, cursorMs: null, paused: false, rate: 1, hasHistory: true },
  view: { zoom: 8, lng: -122.6, lat: 37.7, pitch: 0 },
  selected: null,
  series: null,
  hover: null,
  inspectorOpen: false,
  shortcutsOpen: false,
  cardExpanded: false,
  guide: { pan: false, scrub: false, firehose: false, dismissed: false },
  set: (partial) => set(partial),
}));

/** What the overlays ask for. The socket client implements it. */
export interface LensController {
  toggleFirehose(): void;
  goLive(): void;
  togglePause(): void;
  setRate(rate: 1 | 10 | 60): void;
  seek(cursorMs: number): void;
  select(mmsi: number): void;
  closeVessel(): void;
}

let controller: LensController | null = null;
export const setController = (c: LensController | null) => {
  controller = c;
};
export const lensActions: LensController = {
  toggleFirehose: () => controller?.toggleFirehose(),
  goLive: () => controller?.goLive(),
  togglePause: () => controller?.togglePause(),
  setRate: (r) => controller?.setRate(r),
  seek: (t) => controller?.seek(t),
  select: (m) => controller?.select(m),
  closeVessel: () => controller?.closeVessel(),
};
