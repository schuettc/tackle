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
