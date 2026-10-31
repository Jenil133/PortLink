// Canvas colours for deck.gl and MapLibre, from design spec v3, sheet 02.
// CSS variables cannot reach WebGL, so these mirror tokens.css.
export type RGBA = [number, number, number, number];

export const PEARL: RGBA = [242, 240, 233, 255];
export const LIME: RGBA = [185, 241, 47, 255];
export const FLEET: RGBA = [220, 230, 226, 255];

export const deckColors = {
  vessel: FLEET,
  vesselDimmed: [220, 230, 226, 64] as RGBA,
  spriteOutline: [5, 33, 43, 230] as RGBA,
  selectedLive: LIME,
  selectedRest: PEARL,
  hoverRing: [242, 240, 233, 200] as RGBA,
  headingLine: PEARL,
  trackRibbon: [242, 240, 233, 170] as RGBA,
  trackAhead: [242, 240, 233, 60] as RGBA,
  trackShadow: [5, 33, 43, 130] as RGBA,
  trackLiveEnd: LIME,
  clusterBody: [38, 112, 132, 255] as RGBA,
  clusterCapLive: LIME,
  clusterCapRest: PEARL,
  clusterLabel: PEARL,
  clusterLabelOutline: [5, 33, 43, 255] as RGBA,
  clusterShadow: [5, 33, 43, 120] as RGBA,
  hullShadow: [5, 33, 43, 140] as RGBA,
  grid: [26, 92, 110, 150] as RGBA,
  storyArc: [220, 230, 226, 60] as RGBA,
  storyArcLens: [185, 241, 47, 150] as RGBA,
  storyWake: [220, 230, 226, 140] as RGBA,
};

export const mapColors = {
  water: "#0A3A47",
  land: "#2F423D",
  coastline: "#F2F0E9",
  coastlineOpacity: 0.3,
  buildings: "#3E524C",
  roads: "#263834",
  boundaries: "#5C6F69",
  labelOnLand: "#C9D4D6",
  labelOnWater: "#8FB0B9",
  labelHalo: "#05212B",
};
