import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { Metric, MetricSeriesSchema, SeriesSchema, Tier, TrackPointSchema } from "../gen/lens/v1/lens_pb";
import { padBBox } from "../lib/geo";
import { seriesToData, trackFraction } from "./client";

describe("padBBox", () => {
  it("adds a margin and stays inside the world", () => {
    expect(padBBox({ minLon: -10, minLat: 0, maxLon: 10, maxLat: 10 })).toEqual({ minLon: -12, minLat: -1, maxLon: 12, maxLat: 11 });
    expect(padBBox({ minLon: -180, minLat: -85, maxLon: 180, maxLat: 85 })).toEqual({ minLon: -180, minLat: -85, maxLon: 180, maxLat: 85 });
  });
});

describe("trackFraction", () => {
  it("finds the cursor along a time sorted track", () => {
    expect(trackFraction([0, 10, 20, 30, 40], 20)).toBeCloseTo(0.5);
    expect(trackFraction([0, 10], 100)).toBe(1);
    expect(trackFraction([], 5)).toBe(1);
  });
});

describe("seriesToData", () => {
  it("converts offsets to times and finds the range", () => {
    const { data, track, trackTimes } = seriesToData(
      create(SeriesSchema, {
        mmsi: 1,
        startMs: 1000n,
        endMs: 5000n,
        tier: Tier.TIER_1S,
        rawPoints: 2160,
        metrics: [create(MetricSeriesSchema, { metric: Metric.SOG, tOffsetS: [0, 2], values: [8, 12] })],
        track: [create(TrackPointSchema, { tOffsetS: 1, latE6: 37e6, lonE6: -122e6 })],
      }),
    );
    expect(data?.tier).toBe("1s");
    expect(data?.rawPoints).toBe(2160);
    expect(data?.metrics[0]).toMatchObject({ key: "sog", times: [1000, 3000], min: 8, max: 12 });
    expect(track).toEqual([[-122, 37]]);
    expect(trackTimes).toEqual([2000]);
  });

  it("reports no series when the vessel has no history", () => {
    expect(seriesToData(create(SeriesSchema, { mmsi: 1, rawPoints: 0 })).data).toBeNull();
  });
});
