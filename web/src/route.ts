// Minimal hash-based router. The URL fragment encodes the active tab and the
// selected environment so a refresh — or a shared link — restores both.
//
// Format:  #/<tab>            (local host)
//          #/<tab>?env=<id>   (remote environment id)
//
// Hash routing (rather than the History API) is deliberate: the fragment never
// reaches the server, so it works the same behind the Vite dev proxy and the
// Go-served SPA build without any path-fallback configuration.

export type Route = { tab: string; env: number };

// readRoute parses the current location hash. An absent/blank tab yields "" so
// callers can apply their own default.
export function readRoute(): Route {
  const raw = location.hash.replace(/^#\/?/, "");
  const [path, query] = raw.split("?");
  const params = new URLSearchParams(query || "");
  const env = parseInt(params.get("env") || "0", 10);
  return { tab: decodeURIComponent(path || ""), env: Number.isFinite(env) && env > 0 ? env : 0 };
}

function write(tab: string, env: number, replace: boolean) {
  const hash = `#/${encodeURIComponent(tab)}${env ? `?env=${env}` : ""}`;
  if (hash === location.hash) return;
  if (replace) history.replaceState(null, "", hash);
  else location.hash = hash; // fires "hashchange" so back/forward stays in sync
}

// setRouteTab updates only the tab, preserving the current environment.
export function setRouteTab(tab: string, replace = false) {
  write(tab, readRoute().env, replace);
}

// setRouteEnv updates only the environment, preserving the current tab.
export function setRouteEnv(env: number, replace = false) {
  write(readRoute().tab, env, replace);
}

// onRouteChange fires on back/forward navigation and manual hash edits.
export function onRouteChange(fn: () => void): () => void {
  window.addEventListener("hashchange", fn);
  return () => window.removeEventListener("hashchange", fn);
}
