import { useMemo } from "react";

/** A plain SVG line. The path string only changes when the data does. */
export function Sparkline({ values, width, height, stroke, min, max, floor }: { values: number[]; width: number; height: number; stroke: string; min?: number; max?: number; floor?: number }) {
  const d = useMemo(() => linePath(values, width, height, min, max), [values, width, height, min, max]);
  const lo = min ?? Math.min(...values);
  const hi = max ?? Math.max(...values);
  const floorY = floor === undefined || hi === lo ? null : height - 1 - ((floor - lo) / (hi - lo)) * (height - 2);
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden="true" style={{ display: "block", overflow: "visible" }}>
      {floorY !== null && <line x1="0" x2={width} y1={floorY} y2={floorY} stroke="var(--line-on-card-strong)" strokeDasharray="2 2" strokeWidth="1" />}
      {values.length > 1 && <path d={d} fill="none" stroke={stroke} strokeWidth="1.5" strokeLinejoin="round" />}
    </svg>
  );
}

/** A line placed by time. LTTB keeps the points that matter, so they are not evenly spaced. */
export function timeLinePath(times: number[], values: number[], width: number, height: number, startMs: number, endMs: number, min: number, max: number): string {
  const span = max - min || 1;
  const range = endMs - startMs || 1;
  let d = "";
  for (let i = 0; i < values.length; i++) {
    const x = ((times[i] - startMs) / range) * width;
    const y = height - 1 - ((values[i] - min) / span) * (height - 2);
    d += `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`;
  }
  return d;
}

export function linePath(values: number[], width: number, height: number, min?: number, max?: number): string {
  if (values.length < 2) return "";
  const lo = min ?? Math.min(...values);
  const hi = max ?? Math.max(...values);
  const span = hi - lo || 1;
  let d = "";
  for (let i = 0; i < values.length; i++) {
    const x = (i / (values.length - 1)) * width;
    // Clamped, because a fixed range such as the fps line can see values outside it.
    const y = height - 1 - (Math.min(1, Math.max(0, (values[i] - lo) / span))) * (height - 2);
    d += `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`;
  }
  return d;
}
