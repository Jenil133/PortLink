export const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));
export const lerp = (a: number, b: number, t: number) => a + (b - a) * t;

/** 0 to 1 as v moves from a to b, clamped. */
export const range = (v: number, a: number, b: number) => clamp((v - a) / (b - a), 0, 1);
export const smoothstep = (t: number) => t * t * (3 - 2 * t);
/** range() with gentle start and end. */
export const srange = (v: number, a: number, b: number) => smoothstep(range(v, a, b));

export const D2R = Math.PI / 180;

/** Metres per screen pixel at a zoom and latitude, for MapLibre's 512 px world. */
export const metersPerPixel = (zoom: number, lat: number) => (78271.517 * Math.cos(lat * D2R)) / 2 ** zoom;

export const lngToMerc = (lng: number) => (lng + 180) / 360;
export const latToMerc = (lat: number) => {
  const s = Math.sin(lat * D2R);
  return 0.5 - Math.log((1 + s) / (1 - s)) / (4 * Math.PI);
};
export const mercToLng = (x: number) => x * 360 - 180;
export const mercToLat = (y: number) => (Math.atan(Math.sinh(Math.PI * (1 - 2 * y))) * 180) / Math.PI;

/** Seeded generator, so the fixture is the same on every load. */
export function seeded(seed: number) {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
