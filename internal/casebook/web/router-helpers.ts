// router-helpers.ts — pure, side-effect-free helpers for the hash router.
//
// Extracted so that router.test.ts can import the real implementations rather
// than keeping inline copies.  This module has **no** `window` access at
// module scope (no `window.addEventListener`, no `location`) so it is safe to
// import under Node's --test runner.

/** Wrap decodeURIComponent so a malformed percent-escape returns the raw string. */
export function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

/**
 * pathEscape mirrors Go's url.PathEscape: encodes characters unsafe in a URL
 * path segment (/ # ? % space …) but leaves : and @ unencoded, matching the
 * server's casebook_open URL builder.
 */
export function pathEscape(s: string): string {
  return encodeURIComponent(s).replace(/%3A/gi, ':').replace(/%40/gi, '@');
}

/**
 * Parse a location.hash string into { section, sub, query }. The query is
 * what follows the path's first literal ?: a key's own ? is encoded (%3F),
 * so the split never cuts a key.
 */
export function parse(hash: string): {
  section: string;
  sub: string;
  query: string;
} {
  const full = hash.replace(/^#\//, '');
  const q = full.indexOf('?');
  const path = q === -1 ? full : full.slice(0, q);
  const query = q === -1 ? '' : full.slice(q + 1);
  const slash = path.indexOf('/');
  const head = slash === -1 ? path : path.slice(0, slash);
  // Addresses from before decide and apply: Attention's views and the
  // Rules tab land on decide (the views are gone; a rule narrows the list
  // to it), never read as an item key.
  if (head === 'attention' || head === 'rules') {
    const id = slash === -1 ? '' : safeDecode(path.slice(slash + 1));
    return {
      section: 'decide',
      sub: '',
      query: head === 'rules' && id ? `rule=${encodeURIComponent(id)}` : query,
    };
  }
  if (slash === -1) {
    return { section: path || 'decide', sub: '', query };
  }
  // Decode the sub so keys with /, #, @ etc. produced by url.PathEscape
  // (server) or pathEscape (client) are returned as the raw key string.
  return { section: head, sub: safeDecode(path.slice(slash + 1)), query };
}

/**
 * decideQuery is the decide list's address query: the rule it is narrowed
 * to, the selected saved filters (filters=a,b, the ids joined with a raw
 * comma), then flat.
 */
export function decideQuery(o: {
  flat: boolean;
  filters: string[];
  rule?: string;
}): string {
  const parts: string[] = [];
  if (o.rule) parts.push(`rule=${o.rule}`);
  if (o.filters.length) parts.push(`filters=${o.filters.join(',')}`);
  if (o.flat) parts.push('flat');
  return parts.join('&');
}

/** ruleHash is a rule's address: decide, narrowed to what it recommends. */
export function ruleHash(id: string): string {
  return `#/decide?rule=${encodeURIComponent(id)}`;
}

/**
 * flatFor is whether the list is flat: an address that names a view
 * (explicit) says so with its query; otherwise the stored choice holds.
 */
export function flatFor(
  explicit: boolean,
  query: string,
  stored: boolean,
): boolean {
  return explicit ? new URLSearchParams(query).has('flat') : stored;
}
