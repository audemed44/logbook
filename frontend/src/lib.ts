export type Tone = "good" | "warn" | "bad" | "accent" | "";

export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const s = Math.max(0, (now - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 48 * 3600) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

export const LEVELS = [
  { value: "", label: "All" },
  { value: "info", label: "Info+" },
  { value: "warn", label: "Warn+" },
  { value: "error", label: "Errors" },
];

export function levelName(level: number): "debug" | "info" | "warn" | "error" {
  if (level >= 8) return "error";
  if (level >= 4) return "warn";
  if (level >= 0) return "info";
  return "debug";
}

export function levelTone(level: number): Tone {
  if (level >= 8) return "bad";
  if (level >= 4) return "warn";
  return "";
}

/** A line's time: the clock today, the date before that. */
export function clock(iso: string, now = new Date()): string {
  const d = new Date(iso);
  const time = d.toLocaleTimeString("en-GB", { hour12: false });
  if (d.toDateString() === now.toDateString()) return time;
  const date = d.toLocaleDateString("en-GB", { day: "numeric", month: "short" });
  return `${date} ${time}`;
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

export function count(n: number): string {
  if (n < 10_000) return n.toLocaleString("en-GB");
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}

export function duration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
  const m = Math.floor(ms / 60_000);
  return `${m}m ${Math.round((ms % 60_000) / 1000)}s`;
}

/** The fields of a line's attrs, flattened to strings. */
export function fields(attrs?: string): [string, string][] {
  if (!attrs) return [];
  try {
    const obj = JSON.parse(attrs) as Record<string, unknown>;
    return Object.entries(obj).map(([k, v]) => [k, typeof v === "string" ? v : JSON.stringify(v)]);
  } catch {
    return [["attrs", attrs]];
  }
}

/** Words to search for a message: no numbers, at most six. */
export function searchWords(msg: string): string {
  return msg
    .split(/[^\p{L}]+/u)
    .filter((w) => w.length > 1)
    .slice(0, 6)
    .join(" ");
}
