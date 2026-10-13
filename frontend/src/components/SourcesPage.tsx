import { api } from "../api";
import { useData } from "../hooks";
import { ago, count, type Tone } from "../lib";
import type { Source } from "../types";
import { Dot, Empty, ErrorNote, SectionHead } from "./ui";

const STATUS: Record<Source["status"], { tone: Tone; label: string }> = {
  following: { tone: "good", label: "Following" },
  stopped: { tone: "", label: "Stopped" },
  excluded: { tone: "", label: "Excluded" },
  gone: { tone: "", label: "Gone" },
};

export function SourcesPage() {
  const { data, error } = useData(api.sources, 30_000);
  const { data: ov } = useData(api.overview);
  const quota = ov?.limits.daily_quota ?? 0;

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Containers</div>
        <h1 class="page-title page-title-small">Sources</h1>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      <section class="section">
        <SectionHead index={1} title="Last 24 hours" />
        {!data && <div class="loading loading-list" />}
        {data && data.length === 0 && (
          <Empty>No containers yet. Is the Docker socket mounted?</Empty>
        )}
        {data && data.length > 0 && (
          <div class="table-wrap">
            <table class="table sources">
              <thead>
                <tr>
                  <th>Container</th>
                  <th class="num">Lines</th>
                  <th class="num">Warnings</th>
                  <th class="num">Errors</th>
                  <th class="num">Held back</th>
                  <th class="num">Today</th>
                  <th>Last line</th>
                </tr>
              </thead>
              <tbody>
                {data.map((s) => {
                  const st = STATUS[s.status];
                  const full = quota > 0 && s.today >= quota;
                  return (
                    <tr key={s.name}>
                      <td>
                        <a
                          class="source-name"
                          href={`/logs?source=${encodeURIComponent(s.name)}`}
                          title={st.label}
                        >
                          <Dot tone={st.tone} title={st.label} />
                          {s.name}
                          {s.status !== "following" && (
                            <span class="muted"> · {st.label.toLowerCase()}</span>
                          )}
                        </a>
                      </td>
                      <td class="num mono">{count(s.lines_24h)}</td>
                      <td class={`num mono ${s.warns_24h ? "tone-warn" : "muted"}`}>
                        {count(s.warns_24h)}
                      </td>
                      <td class={`num mono ${s.errors_24h ? "tone-bad" : "muted"}`}>
                        {count(s.errors_24h)}
                      </td>
                      <td class={`num mono ${s.dropped_24h ? "tone-warn" : "muted"}`}>
                        {count(s.dropped_24h)}
                      </td>
                      <td
                        class={`num mono ${full ? "tone-warn" : ""}`}
                        title={quota ? `of ${count(quota)} a day` : ""}
                      >
                        {count(s.today)}
                        {full && " · full"}
                      </td>
                      <td class="muted">{s.last_line ? ago(s.last_line) : "—"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
