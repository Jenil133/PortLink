const int = new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 });

export const fmtInt = (v: number | null | undefined) => (v === null || v === undefined ? "--" : int.format(Math.round(v)));
export const fmt1 = (v: number | null | undefined) => (v === null || v === undefined ? "--" : v.toLocaleString("en-US", { minimumFractionDigits: 1, maximumFractionDigits: 1 }));
export const fmtKB = (bytes: number) => (bytes / 1000).toFixed(1);

export function typeWord(aisType: number): string {
  if (aisType >= 70 && aisType < 80) return "cargo";
  if (aisType >= 80 && aisType < 90) return "tanker";
  if (aisType === 30) return "fishing";
  if (aisType >= 60 && aisType < 70) return "passenger";
  return "other";
}

const pad = (n: number) => String(n).padStart(2, "0");

export const clockHM = (ms: number) => {
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
};
export const clockHMS = (ms: number) => {
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
};

const tzName = new Intl.DateTimeFormat("en-US", { timeZoneName: "short" }).formatToParts(new Date()).find((p) => p.type === "timeZoneName")?.value ?? "";

export const dateTime = (ms: number) => {
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${clockHMS(ms)} ${tzName}`;
};
export const timeTz = (ms: number) => `${clockHMS(ms)} ${tzName}`;

/** "-2h 14m" */
export function offsetLabel(ms: number): string {
  const totalMin = Math.max(0, Math.round(ms / 60000));
  return `-${Math.floor(totalMin / 60)}h ${pad(totalMin % 60)}m`;
}

export const fmtLng = (v: number, digits = 2) => `${Math.abs(v).toFixed(digits)}${v < 0 ? "W" : "E"}`;
export const fmtLat = (v: number, digits = 2) => `${Math.abs(v).toFixed(digits)}${v < 0 ? "S" : "N"}`;
