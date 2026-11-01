// Selected vessel panel: live values, five sparklines with a readout, compass and fuel gauge.
import { useMemo, useRef, useState, type PointerEvent, type KeyboardEvent } from "react";
import { CaretDown, CaretUp, X } from "@phosphor-icons/react";
import { fmt1, fmtInt, clockHM, typeWord } from "../../state/format";
import { lensActions, useLens, type MetricSeries } from "../../state/store";
import { timeLinePath } from "./Sparkline";
import type { Layout } from "./useLayout";

const SPARK_W = 132;
const SPARK_H = 28;

const fmtMetric = (m: MetricSeries, v: number) => (m.key === "rpm" ? fmtInt(v) : fmt1(v));

function nearestIndex(times: number[], t: number) {
  let lo = 0;
  let hi = times.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (times[mid] < t) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

function SparkRow({ metric, cursorT, current, startMs, endMs }: { metric: MetricSeries; cursorT: number | null; current: number | null; startMs: number; endMs: number }) {
  const d = useMemo(() => timeLinePath(metric.times, metric.values, SPARK_W, SPARK_H, startMs, endMs, metric.min, metric.max), [metric, startMs, endMs]);
  const [hover, setHover] = useState<number | null>(null);
  const svg = useRef<SVGSVGElement>(null);

  const toIndex = (clientX: number) => {
    const r = svg.current!.getBoundingClientRect();
    const t = Math.max(0, Math.min(1, (clientX - r.left) / r.width));
    return nearestIndex(metric.times, startMs + t * (endMs - startMs));
  };
  const onMove = (e: PointerEvent) => setHover(toIndex(e.clientX));
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowLeft") setHover((h) => Math.max(0, (h ?? metric.values.length - 1) - 10));
    else if (e.key === "ArrowRight") setHover((h) => Math.min(metric.values.length - 1, (h ?? 0) + 10));
  };

  const lo = metric.min;
  const span = metric.max - lo || 1;
  const yOf = (v: number) => SPARK_H - 1 - ((v - lo) / span) * (SPARK_H - 2);
  const cursorIndex = cursorT === null ? null : nearestIndex(metric.times, startMs + cursorT * (endMs - startMs));
  const hoverX = hover === null ? 0 : ((metric.times[hover] - startMs) / (endMs - startMs || 1)) * SPARK_W;

  return (
    <div className="spark">
      <div className="spark__label">
        <span className="t-label">
          {metric.label} {metric.unit}
        </span>
        <span className="n-xs spark__range">
          {fmtMetric(metric, metric.min)} to {fmtMetric(metric, metric.max)}
        </span>
      </div>
      <div className="spark__chart">
        <svg
          ref={svg}
          width={SPARK_W}
          height={SPARK_H}
          viewBox={`0 0 ${SPARK_W} ${SPARK_H}`}
          tabIndex={0}
          role="img"
          aria-label={`${metric.label} over six hours, from ${fmtMetric(metric, metric.min)} to ${fmtMetric(metric, metric.max)}`}
          onPointerMove={onMove}
          onPointerLeave={() => setHover(null)}
          onFocus={() => setHover(metric.values.length - 1)}
          onBlur={() => setHover(null)}
          onKeyDown={onKey}
        >
          <path d={d} fill="none" stroke="var(--pearl)" strokeWidth="1.25" strokeLinejoin="round" />
          {cursorIndex !== null && (
            <>
              <line x1={(cursorT as number) * SPARK_W} x2={(cursorT as number) * SPARK_W} y1="0" y2={SPARK_H} stroke="var(--pearl)" strokeWidth="1" />
              <circle cx={(cursorT as number) * SPARK_W} cy={yOf(metric.values[cursorIndex])} r="3" fill="var(--pearl)" />
            </>
          )}
          {hover !== null && <line x1={hoverX} x2={hoverX} y1="0" y2={SPARK_H} stroke="var(--teal-muted-1)" strokeWidth="1" />}
        </svg>
        {hover !== null && (
          // Sits below the line and clamps inside the chart column, so it never covers the row above.
          <span className="spark__readout" style={{ left: Math.min(hoverX, SPARK_W - 92) }}>
            <span className="n-xs">
              {fmtMetric(metric, metric.values[hover])} {metric.unit}
            </span>{" "}
            <span className="n-xs spark__range">{clockHM(metric.times[hover])}</span>
          </span>
        )}
      </div>
      <span className="n-sm spark__value">{current === null ? "--" : fmtMetric(metric, current)}</span>
    </div>
  );
}

function EmptyRow({ label }: { label: string }) {
  return (
    <div className="spark">
      <div className="spark__label">
        <span className="t-label">{label}</span>
      </div>
      <div className="spark__chart">
        <svg width={SPARK_W} height={SPARK_H} aria-hidden="true">
          <line x1="0" x2={SPARK_W} y1={SPARK_H / 2} y2={SPARK_H / 2} stroke="var(--teal-muted-2)" strokeDasharray="3 3" />
        </svg>
      </div>
      <span className="t-label spark__range">no series yet</span>
    </div>
  );
}

function Compass({ cog, heading }: { cog: number | null; heading: number | null }) {
  const needle = (deg: number, dash: boolean) => {
    const a = (deg - 90) * (Math.PI / 180);
    return <line x1="24" y1="24" x2={24 + Math.cos(a) * 18} y2={24 + Math.sin(a) * 18} stroke="var(--pearl)" strokeWidth="2" strokeDasharray={dash ? "3 2" : undefined} />;
  };
  return (
    <svg width="48" height="48" viewBox="0 0 48 48" role="img" aria-label={`Course ${cog ?? "unknown"} degrees, heading ${heading ?? "unknown"} degrees`}>
      <circle cx="24" cy="24" r="22" fill="none" stroke="var(--line-on-sea-strong)" />
      {[0, 90, 180, 270].map((d) => {
        const a = (d - 90) * (Math.PI / 180);
        return <line key={d} x1={24 + Math.cos(a) * 19} y1={24 + Math.sin(a) * 19} x2={24 + Math.cos(a) * 22} y2={24 + Math.sin(a) * 22} stroke="var(--pearl)" />;
      })}
      {cog !== null && needle(cog, true)}
      {heading !== null && needle(heading, false)}
      <circle cx="24" cy="24" r="2" fill="var(--pearl)" />
    </svg>
  );
}

function Cell({ label, value, unit, big }: { label: string; value: string; unit: string; big?: boolean }) {
  return (
    <div className="vessel__cell">
      <span className="t-label vessel__muted">{label}</span>
      <span>
        <span className={big ? "n-lg" : "n-sm"}>{value}</span> <span className="n-unit vessel__muted">{unit}</span>
      </span>
    </div>
  );
}

export function VesselPanel({ layout, onFitTrack }: { layout: Layout; onFitTrack: () => void }) {
  const selected = useLens((s) => s.selected);
  const series = useLens((s) => s.series);
  const cursorMs = useLens((s) => s.timeline.cursorMs);
  const [open, setOpen] = useState(false);
  if (!selected) return null;

  const cursorT = series && cursorMs !== null ? Math.max(0, Math.min(1, (cursorMs - series.startMs) / (series.endMs - series.startMs))) : null;
  const history = cursorMs !== null;
  const metric = (k: MetricSeries["key"]) => (k === "sog" ? selected.sog : k === "rpm" ? selected.rpm : k === "fuel" ? selected.fuelRate : k === "temp" ? selected.temp : selected.level);

  const header = (
    <header className="vessel__head">
      <div className="vessel__id">
        <h2 className="d-head vessel__name" title={selected.name}>
          {selected.name}
        </h2>
        <p>
          <span className="n-xs vessel__muted">{selected.mmsi}</span> <span className="t-label vessel__muted">{typeWord(selected.aisType)}</span>
        </p>
      </div>
      <button className="icon-btn icon-btn--sea" aria-label="Close vessel panel" onClick={() => lensActions.closeVessel()}>
        <X size={16} />
      </button>
    </header>
  );

  if (layout === "phone" && !open) {
    return (
      <section className="vessel vessel--bar" aria-label="Selected vessel">
        <button className="vessel__handle" aria-label="Open vessel panel" aria-expanded={false} onClick={() => setOpen(true)}>
          <CaretUp size={14} aria-hidden="true" />
        </button>
        <div className="vessel__id">
          <h2 className="t-title vessel__name">{selected.name}</h2>
          <p>
            <span className="n-xs vessel__muted">{selected.mmsi}</span> <span className="t-label vessel__muted">{typeWord(selected.aisType)}</span>
          </p>
        </div>
        <span>
          <span className="n-md">{fmt1(selected.sog)}</span> <span className="n-unit vessel__muted">kn</span>
        </span>
        <span>
          <span className="n-md">{fmtInt(selected.cog)}</span> <span className="n-unit vessel__muted">deg</span>
        </span>
        <button className="icon-btn icon-btn--sea" aria-label="Close vessel panel" onClick={() => lensActions.closeVessel()}>
          <X size={16} />
        </button>
      </section>
    );
  }

  return (
    <section className={`vessel vessel--${layout}`} aria-label="Selected vessel">
      {layout === "phone" && (
        <button className="vessel__handle" aria-label="Collapse vessel panel" aria-expanded onClick={() => setOpen(false)}>
          <CaretDown size={14} aria-hidden="true" />
        </button>
      )}
      {header}
      <div className="vessel__row vessel__row--big">
        <Cell label={history ? "SOG at cursor" : "SOG"} value={fmt1(selected.sog)} unit="kn" big />
        <Cell label="COG" value={fmtInt(selected.cog)} unit="deg" />
        <Cell label="HDG" value={fmtInt(selected.heading)} unit="deg" />
      </div>
      <div className="vessel__row vessel__row--four">
        <Cell label="RPM" value={fmtInt(selected.rpm)} unit="" />
        <Cell label="fuel" value={fmt1(selected.fuelRate)} unit="L/h" />
        <Cell label="temp" value={fmt1(selected.temp)} unit="C" />
        <Cell label="level" value={fmt1(selected.level)} unit="%" />
      </div>
      <div className="vessel__sparks">
        {series ? series.metrics.map((m) => <SparkRow key={m.key} metric={m} cursorT={cursorT} current={metric(m.key)} startMs={series.startMs} endMs={series.endMs} />) : ["SOG kn", "RPM", "fuel L/h", "temp C", "level %"].map((l) => <EmptyRow key={l} label={l} />)}
      </div>
      <div className="vessel__instruments">
        <Compass cog={selected.cog} heading={selected.heading} />
        <div className="vessel__compass-text">
          <span className="t-label vessel__muted">COG dashed, HDG solid</span>
          <span>
            <span className="n-sm">{fmtInt(selected.cog)}</span> <span className="n-unit vessel__muted">cog</span> <span className="n-sm">{fmtInt(selected.heading)}</span> <span className="n-unit vessel__muted">hdg</span>
          </span>
        </div>
        <div className="vessel__gauge">
          <span className="t-label vessel__muted">fuel level</span>
          <span className="gauge" role="meter" aria-label="Fuel level" aria-valuemin={0} aria-valuemax={100} aria-valuenow={selected.level ?? 0}>
            <span className="gauge__fill" style={{ width: `${selected.level ?? 0}%` }} />
          </span>
          <span className="n-xs">{fmt1(selected.level)} %</span>
        </div>
      </div>
      <footer className="vessel__footer">
        <span className="n-xs">{series ? `${series.tier} tier, ${fmtInt(series.rawPoints)} -> ${fmtInt(series.metrics[0].values.length)} points (LTTB)` : "series not received, values above are live"}</span>
        <button className="text-btn t-label" onClick={onFitTrack} disabled={!series}>
          fit track
        </button>
      </footer>
    </section>
  );
}
