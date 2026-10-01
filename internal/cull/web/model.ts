// model.ts — pure review logic: what Jev leans toward and how we say so.

export type Lean = 'cut' | 'keep' | 'review' | 'consolidate' | 'keep_separate';

export interface Concern {
  flag: string;
  value: number;
  short: string;
  long: string;
}

const CONCERNS: Record<string, { short: string; long: string }> = {
  tautological: {
    short: 'restates the code',
    long: 'it may only restate what the code does, or check values it set up itself',
  },
  incidental_detail: {
    short: 'pins a detail',
    long: 'it pins a detail that could change harmlessly (wording, formatting, structure)',
  },
  framework_behavior: {
    short: 'tests a library',
    long: "it mostly checks a library or framework, not this project's code",
  },
  mock_only: {
    short: 'mocks only',
    long: 'it only checks that mocks were called',
  },
  not_missed: {
    short: 'might not be missed',
    long: 'a maintainer might not miss it if it were deleted',
  },
};
const FLAG_ORDER = [
  'tautological',
  'incidental_detail',
  'framework_behavior',
  'mock_only',
  'not_missed',
];
export const CONCERN_THRESHOLD = 0.35;

const TEST_LEANS = ['cut', 'keep', 'review'] as const;
const GROUP_LEANS = ['consolidate', 'keep_separate', 'review'] as const;

export function lean(item: Item): Lean {
  const probs = item.jev.verdict.probabilities as unknown as Record<
    string,
    number
  >;
  const keys: readonly Lean[] =
    item.kind === 'group' ? GROUP_LEANS : TEST_LEANS;
  let best = keys[0];
  for (const k of keys) if (probs[k] > probs[best]) best = k;
  return best;
}

/** The probability of the acting verdict: cut, or consolidate. */
export function actP(item: Item): number {
  const probs = item.jev.verdict.probabilities as unknown as Record<
    string,
    number
  >;
  return item.kind === 'group' ? probs.consolidate : probs.cut;
}

export function concern(item: TestItem): Concern | null {
  const jev = item.jev as unknown as Record<string, Noul>;
  let best: string | null = null;
  let bestV = CONCERN_THRESHOLD;
  for (const flag of FLAG_ORDER) {
    const v = jev[flag]?.noul;
    if (typeof v === 'number' && v >= bestV && (best === null || v > bestV)) {
      best = flag;
      bestV = v;
    }
  }
  if (best === null) return null;
  return { flag: best, value: bestV, ...CONCERNS[best] };
}

export const NO_CONCERN = 'no clear reason';

export function strength(p: number): 'leaning' | 'slightly' {
  return p >= 0.5 ? 'leaning' : 'slightly';
}

export function rvLevel(
  score: number,
): 'nothing' | 'cosmetic' | 'real but minor' | 'important' {
  const n = Math.max(0, Math.min(3, Math.round(score)));
  return (['nothing', 'cosmetic', 'real but minor', 'important'] as const)[n];
}

export function recommendation(item: Item): string {
  const p = actP(item).toFixed(2);
  if (item.kind === 'group') {
    const j = item.jev;
    const l = lean(item);
    const verb =
      l === 'consolidate' ? 'merge' : l === 'keep_separate' ? 'separate' : null;
    const lead = verb
      ? `Jev leans ${verb} (merge ${p})`
      : `Jev can't decide (merge ${p})`;
    const exact = j.exact_duplicate.noul >= 0.7;
    return `${lead}. Same behavior ${j.same_behavior.noul.toFixed(2)}; loss if merged ${j.loss_if_merged.noul.toFixed(2)}${exact ? '; one of these looks like an exact duplicate' : ''}.`;
  }
  const l = lean(item);
  const lead =
    l === 'cut'
      ? `Jev leans cut (${p})`
      : l === 'keep'
        ? `Jev leans keep (cut ${p})`
        : `Jev can't decide (cut ${p})`;
  const c = concern(item);
  const why = c
    ? `Jev's main concern: ${c.long} (${c.value.toFixed(2)}).`
    : 'No single concern stood out; Jev is split on whether it earns its place.';
  return `${lead}. ${why} Protects behavior rated ${rvLevel(item.jev.regression_value.score)}.`;
}

/** The value the API takes for an answer: tests keep|cut, groups separate|merge. */
export function answerValue(item: Item, choice: Lean): string {
  if (item.kind === 'group')
    return choice === 'consolidate' ? 'merge' : 'separate';
  return choice === 'cut' ? 'cut' : 'keep';
}
