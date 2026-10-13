import { api } from "../api";
import { useData } from "../hooks";
import { duration } from "../lib";
import { LineList } from "./LineList";
import { Empty, ErrorNote, Figure } from "./ui";

/** Every line of one request or job run, in order, timed from the first. */
export function TracePage(props: { id: string }) {
  const { data, error } = useData(
    () => api.lines(new URLSearchParams({ trace: props.id, limit: "500" }).toString()),
    0,
    [props.id],
  );
  const lines = data ? [...data.lines].reverse() : null;
  const first = lines?.[0];
  const last = lines?.[lines.length - 1];
  const sources = lines ? [...new Set(lines.map((l) => l.source))] : [];
  const worst = lines ? Math.max(...lines.map((l) => l.level), -4) : 0;

  return (
    <div class="page">
      <header class="page-head">
        <a class="eyebrow back" href={`/logs?trace=${encodeURIComponent(props.id)}`}>
          Trace · {props.id}
        </a>
        <h1 class="page-title page-title-small">{first ? first.msg : "Trace"}</h1>
        {lines && lines.length > 0 && (
          <div class="figures stagger">
            <Figure value={lines.length} label="Lines" />
            <Figure
              value={duration(new Date(last!.time).getTime() - new Date(first!.time).getTime())}
              label="First to last"
            />
            <Figure
              value={sources.join(", ")}
              label={sources.length === 1 ? "Container" : "Containers"}
            />
            {worst >= 4 && (
              <Figure
                value={worst >= 8 ? "Error" : "Warning"}
                label="Worst"
                tone={worst >= 8 ? "bad" : "warn"}
              />
            )}
          </div>
        )}
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      {!lines && !error && <div class="loading loading-list" />}
      {lines && lines.length === 0 && (
        <Empty>No lines carry this trace (they may have been pruned).</Empty>
      )}
      {lines && lines.length > 0 && <LineList lines={lines} offsetFrom={first!.time} />}
    </div>
  );
}
