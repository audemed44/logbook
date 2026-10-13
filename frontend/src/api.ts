import type { Line, LinesPage, Overview, Session, Source } from "./types";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

/** Called when the session has expired, so the app can show the sign-in. */
let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: "same-origin", ...init });
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      message = (await res.json()).error ?? message;
    } catch {
      // not JSON
    }
    if (res.status === 401 && !path.startsWith("/api/session")) onUnauthorized();
    throw new ApiError(message, res.status);
  }
  if (res.status === 204) return undefined as T;
  const type = res.headers.get("Content-Type") ?? "";
  return (type.includes("json") ? res.json() : res.text()) as Promise<T>;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const api = {
  session: () => request<Session>("/api/session"),
  login: (token: string) => request<Session>("/api/session", json("POST", { token })),
  logout: () => request<void>("/api/session", { method: "DELETE" }),

  lines: (query: string) => request<LinesPage>(`/api/lines?${query}`),
  around: (id: number) => request<Line[]>(`/api/lines/${id}/around`),
  sources: () => request<Source[]>("/api/sources"),
  overview: () => request<Overview>("/api/overview"),
};

/** The live tail's address for a filter query. */
export const tailURL = (query: string) => `/api/tail?${query}`;
