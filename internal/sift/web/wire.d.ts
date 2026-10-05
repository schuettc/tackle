// Wire types for GET /api/review (internal/sift/serve/api.go). Ambient.

interface Fact {
  name: string;
  value: string;
}

interface Source {
  file: string;
  repo?: string;
  ref?: string;
  path?: string;
  start?: number;
  end?: number;
  entry?: string;
}

interface Decision {
  action: 'accept' | 'edit' | 'reject';
  verdict?: string;
  title?: string;
  text?: string;
  cleared?: ('title' | 'text')[];
  note?: string;
  sent?: boolean;
}

interface Finding {
  id: string;
  check: string;
  summary: string;
  source: Source;
  passage?: string;
  evidence?: Fact[];
  verdict?: string;
  title?: string;
  destination?: string;
  text?: string;
  reason?: string;
  certain: boolean;
  fix?: string;
  decision?: Decision;
}

interface ReviewRound {
  id: number;
  kind: string;
  at: string;
  summary: Record<string, number>;
  owner: string;
}

interface ApplyRecord {
  repo: string;
  base: string;
  branch: string;
  pr: string;
  state: string;
  detail: string;
  rows: string[];
  at: string;
  last_state?: string;
  last_detail?: string;
}

interface Review {
  cursor: string;
  round: ReviewRound | null;
  rows: Finding[];
  applies: ApplyRecord[];
  sends: number;
  home: string;
}
