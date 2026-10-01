// Wire types for GET /api/review (internal/cull/serve/api.go). Ambient.

type Noul = { type: 'noul'; noul: number };

interface Answer {
  value: string;
  note: string;
  via: string;
  blind: boolean;
  answered_at?: string;
  sent_at?: string;
}

interface TestState {
  note: string;
  language: string;
  framework: string;
  test_name: string;
  test_source: string;
  setup_context: string;
  code_under_test: { symbol: string; file: string; source: string }[];
  truncated: boolean;
}

interface GroupState {
  note: string;
  language: string;
  framework: string;
  file: string;
  tests: { name: string; source: string }[];
  truncated: boolean;
}

interface TestJev {
  verdict: {
    type: 'choice';
    choice: string;
    probabilities: { cut: number; keep: number; review: number };
    confidence: number;
  };
  regression_value: {
    type: 'score';
    score: number;
    probabilities?: Record<string, number>;
    confidence?: number;
  };
  tautological: Noul;
  mock_only: Noul;
  framework_behavior: Noul;
  incidental_detail: Noul;
  not_missed: Noul;
}

interface GroupJev {
  verdict: {
    type: 'choice';
    choice: string;
    probabilities: {
      consolidate: number;
      keep_separate: number;
      review: number;
    };
    confidence: number;
  };
  same_behavior: Noul;
  exact_duplicate: Noul;
  loss_if_merged: Noul;
}

interface ItemBase {
  id: string;
  hash: string;
  file: string;
  name: string;
  verdict: string;
  rule: string;
  model: string;
  jev: unknown;
  answer?: Answer;
}

interface TestItem extends ItemBase {
  kind: 'test';
  state: TestState;
  jev: TestJev;
}

interface GroupItem extends ItemBase {
  kind: 'group';
  state: GroupState;
  jev: GroupJev;
  rows?: string[][];
  members?: string[];
}

type Item = TestItem | GroupItem;

interface ReviewRun {
  id: number;
  at: string;
  mode: string;
  base: string;
  total: number;
  summary: Record<string, number>;
}

interface Review {
  cursor: string;
  project: { id: number; root: string };
  run: ReviewRun | null;
  items: Item[];
  answered: { total: number; sent: number };
  blind: boolean;
}
