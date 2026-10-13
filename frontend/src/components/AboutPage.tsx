import { SectionHead } from "./ui";

export function AboutPage() {
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">About</div>
        <h1 class="page-title">Logbook</h1>
        <p class="muted page-lede">Every homelab container's logs in one place: search, follow a request, alert on error bursts, with hard storage limits.</p>
      </header>
      <section class="section">
        <SectionHead index={1} title="Foyer" />
        <p class="muted page-lede">
          Add Logbook to Foyer as an <code>app</code> widget at{" "}
          <code>http://logbook:8080/api/foyer/widget</code>, with the token as its key.
        </p>
      </section>
    </div>
  );
}
