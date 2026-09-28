// Hash-based router for casebook.
// Routes: #/attention/<view>, #/item/<key>, #/rules/<id>, #/apply/<job>
//
// Pure helpers (safeDecode, pathEscape, parse) live in router-helpers.ts so
// they can be imported by router.test.ts under Node without any window access.

export { safeDecode, pathEscape, parse } from './router-helpers.ts';
import { parse, pathEscape } from './router-helpers.ts';

export interface Route {
  section: string;
  sub: string;
}

const listeners: Array<(r: Route) => void> = [];

/** Navigate to a section, optionally to a sub-path. */
export function go(section: string, sub?: string): void {
  // Encode the sub so characters like / and # in item keys don't break the
  // hash.  pathEscape matches url.PathEscape so client- and server-built URLs
  // look the same and always round-trip through parse().
  location.hash = sub ? `#/${section}/${pathEscape(sub)}` : `#/${section}`;
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
