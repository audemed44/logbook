import { useState } from "preact/hooks";
import { count } from "../lib";
import type { Bucket } from "../types";

const H = 120;
const GAP = 2;

/**
 * Lines per hour, stacked: errors at the base, then warnings, then the
 * rest. Status colours for errors and warnings, a neutral grey for the
 * rest; the legend names all three and each bar has a tooltip.
 */
export function HourChart(props: { hours: Bucket[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const hours = props.hours;
  const max = Math.max(1, ...hours.map((h) => h.lines));
  const n = hours.length;
  const label = (i: number) =>
    new Date(hours[i].hour).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
  const h = hover !== null ? hours[hover] : null;

  return (
    <figure class="chart">
      <div class="chart-legend" aria-hidden="true">
        <span>
          <i class="key key-bad" /> Errors
        </span>
        <span>
          <i class="key key-warn" /> Warnings
        </span>
        <span>
          <i class="key key-rest" /> Other lines
        </span>
        <span class="spacer" />
        <span class="chart-readout mono">
          {h
            ? `${label(hover!)} · ${count(h.lines)} lines · ${h.errors} errors · ${h.warns} warnings${h.dropped ? ` · ${count(h.dropped)} held back` : ""}`
            : `peak ${count(max)} lines an hour`}
        </span>
      </div>
      <svg
        class="chart-svg"
        viewBox={`0 0 ${n * 10} ${H}`}
        preserveAspectRatio="none"
        role="img"
        aria-label="Log lines per hour over the last 24 hours"
        onMouseLeave={() => setHover(null)}
      >
        <line x1="0" x2={n * 10} y1={H - 0.5} y2={H - 0.5} class="chart-base" />
        {hours.map((b, i) => {
          const scale = (v: number) => (v / max) * (H - 4);
          const x = i * 10 + 1;
          const w = 8;
          const segs = [
            { v: b.errors, cls: "bar-bad" },
            { v: b.warns, cls: "bar-warn" },
            { v: Math.max(0, b.lines - b.errors - b.warns), cls: "bar-rest" },
          ].filter((s) => s.v > 0);
          let y = H;
          return (
            <g
              key={b.hour}
              class={hover === i ? "bar hover" : "bar"}
              onMouseEnter={() => setHover(i)}
              onClick={() => setHover(i)}
            >
              <rect x={i * 10} y="0" width="10" height={H} class="bar-hit" />
              {segs.map((s, j) => {
                const hgt = Math.max(1, scale(s.v));
                y -= hgt;
                const rect = <rect key={j} x={x} y={y} width={w} height={hgt} class={s.cls} />;
                y -= GAP; // a gap of background between segments
                return rect;
              })}
            </g>
          );
        })}
      </svg>
      <div class="chart-axis mono" aria-hidden="true">
        <span>{n ? label(0) : ""}</span>
        <span>{n > 12 ? label(Math.floor(n / 2)) : ""}</span>
        <span>now</span>
      </div>
      <table class="sr-only">
        <caption>Lines per hour</caption>
        <thead>
          <tr>
            <th>Hour</th>
            <th>Lines</th>
            <th>Errors</th>
            <th>Warnings</th>
          </tr>
        </thead>
        <tbody>
          {hours.map((b, i) => (
            <tr key={b.hour}>
              <td>{label(i)}</td>
              <td>{b.lines}</td>
              <td>{b.errors}</td>
              <td>{b.warns}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  );
}
