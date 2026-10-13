import { ChevronDown, Pause, Play, Search, X } from "lucide-preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { api, tailURL } from "../api";
import { useData } from "../hooks";
import { LEVELS } from "../lib";
import { navigate } from "../router";
import type { Line } from "../types";
import { LineList } from "./LineList";
import { Empty, ErrorNote } from "./ui";

const SINCE = [
  { value: "15m", label: "15 min" },
  { value: "1h", label: "1 hour" },
  { value: "6h", label: "6 hours" },
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "", label: "All" },
];

const KINDS = ["", "request", "job", "audit", "docker", "suppressed", "limit"];

/** Lines kept on screen while following live. */
const MAX_LIVE = 1000;

export function LogsPage(props: { query: URLSearchParams }) {
  const query = props.query;
  const qs = query.toString();
  const [lines, setLines] = useState<Line[] | null>(null);
  const [next, setNext] = useState("");
  const [error, setError] = useState("");
  const [loadingMore, setLoadingMore] = useState(false);
  const [live, setLive] = useState(false);
  const [search, setSearch] = useState(query.get("q") ?? "");
  const { data: sources } = useData(api.sources);

  const set = (key: string, value: string) => {
    const q = new URLSearchParams(query);
    if (value) q.set(key, value);
    else q.delete(key);
    navigate(`/logs${q.size ? `?${q}` : ""}`, true);
  };

  useEffect(() => setSearch(query.get("q") ?? ""), [qs]);

  useEffect(() => {
    let stale = false;
    setLines(null);
    setError("");
    api
      .lines(qs)
      .then((p) => {
        if (stale) return;
        setLines(p.lines);
        setNext(p.next);
      })
      .catch((e: Error) => !stale && setError(e.message));
    return () => {
      stale = true;
    };
  }, [qs]);

  // Live: new lines from the server as they're stored, newest on top.
  const linesRef = useRef(lines);
  linesRef.current = lines;
  useEffect(() => {
    if (!live) return;
    const tail = new URLSearchParams(query);
    tail.delete("since");
    tail.delete("until");
    tail.delete("before");
    const es = new EventSource(tailURL(tail.toString()));
    es.onmessage = (e) => {
      const l = JSON.parse(e.data) as Line;
      setLines((cur) => [l, ...(cur ?? [])].slice(0, MAX_LIVE));
    };
    es.addEventListener("missed", () => {
      api.lines(qs).then((p) => setLines(p.lines));
    });
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) setLive(false);
    };
    return () => es.close();
  }, [live, qs]);

  const more = async () => {
    setLoadingMore(true);
    try {
      const q = new URLSearchParams(query);
      q.set("before", next);
      const p = await api.lines(q.toString());
      setLines((cur) => [...(cur ?? []), ...p.lines]);
      setNext(p.next);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoadingMore(false);
    }
  };

  const names = useMemo(
    () => (sources ?? []).filter((s) => s.status !== "excluded").map((s) => s.name),
    [sources],
  );
  const level = query.get("level") ?? "";
  const filtered = [...query.keys()].some((k) => k !== "since");

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">
          {query.get("trace") ? `Trace ${query.get("trace")}` : "Search"}
        </div>
        <h1 class="page-title page-title-small">Logs</h1>
      </header>

      <div class="filters">
        <form
          class="search"
          onSubmit={(e) => {
            e.preventDefault();
            set("q", search.trim());
          }}
        >
          <Search size={15} />
          <input
            class="input"
            type="search"
            placeholder="Search messages, errors and fields"
            value={search}
            onInput={(e) => setSearch(e.currentTarget.value)}
          />
        </form>
        <div class="filter-row">
          <label class="select">
            <select
              value={query.get("source") ?? ""}
              onChange={(e) => set("source", e.currentTarget.value)}
            >
              <option value="">All containers</option>
              {names.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
              {query.get("source") && !names.includes(query.get("source")!) && (
                <option value={query.get("source")!}>{query.get("source")}</option>
              )}
            </select>
            <ChevronDown size={14} />
          </label>
          <div class="seg" role="group" aria-label="Level">
            {LEVELS.map((l) => (
              <button
                key={l.value}
                type="button"
                class={level === l.value ? "active" : ""}
                onClick={() => set("level", l.value)}
              >
                {l.label}
              </button>
            ))}
          </div>
          <label class="select">
            <select
              value={query.get("since") ?? ""}
              onChange={(e) => set("since", e.currentTarget.value)}
            >
              {SINCE.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.value ? `Last ${s.label}` : "Any time"}
                </option>
              ))}
            </select>
            <ChevronDown size={14} />
          </label>
          <label class="select">
            <select
              value={query.get("kind") ?? ""}
              onChange={(e) => set("kind", e.currentTarget.value)}
            >
              {KINDS.map((k) => (
                <option key={k} value={k}>
                  {k ? k[0].toUpperCase() + k.slice(1) + "s" : "Every kind"}
                </option>
              ))}
            </select>
            <ChevronDown size={14} />
          </label>
          <span class="spacer" />
          {filtered && (
            <button class="btn btn-ghost btn-small" onClick={() => navigate("/logs", true)}>
              <X size={14} /> Clear
            </button>
          )}
          <button
            class={`btn btn-small ${live ? "btn-primary" : ""}`}
            onClick={() => setLive(!live)}
            title="Show new lines as they come in"
          >
            {live ? <Pause size={14} /> : <Play size={14} />} {live ? "Live" : "Follow"}
          </button>
        </div>
      </div>

      {error && <ErrorNote>{error}</ErrorNote>}
      {!lines && !error && <div class="loading loading-list" />}
      {lines && lines.length === 0 && (
        <Empty>
          {live ? "Waiting for new lines…" : "No lines match. Widen the time or clear the filters."}
        </Empty>
      )}
      {lines && lines.length > 0 && <LineList lines={lines} />}
      {lines && next && !live && (
        <button class="btn btn-ghost more" onClick={more} disabled={loadingMore}>
          {loadingMore ? "Loading…" : "Older lines"}
        </button>
      )}
    </div>
  );
}
