// Procedural low poly hull from design spec v3, sheet 03. No external asset. Flat shaded, so each
// face has its own vertices. Hull units: bow faces +y, about 2 long, deck at z = 0.
type V = [number, number, number];

const OUTLINE: [number, number][] = [
  [0, 1],
  [0.32, 0.45],
  [0.36, -0.55],
  [0.26, -1],
  [-0.26, -1],
  [-0.36, -0.55],
  [-0.32, 0.45],
];
const KEEL_INSET = 0.6;
const DEPTH = 0.22;

function buildHull() {
  const positions: number[] = [];
  const normals: number[] = [];

  const tri = (a: V, b: V, c: V) => {
    const u = [b[0] - a[0], b[1] - a[1], b[2] - a[2]];
    const v = [c[0] - a[0], c[1] - a[1], c[2] - a[2]];
    let n: V = [u[1] * v[2] - u[2] * v[1], u[2] * v[0] - u[0] * v[2], u[0] * v[1] - u[1] * v[0]];
    const len = Math.hypot(...n) || 1;
    n = [n[0] / len, n[1] / len, n[2] / len];
    positions.push(...a, ...b, ...c);
    normals.push(...n, ...n, ...n);
  };
  const quad = (a: V, b: V, c: V, d: V) => {
    tri(a, b, c);
    tri(a, c, d);
  };

  const deck: V[] = OUTLINE.map(([x, y]) => [x, y, DEPTH]);
  const keel: V[] = OUTLINE.map(([x, y]) => [x * KEEL_INSET, y * KEEL_INSET, 0]);

  // Deck fan, hull sides. Outline runs clockwise seen from above, so winding is reversed for up normals.
  for (let i = 1; i < deck.length - 1; i++) tri(deck[0], deck[i + 1], deck[i]);
  for (let i = 0; i < deck.length; i++) {
    const j = (i + 1) % deck.length;
    quad(deck[i], deck[j], keel[j], keel[i]);
  }

  // Superstructure box, placed aft.
  const [x0, x1, y0, y1, z0, z1] = [-0.22, 0.22, -0.85, -0.45, DEPTH, DEPTH + 0.3];
  quad([x0, y0, z1], [x1, y0, z1], [x1, y1, z1], [x0, y1, z1]);
  quad([x0, y1, z0], [x0, y1, z1], [x1, y1, z1], [x1, y1, z0]);
  quad([x1, y0, z0], [x1, y1, z0], [x1, y1, z1], [x1, y0, z1]);
  quad([x0, y0, z0], [x1, y0, z0], [x1, y0, z1], [x0, y0, z1]);
  quad([x0, y1, z0], [x0, y0, z0], [x0, y0, z1], [x0, y1, z1]);

  return {
    positions: { size: 3, value: new Float32Array(positions) },
    normals: { size: 3, value: new Float32Array(normals) },
    texCoords: { size: 2, value: new Float32Array((positions.length / 3) * 2) },
  };
}

// deck.gl accepts plain attribute objects at runtime; its types only name its Mesh class.
export const HULL_MESH = buildHull() as never;

/** The deck outline as [x, y] hull units, for the ground shadow polygon. */
export const HULL_OUTLINE = OUTLINE;
/** Metres per hull unit along the length: a 60 m long vessel. */
export const HULL_LENGTH_UNITS = 2;
