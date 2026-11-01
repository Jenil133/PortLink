// The docked timeline rail. One track from the start of retained history to now.
import { useMemo, useRef, type KeyboardEvent, type PointerEvent } from "react";
import { Pause, Play } from "@phosphor-icons/react";
import { clockHM, dateTime, offsetLabel, timeTz } from "../../state/format";
import { lensActions, useLens } from "../../state/store";
import { timeLinePath } from "./Sparkline";
import type { Layout } from "./useLayout";

const RATES = [1, 10, 60] as const;
const HOUR = 3600 * 1000;

const tierStepMs = (zoom: number) => (zoom >= 11 ? 1000 : zoom >= 7 ? 60000 : 600000);

export function Timeline({ layout }: { layout: Layout }) {
  const s = useLens();
  const t = s.timeline;
  const track = useRef<HTMLDivElement>(null);
  const stale = s.connection === "reconnecting" || s.connection === "offline";
  const live = t.cursorMs === null && !stale;
  const span = Math.max(1, t.nowMs - t.historyStartMs);
  const pos = t.cursorMs === null ? 1 : (t.cursorMs - t.historyStartMs) / span;
  const disabled = !t.hasHistory || stale;

  const ticks = useMemo(() => {
    const out: { key: number; at: number; label: string }[] = [];
    for (let h = Math.ceil(t.historyStartMs / HOUR) * HOUR; h < t.nowMs; h += HOUR) out.push({ key: h, at: (h - t.historyStartMs) / span, label: clockHM(h) });
    return out;
    // Ticks only need to move when the hour changes or the range appears.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [Math.floor(t.nowMs / 60000), t.historyStartMs]);

  const speed = s.series?.metrics.find((m) => m.key === "sog");
  // Placed on this track's own range, so a point on the line sits above its moment on the track.
  const speedPath = useMemo(() => (speed ? timeLinePath(speed.times, speed.values, 1000, 18, t.historyStartMs, t.nowMs, speed.min, speed.max) : ""), [speed]);

  const seekFromX = (clientX: number) => {
    const r = track.current!.getBoundingClientRect();
    const f = (clientX - r.left) / r.width;
    if (f >= 0.998) lensActions.goLive();
    else lensActions.seek(t.historyStartMs + Math.max(0, f) * span);
  };
  const onDown = (e: PointerEvent<HTMLDivElement>) => {
    if (disabled) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    seekFromX(e.clientX);
  };
  const onMove = (e: PointerEvent<HTMLDivElement>) => {
    if (!disabled && e.currentTarget.hasPointerCapture(e.pointerId)) seekFromX(e.clientX);
  };
  const onKey = (e: KeyboardEvent) => {
    if (disabled) return;
    const current = t.cursorMs ?? t.nowMs;
    const step = e.shiftKey ? 600000 : tierStepMs(s.view.zoom);
    if (e.key === "ArrowLeft") lensActions.seek(current - step);
    else if (e.key === "ArrowRight") {
      if (current + step >= t.nowMs) lensActions.goLive();
      else lensActions.seek(current + step);
    } else if (e.key === "Home") lensActions.seek(t.historyStartMs);
    else if (e.key === "End") lensActions.goLive();
    else return;
    e.preventDefault();
  };

  const shownTime = t.cursorMs ?? t.nowMs;
  const offset = t.cursorMs === null ? "" : offsetLabel(t.nowMs - t.cursorMs);
  const phone = layout === "phone";

  return (
    <section className={`rail rail--${layout}`} aria-label="Timeline">
      <div className="rail__controls">
        <button className={`live-btn${live ? " is-live" : ""}`} onClick={() => lensActions.goLive()} disabled={stale} aria-pressed={live}>
          LIVE
        </button>
        <button className="rail__btn" onClick={() => lensActions.togglePause()} disabled={disabled} aria-label={t.cursorMs !== null && t.paused ? "Play" : "Pause"}>
          {t.cursorMs !== null && t.paused ? <Play size={16} aria-hidden="true" /> : <Pause size={16} aria-hidden="true" />}
        </button>
        <div className="rail__rates" role="group" aria-label="Playback rate">
          {RATES.map((r) => (
            <button key={r} className={`rail__rate n-xs${t.rate === r ? " is-active" : ""}`} aria-pressed={t.rate === r} disabled={disabled} onClick={() => lensActions.setRate(r)}>
              {r}x
            </button>
          ))}
        </div>
        {!phone && <span className="n-xs rail__retained">{t.hasHistory ? "retained 6h" : ""}</span>}
      </div>

      <div
        ref={track}
        className={`rail__track${disabled ? " is-disabled" : ""}`}
        role="slider"
        tabIndex={disabled ? -1 : 0}
        aria-label="Time cursor"
        aria-valuemin={t.historyStartMs}
        aria-valuemax={t.nowMs}
        aria-valuenow={Math.round(shownTime)}
        aria-valuetext={t.cursorMs === null ? "Live" : offset}
        aria-disabled={disabled}
        onPointerDown={onDown}
        onPointerMove={onMove}
        onKeyDown={onKey}
      >
        {t.hasHistory ? (
          <>
            {speed && (
              <svg className="rail__speed" viewBox="0 0 1000 18" preserveAspectRatio="none" aria-hidden="true">
                <path d={speedPath} fill="none" stroke="var(--teal-muted-2)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
              </svg>
            )}
            <div className="rail__line" />
            {t.cursorMs !== null && <div className="rail__hatch" style={{ width: `${pos * 100}%` }} />}
            {!phone &&
              ticks.map((tk) => (
                <span key={tk.key} className="rail__tick" style={{ left: `${tk.at * 100}%` }}>
                  {layout === "full" && <span className="n-xs rail__tick-label">{tk.label}</span>}
                </span>
              ))}
            <div className={`rail__thumb${live ? " is-live" : ""}`} style={{ left: `${pos * 100}%` }} />
          </>
        ) : (
          <>
            <div className="rail__line rail__line--dashed" />
            <span className="t-label rail__nohistory">{t.historyStartMs === 0 ? "loading history range" : "no history range, reconnect or run the backfill"}</span>
          </>
        )}
      </div>

      <div className="rail__readout">
        {!phone && <span className="t-label rail__now">now</span>}
        <span className="n-sm rail__time">{layout === "full" ? dateTime(shownTime) : timeTz(shownTime)}</span>
        {stale ? (
          <span className="rail__mode">
            <span className="d-word" style={{ color: "var(--status-warn-on-sea)" }}>
              STALE
            </span>
            <span className="n-lg">{s.staleAgeS} s</span>
          </span>
        ) : live ? (
          <span className="d-word rail__mode" style={{ color: "var(--text-live)" }}>
            LIVE
          </span>
        ) : (
          <span className="rail__mode">
            <span className="d-word rail__history">HISTORY</span>
            <span className="n-lg">{offset}</span>
          </span>
        )}
      </div>
    </section>
  );
}
