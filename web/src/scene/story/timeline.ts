// Story timing from design spec v3, sheet 04. The whole story is a pure function of one progress
// value p, so scrolling backward reverses it exactly.
import { clamp, latToMerc, lerp, lngToMerc, mercToLat, mercToLng, range, smoothstep } from "../math";

export interface Camera {
  lng: number;
  lat: number;
  zoom: number;
  pitch: number;
  bearing: number;
}

interface Key extends Camera {
  p: number;
  /** Linear in p with no smoothstep, so the scene 5 orbit feels continuous under the user's scroll. */
  linear?: boolean;
  /** A narrow window can't show the desktop's open water and the coast together. */
  phoneLng?: number;
}

const KEYS: Key[] = [
  { p: 0, phoneLng: -125.2, lng: -130.0, lat: 39.8, zoom: 5.0, pitch: 48, bearing: -14 },
  { p: 0.16, phoneLng: -125.2, lng: -130.2, lat: 39.2, zoom: 5.3, pitch: 55, bearing: -8 },
  { p: 0.3, phoneLng: -123.9, lng: -126.0, lat: 38.2, zoom: 6.3, pitch: 45, bearing: -4 },
  { p: 0.34, phoneLng: -123.9, lng: -126.0, lat: 38.2, zoom: 6.3, pitch: 45, bearing: -4 },
  { p: 0.38, phoneLng: -123.9, lng: -125.15, lat: 38.0, zoom: 6.6, pitch: 42, bearing: 0 },
  { p: 0.54, phoneLng: -123.9, lng: -125.15, lat: 38.0, zoom: 6.6, pitch: 42, bearing: 0 },
  { p: 0.58, phoneLng: -122.9, lng: -123.4, lat: 37.6, zoom: 8.0, pitch: 30, bearing: 0 },
  { p: 0.7, phoneLng: -122.9, lng: -123.4, lat: 37.6, zoom: 8.0, pitch: 30, bearing: 0 },
  { p: 0.78, lng: -122.54, lat: 37.81, zoom: 13.0, pitch: 55, bearing: 0 },
  { p: 0.88, lng: -122.54, lat: 37.81, zoom: 13.0, pitch: 55, bearing: 70, linear: true },
  { p: 0.92, lng: -122.6, lat: 37.7, zoom: 8.0, pitch: 0, bearing: 0 },
  { p: 1, lng: -122.6, lat: 37.7, zoom: 8.0, pitch: 0, bearing: 0 },
];

export const DEFAULT_CAMERA: Camera = { lng: -122.6, lat: 37.7, zoom: 8, pitch: 0, bearing: 0 };

/** One representative frame per scene, for reduced motion: no camera travel between them. */
const REDUCED_FRAMES = [0.12, 0.29, 0.5, 0.66, 0.85, 0.91];
const SCENE_STARTS = [0, 0.16, 0.34, 0.54, 0.7, 0.88];

export function sceneIndex(p: number): number {
  let i = SCENE_STARTS.length - 1;
  while (i > 0 && p < SCENE_STARTS[i]) i--;
  return i;
}
export const reducedFrame = (p: number) => REDUCED_FRAMES[sceneIndex(p)];

export function cameraAt(p: number, isPhone: boolean, aspect: number, reduced = false): Camera {
  let i = 1;
  while (i < KEYS.length - 1 && p > KEYS[i].p) i++;
  const a = KEYS[i - 1];
  const b = KEYS[i];
  const raw = range(p, a.p, b.p);
  const t = b.linear ? raw : smoothstep(raw);

  // Interpolated in Mercator so the camera travels in a straight line on screen.
  const lngOf = (k: Key) => (isPhone && k.phoneLng !== undefined ? k.phoneLng : k.lng);
  const x = lerp(lngToMerc(lngOf(a)), lngToMerc(lngOf(b)), t);
  const y = lerp(latToMerc(a.lat), latToMerc(b.lat), t);

  let pitch = lerp(a.pitch, b.pitch, t);
  let bearing = lerp(a.bearing, b.bearing, t);
  // Without terrain MapLibre shows sky above about 60 degrees. Narrower windows see further up.
  pitch = Math.min(pitch, aspect < 1.5 ? 50 : 55);
  if (isPhone) {
    pitch = Math.min(pitch, 45);
    bearing = p >= 0.78 && p <= 0.88 ? (bearing * 40) / 70 : 0;
  }
  if (reduced && p >= 0.78 && p <= 0.88) bearing = 35;

  return { lng: mercToLng(x), lat: mercToLat(y), zoom: lerp(a.zoom, b.zoom, t), pitch, bearing };
}

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Where the lens frame rests in scene 3. Spec size is 720 by 440 at left 400, top 260 in a 1440 by 900 window. */
export function lensRest(vw: number, vh: number, isPhone: boolean): Rect {
  if (isPhone) {
    const w = Math.min(320, vw - 70);
    const h = Math.min(340, vh * 0.42);
    return { x: (vw - w) / 2, y: vh * 0.14, w, h };
  }
  const w = clamp(vw * 0.5, 420, 720);
  const h = clamp(vh * 0.49, 280, 440);
  return { x: vw * 0.278, y: vh * 0.289, w, h };
}

/** Lens frame during the sweep. The spec starts it at translateX -110 percent and it rests at p 0.46. */
export function lensSweepX(p: number, rest: Rect): number {
  return lerp(rest.x - rest.w * 1.1, rest.x, smoothstep(range(p, 0.38, 0.46)));
}

/** The padded map area: the console's viewport ticks, and where the frame ends up in the handoff. */
export function consoleFrame(vw: number, vh: number, railH: number, rightInset = 0): Rect {
  return { x: 8, y: 8, w: vw - rightInset - 16, h: vh - railH - 16 };
}

/** Text area of each scene, in pixels, which vessels and arcs must stay out of. */
export function copyRect(p: number, vw: number, vh: number, isPhone: boolean): Rect | null {
  const scene = sceneIndex(p);
  if (scene >= 5) return null;
  if (isPhone) return { x: 0, y: vh * 0.62, w: vw, h: vh * 0.38 };
  const heights = [200, 320, 240, 340, 160];
  const widths = [560, 580, 340, 560, 440];
  return { x: 64, y: 88, w: Math.min(widths[scene], vw * 0.45), h: heights[scene] };
}

