// Sprite atlas for the single vessel IconLayer. Sprites are drawn in their final colour because a
// tinted mask would also tint the dark outline that keeps overlapping arrows readable.
const CELL = 64;

export type SpriteShape = "arrow" | "cargo" | "tanker" | "fishing" | "passenger";
export type SpriteTint = "fleet" | "lime" | "pearl";

const SHAPES: Record<SpriteShape, (c: CanvasRenderingContext2D) => void> = {
  // Six wide at thirteen tall. Slim so lanes stay readable and heading reads at 10 px.
  arrow(c) {
    c.moveTo(0, -27);
    c.lineTo(12, 25);
    c.lineTo(0, 16);
    c.lineTo(-12, 25);
  },
  // Pointed bow on every silhouette, so heading never depends on the vessel type.
  cargo(c) {
    c.moveTo(0, -27);
    c.lineTo(14, -10);
    c.lineTo(14, 25);
    c.lineTo(-14, 25);
    c.lineTo(-14, -10);
  },
  tanker(c) {
    c.moveTo(0, -27);
    c.lineTo(18, -8);
    c.lineTo(18, 12);
    c.quadraticCurveTo(18, 26, 0, 26);
    c.quadraticCurveTo(-18, 26, -18, 12);
    c.lineTo(-18, -8);
  },
  fishing(c) {
    c.moveTo(0, -27);
    c.lineTo(16, 2);
    c.lineTo(14, 25);
    c.lineTo(0, 14);
    c.lineTo(-14, 25);
    c.lineTo(-16, 2);
  },
  passenger(c) {
    c.moveTo(0, -27);
    c.lineTo(10, -12);
    c.lineTo(10, 16);
    c.quadraticCurveTo(10, 26, 0, 26);
    c.quadraticCurveTo(-10, 26, -10, 16);
    c.lineTo(-10, -12);
  },
};

const TINTS: Record<SpriteTint, string> = { fleet: "#DCE6E2", lime: "#B9F12F", pearl: "#F2F0E9" };

export const spriteName = (shape: SpriteShape, tint: SpriteTint) => `${shape}-${tint}`;

export function shapeForType(aisType: number): SpriteShape {
  if (aisType >= 70 && aisType < 80) return "cargo";
  if (aisType >= 80 && aisType < 90) return "tanker";
  if (aisType === 30) return "fishing";
  if (aisType >= 60 && aisType < 70) return "passenger";
  return "arrow";
}

export type IconMapping = Record<string, { x: number; y: number; width: number; height: number; anchorY: number; mask: boolean }>;

let cached: { url: string; mapping: IconMapping } | null = null;

/** Built once. The vessel layer and the silhouette legend share it. */
export function getAtlas(): { url: string; mapping: IconMapping } {
  if (cached) return cached;
  const shapes = Object.keys(SHAPES) as SpriteShape[];
  const tints = Object.keys(TINTS) as SpriteTint[];
  const canvas = document.createElement("canvas");
  canvas.width = CELL * shapes.length;
  canvas.height = CELL * tints.length;
  const c = canvas.getContext("2d")!;
  const mapping: IconMapping = {};

  tints.forEach((tint, row) => {
    shapes.forEach((shape, col) => {
      const x = col * CELL;
      const y = row * CELL;
      mapping[spriteName(shape, tint)] = { x, y, width: CELL, height: CELL, anchorY: CELL / 2, mask: false };

      c.save();
      c.translate(x + CELL / 2, y + CELL / 2);
      c.lineJoin = "round";
      c.beginPath();
      SHAPES[shape](c);
      c.closePath();
      c.shadowColor = "rgba(5, 33, 43, 0.35)";
      c.shadowBlur = 8;
      c.strokeStyle = "rgba(5, 33, 43, 0.9)";
      c.lineWidth = 6;
      c.stroke();
      c.shadowColor = "transparent";
      c.fillStyle = TINTS[tint];
      c.fill();
      c.restore();
    });
  });
  cached = { url: canvas.toDataURL("image/png"), mapping };
  return cached;
}
