// item.ts — the reading-column document for a casebook item.
//
// renderItem(ctx, detail) produces the full item reading column:
//   kicker · h1 title · facts · body excerpt (foldable) ·
//   pending-proposal card slot · decide buttons (wired Task 4) ·
//   evidence section · history section
//
// The returned element replaces the .kit-read content; it is a plain
// HTMLElement, not a kit component, so it can be replaced at will.

import { h, facts, fold } from '/_kit/kit.js';
import type { ItemDetailView, Evidence, Event, LogEntry } from './wire.d.ts';
import type { Ctx } from './app.ts';
import {
  openDecideSheet,
  DANGER_DISPS,
  getVocab,
  allowedForKind,
} from './decide.ts';
import { keyWithoutKind } from './decide-math.ts';
import type { DecisionVocabView } from './wire.d.ts';
import { proposalCard } from './proposals.ts';

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

// Short mono date for history entries: MM-DD (year omitted for brevity).
function fmtShortDate(s: string | undefined): string {
  if (!s)
    return '\
def';
  const d = new Date(s);
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  return `${mm}-${dd}`;
}

// ---- evidence ---------------------------------------------------------------

function renderEvidence(evs: Evidence[]): HTMLElement {
  const section = h('section', { class: 'cb-evidence' });
  section.append(h('h3', { class: 'kit-label' }, 'evidence'));
  if (evs.length === 0) {
    section.append(h('p', { class: 'cb-empty' }, 'no evidence'));
    return section;
  }
  for (const ev of evs) {
    // Author is shown in agent colour at the start of the meta line.
    const authorEl = ev.author
      ? h('span', { class: 'cb-evidence-author' }, ev.author + ' ')
      : null;
    const time = h(
      'span',
      { class: 'cb-muted' },
      ` · ${fmtShortDate(ev.created_at)}`,
    );
    const item = h(
      'div',
      { class: 'cb-evidence-item' },
      h(
        'p',
        { class: 'cb-evidence-meta' },
        ...(authorEl ? [authorEl] : []),
        ev.text,
        time,
      ),
    );
    section.append(item);
  }
  return section;
}

// ---- history ----------------------------------------------------------------

function renderHistory(events: Event[], decisions: LogEntry[]): HTMLElement {
  const section = h('section', { class: 'cb-history' });
  section.append(h('h3', { class: 'kit-label' }, 'history'));

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
        h('span', { class: 'cb-history-time' }, fmtShortDate(entry.Time)),
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
        h('span', { class: 'cb-history-time' }, fmtShortDate(ev.ts)),
        h('span', { class: 'cb-history-msg' }, label),
      ),
    );
  }

  return section;
}

// proposal card is rendered by proposals.ts (proposalCard)

// ---- decide buttons ---------------------------------------------------------

function renderDecideSection(ctx: Ctx, key: string, kind: string): HTMLElement {
  const section = h('section', { class: 'cb-decide' });
  section.append(h('h3', { class: 'kit-label' }, 'decide'));

  const dispRow = h('div', { class: 'cb-decide-btns' });
  section.append(dispRow);

  // The vocabulary is fetched once and cached; this resolves instantly after the
  // first call.  Buttons appear after the micro-task queue drains.
  void getVocab(ctx).then((vocab: DecisionVocabView) => {
    const vocabKind = (vocab.kinds ?? []).find((k) => k.kind === kind);
    const kindAllowed = allowedForKind(vocab, kind);
    const needsUntilSet = new Set(vocabKind?.needs_until ?? []);
    for (const d of kindAllowed) {
      const label = needsUntilSet.has(d) ? `${d}\u2026` : d;
      dispRow.append(
        h(
          'button',
          {
            type: 'button',
            class:
              'kit-btn cb-sheet-disp' +
              (DANGER_DISPS.has(d) ? ' cb-sheet-disp--danger danger' : ''),
            onclick() {
              openDecideSheet(ctx, [key], () => {
                // The 'decided' live event triggers a list reload.
              });
            },
          },
          label,
        ),
      );
    }
  });

  return section;
}

// ---- public -----------------------------------------------------------------

export function renderItem(
  ctx: Ctx,
  detail: ItemDetailView,
  onRefresh?: () => void,
): HTMLElement {
  const it = detail.item;

  const el = h('article', { class: 'cb-item' });

  // ---- kicker ---------------------------------------------------------------
  // Show the kind prefix once, then the key without its kind prefix.
  const displayKey = keyWithoutKind(it.key);
  const kickerParts = [it.kind, displayKey, it.relation]
    .filter(Boolean)
    .join(' · ');
  el.append(h('p', { class: 'cb-kicker kit-kick' }, kickerParts));

  // ---- title ----------------------------------------------------------------
  // Fallback: use the display key (without kind prefix) so titleless items
  // like branch:schuettc/hail@feat/client show "schuettc/hail@feat/client"
  // rather than repeating the kind already shown in the kicker.
  el.append(
    h('h1', { class: 'cb-title kit-h1' }, it.title ?? keyWithoutKind(it.key)),
  );

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
  const propCard = proposalCard(ctx, detail, onRefresh);
  if (propCard) {
    el.append(propCard);
  }

  // ---- decide ---------------------------------------------------------------
  el.append(renderDecideSection(ctx, it.key, it.kind));

  // ---- evidence -------------------------------------------------------------
  el.append(renderEvidence(detail.evidence ?? []));

  // ---- history (journal + decision commits) ---------------------------------
  el.append(renderHistory(detail.history ?? [], detail.decisions ?? []));

  // Wrap in .kit-doc for the kit's centred, max-width reading column.
  return h('div', { class: 'kit-doc' }, el);
}
