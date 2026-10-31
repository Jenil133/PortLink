export interface BBoxT {
  minLon: number;
  minLat: number;
  maxLon: number;
  maxLat: number;
}

/** A margin around the view, so panning shows vessels before the next frame arrives. */
export function padBBox(b: BBoxT, frac = 0.1): BBoxT {
  const dLon = (b.maxLon - b.minLon) * frac;
  const dLat = (b.maxLat - b.minLat) * frac;
  return {
    minLon: Math.max(-180, b.minLon - dLon),
    minLat: Math.max(-85, b.minLat - dLat),
    maxLon: Math.min(180, b.maxLon + dLon),
    maxLat: Math.min(85, b.maxLat + dLat),
  };
}
