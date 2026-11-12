// The instrument card, in every state from design spec v3, sheet 06, plus the contract inspector.
import { ArrowUp, CaretDown, CaretUp, WarningCircle, X } from "@phosphor-icons/react";
import { fmt1, fmtInt, fmtKB, clockHMS, fmtLat, fmtLng } from "../../state/format";
import { lensActions, useLens } from "../../state/store";
import { Sparkline } from "./Sparkline";
import type { Layout } from "./useLayout";

const TEAL = "var(--text-on-lime)";

function useCardState() {
  const s = useLens();
  const connected = s.connection === "connected";
  const stale = s.connection === "reconnecting" || s.connection === "offline";
  const liveNow = connected && s.truth === "live" && s.firstFrame;
  const { on, settlingS, value, lastLens } = s.firehose;
  const selected = s.selected !== null;
  const firehoseActive = on && connected && s.truth === "live";
  const showRatio = firehoseActive && settlingS === 0 && !selected && value !== null && lastLens !== null && lastLens > 0;
  const ratio = showRatio ? value! / lastLens! : null;
  return { s, connected, stale, liveNow, firehoseActive, selected, settlingS, ratio };
}

function Word({ text, muted }: { text: string; muted?: boolean }) {
  return (
    <span className="d-word card__word" style={muted ? { color: "var(--text-on-card-muted)" } : undefined}>
      {text}
    </span>
  );
}

function FirehoseSwitch({ note }: { note: string | null }) {
  const { s, connected } = useCardState();
  const disabled = s.truth !== "live" || !connected;
  const on = s.firehose.on;
  return (
    <div className="card__switch-row">
      <button className={`switch${on ? " is-on" : ""}`} role="switch" aria-checked={on} aria-label="Firehose baseline" disabled={disabled} onClick={() => lensActions.toggleFirehose()}>
        <span className="switch__knob" />
      </button>
      <span className="t-title" style={disabled ? { color: "var(--text-disabled-on-card)" } : undefined}>
        firehose baseline
      </span>
      {note && <span className="t-label card__muted card__note">{note}</span>}
    </div>
  );
}

function Hero() {
  const { s, stale, firehoseActive, selected, settlingS, ratio } = useCardState();
  const t = s.telemetry;
  const { value, lastLens } = s.firehose;

  if (!s.firstFrame) {
    return (
      <>
        <p className="t-label card__slab-label">bytes per second, lens</p>
        <div className="skeleton skeleton--slab" style={{ width: 280, height: 60 }} />
      </>
    );
  }

  if (ratio !== null) {
    const track = 100;
    const lensShare = (track * lastLens!) / value!;
    return (
      <>
        <p className="t-label card__slab-label">lens is</p>
        <p className="card__hero-row">
          <span className="n-hero">{fmt1(ratio)}</span>
          <span className="d-word card__times">times smaller</span>
        </p>
        <div className="bars" aria-label={`Firehose ${fmtInt(value)} bytes per second, lens ${fmtInt(lastLens)} bytes per second`}>
          <span className="t-label">firehose</span>
          <span className="bar">
            <span className="bar__fill" style={{ width: "100%" }} />
          </span>
          <span className="n-xs">{fmtInt(value)} B/s</span>
          <span className="t-label">lens</span>
          <span className="bar">
            <span className="bar__fill" style={{ width: `${lensShare}%` }} />
            <span className="bar__marker" style={{ left: `calc(${lensShare}% + 2px)` }} />
          </span>
          <span className="n-xs">{fmtInt(lastLens)} B/s</span>
        </div>
      </>
    );
  }

  if (firehoseActive) {
    return (
      <>
        <p className="t-label card__slab-label">bytes per second, firehose{settlingS > 0 ? ", settling" : ""}</p>
        <p className="card__hero-row">
          <span className="n-hero">{fmtInt(value)}</span>
          <span className="n-unit">B/s</span>
        </p>
        <p className="card__last">
          <span className="t-label card__slab-label">lens, last measured</span> <span className="n-sm">{fmtInt(lastLens)}</span> <span className="n-unit card__slab-label">B/s</span>
        </p>
      </>
    );
  }

  const label = stale ? "bytes per second, lens, last value" : selected ? "bytes per second, lens, includes series" : "bytes per second, lens";
  return (
    <>
      <p className="t-label card__slab-label">{label}</p>
      <div className="card__hero-split">
        <p className="card__hero-row" style={stale ? { color: "var(--text-on-card-muted)" } : undefined}>
          <span className="n-hero">{fmtInt(t.bytesPerSec)}</span>
          <span className="n-unit">B/s</span>
        </p>
        {s.bytesHistory.length > 1 && !stale && (
          <div className="card__chart">
            <Sparkline values={s.bytesHistory} width={136} height={36} stroke={TEAL} min={0} />
            <span className="t-label card__slab-label">{s.truth === "history" && s.timeline.paused ? "last 60 s, paused" : "last 60 s"}</span>
          </div>
        )}
      </div>
    </>
  );
}

function slabWord(s: ReturnType<typeof useLens.getState>) {
  // Stale means old data on screen. A client that never got a frame has nothing to be stale about.
  if (s.connection === "reconnecting" || s.connection === "offline") return s.firstFrame ? `STALE ${s.staleAgeS} s` : "OFFLINE";
  if (s.truth === "history") return "HISTORY";
  return "LIVE";
}

function connectionText(s: ReturnType<typeof useLens.getState>) {
  if (s.connection === "reconnecting") return `reconnecting, attempt ${s.attempt}, next in ${s.nextRetryS} s`;
  if (s.connection === "offline") return "offline";
  if (s.connection === "connecting") return "connecting";
  return "connected";
}

function Footer() {
  const s = useLens();
  const t = s.telemetry;
  const stale = s.connection !== "connected";
  return (
    <p className="card__footer n-xs">
      {`tier ${s.tier}`}
      {!stale && t.seekMs !== null && ` · seek ${t.seekMs} ms`}
      {" · "}
      <span style={stale ? { color: "var(--status-warn-on-card)" } : undefined}>{t.lagMs === null ? (stale ? "lag --" : "lag paused") : stale ? `lag ${(t.lagMs / 1000).toFixed(1)} s` : `lag ${t.lagMs} ms`}</span>
      {" · "}
      <StatusText />
    </p>
  );
}

function StatusText() {
  const s = useLens();
  if (s.error) {
    return (
      <span style={{ color: "var(--status-error-on-card)" }}>
        <WarningCircle size={12} style={{ verticalAlign: -2 }} aria-hidden="true" /> {s.error}
      </span>
    );
  }
  if (s.connection === "reconnecting") return <span style={{ color: "var(--status-error-on-card)" }}>stream closed, retrying</span>;
  if (s.connection === "offline") return <span style={{ color: "var(--status-error-on-card)" }}>server unreachable</span>;
  if (s.telemetry.droppedRising) return <span style={{ color: "var(--status-warn-on-card)" }}>ticks dropped, positions current</span>;
  if (s.telemetry.inView === 0 && s.firstFrame) return <>empty viewport</>;
  return <>no errors</>;
}

function Strip() {
  const s = useLens();
  const t = s.telemetry;
  if (!s.firstFrame) {
    return (
      <div className="strip">
        {[0, 1, 2, 3, 4].map((i) => (
          <div key={i} className="strip__cell">
            <div className="skeleton" style={{ height: 12, width: 48 }} />
            <div className="skeleton" style={{ height: 20, width: 56, marginTop: 8 }} />
          </div>
        ))}
      </div>
    );
  }
  return (
    <div className="strip">
      <div className="strip__cell">
        <span className="t-label card__muted">render fps</span>
        <span className="n-md">{fmtInt(t.renderFps)}</span>
        {s.fpsHistory.length > 1 && (
          <span title="55 fps floor">
            <Sparkline values={s.fpsHistory} width={84} height={18} stroke="var(--text-on-card)" min={40} max={65} floor={55} />
          </span>
        )}
      </div>
      <div className="strip__cell">
        <span className="t-label card__muted">frames/s</span>
        <span>
          <span className="n-md">{t.framesPerSec.toFixed(1)}</span> <span className="n-unit card__muted">Hz</span>
        </span>
      </div>
      <div className="strip__cell">
        <span className="t-label card__muted">last frame</span>
        <span>
          <span className="n-md">{fmtKB(t.lastFrameBytes)}</span> <span className="n-unit card__muted">KB</span>
        </span>
      </div>
      <div className="strip__cell">
        <span className="t-label card__muted">{s.firehose.on && s.truth === "live" ? "world" : "vessels"}</span>
        <span className="n-md">{fmtInt(t.inView)}</span>
      </div>
      <div className="strip__cell">
        <span className="t-label card__muted">dropped</span>
        <span className="n-md" style={t.droppedRising ? { color: "var(--status-warn-on-card)" } : undefined}>
          {fmtInt(t.dropped)}
          {t.droppedRising && <ArrowUp size={14} style={{ verticalAlign: -1 }} aria-label="rising" />}
        </span>
      </div>
    </div>
  );
}

function ContractStrip() {
  const s = useLens();
  return (
    <button className="card__contract" aria-expanded={s.inspectorOpen} onClick={() => s.set({ inspectorOpen: !s.inspectorOpen })}>
      <span className="t-title">contract</span>
      <span className="n-xs card__muted">
        seq {fmtInt(s.contract.seq)} · view {s.contract.viewId.toString(16)}
        {s.firehose.on && s.truth === "live" ? " · firehose" : ""}
      </span>
      {s.inspectorOpen ? <CaretDown size={14} aria-hidden="true" /> : <CaretUp size={14} aria-hidden="true" />}
    </button>
  );
}

export function InstrumentCard({ layout }: { layout: Layout }) {
  const state = useCardState();
  const { s, liveNow, firehoseActive, selected, settlingS, ratio } = state;

  let note: string | null = null;
  if (s.truth === "history") note = "live only, press LIVE to compare";
  else if (firehoseActive && settlingS > 0) note = `ratio after settling, ${settlingS} s`;
  else if (firehoseActive && selected) note = "close the vessel panel for a clean comparison";
  else if (selected) note = "close the vessel panel for a clean comparison";
  else if (firehoseActive && ratio !== null) note = "settled 30 s, same encoding";
  else if (firehoseActive) note = "no lens baseline yet, turn off for 30 s";

  const expanded = layout === "full" || s.cardExpanded;

  if (layout === "phone" && !s.cardExpanded) {
    return (
      <button className={`ticker${liveNow ? " is-live" : ""}`} aria-expanded={false} onClick={() => s.set({ cardExpanded: true })}>
        <span className="n-lg">{fmtInt(s.telemetry.bytesPerSec)}</span>
        <span className="n-unit">B/s {s.firehose.on ? "firehose" : "lens"}</span>
        <span className="ticker__fps">
          <span className="t-label">fps</span> <span className="n-sm">{fmtInt(s.telemetry.renderFps)}</span>
        </span>
        <CaretDown size={16} aria-hidden="true" />
      </button>
    );
  }

  if (!expanded) {
    const t = s.telemetry;
    return (
      <section className="card card--compact on-card" aria-label="Telemetry">
        <button className={`slab slab--compact${liveNow ? " is-live" : ""}`} onClick={() => s.set({ cardExpanded: true })} aria-expanded={false}>
          <span className="card__header">
            <span className="d-word card__title-small">Telemetry</span>
            <Word text={slabWord(s)} muted={s.connection === "reconnecting" || s.connection === "offline"} />
          </span>
          <span className="t-label card__slab-label">{firehoseActive ? "bytes per second, firehose" : "bytes per second, lens"}</span>
          <span className="card__hero-row" style={s.connection !== "connected" ? { color: "var(--text-on-card-muted)" } : undefined}>
            <span className="n-hero card__hero-compact">{fmtInt(ratio !== null ? ratio : t.bytesPerSec)}</span>
            <span className="n-unit">{ratio !== null ? "times smaller" : "B/s"}</span>
          </span>
        </button>
        <div className="slab-gap" />
        <div className="card__body">
          <div className="strip strip--compact">
            <div className="strip__cell">
              <span className="t-label card__muted">render fps</span>
              <span className="n-sm">{fmtInt(t.renderFps)}</span>
            </div>
            <div className="strip__cell">
              <span className="t-label card__muted">frames/s</span>
              <span>
                <span className="n-sm">{t.framesPerSec.toFixed(1)}</span> <span className="n-unit card__muted">Hz</span>
              </span>
            </div>
            <div className="strip__cell">
              <span className="t-label card__muted">vessels</span>
              <span className="n-sm">{fmtInt(t.inView)}</span>
            </div>
          </div>
          <p className="card__footer n-xs">
            {fmtKB(t.lastFrameBytes)} KB · {t.lagMs === null ? "lag paused" : `lag ${t.lagMs} ms`} · <StatusText />
          </p>
        </div>
      </section>
    );
  }

  return (
    <section className={`card on-card${layout === "phone" ? " card--sheet" : ""}`} aria-label="Telemetry">
      <div className={`slab${liveNow ? " is-live" : ""}`}>
        <div className="card__header">
          <h2 className="card__title">Telemetry</h2>
          <span className="t-label card__connection" style={s.connection === "reconnecting" ? { color: "var(--status-warn-on-card)" } : undefined}>
            {s.connection === "connected" && <span className="dot" />} {connectionText(s)}
          </span>
          {layout !== "full" && (
            <button className="icon-btn" aria-label="Collapse telemetry" onClick={() => s.set({ cardExpanded: false })}>
              <X size={16} />
            </button>
          )}
        </div>
        <div className="card__word-row">
          <Word text={slabWord(s)} muted={s.connection === "reconnecting" || s.connection === "offline"} />
        </div>
        <Hero />
      </div>
      <div className="slab-gap" />
      <div className="card__body">
        <FirehoseSwitch note={note} />
        <p className="t-label card__muted card__definition">firehose = every vessel, every field, every sample, same protobuf encoding</p>
        <div className="card__rule" />
        <Strip />
        <Footer />
        <ContractStrip />
      </div>
    </section>
  );
}

export function Inspector({ layout }: { layout: Layout }) {
  const s = useLens();
  if (!s.inspectorOpen) return null;
  const c = s.contract;
  const t = s.timeline;
  const b = c.bbox;
  const asked: [string, string][] = [
    ["bbox", b ? `${fmtLng(b[0], 1)} ${fmtLat(b[1], 1)}` : "--"],
    ["", b ? `${fmtLng(b[2], 1)} ${fmtLat(b[3], 1)}` : ""],
    ["zoom", s.view.zoom.toFixed(1)],
    ["mode", s.truth === "history" ? "history" : s.firehose.on ? "firehose" : "lens"],
    ["cursor", t.cursorMs === null ? "live" : clockHMS(t.cursorMs)],
    ["playback rate", t.cursorMs !== null && t.paused ? "0x" : `${t.rate}x`],
    ["selected MMSI", s.selected ? String(s.selected.mmsi) : "none"],
    ["view id", c.viewId.toString(16)],
  ];
  const sent: [string, string][] = [
    ["sequence", fmtInt(c.seq)],
    ["tier", s.tier],
    ["render mode", c.renderMode],
    ["vessels in frame", fmtInt(c.vesselsInFrame)],
    ["removed", fmtInt(c.removed)],
    ["in view", fmtInt(s.telemetry.inView)],
    ["dropped total", fmtInt(s.telemetry.dropped)],
    ["last frame", `${fmtInt(s.telemetry.lastFrameBytes)} B`],
  ];
  return (
    <section className={`inspector inspector--${layout}`} aria-label="Contract inspector">
      <header className="inspector__head">
        <h2 className="d-word inspector__title">Contract</h2>
        <span className="t-label inspector__muted">what the browser asked, what the server sent</span>
        <button className="icon-btn icon-btn--sea" aria-label="Close contract inspector" onClick={() => s.set({ inspectorOpen: false })}>
          <X size={16} />
        </button>
      </header>
      <div className="inspector__cols">
        <dl>
          <dt className="t-title inspector__col-title">Browser asked</dt>
          {asked.map(([k, v], i) => (
            <div key={i} className="inspector__row">
              <span className="t-label inspector__muted">{k}</span>
              <span className="n-xs">{v}</span>
            </div>
          ))}
        </dl>
        <dl>
          <dt className="t-title inspector__col-title">Server sent</dt>
          {sent.map(([k, v]) => (
            <div key={k} className="inspector__row">
              <span className="t-label inspector__muted">{k}</span>
              <span className="n-xs">{v}</span>
            </div>
          ))}
        </dl>
      </div>
      {layout !== "phone" && (
        <div className="inspector__log">
          <div className="inspector__log-head">
            <span className="t-title">Last 8 frames</span>
            <span className="t-label inspector__muted">2 Hz, newest first</span>
          </div>
          <table>
            <thead>
              <tr className="t-label inspector__muted">
                <th>time</th>
                <th>vessels</th>
                <th>bytes</th>
                <th>snapshot</th>
              </tr>
            </thead>
            <tbody className="n-xs">
              {Array.from({ length: 8 }, (_, i) => {
                const r = c.log[i];
                return (
                  <tr key={i}>
                    <td>{r ? clockHMS(r.time) : ""}</td>
                    <td>{r ? fmtInt(r.vessels) : ""}</td>
                    <td>{r ? fmtInt(r.bytes) : ""}</td>
                    <td>{r?.snapshot ? "snapshot" : ""}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
