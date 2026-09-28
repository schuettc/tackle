// proposals.ts — accept, change and reject proposals.
//
// Exported:
//   agentFromSource(source)          — "pi:session-id" → "pi"
//   proposalCard(ctx, detail, onDone) — amber card above the decide section
//   openRejectSheet(ctx, ids, onDone) — reason field + POST /proposals/reject
//   bulkProposalFoot(ctx, onDone)    — accept N / reject N… foot; call .update()

import { card, sheet, noteField, h, type Button } from '/_kit/kit.js';
import type {
  AcceptResult,
  DecideResult,
  ItemDetailView,
  RejectResult,
} from './wire.d.ts';
import type { Ctx } from './app.ts';
import { openDecideSheet } from './decide.ts';

// ---- agent name from source -------------------------------------------------

/**
 * agentFromSource extracts the agent name from a proposal source string.
 * "pi:session-id" → "pi"
 * "claude:xyz"    → "claude"
 * "rule:id"       → "rule"
 */
export function agentFromSource(source: string): string {
  const i = source.indexOf(':');
  return i === -1 ? source : source.slice(0, i);
}

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
    title: `reject ${ids.length} proposal${ids.length === 1 ? '' : 's'}`,
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

// ---- proposal card ----------------------------------------------------------

/**
 * proposalCard renders the amber kit card above the decide section.
 * Returns null if the item has no pending proposal.
 *
 * The card head is "<agent> proposes · <disposition>" in agent colour
 * (small uppercase mono via the kit's edge:'agent' styling).
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
): HTMLElement | null {
  const proposal = detail.item.proposal;
  if (!proposal || proposal.state !== 'pending') return null;
  const p = proposal; // narrowed: non-null Proposal with state === 'pending'

  const agent = agentFromSource(p.source);
  // Head: "<agent> proposes · <disposition>" — small uppercase mono via kit.
  const head = `${agent} proposes \u00b7 ${p.disposition}`;

  const lines: (Node | string)[] = [];
  if (p.note) {
    const noteEl = h('p', { class: 'cb-proposal-note' });
    noteEl.textContent = p.note;
    lines.push(noteEl);
  }

  const bodyEl = h('div', { class: 'cb-proposal-body' }, ...lines);

  function doAccept(): void {
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

// ---- bulk proposal foot -----------------------------------------------------

/** Handle returned by bulkProposalFoot; call update() when selection changes. */
export interface BulkProposalFoot {
  el: HTMLElement;
  /**
   * update recalculates which selected items have pending proposals and updates
   * the button labels/visibility.  Pass the loaded items and the current
   * selection ids so the foot can find the intersection.
   */
  update(proposalIds: number[], itemKeys: string[]): void;
}

/**
 * bulkProposalFoot creates the accept N / reject N… foot buttons for the
 * proposed view.  Returns { el, update } — the caller must call update()
 * whenever the selection changes or items are reloaded.
 *
 * onDone(keys) is called after a successful accept/reject with the item keys
 * to deselect.
 */
export function bulkProposalFoot(
  ctx: Ctx,
  onDone: (keys: string[]) => void,
): BulkProposalFoot {
  let _ids: number[] = [];
  let _keys: string[] = [];

  const acceptBtn = h('button', {
    class: 'kit-btn fill cb-prop-accept',
    hidden: true,
    onclick() {
      if (_ids.length === 0) return;
      const ids = [..._ids];
      const keys = [..._keys];
      void ctx.api
        .post<AcceptResult>('/proposals/accept', { ids })
        .then(() => {
          onDone(keys);
        })
        .catch(() => {});
    },
  }) as HTMLButtonElement;
  acceptBtn.textContent = 'accept 0';

  const rejectBtn = h('button', {
    class: 'kit-btn cb-prop-reject',
    hidden: true,
    onclick() {
      if (_ids.length === 0) return;
      const keys = [..._keys];
      openRejectSheet(ctx, [..._ids], () => {
        onDone(keys);
      });
    },
  }) as HTMLButtonElement;
  rejectBtn.textContent = 'reject 0\u2026';

  const el = h('div', { class: 'cb-prop-bulk' }, acceptBtn, rejectBtn);

  function update(proposalIds: number[], itemKeys: string[]): void {
    _ids = proposalIds;
    _keys = itemKeys;
    const n = proposalIds.length;
    if (n === 0) {
      acceptBtn.hidden = true;
      rejectBtn.hidden = true;
    } else {
      acceptBtn.textContent = `accept ${n}`;
      rejectBtn.textContent = `reject ${n}\u2026`;
      acceptBtn.hidden = false;
      rejectBtn.hidden = false;
    }
  }

  return { el, update };
}
