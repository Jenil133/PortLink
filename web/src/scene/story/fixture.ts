// Generated illustration data for the scroll story, on the same routes as the synthetic feed. The
// story never opens a subscription and every scene that shows it is labelled as an illustration.
import { D2R, latToMerc, lngToMerc, seeded } from "../math";

// [lat, lon], matching server/internal/feed/synth.go.
const ROUTES: [number, number][][] = [
  [[37.75, -122.7], [36.6, -122.2], [35.3, -121.3], [34.4, -120.7], [34.2, -119.8], [34.05, -119.2], [33.7, -118.4]],
  [[33.7, -118.25], [33.3, -117.8], [32.65, -117.3]],
  [[37.8, -122.75], [38.9, -123.9], [40.4, -124.6], [42, -124.7], [44, -124.5], [46.2, -124.4], [48.4, -124.9]],
  [[37.78, -122.6], [37.6, -123.5], [37, -126]],
];
const ROUTE_WEIGHTS = [0.42, 0.13, 0.33, 0.12];

const TYPES: [number, number][] = [[70, 0.4], [80, 0.3], [30, 0.2], [60, 0.1]];

export interface Fixture {
  count: number;
  lng: Float32Array;
  lat: Float32Array;
  /** Course over ground in degrees clockwise from north. */
  cog: Float32Array;
  aisType: Uint8Array;
  indices: number[];
  /** Web Mercator, for cheap screen space work. */
  mx: Float64Array;
  my: Float64Array;
  trails: { path: [number, number][]; timestamps: number[] }[];
  clusters: FixtureCluster[];
}

export interface FixtureCluster {
  lng: number;
  lat: number;
  count: number;
  /** 0 for the smallest column, 1 for the largest, so columns can rise in count order. */
  rank: number;
}

function pick<T>(weights: [T, number][] | { v: T; w: number }[], r: number): T {
  let acc = 0;
  for (const item of weights) {
    const [v, w] = Array.isArray(item) ? item : [item.v, item.w];
    acc += w;
    if (r < acc) return v;
  }
  const last = weights[weights.length - 1];
  return Array.isArray(last) ? last[0] : last.v;
}

function pointOnRoute(route: [number, number][], t: number) {
  const seg = Math.min(route.length - 2, Math.floor(t * (route.length - 1)));
  const f = t * (route.length - 1) - seg;
  const [aLat, aLon] = route[seg];
  const [bLat, bLon] = route[seg + 1];
  const cog = (Math.atan2((bLon - aLon) * Math.cos(aLat * D2R), bLat - aLat) / D2R + 360) % 360;
  return { lat: aLat + (bLat - aLat) * f, lon: aLon + (bLon - aLon) * f, cog };
}

export function buildFixture(count: number, trailCount: number): Fixture {
  const rand = seeded(42);
  const gauss = () => (rand() + rand() + rand()) / 1.5 - 1;

  const lng = new Float32Array(count);
  const lat = new Float32Array(count);
  const cog = new Float32Array(count);
  const aisType = new Uint8Array(count);
  const mx = new Float64Array(count);
  const my = new Float64Array(count);

  for (let i = 0; i < count; i++) {
    const routeIndex = pick(ROUTE_WEIGHTS.map((w, v) => ({ v, w })), rand());
    const p = pointOnRoute(ROUTES[routeIndex], rand());
    const opposing = rand() < 0.5;
    // Two lanes either side of the route, always pushed seaward. The Pacific route spreads north and south.
    const lane = opposing ? 0.09 : 0.26;
    const spread = lane + gauss() * 0.13;
    if (routeIndex === 3) {
      lat[i] = p.lat + (opposing ? spread : -spread);
      lng[i] = p.lon + gauss() * 0.12;
    } else {
      lng[i] = p.lon - Math.abs(spread);
      lat[i] = p.lat + gauss() * 0.06;
    }
    cog[i] = (p.cog + (opposing ? 180 : 0) + gauss() * 8 + 360) % 360;
    aisType[i] = pick(TYPES, rand());
    mx[i] = lngToMerc(lng[i]);
    my[i] = latToMerc(lat[i]);
  }

  // Wakes: forty minutes behind the vessel at twelve knots, about 15 km, on a subset of the fleet.
  const trails: Fixture["trails"] = [];
  const step = Math.max(1, Math.floor(count / trailCount));
  for (let i = 0; i < count && trails.length < trailCount; i += step) {
    const a = cog[i] * D2R;
    const dLat = (Math.cos(a) * 0.135) / 11;
    const dLng = (Math.sin(a) * 0.135) / 11 / Math.cos(lat[i] * D2R);
    const path: [number, number][] = [];
    const timestamps: number[] = [];
    for (let k = 0; k < 12; k++) {
      path.push([lng[i] - dLng * (11 - k), lat[i] - dLat * (11 - k)]);
      timestamps.push((k / 11) * 40);
    }
    trails.push({ path, timestamps });
  }

  return {
    count,
    lng,
    lat,
    cog,
    aisType,
    indices: Array.from({ length: count }, (_, i) => i),
    mx,
    my,
    trails,
    clusters: binClusters(lng, lat, 1.1),
  };
}

/** Square bins that imitate the server's H3 clusters for illustration. */
export function binClusters(lng: ArrayLike<number>, lat: ArrayLike<number>, cell: number): FixtureCluster[] {
  const bins = new Map<string, { sumLng: number; sumLat: number; count: number }>();
  for (let i = 0; i < lng.length; i++) {
    const key = `${Math.floor(lng[i] / cell)}:${Math.floor(lat[i] / cell)}`;
    const b = bins.get(key) ?? { sumLng: 0, sumLat: 0, count: 0 };
    b.sumLng += lng[i];
    b.sumLat += lat[i];
    b.count++;
    bins.set(key, b);
  }
  const clusters = [...bins.values()].map((b) => ({ lng: b.sumLng / b.count, lat: b.sumLat / b.count, count: b.count, rank: 0 }));
  clusters.sort((a, b) => a.count - b.count);
  clusters.forEach((c, i) => (c.rank = clusters.length > 1 ? i / (clusters.length - 1) : 1));
  return clusters;
}

/** The selected vessel of scenes 4 and 5: six hours up the coast and into the Golden Gate. */
export const STORY_TRACK: [number, number][] = [
  [-122.2, 36.5],
  [-122.3, 36.75],
  [-122.42, 37.0],
  [-122.56, 37.25],
  [-122.66, 37.5],
  [-122.72, 37.68],
  [-122.68, 37.78],
  [-122.6, 37.805],
  [-122.54, 37.81],
];
export const STORY_VESSEL = { lng: -122.54, lat: 37.81, cog: 84 };
