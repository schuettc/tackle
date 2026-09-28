// decide-math.ts — pure decision-vocabulary helpers with no browser or kit
// imports.  This module is safe to import in node --test unit tests.
//
// These functions are the selection math the decide sheet uses:
//   kindFromKey   — extract the kind prefix from a casebook key
//   allowedForKind — look up allowed dispositions for one kind from server vocab
//   allowedForKeys — intersect allowed sets for a mixed-kind selection

import type { DecisionVocabView } from './wire.d.ts';

/**
 * kindFromKey extracts the kind from a key by taking the prefix before the
 * first colon.  "pr:schuettc/hail#3" → "pr".
 */
export function kindFromKey(key: string): string {
  const i = key.indexOf(':');
  return i > 0 ? key.slice(0, i) : '';
}

/**
 * allowedForKind returns the valid dispositions for one item kind from the
 * server's vocabulary.  Falls back to an empty array for unknown kinds.
 */
export function allowedForKind(
  vocab: DecisionVocabView,
  kind: string,
): string[] {
  return (vocab.kinds ?? []).find((k) => k.kind === kind)?.allowed ?? [];
}

/**
 * keyWithoutKind strips the kind prefix from a casebook key, leaving only the
 * identifier part suitable for display beside a kind label.
 * "issue:schuettc/hail#4" → "schuettc/hail#4".
 * Returns the full key unchanged when there is no colon.
 */
export function keyWithoutKind(key: string): string {
  const i = key.indexOf(':');
  return i > 0 ? key.slice(i + 1) : key;
}

/**
 * allowedForKeys returns the intersection of allowed dispositions for every
 * key in the slice, derived from each key's kind prefix.  This is the correct
 * way to compute allowed for a selection: it works for keys on unrendered
 * pages (where no ItemView is available) as well as rendered ones.
 */
export function allowedForKeys(
  vocab: DecisionVocabView,
  keys: string[],
): string[] {
  if (keys.length === 0) return [];
  const kinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  if (kinds.length === 0) return [];
  const sets = kinds.map((k) => new Set(allowedForKind(vocab, k)));
  const first = [...(sets[0] ?? new Set())];
  return first.filter((d) => sets.every((s) => s.has(d)));
}

// ---- history display helpers ------------------------------------------------

/**
 * stripOwnKey removes the item's own key from a history entry subject line,
 * in display only.
 * e.g. "decide pr:schuettc/hail#3 → close by schuettc"
 *   → "decide → close by schuettc"
 *
 * Only the exact key (and any immediately trailing space) is removed.
 * Entries that mention a different key are left unchanged.
 */
export function stripOwnKey(subject: string, key: string): string {
  if (!key) return subject;
  const withSpace = key + ' ';
  if (subject.includes(withSpace)) {
    return subject.replace(withSpace, '');
  }
  if (subject.includes(key)) {
    return subject.replace(key, '');
  }
  return subject;
}
