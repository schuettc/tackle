// item.ts — the reading-column document for a casebook item.
//
// renderItem(ctx, detail) produces the full item reading column:
//   kicker · h1 title · facts · body excerpt (foldable) ·
//   the decide step (serve's question, the recommendation card, the choice
//   cards: choices.ts) · evidence section · history section
//
// The returned element replaces the .kit-read content; it is a plain
// HTMLElement, not a kit component, so it can be replaced at will.

import { h, facts, fold } from '/_kit/kit.js';
import type { ItemDetailView, Evidence, Event, LogEntry } from './wire.d.ts';
import type { Ctx } from './app.ts';
import { getVocab } from './decide.ts';
import { renderChoices, asNotNow } from './choices.ts';
import { keyWithoutKind, stripOwnKey } from './decide-math.ts';
import type { DecisionVocabView } from './wire.d.ts';
import { proposalCard } from './proposals.ts';
import { lookingLine } from './sessions.ts';

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

function renderHistory(
  events: Event[],
  decisions: LogEntry[],
  ownKey: string,
): HTMLElement {
  const section = h('section', { class: 'cb-history' });
  section.append(h('h3', { class: 'kit-label' }, 'history'));

  if (events.length === 0 && decisions.length === 0) {
    section.append(h('p', { class: 'cb-empty' }, 'no history'));
    return section;
  }

  // Decisions (git log): strip the item's own key from the subject so entries
  // read as "decide \u2192 close by schuettc" rather than repeating the key.
  for (const entry of decisions) {
    const display = stripOwnKey(entry.Subject, ownKey);
    section.append(
      h(
        'div',
        { class: 'cb-history-item cb-history-decision' },
        h('span', { class: 'cb-history-time' }, fmtShortDate(entry.Time)),
        h('span', { class: 'cb-history-msg' }, display),
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

// ---- the decide step ---------------------------------------------------------

/** What the decide step does with a pick and an accept (attention.ts). */
export interface ItemHooks {
  /** Re-render after a reject or a change from the recommendation card. */
  onRefresh?: () => void;
  /** A card was picked: decide the item (Not now with its until, a close
   * with its closing comment: note, '' for none). */
  decide?: (disposition: string, until?: string, note?: string) => void;
  /** The recommendation's accept (the card's button, like the a key). */
  accept?: () => void;
}

// renderDecideSection is the decide step: serve's question for the kind,
// the recommendation card when a proposal is pending, then the cards.
function renderDecideSection(
  ctx: Ctx,
  detail: ItemDetailView,
  hooks: ItemHooks,
): HTMLElement {
  const it = detail.item;
  const section = h('section', { class: 'cb-decide' });
  const question = h('h2', { class: 'cb-question' });
  // "‹session› is looking into it": Court asked the session to (attention
  // keeps it current, and offers the ask, under the cards).
  const looking = lookingLine(it.looking);
  const lookingEl = h('p', { class: 'cb-looking', hidden: !looking }, looking);
  const recSlot = h('div', { class: 'cb-rec-slot' });
  const pending =
    it.proposal && it.proposal.state === 'pending' ? it.proposal : null;
  const cards = renderChoices(ctx, it.kind, {
    recommended: pending?.disposition,
    chosen: it.decision?.disposition,
    note: it.decision?.note,
    onPick(d, until, note) {
      hooks.decide?.(d, until, note);
    },
  });
  section.append(question, lookingEl, recSlot, cards);

  // The vocabulary is fetched once and cached: this resolves at once after
  // the first item.
  void getVocab(ctx).then((vocab: DecisionVocabView) => {
    const kv = (vocab.kinds ?? []).find((k) => k.kind === it.kind);
    question.textContent = kv?.question ?? '';
    const rec = asNotNow(pending?.disposition);
    const label = kv?.choices?.find((c) => c.disposition === rec)?.label;
    const card = proposalCard(ctx, detail, hooks.onRefresh, {
      label,
      accept: hooks.accept,
    });
    if (card) recSlot.replaceWith(card);
    else recSlot.remove();
  });

  return section;
}

// ---- public -----------------------------------------------------------------

export function renderItem(
  ctx: Ctx,
  detail: ItemDetailView,
  hooks: ItemHooks = {},
): HTMLElement {
  const it = detail.item;

  const el = h('article', { class: 'cb-item', dataset: { key: it.key } });

  // ---- kicker ---------------------------------------------------------------
  // Show the kind prefix once, then the key without its kind prefix. The key
  // links to the item's GitHub page (serve's url: a PR, an issue, a repo) in
  // a new tab; a branch or a worktree has none, and its key is plain text.
  const displayKey = keyWithoutKind(it.key);
  const keyEl: Node | string = it.url
    ? h('a', { href: it.url, target: '_blank', rel: 'noopener' }, displayKey)
    : displayKey;
  const kicker = h('p', { class: 'cb-kicker kit-kick' });
  const parts = [it.kind, keyEl, it.relation].filter(
    (part): part is Node | string => !!part,
  );
  parts.forEach((part, i) => {
    if (i > 0) kicker.append(' \u00b7 ');
    kicker.append(part);
  });
  el.append(kicker);

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
  // Why a Not now or a Leave it open came back (serve's words: its
  // condition was met, it names something GitHub can't find, or new
  // activity since).
  if (it.due_reason)
    el.append(h('p', { class: 'cb-due-reason' }, it.due_reason));

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

  // ---- decide: the question, the recommendation, the cards -----------------
  el.append(renderDecideSection(ctx, detail, hooks));

  // ---- evidence -------------------------------------------------------------
  el.append(renderEvidence(detail.evidence ?? []));

  // ---- history (journal + decision commits) ---------------------------------
  el.append(
    renderHistory(detail.history ?? [], detail.decisions ?? [], it.key),
  );

  // Wrap in .kit-doc for the kit's centred, max-width reading column.
  return h('div', { class: 'kit-doc' }, el);
}
