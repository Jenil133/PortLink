// Count labels on cluster columns collide where columns bunch up at low zoom. fitLabels keeps the
// ones that fit: largest count first, and a label whose box would touch one already kept is left
// out. The column itself stays, so nothing leaves the map, only a number that could not be read.
import { WebMercatorViewport } from "@deck.gl/core";
import type { Map as MapLibreMap } from "maplibre-gl";

/** What fitLabels needs from a deck.gl viewport. */
export interface LabelViewport {
  width: number;
  height: number;
  project(xyz: number[]): number[];
}

// Box of one count label: Geist Mono at 12 px advances 7.2 px per character, plus a 2 px outline.
const CHAR_W = 7.2;
const LINE_H = 12;
const OUTLINE = 2;
/** Space kept clear between two labels, so two numbers never read as one. */
const GAP = 4;

/**
 * Returns the items whose labels fit on screen without overlapping, in their original order.
 * `anchor` is the label's position in [lng, lat, metres], `offsetY` its pixel offset. When the
 * result equals `prev`, `prev` itself comes back, so deck.gl keeps the text layer's buffers.
 */
export function fitLabels<T>(
  items: readonly T[],
  vp: LabelViewport,
  anchor: (t: T) => [number, number, number],
  text: (t: T) => string,
  priority: (t: T) => number,
  offsetY: number,
  prev: T[] = [],
): T[] {
  const order = items.map((t, i) => ({ t, i, p: priority(t) })).sort((a, b) => b.p - a.p || a.i - b.i);
  const boxes: number[] = [];
  const kept: { t: T; i: number }[] = [];
  for (const o of order) {
    const [x, y0] = vp.project(anchor(o.t));
    const y = y0 + offsetY;
    const hw = (text(o.t).length * CHAR_W) / 2 + OUTLINE + GAP / 2;
    const hh = LINE_H / 2 + OUTLINE + GAP / 2;
    // Off screen it is not drawn, so it takes no room. NaN (behind the camera) fails here too.
    if (!(x + hw > 0 && x - hw < vp.width && y + hh > 0 && y - hh < vp.height)) continue;
    let free = true;
    for (let k = 0; k < boxes.length && free; k += 4) {
      free = x - hw >= boxes[k + 2] || x + hw <= boxes[k] || y - hh >= boxes[k + 3] || y + hh <= boxes[k + 1];
    }
    if (!free) continue;
    boxes.push(x - hw, y - hh, x + hw, y + hh);
    kept.push(o);
  }
  kept.sort((a, b) => a.i - b.i);
  if (kept.length === prev.length && kept.every((k, j) => k.t === prev[j])) return prev;
  return kept.map((k) => k.t);
}

/** The viewport deck.gl's MapboxOverlay draws with over this map: its getViewState, without terrain. */
export function mapViewport(map: MapLibreMap): WebMercatorViewport {
  const { lng, lat } = map.getCenter();
  const canvas = map.getCanvas();
  return new WebMercatorViewport({
    width: canvas.clientWidth,
    height: canvas.clientHeight,
    longitude: ((lng + 540) % 360) - 180,
    latitude: lat,
    zoom: map.getZoom(),
    bearing: map.getBearing(),
    pitch: map.getPitch(),
    padding: map.getPadding(),
  });
}
