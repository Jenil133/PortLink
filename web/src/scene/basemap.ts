// CARTO dark matter recoloured for the chart table look. The style JSON is fetched and edited before
// the map sees it, so the original colours never flash on screen.
import type { StyleSpecification } from "maplibre-gl";
import { mapColors } from "../theme/colors";

const STYLE_URL = "https://basemaps.cartocdn.com/gl/dark-matter-gl-style/style.json";

/** Used when CARTO cannot be reached: a plain sea, so vessels still draw and the app stays usable. */
const OFFLINE_STYLE: StyleSpecification = {
  version: 8,
  sources: {},
  layers: [{ id: "background", type: "background", paint: { "background-color": mapColors.water } }],
};

const HIDDEN = /^(landcover|park_|landuse|boundary_county|water_shadow|waterway_label|watername_lake|poi_|roadname_|housenumber|place_hamlet|place_suburbs|place_villages|place_city_dot_r7|place_city_dot_z7|place_capital_dot_z7|road_path|tunnel_|aeroway|rail)/;
const ROAD_CASING = /_case/;
// Sea and bays only. Outlining every river and pond draws a bright inland network that competes with the fleet.
const COASTAL_WATER = ["any", ["==", ["get", "kind"], "ocean"], ["all", ["==", ["get", "kind"], "water"], ["!", ["has", "kind_detail"]]]];

type AnyLayer = {
  id: string;
  type: string;
  source?: string;
  minzoom?: number;
  layout?: Record<string, unknown>;
  paint?: Record<string, unknown>;
};

function recolor(style: StyleSpecification): StyleSpecification {
  const source = Object.keys(style.sources)[0];
  const layers: AnyLayer[] = [];

  for (const original of style.layers as unknown as AnyLayer[]) {
    const l: AnyLayer = { ...original, paint: { ...original.paint }, layout: { ...original.layout } };

    if (HIDDEN.test(l.id) || ROAD_CASING.test(l.id)) {
      l.layout!.visibility = "none";
    } else if (l.id === "background") {
      l.paint!["background-color"] = mapColors.land;
    } else if (l.id === "water") {
      l.paint!["fill-color"] = mapColors.water;
      l.paint!["fill-opacity"] = 1;
    } else if (l.id === "waterway") {
      l.paint!["line-color"] = mapColors.water;
    } else if (l.id === "building") {
      l.paint!["fill-color"] = mapColors.buildings;
      l.paint!["fill-opacity"] = 0.5;
      delete l.paint!["fill-antialias"];
    } else if (l.id === "building-top") {
      l.layout!.visibility = "none";
    } else if (l.type === "line" && /^(road_|bridge_)/.test(l.id)) {
      l.minzoom = Math.max(l.minzoom ?? 0, 11);
      l.paint!["line-color"] = mapColors.roads;
      l.paint!["line-opacity"] = 0.6;
    } else if (l.type === "line" && l.id.startsWith("boundary_")) {
      l.paint!["line-color"] = mapColors.boundaries;
      l.paint!["line-opacity"] = 0.6;
      l.paint!["line-dasharray"] = [2, 3];
    } else if (l.type === "symbol" && l.id.startsWith("watername_")) {
      l.paint!["text-color"] = mapColors.labelOnWater;
      l.paint!["text-halo-width"] = 0;
    } else if (l.type === "symbol") {
      if (l.id === "place_town") l.minzoom = 10;
      // Columns stand offshore on this coast, so city names sit to the east of their point, on land.
      if (l.id.startsWith("place_city")) {
        l.layout!["text-anchor"] = "left";
        l.layout!["text-offset"] = [0.6, 0];
        delete l.layout!["text-variable-anchor"];
      }
      if (l.id === "place_city_r6" || l.id === "place_city_r5") l.minzoom = 7;
      l.paint!["text-color"] = mapColors.labelOnLand;
      l.paint!["text-halo-color"] = mapColors.labelHalo;
      l.paint!["text-halo-width"] = 1;
      if ("icon-opacity" in l.paint!) l.paint!["icon-opacity"] = 0;
    }

    layers.push(l);

    if (l.id === "water") {
      // Shore band and coastline, drawn from the water polygons' edges. The grid sits between them and
      // the labels. Land is the style background rather than a polygon, so the grid cannot be clipped
      // to the sea; it runs across land too, as graticules do on printed charts.
      layers.push(
        {
          id: "shore-band",
          type: "line",
          source,
          "source-layer": "water",
          filter: COASTAL_WATER,
          paint: { "line-color": mapColors.buildings, "line-opacity": 0.55, "line-width": ["interpolate", ["linear"], ["zoom"], 4, 2, 8, 7, 13, 10] },
        } as unknown as AnyLayer,
        {
          id: "coastline",
          type: "line",
          source,
          "source-layer": "water",
          filter: COASTAL_WATER,
          paint: { "line-color": mapColors.coastline, "line-opacity": mapColors.coastlineOpacity, "line-width": 1 },
        } as unknown as AnyLayer,
        {
          id: "chart-grid",
          type: "line",
          source: "chart-grid",
          paint: { "line-color": "#1A5C6E", "line-opacity": 0.6, "line-width": 1 },
        },
      );
    }
  }

  return {
    ...style,
    sources: { ...style.sources, "chart-grid": { type: "geojson", data: { type: "FeatureCollection", features: [] } } },
    layers: layers as unknown as StyleSpecification["layers"],
  };
}

export async function loadBasemapStyle(): Promise<StyleSpecification> {
  try {
    const res = await fetch(STYLE_URL);
    if (!res.ok) throw new Error(`status ${res.status}`);
    return recolor((await res.json()) as StyleSpecification);
  } catch (err) {
    console.warn("Basemap unavailable, drawing a plain sea.", err);
    return OFFLINE_STYLE;
  }
}

/** Grid spacing in degrees for a zoom. Coarse on purpose: a chart grid, not graph paper. */
export function gridStep(zoom: number): number {
  if (zoom < 5) return 10;
  if (zoom < 6) return 5;
  if (zoom < 9) return 1;
  if (zoom < 11) return 0.25;
  return 0.05;
}

export function gridLines(step: number, bounds: [number, number, number, number]) {
  const [w, s, e, n] = bounds;
  const features: GeoJSON.Feature<GeoJSON.LineString>[] = [];
  const line = (coordinates: [number, number][]) => features.push({ type: "Feature", properties: {}, geometry: { type: "LineString", coordinates } });
  for (let x = Math.floor(w / step) * step; x <= e; x += step) line([[x, s], [x, n]]);
  for (let y = Math.floor(s / step) * step; y <= n; y += step) line([[w, y], [e, y]]);
  return { type: "FeatureCollection" as const, features };
}
