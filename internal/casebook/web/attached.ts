// attached.ts — what goes with a message: the composer's `attached:` line.
//
// Pure helpers with no browser or kit imports, so node --test can load them.
//
//   attachedLabel  — the line's display value: "4 prs selected",
//                    "schuettc/hail#4", "rule landed-branches", "job #3".
//   attachedText   — the editable form of the same value, in the words the
//                    delivery text uses: "pr:o/r#1 pr:o/r#2", "open pr:o/r#4",
//                    "rule landed-branches", "job 3".
//   parseAttached  — reads an edited line back into an Attached. Words it
//                    doesn't recognise are dropped; the line then re-renders
//                    from what was parsed, so what Court sees is what is sent.

import type { Attached } from './wire.d.ts';
import { keyWithoutKind, kindFromKey, pluralize } from './decide-math.ts';

// Plural nouns for item kinds. The default adds "s"; branch is the exception.
const KIND_PLURAL: Record<string, string> = { branch: 'branches' };

/** isEmpty reports whether nothing is attached. */
export function isEmpty(a: Attached): boolean {
  return !a.keys?.length && !a.open && !a.rule && !a.job;
}

/** attachedLabel is the attached line's display value ('' when empty). */
export function attachedLabel(a: Attached): string {
  const parts: string[] = [];
  const keys = a.keys ?? [];
  if (keys.length === 1) {
    parts.push(keyWithoutKind(keys[0]));
  } else if (keys.length > 1) {
    const kinds = new Set(keys.map(kindFromKey));
    const kind = kinds.size === 1 ? [...kinds][0] : '';
    const noun = kind
      ? pluralize(keys.length, kind, KIND_PLURAL[kind])
      : pluralize(keys.length, 'item');
    parts.push(`${noun} selected`);
  }
  if (a.open && !(keys.length === 1 && keys[0] === a.open)) {
    parts.push(keyWithoutKind(a.open));
  }
  if (a.rule) parts.push(`rule ${a.rule}`);
  if (a.job) parts.push(`job #${a.job}`);
  return parts.join(' \u00b7 ');
}

/** attachedText is the editable form of an Attached. */
export function attachedText(a: Attached): string {
  const words: string[] = [...(a.keys ?? [])];
  if (a.open && !(a.keys?.length === 1 && a.keys[0] === a.open)) {
    words.push(`open ${a.open}`);
  }
  if (a.rule) words.push(`rule ${a.rule}`);
  if (a.job) words.push(`job ${a.job}`);
  return words.join(' ');
}

/** parseAttached reads the editable form back into an Attached. */
export function parseAttached(text: string): Attached {
  const words = text
    .split(/[\s,]+/)
    .map((w) => w.trim())
    .filter(Boolean);
  const out: Attached = {};
  const keys: string[] = [];
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    const next = words[i + 1];
    if ((w === 'rule' || w === 'job' || w === 'open') && next) {
      if (w === 'rule') out.rule = next;
      else if (w === 'job') out.job = next.replace(/^#/, '');
      else if (isKey(next)) out.open = next;
      i++;
      continue;
    }
    if (isKey(w) && !keys.includes(w)) keys.push(w);
  }
  if (keys.length) out.keys = keys;
  return out;
}

// isKey reports whether a word looks like an item key: <kind>:<id>.
function isKey(w: string): boolean {
  return /^[a-z]+:\S+$/.test(w);
}

/** sameAttached compares two Attached values by content. */
export function sameAttached(a: Attached, b: Attached): boolean {
  return attachedText(a) === attachedText(b);
}
