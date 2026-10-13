import { SectionHead } from "./ui";

export function AboutPage() {
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">About</div>
        <h1 class="page-title page-title-small">Logbook</h1>
        <p class="muted page-lede">
          Every homelab container's logs in one place. Logbook reads them from Docker, keeps them in
          SQLite within hard limits, and tells Lookout when a container starts failing.
        </p>
      </header>
      <section class="section">
        <SectionHead index={1} title="What it collects" />
        <p class="muted page-lede">
          Every running container's output, through the read-only Docker socket. JSON lines (the
          homelab apps' <code>internal/logging</code>) keep their level, trace, user and fields;
          logfmt and plain text get a level guessed from the line. Stack traces are joined to the
          line they belong to. Container starts, stops, crashes and out-of-memory kills become lines
          too. Leave a container out with <code>LOGBOOK_EXCLUDE</code> or the label{" "}
          <code>logbook.exclude=true</code>.
        </p>
      </section>
      <section class="section">
        <SectionHead index={2} title="Limits" />
        <p class="muted page-lede">
          A message repeated over and over is held back after a few times a minute, with one line
          saying how many were skipped; each container also has a per-minute and a daily cap. The
          database has a size cap: past it the oldest lines go, and the file shrinks. The Overview
          shows the numbers.
        </p>
      </section>
      <section class="section">
        <SectionHead index={3} title="Alerts and Foyer" />
        <p class="muted page-lede">
          With <code>LOGBOOK_NOTIFY_URL</code> set to Lookout's <code>/notify/&lt;key&gt;</code>, a
          burst of errors from one container sends one notification an hour at most, and so does a
          container reaching its daily cap. <code>LOGBOOK_HEARTBEAT_URL</code> pings a Lookout
          heartbeat every minute. Foyer's card is an <code>app</code> widget at{" "}
          <code>http://logbook:8080/api/foyer/widget</code>, with the token as its key.
        </p>
      </section>
    </div>
  );
}
