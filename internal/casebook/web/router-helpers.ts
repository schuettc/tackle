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

/** Parse a location.hash string into { section, sub }. */
export function parse(hash: string): { section: string; sub: string } {
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
