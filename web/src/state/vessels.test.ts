import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { ClusterSchema, FrameSchema, VesselDeltaSchema } from "../gen/lens/v1/lens_pb";
import { Fleet } from "./vessels";

const delta = (mmsi: number, lat: number, lon: number) => create(VesselDeltaSchema, { mmsi, latE6: lat * 1e6, lonE6: lon * 1e6 });
const frame = (init: Parameters<typeof create<typeof FrameSchema>>[1]) => create(FrameSchema, init);

describe("Fleet", () => {
  it("adds vessels without gliding them in", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [delta(1, 37, -122)] }), true, 0);
    expect(f.count).toBe(1);
    expect(f.lat[f.indexOf(1)]).toBeCloseTo(37);
  });

  it("applies a delta to only the fields it carries", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [create(VesselDeltaSchema, { mmsi: 1, latE6: 37e6, lonE6: -122e6, name: "A", sogDkn: 123 })] }), true, 0);
    f.applyFrame(frame({ vessels: [create(VesselDeltaSchema, { mmsi: 1, sogDkn: 80 })] }), true, 10);
    const i = f.indexOf(1);
    expect(f.names[i]).toBe("A");
    expect(f.sog[i]).toBeCloseTo(8);
    expect(f.lat[i]).toBeCloseTo(37);
  });

  it("treats a latitude of zero as a real value", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [delta(1, 37, -122)] }), false, 0);
    f.applyFrame(frame({ vessels: [create(VesselDeltaSchema, { mmsi: 1, latE6: 0, lonE6: 0 })] }), false, 10);
    expect(f.lat[f.indexOf(1)]).toBe(0);
  });

  it("glides to a new position and lands on it", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [delta(1, 37, -122)] }), true, 0);
    f.applyFrame(frame({ vessels: [delta(1, 38, -122)] }), true, 500);
    const i = f.indexOf(1);
    expect(f.step(750)).toBe(true);
    expect(f.lat[i]).toBeCloseTo(37.5);
    f.step(5000);
    expect(f.lat[i]).toBeCloseTo(38);
    expect(f.step(6000)).toBe(false);
  });

  it("jumps instead of gliding when told not to animate", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [delta(1, 37, -122)] }), true, 0);
    f.applyFrame(frame({ vessels: [delta(1, 38, -122)] }), false, 500);
    expect(f.lat[f.indexOf(1)]).toBeCloseTo(38);
    expect(f.step(600)).toBe(false);
  });

  it("turns the short way round the compass", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [create(VesselDeltaSchema, { mmsi: 1, latE6: 37e6, lonE6: -122e6, cogDdeg: 3500 })] }), true, 0);
    f.applyFrame(frame({ vessels: [create(VesselDeltaSchema, { mmsi: 1, cogDdeg: 100 })] }), true, 100);
    f.step(350);
    expect(f.cog[f.indexOf(1)]).toBeCloseTo(0);
  });

  it("removes a vessel and keeps the others reachable", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [delta(1, 1, 1), delta(2, 2, 2), delta(3, 3, 3)] }), false, 0);
    f.applyFrame(frame({ removed: [1] }), false, 10);
    expect(f.count).toBe(2);
    expect(f.indexOf(1)).toBe(-1);
    expect(f.lat[f.indexOf(3)]).toBeCloseTo(3);
    expect(f.mmsi[f.indexOf(3)]).toBe(3);
    expect(f.data).toEqual([0, 1]);
  });

  it("clears everything on a snapshot", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [delta(1, 1, 1)] }), false, 0);
    f.applyFrame(frame({ snapshot: true, vessels: [delta(2, 2, 2)] }), false, 10);
    expect(f.indexOf(1)).toBe(-1);
    expect(f.count).toBe(1);
  });

  it("keeps the data array stable until membership changes", () => {
    const f = new Fleet();
    f.applyFrame(frame({ vessels: [delta(1, 1, 1)] }), false, 0);
    const before = f.data;
    f.applyFrame(frame({ vessels: [delta(1, 2, 2)] }), false, 10);
    expect(f.data).toBe(before);
  });

  it("holds clusters until a frame without them", () => {
    const f = new Fleet();
    f.applyFrame(frame({ snapshot: true, clustered: true, clusters: [create(ClusterSchema, { latE6: 37e6, lonE6: -122e6, count: 12 })] }), true, 0);
    expect(f.clusters).toEqual([{ lng: -122, lat: 37, count: 12 }]);
    f.applyFrame(frame({ vessels: [delta(1, 1, 1)] }), true, 10);
    expect(f.clusters).toBeNull();
  });

  it("grows past its initial capacity", () => {
    const f = new Fleet();
    const vessels = Array.from({ length: 5000 }, (_, i) => delta(i + 1, 1, 1));
    f.applyFrame(frame({ vessels }), false, 0);
    expect(f.count).toBe(5000);
    expect(f.mmsi[f.indexOf(5000)]).toBe(5000);
  });
});
