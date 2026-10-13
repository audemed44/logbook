import { api } from "../api";
import { useData } from "../hooks";
import { ago, bytes, count, levelTone, searchWords } from "../lib";
import type { Overview } from "../types";
import { HourChart } from "./HourChart";
import { Dot, Empty, ErrorNote, Figure, SectionHead } from "./ui";

export function OverviewPage() {
  const { data, error } = useData(api.overview, 30_000);
  const sum = (k: "lines" | "errors" | "warns" | "dropped") =>
    data ? data.hours.reduce((n, h) => n + h[k], 0) : 0;
  const errors = sum("errors");
  const used = data ? data.usage.bytes / data.limits.max_bytes : 0;

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Last 24 hours</div>
        <h1 class="page-title">Logbook</h1>
        <div class="figures stagger">
          <a class="figure-link" href="/logs?level=error&since=24h">
            <Figure
              value={data ? count(errors) : "—"}
              label="Errors"
              tone={errors ? "bad" : "good"}
            />
          </a>
          <a class="figure-link" href="/logs?level=warn&since=24h">
            <Figure
              value={data ? count(sum("warns")) : "—"}
              label="Warnings"
              tone={sum("warns") ? "warn" : ""}
            />
          </a>
          <Figure value={data ? count(sum("lines")) : "—"} label="Lines" />
          <Figure
            value={data ? bytes(data.usage.bytes) : "—"}
            label={data ? `Stored, of ${bytes(data.limits.max_bytes)}` : "Stored"}
            tone={used >= 0.9 ? "warn" : ""}
          />
          {sum("dropped") > 0 && (
            <Figure value={count(sum("dropped"))} label="Held back by limits" tone="warn" />
          )}
        </div>
      </header>

      {error && <ErrorNote>{error}</ErrorNote>}

      <section class="section">
        <SectionHead index={1} title="By hour" />
        {!data && <div class="loading loading-list" />}
        {data && <HourChart hours={data.hours} />}
      </section>

      <section class="section">
        <SectionHead index={2} title="Problems">
          <span class="muted">warnings and errors, grouped</span>
        </SectionHead>
        {data && data.problems.length === 0 && (
          <Empty>Nothing went wrong in the last 24 hours.</Empty>
        )}
        {data && data.problems.length > 0 && (
          <div class="list">
            {data.problems.map((p) => (
              <a
                key={p.source + p.msg}
                class="list-row"
                href={`/logs?${new URLSearchParams({
                  source: p.source,
                  level: p.level >= 8 ? "error" : "warn",
                  q: searchWords(p.msg),
                  since: "24h",
                })}`}
              >
                <Dot tone={levelTone(p.level)} />
                <span class="list-main list-link">
                  <span class="list-title mono line-msg">{p.msg}</span>
                  <span class="list-sub">
                    {p.source} · last {ago(p.last)}
                  </span>
                </span>
                <span class="problem-count mono">{count(p.count)}×</span>
              </a>
            ))}
          </div>
        )}
      </section>

      {data && <LimitsSection data={data} />}
    </div>
  );
}

function LimitsSection({ data }: { data: Overview }) {
  const l = data.limits;
  const pct = Math.min(100, Math.round((data.usage.bytes / l.max_bytes) * 100));
  return (
    <section class="section">
      <SectionHead index={3} title="Limits">
        <span class="muted">set in the environment</span>
      </SectionHead>
      <div class="meter" title={`${pct}% of the cap`}>
        <div class={`meter-fill ${pct >= 90 ? "warn" : ""}`} style={{ width: `${pct}%` }} />
      </div>
      <p class="muted">
        {bytes(data.usage.bytes)} of {bytes(l.max_bytes)} in use ({count(data.usage.lines)} lines
        {data.usage.oldest ? `, oldest ${ago(data.usage.oldest)}` : ""}). At the cap, the oldest
        lines go first.
      </p>
      <div class="table-wrap">
        <table class="table limits">
          <tbody>
            <tr>
              <td>Storage cap</td>
              <td class="mono">{bytes(l.max_bytes)}</td>
              <td class="muted">LOGBOOK_MAX_SIZE</td>
            </tr>
            <tr>
              <td>Kept for</td>
              <td class="mono">{l.retention_days} days</td>
              <td class="muted">LOGBOOK_RETENTION</td>
            </tr>
            <tr>
              <td>Lines per container per day</td>
              <td class="mono">{count(l.daily_quota)}</td>
              <td class="muted">LOGBOOK_DAILY_QUOTA</td>
            </tr>
            <tr>
              <td>Lines per container per minute</td>
              <td class="mono">{l.per_minute}</td>
              <td class="muted">LOGBOOK_PER_MINUTE</td>
            </tr>
            <tr>
              <td>Repeats of one message per minute</td>
              <td class="mono">{l.per_message}</td>
              <td class="muted">LOGBOOK_PER_MESSAGE</td>
            </tr>
            <tr>
              <td>Longest line kept</td>
              <td class="mono">{bytes(l.max_line)}</td>
              <td class="muted">LOGBOOK_MAX_LINE</td>
            </tr>
            <tr>
              <td>Alert after errors in 5 minutes</td>
              <td class="mono">{l.notify ? l.burst_errors : "off"}</td>
              <td class="muted">LOGBOOK_ALERT_ERRORS, LOGBOOK_NOTIFY_URL</td>
            </tr>
          </tbody>
        </table>
      </div>
      {data.upkeep?.err && <ErrorNote>Last upkeep failed: {data.upkeep.err}</ErrorNote>}
    </section>
  );
}
