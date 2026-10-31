// Shared layer settings.
export const FLAT = {
  // Flat overlays never fight the basemap's depth buffer.
  parameters: { depthCompare: "always", depthWriteEnabled: false },
} as const;

export const COLUMN_MATERIAL = { ambient: 0.7, diffuse: 0.75, shininess: 8, specularColor: [30, 50, 60] as [number, number, number] };
export const HULL_MATERIAL = { ambient: 0.45, diffuse: 0.8, shininess: 12, specularColor: [60, 80, 90] as [number, number, number] };

/** Column height from design spec v3, tuned so counts read as visibly different heights. */
export function columnHeightPx(count: number, spacingPx: number): number {
  const px = 16 + 72 * (Math.log(1 + count) / Math.log(501));
  return Math.min(px, Math.max(72, 2.4 * spacingPx));
}

/** Column radius from design spec v3, sheet 03. Res 2 below zoom 4, res 3 from there. */
export function clusterSpacingPx(zoom: number): number {
  return zoom < 4 ? 70 * 2 ** (zoom - 4) : 27 * 2 ** (zoom - 4);
}
export function columnRadiusPx(zoom: number): number {
  return Math.max(6, Math.min(0.3 * clusterSpacingPx(zoom), 15));
}
