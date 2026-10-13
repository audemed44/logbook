import { useEffect, useState } from "preact/hooks";

/**
 * Path routes (the server answers every non-API path with the app):
 *   /                    overview: the last 24 hours, problems, limits
 *   /logs?source=…       the log lines, filtered by the query string
 *   /trace/:id           every line of one request or job run
 *   /sources             the containers and their numbers
 *   /about               how it's set up
 */
export type Route =
  | { page: "home" }
  | { page: "logs"; query: URLSearchParams }
  | { page: "trace"; id: string }
  | { page: "sources" }
  | { page: "about" };

export function parseRoute(path: string, search = ""): Route {
  const parts = path.split("/").filter(Boolean);
  switch (parts[0]) {
    case "logs":
      return { page: "logs", query: new URLSearchParams(search) };
    case "trace":
      if (parts[1]) return { page: "trace", id: decodeURIComponent(parts[1]) };
      break;
    case "sources":
    case "about":
      return { page: parts[0] };
  }
  return { page: "home" };
}

const listeners = new Set<() => void>();

const here = () => window.location.pathname + window.location.search;

export function navigate(url: string, replace = false) {
  if (url === here()) return;
  const samePage = url.split("?")[0] === window.location.pathname;
  if (replace) history.replaceState(null, "", url);
  else history.pushState(null, "", url);
  if (!samePage) window.scrollTo(0, 0);
  listeners.forEach((fn) => fn());
}

/** Lets plain <a href="/…"> links navigate without a page load. */
export function onLinkClick(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
    return;
  }
  const a = (e.target as Element).closest("a");
  if (
    !a ||
    a.target ||
    a.hasAttribute("download") ||
    a.origin !== window.location.origin ||
    a.pathname.startsWith("/api/")
  ) {
    return;
  }
  e.preventDefault();
  navigate(a.pathname + a.search);
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() =>
    parseRoute(window.location.pathname, window.location.search),
  );
  useEffect(() => {
    const update = () => setRoute(parseRoute(window.location.pathname, window.location.search));
    listeners.add(update);
    window.addEventListener("popstate", update);
    return () => {
      listeners.delete(update);
      window.removeEventListener("popstate", update);
    };
  }, []);
  return route;
}
