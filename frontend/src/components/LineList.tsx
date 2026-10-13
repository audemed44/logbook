import { Link2, ListTree } from "lucide-preact";
import { Fragment } from "preact";
import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { clock, duration, fields, levelName } from "../lib";
import type { Line } from "../types";
import { Dialog, ErrorNote } from "./ui";

/** Log lines, newest first; a line opens to show all its fields. */
export function LineList(props: { lines: Line[]; offsetFrom?: string; highlight?: number }) {
  const [open, setOpen] = useState<number | null>(null);
  const [around, setAround] = useState<Line | null>(null);
  return (
    <div class="lines">
      {props.lines.map((l) => (
        <LineRow
          key={l.id}
          line={l}
          open={open === l.id}
          highlight={props.highlight === l.id}
          offsetFrom={props.offsetFrom}
          onToggle={() => setOpen(open === l.id ? null : l.id)}
          onAround={() => setAround(l)}
        />
      ))}
      {around && <AroundDialog line={around} onClose={() => setAround(null)} />}
    </div>
  );
}

function LineRow(props: {
  line: Line;
  open: boolean;
  highlight?: boolean;
  offsetFrom?: string;
  onToggle: () => void;
  onAround: () => void;
}) {
  const l = props.line;
  const lvl = levelName(l.level);
  const f = fields(l.attrs);
  const status = f.find(([k]) => k === "status")?.[1];
  const when = props.offsetFrom
    ? `+${duration(new Date(l.time).getTime() - new Date(props.offsetFrom).getTime())}`
    : clock(l.time);
  return (
    <div
      class={`line lvl-${lvl} ${props.open ? "open" : ""} ${props.highlight ? "highlight" : ""}`}
    >
      <button class="line-head" onClick={props.onToggle} aria-expanded={props.open}>
        <span class="line-time mono">{when}</span>
        <span class="line-source">{l.source}</span>
        <span class={`line-level mono lvl-${lvl}`}>{lvl}</span>
        <span class="line-msg mono">
          {l.msg}
          {l.err && l.err !== l.msg && <span class="line-err"> {l.err}</span>}
        </span>
        <span class="line-meta mono">
          {status && (
            <span
              class={Number(status) >= 500 ? "tone-bad" : Number(status) >= 400 ? "tone-warn" : ""}
            >
              {status}
            </span>
          )}
          {l.dur_ms !== undefined && l.kind !== "docker" && <span>{duration(l.dur_ms)}</span>}
        </span>
      </button>
      {props.open && (
        <div class="line-detail">
          <pre class="line-full mono">{l.msg}</pre>
          {l.err && l.err !== l.msg && <pre class="line-full mono tone-bad">{l.err}</pre>}
          <dl class="line-fields mono">
            <dt>time</dt>
            <dd>{new Date(l.time).toLocaleString("en-GB")}</dd>
            {l.kind && (
              <>
                <dt>kind</dt>
                <dd>{l.kind}</dd>
              </>
            )}
            {l.user && (
              <>
                <dt>user</dt>
                <dd>{l.user}</dd>
              </>
            )}
            {l.trace && (
              <>
                <dt>trace</dt>
                <dd>{l.trace}</dd>
              </>
            )}
            {f.map(([k, v]) => (
              <Fragment key={k}>
                <dt>{k}</dt>
                <dd>{v}</dd>
              </Fragment>
            ))}
            {l.stderr && (
              <>
                <dt>stream</dt>
                <dd>stderr</dd>
              </>
            )}
          </dl>
          <div class="line-actions">
            {l.trace && (
              <a class="btn btn-small" href={`/trace/${encodeURIComponent(l.trace)}`}>
                <Link2 size={14} /> Follow the trace
              </a>
            )}
            <button class="btn btn-small" onClick={props.onAround}>
              <ListTree size={14} /> Lines around it
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function AroundDialog(props: { line: Line; onClose: () => void }) {
  const [lines, setLines] = useState<Line[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api.around(props.line.id).then(setLines, (e: Error) => setError(e.message));
  }, [props.line.id]);
  return (
    <Dialog
      title={`${props.line.source}, around ${clock(props.line.time)}`}
      onClose={props.onClose}
      wide
    >
      {error && <ErrorNote>{error}</ErrorNote>}
      {!lines && !error && <div class="loading loading-list" />}
      {lines && <LineList lines={lines} highlight={props.line.id} />}
    </Dialog>
  );
}
