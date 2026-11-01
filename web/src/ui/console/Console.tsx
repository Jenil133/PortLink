// The console's 2D chrome: everything above the map. Pointer events pass through to the map except on panels.
import { useEffect, useRef, useState } from "react";
import { Check, X } from "@phosphor-icons/react";
import { gridStep } from "../../scene/basemap";
import type { SceneEngine } from "../../scene/engine";
import { getAtlas } from "../../scene/sprites";
import { fmt1, fmtInt, fmtLat, fmtLng, typeWord } from "../../state/format";
import { lensActions, useLens } from "../../state/store";
import { InstrumentCard, Inspector } from "./InstrumentCard";
import { Timeline } from "./Timeline";
import { useLayout, type Layout } from "./useLayout";
import { VesselPanel } from "./VesselPanel";
import "./console.css";

const LEGEND = [
  { label: "cargo", col: 1 },
  { label: "tanker", col: 2 },
  { label: "fishing", col: 3 },
  { label: "passenger", col: 4 },
  { label: "other", col: 0 },
];

function Header({ engine, layout }: { engine: SceneEngine | null; layout: Layout }) {
  const s = useLens();
  const stale = s.connection === "reconnecting" || s.connection === "offline";
  const v = s.view;
  const word = stale ? (
    <span className="d-word" style={{ color: "var(--status-warn-on-sea)" }}>
      STALE
    </span>
  ) : s.truth === "history" ? (
    <span className="d-word" style={{ color: "var(--text-on-sea-muted)" }}>
      HISTORY
    </span>
  ) : (
    <span className="d-word" style={{ color: "var(--text-live)" }}>
      LIVE
    </span>
  );
  const detail = s.clustered ? `clusters res ${v.zoom < 4 ? 2 : 3}` : `tier ${s.tier}`;
  const phone = layout === "phone";
  return (
    <header className={`head head--${layout}`}>
      <div className="head__line">
        {phone ? word : <h1 className="d-head head__name">PortLink Lens</h1>}
        {!phone && (
          <button className="head__link t-body" onClick={() => engine?.backToStory()}>
            Story
          </button>
        )}
        {!phone && word}
        {layout === "full" && (
          <button className="head__keys t-label" onClick={() => s.set({ shortcutsOpen: !s.shortcutsOpen })} aria-label="Keyboard shortcuts">
            ? keys
          </button>
        )}
        {phone && <span className="n-xs head__detail">z {v.zoom.toFixed(1)}</span>}
        {phone && <span className="n-xs head__detail">{detail}</span>}
        {phone && <span className="t-title head__phone-name">PortLink Lens</span>}
      </div>
      {!phone && (
        <p className="n-xs head__detail head__line2">
          z {v.zoom.toFixed(1)}
          {v.pitch > 0.5 && `  pitch ${Math.round(v.pitch)}`}
          {`  ${detail}`}
          {s.firehose.on && s.truth === "live" && "  mode firehose"}
          {layout === "full" && `  ${fmtLng(v.lng)}, ${fmtLat(v.lat)}`}
          {s.subscribing && <span style={{ color: "var(--text-live)" }}>  subscribing</span>}
        </p>
      )}
      {s.clustered && !phone && <p className="t-label head__hint">zoom to 6 for vessels, click a column to zoom in</p>}
    </header>
  );
}

function Legend() {
  const atlas = getAtlas().url;
  return (
    <ul className="legend t-label">
      {LEGEND.map((l) => (
        <li key={l.label}>
          <span className="legend__sprite" style={{ backgroundImage: `url(${atlas})`, backgroundPosition: `${-l.col * 16}px 0` }} />
          {l.label}
        </li>
      ))}
    </ul>
  );
}

function Tooltip() {
  const hover = useLens((s) => s.hover);
  const selected = useLens((s) => s.selected?.mmsi);
  if (!hover || hover.mmsi === selected) return null;
  const flip = hover.x > innerWidth - 400;
  return (
    <div className="tooltip" style={{ transform: `translate(${hover.x + (flip ? -252 : 12)}px, ${hover.y + 12}px)` }}>
      <p>
        <span className="t-title">{hover.name}</span> <span className="t-label tooltip__muted">{typeWord(hover.aisType)}</span>
      </p>
      <p>
        <span className="n-xs">{fmt1(hover.sog)}</span> <span className="n-unit tooltip__muted">kn</span>{" "}
        <svg width="10" height="10" viewBox="-5 -5 10 10" aria-hidden="true" style={{ transform: `rotate(${hover.cog ?? 0}deg)`, verticalAlign: -1 }}>
          <path d="M0-5L3 5 0 3-3 5Z" fill="var(--pearl)" />
        </svg>{" "}
        <span className="n-xs">{fmtInt(hover.cog)}</span> <span className="n-unit tooltip__muted">deg</span>
      </p>
    </div>
  );
}

function Guide() {
  const guide = useLens((s) => s.guide);
  const set = useLens((s) => s.set);
  if (guide.dismissed) return null;
  const Step = ({ done, label }: { done: boolean; label: string }) => (
    <li className={done ? "is-done" : ""}>
      <span className="guide__box">{done && <Check size={12} weight="bold" aria-label="done" />}</span>
      {label}
    </li>
  );
  return (
    <aside className="guide t-label" aria-label="Try this">
      <span className="t-title">Try this</span>
      <ul>
        <Step done={guide.pan} label="pan the map" />
        <Step done={guide.scrub} label="drag the timeline" />
        <Step done={guide.firehose} label="turn on firehose" />
      </ul>
      <button className="icon-btn icon-btn--sea" aria-label="Dismiss" onClick={() => set({ guide: { ...guide, dismissed: true } })}>
        <X size={14} />
      </button>
    </aside>
  );
}

function Banner() {
  const s = useLens();
  if (s.connection === "reconnecting") {
    return (
      <div className="banner" role="status">
        <span className="t-title" style={{ color: "var(--status-warn-on-sea)" }}>
          reconnecting...
        </span>
        <span className="n-xs">
          attempt {s.attempt}, next in {s.nextRetryS} s
        </span>
      </div>
    );
  }
  if (s.connection === "offline") {
    return (
      <div className="banner" role="status">
        <span className="t-title" style={{ color: "var(--status-error-on-sea)" }}>
          server unreachable
        </span>
        <span className="t-label">check the gateway, then reload</span>
      </div>
    );
  }
  if (s.connection === "connected" && s.firstFrame && s.telemetry.inView === 0 && !s.clustered) {
    return (
      <div className="notice t-label" role="status">
        no vessels in view, pan or zoom out
      </div>
    );
  }
  return null;
}

const SHORTCUTS: [string, string][] = [
  ["L", "return to live"],
  ["Space", "pause or play"],
  ["F", "firehose baseline"],
  ["C", "contract inspector"],
  ["Esc", "close panel or sheet"],
  ["?", "this sheet"],
];

function Shortcuts() {
  const open = useLens((s) => s.shortcutsOpen);
  const set = useLens((s) => s.set);
  if (!open) return null;
  return (
    <aside className="shortcuts" aria-label="Keyboard shortcuts">
      <div className="shortcuts__head">
        <span className="t-title">Keys</span>
        <button className="t-label text-btn" onClick={() => set({ shortcutsOpen: false })}>
          ? closes
        </button>
      </div>
      {SHORTCUTS.map(([k, d]) => (
        <div key={k} className="shortcuts__row">
          <kbd className="n-xs">{k}</kbd>
          <span className="t-label">{d}</span>
        </div>
      ))}
    </aside>
  );
}

/** Latitude labels down the left edge and longitude labels along the bottom of the map, redrawn with the view. */
function GridLabels({ engine, layout }: { engine: SceneEngine | null; layout: Layout }) {
  const view = useLens((s) => s.view);
  const [labels, setLabels] = useState<{ key: string; x: number; y: number; text: string }[]>([]);

  useEffect(() => {
    if (!engine || layout === "phone") return;
    const m = engine.map;
    const step = gridStep(view.zoom);
    const h = innerHeight - 64;
    const digits = step >= 1 ? 0 : 2;
    const next: { key: string; x: number; y: number; text: string }[] = [];

    const edge = (x: number, y: number) => {
      const p = m.unproject([x, y]);
      return { lng: p.lng, lat: p.lat };
    };
    // Left edge: find where each parallel crosses it.
    const top = edge(16, 32).lat;
    const bottom = edge(16, h - 32).lat;
    for (let lat = Math.ceil(bottom / step) * step; lat < top; lat += step) {
      let lo = 32;
      let hi = h - 32;
      for (let i = 0; i < 12; i++) {
        const mid = (lo + hi) / 2;
        if (edge(16, mid).lat > lat) lo = mid;
        else hi = mid;
      }
      next.push({ key: `lat${lat}`, x: 12, y: lo, text: fmtLat(lat, digits) });
    }
    // Bottom edge.
    const left = edge(32, h - 16).lng;
    const right = edge(innerWidth - 32, h - 16).lng;
    for (let lng = Math.ceil(left / step) * step; lng < right; lng += step) {
      let lo = 32;
      let hi = innerWidth - 32;
      for (let i = 0; i < 12; i++) {
        const mid = (lo + hi) / 2;
        if (edge(mid, h - 16).lng < lng) lo = mid;
        else hi = mid;
      }
      next.push({ key: `lng${lng}`, x: lo, y: h - 16, text: fmtLng(lng, digits) });
    }
    setLabels(next.slice(0, 40));
  }, [engine, view, layout]);

  if (layout === "phone") return null;
  return (
    <div className="grid-labels" aria-hidden="true">
      {labels.map((l) => (
        <span key={l.key} className="n-xs on-map" style={{ transform: `translate(${l.x}px, ${l.y - 8}px)` }}>
          {l.text}
        </span>
      ))}
    </div>
  );
}

export function Console({ engine }: { engine: SceneEngine | null }) {
  const root = useRef<HTMLDivElement>(null);
  const layout = useLayout();
  const sceneMode = useLens((s) => s.sceneMode);
  const set = useLens((s) => s.set);
  const legend = useLens((s) => s.view.zoom >= 11);
  const inspectorOpen = useLens((s) => s.inspectorOpen);
  const inConsole = sceneMode === "console";
  // The guide shares the top row with the connection banner, and the banner wins.
  const connectionBad = useLens((s) => s.connection === "reconnecting" || s.connection === "offline");

  // The chrome docks during the last moments of the story.
  useEffect(() => {
    const el = root.current;
    if (!el || !engine) return;
    if (inConsole) {
      el.style.setProperty("--dock", "1");
      el.style.visibility = "visible";
      return;
    }
    return engine.subscribeStory((p) => {
      const t = Math.max(0, Math.min(1, (p - 0.985) / 0.015));
      el.style.setProperty("--dock", t.toFixed(3));
      el.style.visibility = t > 0 ? "visible" : "hidden";
    });
  }, [engine, inConsole]);

  useEffect(() => {
    if (!inConsole) return;
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      if (target.closest?.("input, textarea")) return;
      const s = useLens.getState();
      switch (e.key) {
        case "l":
        case "L":
          lensActions.goLive();
          break;
        case " ":
          if (target.tagName === "BUTTON") return;
          e.preventDefault();
          lensActions.togglePause();
          break;
        case "f":
        case "F":
          lensActions.toggleFirehose();
          break;
        case "c":
        case "C":
          set({ inspectorOpen: !s.inspectorOpen });
          break;
        case "?":
          set({ shortcutsOpen: !s.shortcutsOpen });
          break;
        case "Escape":
          if (s.shortcutsOpen) set({ shortcutsOpen: false });
          else if (s.inspectorOpen) set({ inspectorOpen: false });
          else if (s.selected) lensActions.closeVessel();
          else if (s.cardExpanded) set({ cardExpanded: false });
          break;
      }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [inConsole, set]);

  return (
    <div ref={root} className={`console console--${layout}${inConsole ? " is-active" : ""}`} style={{ visibility: "hidden" }}>
      <GridLabels engine={engine} layout={layout} />
      <Header engine={engine} layout={layout} />
      {legend && layout !== "phone" && <Legend />}
      {layout !== "phone" && !connectionBad && <Guide />}
      <Banner />
      <InstrumentCard layout={layout} />
      {inspectorOpen && <Inspector layout={layout} />}
      <VesselPanel layout={layout} onFitTrack={() => engine?.fitTrack()} />
      <Shortcuts />
      <Tooltip />
      <Timeline layout={layout} />
    </div>
  );
}
