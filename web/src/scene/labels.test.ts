import { WebMercatorViewport } from "@deck.gl/core";
import { describe, expect, it } from "vitest";
import { fitLabels, type LabelViewport } from "./labels";

type C = { id: string; x: number; y: number; count: number };
// A flat screen where x and y are already pixels.
const flat: LabelViewport = { width: 1000, height: 800, project: ([x, y]) => [x, y] };
const fit = (items: C[], prev?: C[]) =>
  fitLabels(items, flat, (c) => [c.x, c.y, 0], (c) => String(c.count), (c) => c.count, 0, prev);
const ids = (cs: C[]) => cs.map((c) => c.id);

describe("fitLabels", () => {
  it("keeps the larger count when two labels overlap", () => {
    expect(ids(fit([{ id: "small", x: 100, y: 100, count: 9 }, { id: "big", x: 110, y: 104, count: 450 }]))).toEqual(["big"]);
  });

  it("keeps both when they are clear of each other, in their original order", () => {
    const items = [{ id: "a", x: 100, y: 100, count: 9 }, { id: "b", x: 300, y: 100, count: 450 }];
    expect(ids(fit(items))).toEqual(["a", "b"]);
  });

  it("measures by text width: four digits reach further than one", () => {
    // 20 px apart: two single digits fit, a four digit count does not.
    expect(fit([{ id: "a", x: 100, y: 100, count: 1 }, { id: "b", x: 120, y: 100, count: 2 }])).toHaveLength(2);
    expect(fit([{ id: "a", x: 100, y: 100, count: 1 }, { id: "b", x: 120, y: 100, count: 2000 }])).toHaveLength(1);
  });

  it("lets a dropped label free no room, so a chain keeps every other one", () => {
    const chain = [0, 1, 2].map((i) => ({ id: `c${i}`, x: 100 + i * 16, y: 100, count: 10 - i }));
    expect(ids(fit(chain))).toEqual(["c0", "c2"]);
  });

  it("ignores labels off screen, so they never block one that is on screen", () => {
    const items = [{ id: "off", x: -40, y: 100, count: 999 }, { id: "on", x: 8, y: 100, count: 1 }];
    expect(ids(fit(items))).toEqual(["on"]);
  });

  it("hands back the previous array when nothing changed", () => {
    const items = [{ id: "a", x: 100, y: 100, count: 9 }, { id: "b", x: 300, y: 100, count: 450 }];
    const first = fit(items);
    expect(fit(items, first)).toBe(first);
    expect(fit([items[0]], first)).not.toBe(first);
  });

  it("separates columns once the map zooms in", () => {
    // Two clusters 0.4 degrees apart off San Francisco.
    const cs = [{ id: "a", x: -122.8, y: 37.6, count: 120 }, { id: "b", x: -122.4, y: 37.6, count: 80 }];
    const at = (zoom: number) =>
      fitLabels(cs, new WebMercatorViewport({ width: 1200, height: 800, longitude: -122.6, latitude: 37.6, zoom, pitch: 40 }), (c) => [c.x, c.y, 0], (c) => String(c.count), (c) => c.count, -16);
    expect(ids(at(4.4))).toEqual(["a"]);
    expect(ids(at(6))).toEqual(["a", "b"]);
  });
});
