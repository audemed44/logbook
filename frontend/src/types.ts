export interface Session {
  authenticated: boolean;
  /** Foyer, the homelab's start page (HOMEPAGE_URL). */
  foyer_url?: string;
}

/** One stored log line. level uses slog's numbers: -4 debug, 0 info, 4 warn, 8 error. */
export interface Line {
  id: number;
  time: string;
  source: string;
  level: number;
  kind?: string;
  msg: string;
  trace?: string;
  user?: string;
  err?: string;
  dur_ms?: number;
  /** A JSON object with the line's other fields. */
  attrs?: string;
  stderr?: boolean;
}

export interface LinesPage {
  lines: Line[];
  /** Cursor for older lines; "" at the end. */
  next: string;
}

export interface Bucket {
  hour: string;
  lines: number;
  warns: number;
  errors: number;
  dropped: number;
}

export interface Problem {
  source: string;
  level: number;
  msg: string;
  count: number;
  last: string;
  last_id: number;
}

export interface Limits {
  max_bytes: number;
  retention_days: number;
  daily_quota: number;
  per_minute: number;
  per_message: number;
  max_line: number;
  burst_errors: number;
  notify: boolean;
  heartbeat: boolean;
  excluded?: string;
}

export interface Overview {
  usage: { bytes: number; lines: number; oldest?: string };
  file_bytes: number;
  limits: Limits;
  hours: Bucket[];
  problems: Problem[];
  upkeep?: { at: string; pruned: { Expired: number; ForSize: number }; err?: string };
}

export interface Source {
  name: string;
  image?: string;
  status: "following" | "stopped" | "excluded" | "gone";
  first_seen: string;
  last_line?: string;
  lines_24h: number;
  warns_24h: number;
  errors_24h: number;
  dropped_24h: number;
  today: number;
  stored: number;
}
