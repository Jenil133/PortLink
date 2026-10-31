// The live client: turns the view into Subscribes, frames into the fleet, and counters into the store.
// All measured values on screen come from here, and only from real traffic on this connection.
import { create } from "@bufbuild/protobuf";
import { Metric, Mode, SubscribeSchema, Tier, type Frame, type Series } from "../gen/lens/v1/lens_pb";
import { padBBox } from "../lib/geo";
import type { SceneEngine } from "../scene/engine";
import { useLens, type LensController, type LogRow, type MetricKey, type MetricSeries, type SeriesData, type TierName } from "../state/store";
import { fleet } from "../state/vessels";
import { LensSocket } from "./LensSocket";

const SERIES_WINDOW_MS = 6 * 3600 * 1000;
const SERIES_POINTS = 600;
const SUBSCRIBE_INTERVAL_MS = 100;
const SAMPLE_MS = 500;
/** Rates read low after a snapshot, because every vessel's change threshold resets at once. */
const SETTLE_MS = 30_000;
const LENS_WARMUP_MS = 15_000;
const ERROR_CLEAR_MS = 5000;
const OFFLINE_AFTER_ATTEMPTS = 6;

const TIER_NAMES: Record<number, TierName> = { [Tier.TIER_LIVE]: "live", [Tier.TIER_1S]: "1s", [Tier.TIER_1M]: "1m", [Tier.TIER_10M]: "10m" };
const METRICS: Record<number, { key: MetricKey; label: string; unit: string }> = {
  [Metric.SOG]: { key: "sog", label: "SOG", unit: "kn" },
  [Metric.ENGINE_RPM]: { key: "rpm", label: "RPM", unit: "" },
  [Metric.FUEL_RATE]: { key: "fuel", label: "fuel", unit: "L/h" },
  [Metric.ENGINE_TEMP]: { key: "temp", label: "temp", unit: "C" },
  [Metric.FUEL_LEVEL]: { key: "level", label: "level", unit: "%" },
};

const average = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : 0);
const orNull = (v: number) => (Number.isNaN(v) ? null : v);

export function socketUrl(): string {
  const slow = new URLSearchParams(location.search).get("slow_ms");
  return `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws${slow ? `?slow_ms=${encodeURIComponent(slow)}` : ""}`;
}

/** Fraction along a time sorted track for a cursor time. */
export function trackFraction(times: number[], t: number): number {
  if (times.length < 2) return 1;
  let lo = 0;
  let hi = times.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (times[mid] < t) lo = mid + 1;
    else hi = mid;
  }
  return lo / (times.length - 1);
}

export function seriesToData(s: Series): { data: SeriesData | null; track: [number, number][]; trackTimes: number[] } {
  const startMs = Number(s.startMs);
  const track: [number, number][] = s.track.map((p) => [p.lonE6 / 1e6, p.latE6 / 1e6]);
  const trackTimes = s.track.map((p) => startMs + p.tOffsetS * 1000);
  if (s.rawPoints === 0 || s.metrics.every((m) => m.values.length === 0)) return { data: null, track, trackTimes };
  const metrics: MetricSeries[] = [];
  for (const m of s.metrics) {
    const meta = METRICS[m.metric];
    if (!meta || m.values.length === 0) continue;
    const values = Array.from(m.values);
    metrics.push({ ...meta, times: Array.from(m.tOffsetS, (o) => startMs + o * 1000), values, min: Math.min(...values), max: Math.max(...values) });
  }
  return {
    data: { tier: TIER_NAMES[s.tier] ?? "1s", rawPoints: s.rawPoints, startMs, endMs: Number(s.endMs), metrics },
    track,
    trackTimes,
  };
}

export class LensClient implements LensController {
  private readonly socket: LensSocket;
  private engine: SceneEngine | null = null;
  private running = false;

  private viewId = 0;
  private pendingSeek: { viewId: number; at: number } | null = null;
  private lastSendAt = 0;
  private subscribeTimer = 0;
  private sampler = 0;
  private errorTimer = 0;

  private serverOffsetMs = 0;
  private lastReceiveAt = 0;
  private lastFrameTsMs = 0;
  private retryAt = 0;

  // Counters since the last sample.
  private bytes = 0;
  private frames = 0;
  private sampledAt = 0;
  private recentRates: number[] = [];
  private pendingLog: LogRow[] = [];
  private lastDropped = 0;
  private droppedRisingUntil = 0;

  // Lens against firehose: each number only counts once its mode has settled.
  private lensRates: number[] = [];
  private modeSince = 0;
  private lastLens: number | null = null;

  private trackTimes: number[] = [];

  constructor() {
    this.socket = new LensSocket(socketUrl, {
      onOpen: () => this.handleOpen(),
      onClose: (attempt, retryInMs) => this.handleClose(attempt, retryInMs),
      onHello: (h) => {
        this.serverOffsetMs = Number(h.serverTimeMs) - Date.now();
        const s = useLens.getState();
        const start = Number(h.historyStartMs);
        const now = Date.now() + this.serverOffsetMs;
        s.set({ timeline: { ...s.timeline, historyStartMs: start, nowMs: now, hasHistory: start < now - 60_000 } });
      },
      onFrame: (f, bytes) => this.handleFrame(f, bytes),
      onSeries: (series, bytes) => this.handleSeries(series, bytes),
      onError: (e) => this.showError(`${e.code}: ${e.message}`),
    });
  }

  attach(engine: SceneEngine | null) {
    this.engine = engine;
  }

  /** Opens the socket. The story never does this: it runs entirely on its fixture. */
  start() {
    if (this.running || !this.engine) return;
    this.running = true;
    useLens.getState().set({ connection: "connecting", firstFrame: false, truth: "live" });
    this.modeSince = performance.now();
    this.sampledAt = performance.now();
    this.socket.open();
    this.sampler = window.setInterval(() => this.sample(), SAMPLE_MS);
  }

  stop() {
    if (!this.running) return;
    this.running = false;
    clearInterval(this.sampler);
    clearTimeout(this.subscribeTimer);
    this.subscribeTimer = 0;
    this.socket.close();
    fleet.clear();
    this.engine?.clearSelection();
    this.engine?.setTruth("live");
    this.trackTimes = [];
    this.lensRates = [];
    this.lastLens = null;
    this.recentRates = [];
    this.pendingLog = [];
    const s = useLens.getState();
    s.set({
      connection: "connecting",
      firstFrame: false,
      truth: "live",
      selected: null,
      series: null,
      hover: null,
      inspectorOpen: false,
      firehose: { on: false, settlingS: 0, value: null, lastLens: null },
      bytesHistory: [],
      fpsHistory: [],
      timeline: { ...s.timeline, cursorMs: null, paused: false, rate: 1 },
    });
  }

  /** The camera moved. Throttled with a running timer, so a long drag still sends about ten a second. */
  viewChanged() {
    if (this.running) this.scheduleSubscribe();
  }

  // ---- LensController ----

  toggleFirehose() {
    const s = useLens.getState();
    if (s.truth !== "live" || s.connection !== "connected") return;
    const on = !s.firehose.on;
    this.modeSince = performance.now();
    this.lensRates = [];
    s.set({
      firehose: { on, settlingS: on ? Math.ceil(SETTLE_MS / 1000) : 0, value: null, lastLens: this.lastLens },
      guide: { ...s.guide, firehose: s.guide.firehose || on },
    });
    this.sendSubscribeNow();
  }

  goLive() {
    const s = useLens.getState();
    this.engine?.setTrackCursor(null);
    const connected = s.connection === "connected";
    this.engine?.setTruth(connected ? "live" : "stale");
    s.set({ truth: connected ? "live" : "stale", timeline: { ...s.timeline, cursorMs: null, paused: false, rate: 1 } });
    this.modeSince = performance.now();
    this.lensRates = [];
    this.sendSubscribeNow();
  }

  togglePause() {
    const s = useLens.getState();
    if (s.timeline.cursorMs === null) {
      // Pausing while live freezes the view at this moment, which is history.
      this.seekInternal(this.serverNow(), true);
      return;
    }
    s.set({ timeline: { ...s.timeline, paused: !s.timeline.paused } });
    this.sendSubscribeNow();
  }

  setRate(rate: 1 | 10 | 60) {
    const s = useLens.getState();
    s.set({ timeline: { ...s.timeline, rate, paused: s.timeline.cursorMs === null ? s.timeline.paused : false } });
    if (s.timeline.cursorMs !== null) this.sendSubscribeNow();
  }

  seek(cursorMs: number) {
    this.seekInternal(cursorMs, true);
  }

  select(mmsi: number) {
    const s = useLens.getState();
    const i = fleet.indexOf(mmsi);
    this.trackTimes = [];
    this.lensRates = [];
    this.modeSince = performance.now();
    s.set({
      series: null,
      selected: {
        mmsi,
        name: i >= 0 ? fleet.names[i] || String(mmsi) : String(mmsi),
        aisType: i >= 0 ? fleet.aisType[i] : 0,
        sog: i >= 0 ? fleet.sog[i] : null,
        cog: i >= 0 ? fleet.cog[i] : null,
        heading: i >= 0 ? fleet.heading[i] : null,
        rpm: null,
        fuelRate: null,
        temp: null,
        level: null,
      },
    });
    this.sendSubscribeNow();
  }

  closeVessel() {
    const s = useLens.getState();
    this.engine?.clearSelection();
    this.trackTimes = [];
    this.lensRates = [];
    this.modeSince = performance.now();
    s.set({ selected: null, series: null });
    this.sendSubscribeNow();
  }

  // ---- internals ----

  private serverNow() {
    return Date.now() + this.serverOffsetMs;
  }

  private seekInternal(cursorMs: number, paused: boolean) {
    const s = useLens.getState();
    const { historyStartMs } = s.timeline;
    const now = this.serverNow();
    const clamped = Math.max(historyStartMs, Math.min(now, cursorMs));
    const wasLive = s.timeline.cursorMs === null;
    this.engine?.setTruth("history");
    this.engine?.setTrackCursor(this.cursorFraction(clamped));
    s.set({
      truth: "history",
      // The baseline is defined for live data only.
      firehose: s.firehose.on ? { on: false, settlingS: 0, value: null, lastLens: this.lastLens } : s.firehose,
      guide: { ...s.guide, scrub: true },
      timeline: { ...s.timeline, cursorMs: clamped, nowMs: now, paused: wasLive ? paused : paused || s.timeline.paused },
    });
    this.scheduleSubscribe();
  }

  private cursorFraction(t: number | null): number | null {
    if (t === null) return null;
    return trackFraction(this.trackTimes, t);
  }

  private scheduleSubscribe() {
    if (this.subscribeTimer) return;
    const wait = Math.max(0, SUBSCRIBE_INTERVAL_MS - (performance.now() - this.lastSendAt));
    this.subscribeTimer = window.setTimeout(() => {
      this.subscribeTimer = 0;
      this.send();
    }, wait);
  }

  private sendSubscribeNow() {
    clearTimeout(this.subscribeTimer);
    this.subscribeTimer = 0;
    this.send();
  }

  private send() {
    const engine = this.engine;
    if (!engine || !this.socket.isOpen) return;
    const s = useLens.getState();
    const b = engine.bbox();
    const bbox = padBBox({ minLon: b[0], minLat: b[1], maxLon: b[2], maxLat: b[3] });
    const history = s.timeline.cursorMs !== null;
    this.viewId++;
    this.pendingSeek = { viewId: this.viewId, at: performance.now() };
    this.lastSendAt = performance.now();
    const sub = create(SubscribeSchema, {
      viewId: this.viewId,
      bbox,
      zoom: engine.map.getZoom(),
      mode: history ? Mode.HISTORY : Mode.LIVE,
      cursorMs: BigInt(Math.round(s.timeline.cursorMs ?? this.serverNow())),
      playbackRate: history && !s.timeline.paused ? s.timeline.rate : history ? 0 : 1,
      selectedMmsi: s.selected?.mmsi ?? 0,
      seriesWindowMs: BigInt(s.selected ? SERIES_WINDOW_MS : 0),
      seriesMaxPoints: SERIES_POINTS,
      firehose: s.firehose.on && !history,
    });
    if (this.socket.sendSubscribe(sub)) {
      engine.setSubscribing(true);
      s.set({ subscribing: true, contract: { ...s.contract, bbox: [b[0], b[1], b[2], b[3]], viewId: this.viewId } });
    }
  }

  private handleOpen() {
    const s = useLens.getState();
    this.modeSince = performance.now();
    this.lensRates = [];
    const history = s.timeline.cursorMs !== null;
    this.engine?.setTruth(history ? "history" : "live");
    s.set({ connection: "connected", attempt: 0, nextRetryS: 0, truth: history ? "history" : "live" });
    this.sendSubscribeNow();
  }

  private handleClose(attempt: number, retryInMs: number) {
    const s = useLens.getState();
    this.retryAt = performance.now() + retryInMs;
    // Vessels stay where they were, marked stale, until the reconnect snapshot replaces them.
    this.engine?.setTruth("stale");
    this.engine?.setSubscribing(false);
    s.set({
      connection: attempt >= OFFLINE_AFTER_ATTEMPTS ? "offline" : "reconnecting",
      attempt,
      nextRetryS: Math.ceil(retryInMs / 1000),
      truth: "stale",
      subscribing: false,
      firehose: s.firehose.on ? { on: false, settlingS: 0, value: null, lastLens: this.lastLens } : s.firehose,
    });
  }

  private handleFrame(f: Frame, bytes: number) {
    const s = useLens.getState();
    const history = s.timeline.cursorMs !== null;
    const now = performance.now();

    // Glide in live and playback, jump while a paused scrubber is dragged.
    fleet.applyFrame(f, !history || !s.timeline.paused, now);

    this.bytes += bytes;
    this.frames++;
    this.lastReceiveAt = now;
    this.lastFrameTsMs = Number(f.tsMs);
    if (f.droppedTotal > this.lastDropped) this.droppedRisingUntil = now + 2000;
    this.lastDropped = f.droppedTotal;

    if (f.snapshot) {
      // Rates just after a snapshot say nothing about the steady state.
      this.modeSince = now;
      this.lensRates = [];
    }

    this.pendingLog.push({ time: Date.now(), vessels: f.clustered ? f.clusters.length : f.vessels.length, bytes, snapshot: f.snapshot });
    if (this.pendingLog.length > 8) this.pendingLog.shift();

    let seekMs = s.telemetry.seekMs;
    let subscribing = s.subscribing;
    if (this.pendingSeek && f.viewId >= this.pendingSeek.viewId) {
      seekMs = Math.round(now - this.pendingSeek.at);
      this.pendingSeek = null;
      subscribing = false;
      this.engine?.setSubscribing(false);
    }

    // During playback the server owns the cursor, so the scrubber follows it.
    let timeline = s.timeline;
    if (history && !s.timeline.paused) {
      const ts = Number(f.tsMs);
      if (ts >= this.serverNow() - 3000) {
        this.goLive();
        return;
      }
      timeline = { ...s.timeline, cursorMs: ts };
      this.engine?.setTrackCursor(this.cursorFraction(ts));
    }

    const renderMode = f.clustered ? "clusters" : s.firehose.on && !history ? "firehose" : "vessels";
    s.set({
      firstFrame: true,
      subscribing,
      tier: TIER_NAMES[f.tier] ?? s.tier,
      clustered: f.clustered,
      timeline,
      telemetry: { ...s.telemetry, seekMs, inView: f.inView, dropped: f.droppedTotal, lastFrameBytes: bytes },
      contract: { ...s.contract, seq: f.seq, renderMode, vesselsInFrame: f.vessels.length, removed: f.removed.length },
    });
  }

  private handleSeries(series: Series, bytes: number) {
    this.bytes += bytes;
    const s = useLens.getState();
    if (!s.selected || series.mmsi !== s.selected.mmsi) return;
    const { data, track, trackTimes } = seriesToData(series);
    this.trackTimes = trackTimes;
    this.engine?.setTrack(track.length > 1 ? track : null, this.cursorFraction(s.timeline.cursorMs));
    s.set({ series: data });
  }

  private showError(text: string) {
    useLens.getState().set({ error: text });
    clearTimeout(this.errorTimer);
    this.errorTimer = window.setTimeout(() => useLens.getState().set({ error: null }), ERROR_CLEAR_MS);
  }

  /** Twice a second: counters into the store. Nothing faster than this reaches React. */
  private sample() {
    const engine = this.engine;
    if (!engine) return;
    const s = useLens.getState();
    const now = performance.now();
    const dt = Math.max(1, now - this.sampledAt);
    const rate = (this.bytes * 1000) / dt;
    const framesPerSec = (this.frames * 1000) / dt;
    this.bytes = 0;
    this.frames = 0;
    this.sampledAt = now;

    const connected = s.connection === "connected";
    const history = s.timeline.cursorMs !== null;
    const firehoseOn = s.firehose.on && connected && !history;

    if (connected) {
      this.recentRates.push(rate);
      if (this.recentRates.length > 4) this.recentRates.shift();
    }
    const shown = connected ? average(this.recentRates) : s.telemetry.bytesPerSec;

    const settled = now - this.modeSince >= SETTLE_MS;
    // Lens baseline: live, nothing selected, and long enough since the last snapshot.
    if (connected && !history && !s.firehose.on && !s.selected) {
      // Every vessel's threshold resets at a snapshot, so the first 15 s under-report the steady rate.
      if (now - this.modeSince >= LENS_WARMUP_MS) {
        this.lensRates.push(rate);
        if (this.lensRates.length > SETTLE_MS / SAMPLE_MS) this.lensRates.shift();
      }
      if (settled && this.lensRates.length > 0) this.lastLens = average(this.lensRates);
    }

    const stale = !connected;
    const staleAgeS = stale && this.lastReceiveAt ? Math.floor((now - this.lastReceiveAt) / 1000) : 0;
    const nowMs = this.serverNow();
    const lagMs = stale ? (this.lastReceiveAt ? now - this.lastReceiveAt : null) : history && s.timeline.paused ? null : this.lastFrameTsMs ? (history ? null : Math.max(0, nowMs - this.lastFrameTsMs)) : null;
    const renderFps = engine.stats.fps;

    let selected = s.selected;
    if (selected) {
      const i = fleet.indexOf(selected.mmsi);
      selected =
        i >= 0
          ? {
              ...selected,
              name: fleet.names[i] || selected.name,
              aisType: fleet.aisType[i] || selected.aisType,
              sog: fleet.sog[i],
              cog: fleet.cog[i],
              heading: fleet.heading[i],
              rpm: orNull(fleet.rpm[i]),
              fuelRate: orNull(fleet.fuelRate[i]),
              temp: orNull(fleet.temp[i]),
              level: orNull(fleet.level[i]),
            }
          : // Out of view: the panel stays open and shows that the values are unknown.
            { ...selected, sog: null, cog: null, heading: null, rpm: null, fuelRate: null, temp: null, level: null };
    }

    const settlingLeft = firehoseOn ? Math.max(0, Math.ceil((SETTLE_MS - (now - this.modeSince)) / 1000)) : 0;

    const log = this.pendingLog.length ? [...this.pendingLog].reverse().concat(s.contract.log).slice(0, 8) : s.contract.log;
    this.pendingLog = [];

    const hasHistory = s.timeline.historyStartMs > 0 && s.timeline.historyStartMs < nowMs - 60_000;

    s.set({
      staleAgeS,
      nextRetryS: stale ? Math.max(0, Math.ceil((this.retryAt - now) / 1000)) : 0,
      selected,
      telemetry: {
        ...s.telemetry,
        bytesPerSec: shown,
        framesPerSec,
        renderFps,
        lagMs: lagMs === null ? null : Math.round(lagMs),
        droppedRising: now < this.droppedRisingUntil,
      },
      firehose: firehoseOn ? { on: true, settlingS: settlingLeft, value: shown, lastLens: this.lastLens } : { ...s.firehose, lastLens: this.lastLens },
      bytesHistory: connected ? [...s.bytesHistory.slice(-119), shown] : s.bytesHistory,
      fpsHistory: [...s.fpsHistory.slice(-119), renderFps],
      contract: { ...s.contract, log },
      timeline: { ...s.timeline, nowMs, hasHistory },
    });
  }
}

export const lensClient = new LensClient();
