// The vessels on screen, in typed arrays read directly by the render loop. Nothing here passes through
// React or the store. Deltas are relative to what this holds, so every frame is applied in order.
import type { Frame } from "../gen/lens/v1/lens_pb";

export interface ClusterPoint {
  lng: number;
  lat: number;
  count: number;
}

const INITIAL_CAPACITY = 4096;

export class Fleet {
  capacity = INITIAL_CAPACITY;
  count = 0;

  lng = new Float32Array(this.capacity);
  lat = new Float32Array(this.capacity);
  cog = new Float32Array(this.capacity);
  heading = new Float32Array(this.capacity);
  aisType = new Uint8Array(this.capacity);
  mmsi = new Uint32Array(this.capacity);
  sog = new Float32Array(this.capacity);
  rpm = new Float32Array(this.capacity).fill(NaN);
  fuelRate = new Float32Array(this.capacity).fill(NaN);
  temp = new Float32Array(this.capacity).fill(NaN);
  level = new Float32Array(this.capacity).fill(NaN);
  names: string[] = [];

  private fromLng = new Float32Array(this.capacity);
  private fromLat = new Float32Array(this.capacity);
  private toLng = new Float32Array(this.capacity);
  private toLat = new Float32Array(this.capacity);
  private fromCog = new Float32Array(this.capacity);
  private toCog = new Float32Array(this.capacity);
  private t0 = new Float64Array(this.capacity);
  private dur = new Float32Array(this.capacity);
  private lastPosAt = new Float64Array(this.capacity);
  private inFlight = new Set<number>();

  private indexOfMmsi = new Map<number, number>();

  /** Stable until membership changes, so deck.gl only rebuilds every attribute when it has to. */
  data: number[] = [];
  /** Bumped whenever a position or course changes. */
  version = 0;
  memberVersion = 0;
  clusters: ClusterPoint[] | null = null;
  /** Set when anything changed; the render loop clears it. */
  dirty = false;

  indexOf(mmsi: number) {
    return this.indexOfMmsi.get(mmsi) ?? -1;
  }

  clear() {
    this.count = 0;
    this.indexOfMmsi.clear();
    this.inFlight.clear();
    this.names.length = 0;
    this.data = [];
    this.memberVersion++;
    this.version++;
    this.clusters = null;
    this.dirty = true;
  }

  applyFrame(f: Frame, animate: boolean, now = performance.now()) {
    if (f.snapshot) this.clear();

    if (f.clustered) {
      this.clusters = f.clusters.map((c) => ({ lng: c.lonE6 / 1e6, lat: c.latE6 / 1e6, count: c.count }));
      this.dirty = true;
    } else if (this.clusters) {
      this.clusters = null;
      this.dirty = true;
    }

    let membershipChanged = false;

    for (const d of f.vessels) {
      let i = this.indexOfMmsi.get(d.mmsi);
      const isNew = i === undefined;
      if (i === undefined) {
        i = this.add(d.mmsi, now);
        membershipChanged = true;
      }

      if (d.cogDdeg !== undefined) {
        const cog = d.cogDdeg / 10;
        if (isNew || !animate) {
          this.cog[i] = this.fromCog[i] = this.toCog[i] = cog;
        } else {
          this.fromCog[i] = this.cog[i];
          this.toCog[i] = cog;
          // A course change with no position change still needs a glide window.
          if (this.dur[i] === 0) {
            this.t0[i] = now;
            this.dur[i] = 500;
            this.inFlight.add(i);
          }
        }
      }

      if (d.latE6 !== undefined || d.lonE6 !== undefined) {
        const lat = d.latE6 !== undefined ? d.latE6 / 1e6 : this.toLat[i];
        const lng = d.lonE6 !== undefined ? d.lonE6 / 1e6 : this.toLng[i];
        if (isNew || !animate) {
          this.lat[i] = this.fromLat[i] = this.toLat[i] = lat;
          this.lng[i] = this.fromLng[i] = this.toLng[i] = lng;
          this.dur[i] = 0;
          this.inFlight.delete(i);
        } else {
          // Glide from where the vessel is drawn now, over about the time since its last update.
          this.fromLat[i] = this.lat[i];
          this.fromLng[i] = this.lng[i];
          this.toLat[i] = lat;
          this.toLng[i] = lng;
          this.dur[i] = Math.min(1000, Math.max(100, now - this.lastPosAt[i]));
          this.t0[i] = now;
          this.inFlight.add(i);
        }
        this.lastPosAt[i] = now;
        this.version++;
        this.dirty = true;
      }

      if (d.sogDkn !== undefined) this.sog[i] = d.sogDkn / 10;
      if (d.headingDeg !== undefined) this.heading[i] = d.headingDeg;
      if (d.name !== undefined) this.names[i] = d.name;
      if (d.vesselType !== undefined) {
        this.aisType[i] = d.vesselType;
        membershipChanged = true;
      }
      if (d.engineRpm !== undefined) this.rpm[i] = d.engineRpm;
      if (d.fuelRateDlph !== undefined) this.fuelRate[i] = d.fuelRateDlph / 10;
      if (d.engineTempDc !== undefined) this.temp[i] = d.engineTempDc / 10;
      if (d.fuelLevelDpct !== undefined) this.level[i] = d.fuelLevelDpct / 10;
    }

    for (const mmsi of f.removed) {
      if (this.remove(mmsi)) membershipChanged = true;
    }

    if (membershipChanged) this.syncData();
  }

  /** Advances every glide. Returns true if anything moved. */
  step(now: number): boolean {
    if (this.inFlight.size === 0) return false;
    for (const i of this.inFlight) {
      const k = Math.min(1, (now - this.t0[i]) / this.dur[i]);
      this.lat[i] = this.fromLat[i] + (this.toLat[i] - this.fromLat[i]) * k;
      this.lng[i] = this.fromLng[i] + (this.toLng[i] - this.fromLng[i]) * k;
      // Shortest way round the compass.
      const delta = ((this.toCog[i] - this.fromCog[i] + 540) % 360) - 180;
      this.cog[i] = (this.fromCog[i] + delta * k + 360) % 360;
      if (k >= 1) {
        this.dur[i] = 0;
        this.fromLat[i] = this.toLat[i];
        this.fromLng[i] = this.toLng[i];
        this.fromCog[i] = this.toCog[i];
        this.inFlight.delete(i);
      }
    }
    this.version++;
    return true;
  }

  private add(mmsi: number, now: number): number {
    if (this.count === this.capacity) this.grow();
    const i = this.count++;
    this.indexOfMmsi.set(mmsi, i);
    this.mmsi[i] = mmsi;
    this.names[i] = "";
    this.sog[i] = 0;
    this.cog[i] = this.fromCog[i] = this.toCog[i] = 0;
    this.heading[i] = 0;
    this.aisType[i] = 0;
    this.rpm[i] = this.fuelRate[i] = this.temp[i] = this.level[i] = NaN;
    this.dur[i] = 0;
    this.lastPosAt[i] = now;
    return i;
  }

  private remove(mmsi: number): boolean {
    const i = this.indexOfMmsi.get(mmsi);
    if (i === undefined) return false;
    const last = --this.count;
    this.indexOfMmsi.delete(mmsi);
    this.inFlight.delete(i);
    if (i !== last) {
      // Swap the last vessel into the hole, so the arrays stay dense.
      for (const arr of [this.lng, this.lat, this.cog, this.heading, this.aisType, this.mmsi, this.sog, this.rpm, this.fuelRate, this.temp, this.level, this.fromLng, this.fromLat, this.toLng, this.toLat, this.fromCog, this.toCog, this.t0, this.dur, this.lastPosAt]) {
        arr[i] = arr[last];
      }
      this.names[i] = this.names[last];
      this.indexOfMmsi.set(this.mmsi[i], i);
      if (this.inFlight.delete(last)) this.inFlight.add(i);
    }
    this.names.length = last;
    return true;
  }

  private syncData() {
    this.data = Array.from({ length: this.count }, (_, i) => i);
    this.memberVersion++;
    this.version++;
    this.dirty = true;
  }

  private grow() {
    const cap = this.capacity * 2;
    const grow32 = (a: Float32Array) => {
      const n = new Float32Array(cap);
      n.set(a);
      return n;
    };
    const growNaN = (a: Float32Array) => {
      const n = new Float32Array(cap).fill(NaN);
      n.set(a);
      return n;
    };
    this.lng = grow32(this.lng);
    this.lat = grow32(this.lat);
    this.cog = grow32(this.cog);
    this.heading = grow32(this.heading);
    this.sog = grow32(this.sog);
    this.fromLng = grow32(this.fromLng);
    this.fromLat = grow32(this.fromLat);
    this.toLng = grow32(this.toLng);
    this.toLat = grow32(this.toLat);
    this.fromCog = grow32(this.fromCog);
    this.toCog = grow32(this.toCog);
    this.dur = grow32(this.dur);
    this.rpm = growNaN(this.rpm);
    this.fuelRate = growNaN(this.fuelRate);
    this.temp = growNaN(this.temp);
    this.level = growNaN(this.level);
    const aisType = new Uint8Array(cap);
    aisType.set(this.aisType);
    this.aisType = aisType;
    const mmsi = new Uint32Array(cap);
    mmsi.set(this.mmsi);
    this.mmsi = mmsi;
    const t0 = new Float64Array(cap);
    t0.set(this.t0);
    this.t0 = t0;
    const lastPosAt = new Float64Array(cap);
    lastPosAt.set(this.lastPosAt);
    this.lastPosAt = lastPosAt;
    this.capacity = cap;
  }
}

/** The one fleet on screen. */
export const fleet = new Fleet();
