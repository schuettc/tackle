// Hash-based router for casebook.
// Routes: #/attention/<view>, #/item/<key>, #/rules/<id>, #/apply/<job>

export interface Route {
  section: string;
  sub: string;
}

const listeners: Array<(r: Route) => void> = [];

// safeDecode wraps decodeURIComponent so that a malformed percent-escape
// (e.g. %GG) returns the raw string unchanged instead of throwing.
function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

// pathEscape mirrors Go's url.PathEscape: encodes characters that are unsafe
// in a URL path segment (/ # ? % space …) but leaves : and @ unencoded, as
// the server does when it builds casebook_open URLs from item keys.
export function pathEscape(s: string): string {
  return encodeURIComponent(s).replace(/%3A/gi, ':').replace(/%40/gi, '@');
}

/** Parse a location.hash string into a Route. */
export function parse(hash: string): Route {
  const path = hash.replace(/^#\//, '');
  const slash = path.indexOf('/');
  if (slash === -1) {
    return { section: path || 'attention', sub: '' };
  }
  // Decode the sub so keys with /, #, @ etc. produced by url.PathEscape
  // (server) or pathEscape (client) are returned as the raw key string.
  return {
    section: path.slice(0, slash),
    sub: safeDecode(path.slice(slash + 1)),
  };
}

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
