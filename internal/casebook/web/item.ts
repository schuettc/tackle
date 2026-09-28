// item.ts — the reading-column document for a casebook item.
//
// renderItem(ctx, detail) produces the full item reading column:
//   kicker · h1 title · facts · body excerpt (foldable) ·
//   pending-proposal card slot · decide buttons (wired Task 4) ·
//   evidence section · history section
//
// The returned element replaces the .kit-read content; it is a plain
// HTMLElement, not a kit component, so it can be replaced at will.

import { h, facts, card, fold, buttons, type Button } from '/_kit/kit.js';
import type {
  ItemDetailView,
  Proposal,
  Evidence,
  Event,
  LogEntry,
} from './wire.d.ts';
import type { Ctx } from './app.ts';

// Body is capped at 600 chars per the spec; anything longer is folded.
const BODY_CAP = 600;

// Format a UTC string for display.
function fmtDate(s: string | undefined): string {
  if (!s) return '—';
  const d = new Date(s);
  return d.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}

function fmtTime(s: string): string {
  const d = new Date(s);
  return d.toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// ---- evidence ---------------------------------------------------------------

function renderEvidence(evs: Evidence[]): HTMLElement {
  const section = h('section', { class: 'cb-evidence' });
  section.append(h('h2', { class: 'cb-section-label' }, 'evidence'));
  if (evs.length === 0) {
    section.append(h('p', { class: 'cb-empty' }, 'no evidence'));
    return section;
  }
  for (const ev of evs) {
    const by = ev.author
      ? h('span', { class: 'cb-muted' }, ` by ${ev.author}`)
      : null;
    const time = h(
      'span',
      { class: 'cb-muted' },
      ` · ${fmtDate(ev.created_at)}`,
    );
    const item = h(
      'div',
      { class: 'cb-evidence-item' },
      h('p', { class: 'cb-evidence-text' }, ev.text),
      h('p', { class: 'cb-evidence-meta' }, ...(by ? [by] : []), time),
    );
    section.append(item);
  }
  return section;
}

// ---- history ----------------------------------------------------------------

function renderHistory(events: Event[], decisions: LogEntry[]): HTMLElement {
  const section = h('section', { class: 'cb-history' });
  section.append(h('h2', { class: 'cb-section-label' }, 'history'));

  if (events.length === 0 && decisions.length === 0) {
    section.append(h('p', { class: 'cb-empty' }, 'no history'));
    return section;
  }

  // Decisions (git log).
  for (const entry of decisions) {
    section.append(
      h(
        'div',
        { class: 'cb-history-item cb-history-decision' },
        h('span', { class: 'cb-history-time' }, fmtDate(entry.Time)),
        h('span', { class: 'cb-history-msg' }, entry.Subject),
      ),
    );
  }

  // Journal events (most recent last; already trimmed to 50 by the server).
  for (const ev of events) {
    const label =
      ev.actions && ev.actions.length > 0
        ? ev.actions
            .map((a) => (a as { hook?: string }).hook ?? '')
            .filter(Boolean)
            .join(', ')
        : (ev.hook ?? ev.src);
    section.append(
      h(
        'div',
        { class: 'cb-history-item' },
        h('span', { class: 'cb-history-time' }, fmtTime(ev.ts)),
        h('span', { class: 'cb-history-msg' }, label),
      ),
    );
  }

  return section;
}

// ---- proposal card ----------------------------------------------------------

function renderProposalCard(p: Proposal): HTMLElement {
  const stateLabel =
    p.state === 'pending' ? 'pending proposal' : `proposal · ${p.state}`;
  const lines: Array<Node | string> = [
    h(
      'div',
      { class: 'cb-proposal-detail' },
      h('span', { class: 'cb-label' }, 'disposition '),
      h('strong', null, p.disposition),
      p.until ? h('span', null, ` until ${fmtDate(p.until)}`) : null,
      p.note ? h('span', null, ` · ${p.note}`) : null,
    ),
  ];
  const bodyEl = h('div', { class: 'cb-proposal-body' }, ...lines);
  const cardActions: Button[] = [
    {
      label: 'accept',
      fill: true,
      run() {
        // Task 4 wires accept.
      },
    },
    {
      label: 'change…',
      run() {
        // Task 4 wires change.
      },
    },
    {
      label: 'reject',
      danger: true,
      run() {
        // Task 4 wires reject.
      },
    },
  ];
  return card({
    edge: 'agent',
    head: stateLabel,
    body: bodyEl,
    actions: cardActions,
  });
}

// ---- decide buttons ---------------------------------------------------------

function renderDecideSection(): HTMLElement {
  const section = h('section', { class: 'cb-decide' });
  section.append(h('h2', { class: 'cb-section-label' }, 'decide'));
  // Allowed disposition buttons are wired in Task 4. For now we render the
  // label and leave a slot for Task 4 to fill.
  section.append(
    buttons([
      { label: 'keep', run() {} },
      { label: 'close', run() {} },
      { label: 'ignore', run() {} },
    ]),
  );
  return section;
}

// ---- public -----------------------------------------------------------------

export function renderItem(ctx: Ctx, detail: ItemDetailView): HTMLElement {
  void ctx; // ctx used by Task 4 (decide actions); retain the param.
  const it = detail.item;

  const el = h('article', { class: 'cb-item' });

  // ---- kicker ---------------------------------------------------------------
  const kickerParts = [it.kind, it.key, it.relation]
    .filter(Boolean)
    .join(' · ');
  el.append(h('p', { class: 'cb-kicker' }, kickerParts));

  // ---- title ----------------------------------------------------------------
  el.append(h('h1', { class: 'cb-title' }, it.title ?? it.key));

  // ---- facts ----------------------------------------------------------------
  const factPairs: Array<[string, string]> = [];
  if (it.repo) factPairs.push(['repo', it.repo]);
  if (it.status) factPairs.push(['status', it.status]);
  if (it.author)
    factPairs.push(['author', it.author + (it.author_is_bot ? ' (bot)' : '')]);
  if (it.created_at) factPairs.push(['opened', fmtDate(it.created_at)]);
  if (it.updated_at) factPairs.push(['updated', fmtDate(it.updated_at)]);
  if (it.labels && it.labels.length > 0)
    factPairs.push(['labels', it.labels.join(', ')]);
  if (it.landed) factPairs.push(['landed', it.landed_how ?? it.landed]);
  el.append(facts(factPairs));

  // ---- body -----------------------------------------------------------------
  if (it.body) {
    const excerpt = it.body.slice(0, BODY_CAP);
    const rest = it.body.slice(BODY_CAP);
    const bodyWrap = h('div', { class: 'cb-body' });
    bodyWrap.append(h('p', null, excerpt));
    if (rest) {
      bodyWrap.append(fold('read more', h('p', null, rest)));
    }
    el.append(bodyWrap);
  }

  // ---- pending proposal card ------------------------------------------------
  if (it.proposal) {
    el.append(renderProposalCard(it.proposal));
  }

  // ---- decide ---------------------------------------------------------------
  el.append(renderDecideSection());

  // ---- evidence -------------------------------------------------------------
  el.append(renderEvidence(detail.evidence ?? []));

  // ---- history (journal + decision commits) ---------------------------------
  el.append(renderHistory(detail.history ?? [], detail.decisions ?? []));

  return el;
}
