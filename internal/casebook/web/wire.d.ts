// wire.d.ts — THE GO↔JS CONTRACT, GENERATED. DO NOT EDIT.
//
// Written by internal/casebook/serve/wire_test.go from the json tags of every
// view struct returned by the casebook page API and agent API. Adding a struct
// to wireRoots is how a new endpoint's payload becomes a type the browser
// cannot silently disagree with.
//
// Regenerate with `go test ./internal/casebook/serve/ -run TestTheWireTypeIsCommitted -update` and commit.
// Drift fails `just verify` (Go-only, always run in CI and the pre-push hook).
//
// READING THE OPTIONALITY:
//
//   - `x?: T` — the Go field carries `omitempty` or `omitzero`, so the key is ABSENT from
//     the payload when it is the zero value.
//   - `x: T[] | null` — no `omitempty`, so a nil slice is serialized as JSON
//     `null`. The type says so rather than promising an array.
//   - a `time.Time` is a string (RFC3339). It is not a shape on the wire.

export interface SummaryView {
  machine: string;
  user: string;
  head: string;
  built_at: string;
  synced_at: string;
  offline_queued: number;
  counts: Record<string, number> | null;
  notices: string[] | null;
  sessions: number;
}

export interface ItemsView {
  total: number;
  items: ItemView[] | null;
}

export interface ItemDetailView {
  item: ItemView;
  proposals: Proposal[] | null;
  evidence: Evidence[] | null;
  history: Event[] | null;
  decisions: LogEntry[] | null;
}

export interface DecideResult {
  decided: number;
  errors: string[] | null;
  pushed: boolean;
}

export interface AcceptResult {
  accepted: number;
  errors: string[] | null;
  pushed: boolean;
}

export interface RejectResult {
  rejected: number;
}

export interface SessionsView {
  sessions: Session[] | null;
}

export interface ThreadsView {
  threads: Thread[] | null;
}

export interface MessagesView {
  messages: Message[] | null;
  batch: number;
  drafts: Message[] | null;
}

export interface SendBatchResult {
  sent: number;
}

export interface ResendResult {
  resent: number;
}

export interface WaitView {
  delivery: Delivery | null;
  text: string;
}

export interface ReplyResult {
  settled: number[] | null;
  skipped: SkippedMessage[] | null;
}

export interface ProposeResult {
  proposed: number;
  proposals: Proposal[] | null;
  errors: string[] | null;
}

export interface StatusView {
  counts: Record<string, number> | null;
  since: string;
  page_open: boolean;
}

export interface OpenResult {
  opened: string;
}

export interface SettledResult {
  session: string;
  worked_ms: number;
  delivery?: number;
}

export interface RulesView {
  rules: RuleRow[] | null;
}

export interface RuleRow {
  rule: Rule;
  record: TrackRecord;
}

export interface RuleDetailView {
  rule: Rule;
  record: TrackRecord;
  matches: MatchPreview;
}

export interface MatchPreview {
  total: number;
  by_reason: ReasonCount[] | null;
  groups: RepoGroup[] | null;
  page: MatchRow[] | null;
}

export interface ReasonCount {
  reason: string;
  count: number;
}

export interface RepoGroup {
  repo: string;
  count: number;
  reasons: ReasonCount[] | null;
}

export interface MatchRow {
  key: string;
  reason: string;
  repo: string;
  title: string;
  status: string;
}

export interface VocabularyView {
  fields: Field[] | null;
}

export interface ItemView {
  key: string;
  kind: string;
  repo?: string;
  title?: string;
  url?: string;
  relation?: string;
  status: string;
  decision?: Decision;
  hits?: Hit[];
  observed: Observed;
  stale?: boolean;
  locations?: string[];
  evidence?: string[];
  author?: string;
  author_is_bot?: boolean;
  created_at?: string;
  updated_at?: string;
  labels?: string[];
  body?: string;
  landed?: string;
  landed_how?: string;
  landed_via?: string[];
  landed_tips?: Record<string, string>;
  proposal?: Proposal;
}

export interface Proposal {
  id: number;
  key: string;
  disposition: string;
  until?: string;
  note?: string;
  source: string;
  state: string;
  reason?: string;
  created_at: string;
  settled_at?: string;
}

export interface Evidence {
  id: number;
  key: string;
  text: string;
  author: string;
  created_at: string;
}

export interface Event {
  v: number;
  ts: string;
  src: string;
  hook?: string;
  args?: string[];
  stdin?: string[][];
  truncated?: boolean;
  cwd?: string;
  git_dir?: string;
  claude_id?: string;
  agent_id?: string;
  child?: boolean;
  actions?: Action[];
  exit_code?: number;
  repo?: string;
  machine?: string;
}

export interface LogEntry {
  Commit: string;
  Time: string;
  Subject: string;
}

export interface Session {
  id: string;
  harness: string;
  label: string;
  cwd: string;
  pid: number;
  first_seen: string;
  last_seen: string;
  looked_at?: string;
  busy: boolean;
}

export interface Thread {
  id: number;
  session_id: string;
  name: string;
  created_at: string;
}

export interface Message {
  id: number;
  thread_id: number;
  author: string;
  body: string;
  attached: Attached;
  batch_id?: number;
  batch_pos?: number;
  delivery_id?: number;
  reply_to?: number;
  state: string;
  created_at: string;
  queued_at?: string;
  settled_at?: string;
}

export interface Delivery {
  id: number;
  session_id: string;
  state: string;
  sent_at: string;
  touched_at: string;
  finished_at?: string;
  shown_at?: string;
  stuck: boolean;
  messages: Message[] | null;
}

export interface SkippedMessage {
  id: number;
  state: string;
  reason: string;
}

export interface Rule {
  id: string;
  name: string;
  status: string;
  created_by: string;
  created_at: string;
  edited_at: string;
  match: Condition[] | null;
  propose: RuleAction;
  exclude?: Exclusion[];
}

export interface TrackRecord {
  accepted: number;
  rejected: number;
  pending: number;
}

export interface Field {
  name: string;
  type: number;
  ops: string[] | null;
  values: string[] | null;
}

export interface Decision {
  disposition: string;
  note?: string;
  until?: string;
  decided_by: string;
  decided_at: string;
  proposed_by?: string;
  rule?: string;
  conflict?: Conflict;
}

export interface Hit {
  rule: string;
  detail: string;
}

export interface Observed {
  known: boolean;
  exists: boolean;
  archived?: boolean;
  state?: string;
}

export interface Action {
  tool: string;
  verb: string;
  dir?: string;
  repo?: string;
  number?: number;
  refs?: string[];
  flags?: string[];
}

export interface Attached {
  keys?: string[];
  open?: string;
  rule?: string;
  job?: string;
}

export interface Condition {
  field: string;
  op: string;
  value: string;
}

export interface RuleAction {
  disposition: string;
  until?: string;
  note?: string;
}

export interface Exclusion {
  key: string;
  reason?: string;
  by: string;
  at: string;
}

export interface Conflict {
  disposition: string;
  note?: string;
  until?: string;
  decided_by: string;
  decided_at: string;
}
