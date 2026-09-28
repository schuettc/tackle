// Hash-based router for casebook.
// Routes: #/attention/<view>, #/item/<key>, #/rules/<id>, #/apply/<job>

export interface Route {
  section: string;
  sub: string;
}

const listeners: Array<(r: Route) => void> = [];

/** Parse a location.hash string into a Route. */
export function parse(hash: string): Route {
  const path = hash.replace(/^#\//, '');
  const slash = path.indexOf('/');
  if (slash === -1) {
    return { section: path || 'attention', sub: '' };
  }
  return { section: path.slice(0, slash), sub: path.slice(slash + 1) };
}

/** Navigate to a section, optionally to a sub-path. */
export function go(section: string, sub?: string): void {
  location.hash = sub ? `#/${section}/${sub}` : `#/${section}`;
}

/** Register a callback for route changes (including initial load). */
export function onRoute(cb: (r: Route) => void): void {
  listeners.push(cb);
}

function fire(): void {
  const r = parse(location.hash);
  for (const cb of listeners) cb(r);
}

window.addEventListener('hashchange', fire);

// Export the initial route dispatcher for boot() to call after registering sections.
export function dispatchCurrent(): void {
  fire();
}
