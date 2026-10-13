import { ArrowLeft, LogOut } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api, setUnauthorizedHandler } from "./api";
import { AboutPage } from "./components/AboutPage";
import { LogsPage } from "./components/LogsPage";
import { Login } from "./components/Login";
import { OverviewPage } from "./components/OverviewPage";
import { SourcesPage } from "./components/SourcesPage";
import { TracePage } from "./components/TracePage";
import { onLinkClick, useRoute, type Route } from "./router";
import type { Session } from "./types";

export function App() {
  const route = useRoute();
  const [session, setSession] = useState<Session | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    setUnauthorizedHandler(() => setSession({ authenticated: false }));
    api
      .session()
      .then(setSession)
      .catch((e: Error) => setError(e.message));
  }, []);

  if (error) return <div class="boot">Can't reach Logbook: {error}</div>;
  if (!session) return <div class="boot" />;
  if (!session.authenticated) return <Login onDone={setSession} />;
  return (
    <Shell
      foyerURL={session.foyer_url}
      route={route}
      onSignOut={() => setSession({ authenticated: false })}
    />
  );
}

const NAV: { page: Route["page"]; href: string; label: string }[] = [
  { page: "home", href: "/", label: "Overview" },
  { page: "logs", href: "/logs", label: "Logs" },
  { page: "sources", href: "/sources", label: "Sources" },
  { page: "about", href: "/about", label: "About" },
];

function Shell(props: { route: Route; foyerURL?: string; onSignOut: () => void }) {
  const { route } = props;
  const signOut = async () => {
    await api.logout().catch(() => {});
    props.onSignOut();
  };
  return (
    <div class="shell" onClick={onLinkClick}>
      <header class="topbar">
        {props.foyerURL && (
          <a class="home-link" href={props.foyerURL} title="Back to Foyer">
            <ArrowLeft size={14} />
            <span class="home-link-text">Foyer</span>
          </a>
        )}
        <a class="brand" href="/">
          <span class="brand-mark" aria-hidden="true" />
          Logbook
        </a>
        <span class="spacer" />
        <nav class="topnav" aria-label="Pages">
          {NAV.map((n) => (
            <a
              key={n.page}
              class={
                route.page === n.page || (n.page === "logs" && route.page === "trace")
                  ? "active"
                  : ""
              }
              href={n.href}
            >
              {n.label}
            </a>
          ))}
        </nav>
        <button class="icon-btn" onClick={signOut} title="Sign out" aria-label="Sign out">
          <LogOut size={16} />
        </button>
      </header>
      <main>
        {route.page === "home" && <OverviewPage />}
        {route.page === "logs" && <LogsPage query={route.query} />}
        {route.page === "trace" && <TracePage id={route.id} />}
        {route.page === "sources" && <SourcesPage />}
        {route.page === "about" && <AboutPage />}
      </main>
    </div>
  );
}
