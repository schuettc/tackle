// proposals.ts — accept, change and reject proposals.
//
// Exported:
//   agentFromSource(source)            — "pi:session-id" → "pi"
//   proposalCard(ctx, detail, onDone)   — the recommendation card in the decide step
//   openRejectSheet(ctx, ids, onDone)   — reason field + POST /proposals/reject
//   openDiscardSheet(ctx, n, view, body, onDone) — confirm + POST /proposals/withdraw

import { card, sheet, noteField, h, type Button } from '/_kit/kit.js';
import type {
  AcceptResult,
  DecideResult,
  ItemDetailView,
  RejectResult,
  WithdrawResult,
} from './wire.d.ts';
import type { Ctx } from './app.ts';
import { openDecideSheet } from './decide.ts';
import { agentFromSource, discardConfirm, pluralize } from './decide-math.ts';

// agentFromSource ("pi:session-id" → "pi") lives with the pure helpers.
export { agentFromSource };

// ---- reject sheet -----------------------------------------------------------

/**
 * openRejectSheet opens a reason-entry sheet and POSTs to /proposals/reject.
 * Calls onDone() on success.
 */
export function openRejectSheet(
  ctx: Ctx,
  ids: number[],
  onDone: () => void,
): void {
  let reason = '';
  let submitting = false;
  let sh: ReturnType<typeof sheet> | null = null;

  const reasonInput = noteField({
    placeholder: 'reason (optional)',
    onCommit(v: string) {
      reason = v;
    },
  });

  const errEl = h('p', { class: 'cb-sheet-err' });
  errEl.hidden = true;

  const body = h(
    'div',
    { class: 'cb-sheet-body' },
    h(
      'div',
      { class: 'cb-sheet-row' },
      h('label', { class: 'cb-sheet-label' }, 'reason'),
      reasonInput,
    ),
    errEl,
  );

  async function doReject(): Promise<void> {
    if (submitting) return;
    submitting = true;
    errEl.hidden = true;
    try {
      const payload: Record<string, unknown> = { ids };
      if (reason.trim()) payload['reason'] = reason.trim();
      await ctx.api.post<RejectResult>('/proposals/reject', payload);
      onDone();
      sh?.close();
    } catch (err) {
      errEl.textContent =
        err instanceof Error ? err.message : 'reject failed — try again';
      errEl.hidden = false;
      submitting = false;
    }
  }

  sh = sheet({
    title: `reject ${pluralize(ids.length, 'proposal')}`,
    body,
    actions: [
      {
        label: `Reject ${ids.length}`,
        fill: true,
        run() {
          void doReject();
        },
      },
    ],
    onClose() {
      sh = null;
    },
  });
}

// ---- discard recommendations ------------------------------------------------

/**
 * openDiscardSheet confirms discarding n agent recommendations in view (the
 * kit's sheet: "Discard n recommendations in ‹view›? …", discard and
 * cancel); discard POSTs body ({ids}, or the view and its filters, with a
 * group or without) to /proposals/withdraw and calls onDone with how many
 * were withdrawn. It decides and rejects nothing: the items go back to the
 * agent.
 */
export function openDiscardSheet(
  ctx: Ctx,
  n: number,
  view: string,
  body: Record<string, unknown>,
  onDone: (withdrawn: number) => void,
): void {
  let submitting = false;
  let sh: ReturnType<typeof sheet> | null = null;
  const errEl = h('p', { class: 'cb-sheet-err' });
  errEl.hidden = true;
  async function doDiscard(): Promise<void> {
    if (submitting) return;
    submitting = true;
    errEl.hidden = true;
    try {
      const r = await ctx.api.post<WithdrawResult>('/proposals/withdraw', body);
      sh?.close();
      onDone(r.withdrawn);
    } catch (err) {
      errEl.textContent =
        err instanceof Error ? err.message : 'discard failed, try again';
      errEl.hidden = false;
      submitting = false;
    }
  }

  sh = sheet({
    title: 'discard recommendations',
    body: h(
      'div',
      { class: 'cb-sheet-body cb-discard-confirm' },
      h('p', { class: 'cb-discard-says' }, discardConfirm(n, view)),
      errEl,
    ),
    actions: [
      {
        label: 'discard',
        fill: true,
        run() {
          void doDiscard();
        },
      },
      {
        label: 'cancel',
        run() {
          sh?.close();
        },
      },
    ],
    onClose() {
      sh = null;
    },
  });
}

// ---- proposal card ----------------------------------------------------------

/** What the decide step gives the recommendation card. */
export interface ProposalCardOpts {
  /** serve's label for the recommended choice ("Close it"). */
  label?: string;
  /** accept replaces the card's own accept (the decide step moves on after it). */
  accept?: () => void;
}

/**
 * proposalCard renders the recommendation: the amber kit card between the
 * decide step's question and its cards. Returns null if the item has no
 * pending proposal.
 *
 * The card head is "<source> recommends · <choice label>" in agent colour
 * (small uppercase mono via the kit's edge:'agent' styling); the body is the
 * proposal's note, its reason, and the keys that act on it (a, r).
 *
 * Buttons: accept (filled), change… (opens seeded decide sheet, posts to
 * /proposals/change), reject (opens openRejectSheet).
 *
 * The returned element carries the class cb-proposal-card for probe targeting.
 */
export function proposalCard(
  ctx: Ctx,
  detail: ItemDetailView,
  onDone?: () => void,
  opts: ProposalCardOpts = {},
): HTMLElement | null {
  const proposal = detail.item.proposal;
  if (!proposal || proposal.state !== 'pending') return null;
  const p = proposal; // narrowed: non-null Proposal with state === 'pending'

  const agent = agentFromSource(p.source);
  // Head: "<agent> recommends · <choice>" (as the list's rows say) — small
  // uppercase mono via kit.
  const head = `${agent} recommends \u00b7 ${opts.label ?? p.disposition}`;

  const lines: (Node | string)[] = [];
  if (p.note) {
    const noteEl = h('p', { class: 'cb-proposal-note' });
    noteEl.textContent = p.note;
    lines.push(noteEl);
  }
  if (p.until) {
    lines.push(h('p', { class: 'cb-proposal-until' }, `until ${p.until}`));
  }
  lines.push(
    h(
      'p',
      { class: 'cb-keyhint' },
      h('kbd', null, 'a'),
      ' accept \u00b7 ',
      h('kbd', null, 'r'),
      ' reject',
    ),
  );

  const bodyEl = h('div', { class: 'cb-proposal-body' }, ...lines);

  function doAccept(): void {
    if (opts.accept) {
      opts.accept();
      return;
    }
    void ctx.api
      .post<AcceptResult>('/proposals/accept', { ids: [p.id] })
      .then(() => {
        onDone?.();
      })
      .catch(() => {});
  }

  function doReject(): void {
    openRejectSheet(ctx, [p.id], () => {
      onDone?.();
    });
  }

  const actions: Button[] = [
    { label: 'accept', fill: true, run: doAccept },
    {
      label: 'change\u2026',
      run() {
        // Open the decide sheet seeded with the proposal's disposition and until,
        // posting to /proposals/change instead of /decide.
        openDecideSheet(
          ctx,
          [p.key],
          () => {
            onDone?.();
          },
          {
            disposition: p.disposition,
            until: p.until ?? '',
            note: p.note ?? '',
          },
          async (disp: string, until: string, note: string) => {
            const result = await ctx.api.post<DecideResult>(
              '/proposals/change',
              { id: p.id, disposition: disp, until, note },
            );
            return result.decided_keys ?? [];
          },
        );
      },
    },
    { label: 'reject', run: doReject },
  ];

  const el = card({ edge: 'agent', head, body: bodyEl, actions });
  el.classList.add('cb-proposal-card');
  return el;
}
