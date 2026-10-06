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
  /** This decision's id: new each time a decision is made, never reused.
   * Every decision, clear and Send names the one it saw by it. */
  id?: string;
}

interface Account {
  row: string;
  did: 'fixed' | 'kept';
  how: string;
}

/** A file's recommendation (internal/sift/rec.Rec). */
interface Recommendation {
  file: string;
  base: string;
  content: string;
  findings: Account[];
  links?: string[];
  summary: string;
}

interface FileDecision {
  action: 'accept' | 'edit' | 'reject';
  content?: string;
  note?: string;
  sent?: boolean;
  /** As Decision.id. */
  id?: string;
}

/** One of an audit round's files (internal/sift/serve fileJSON). */
interface FileView {
  key: string;
  path: string;
  source: Source;
  commit?: string;
  class: string;
  budget: number;
  base: string;
  size: number;
  after: number;
  rows: string[];
  rec: Recommendation | null;
  decision: FileDecision | null;
  fingerprint: string;
  group: string[];
  /** The recommendation leaves this file (and its linked files) as it is:
   * it is listed under nothing to change, to agree or disagree with. */
  unchanged: boolean;
  /** Each of its findings is muted (it was agreed with). */
  muted: boolean;
}

interface Progress {
  state: '' | 'checking' | 'recommending' | 'ready' | 'sent' | 'applied';
  files: number;
  recommended: number;
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
  decision?: Decision;
  /** The fingerprint of what the page shows (the proposal, with the edit in
   * force over it); every decision on the row sends it back. */
  fingerprint: string;
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
  progress: Progress;
  rows: Finding[];
  files: FileView[];
  applies: ApplyRecord[];
  sends: number;
  home: string;
}
