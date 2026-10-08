// proposals.ts — accept, change and reject proposals.
//
// Exported:
//   agentFromSource(source)            — "pi:session-id" → "pi"
//   proposalCard(ctx, detail, onDone)   — the recommendation card in the decide step
//   openRejectSheet(ctx, ids, onDone)   — reason field + POST /proposals/reject
//   bulkProposalActions(ctx, onDone)    — accept N / reject N… foot; call .update()
//   agreeWithAll(onAgree)               — the list foot's "agree with all N" lines

import { card, sheet, noteField, h, type Button } from '/_kit/kit.js';
import type {
  AcceptResult,
  DecideResult,
  ItemDetailView,
  LookIntoVocab,
  RejectResult,
} from './wire.d.ts';
import type { Ctx } from './app.ts';
import { openDecideSheet } from './decide.ts';
import {
  agentFromSource,
  agreeText,
  lookIntoAll,
  pluralize,
  type AgreeGroup,
} from './decide-math.ts';

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

// ---- bulk proposal foot -----------------------------------------------------

/** Handle returned by bulkProposalActions; call update() when selection changes. */
export interface BulkProposalActions {
  el: HTMLElement;
  /**
   * update recalculates which selected items have pending proposals and updates
   * the button labels/visibility.  Pass the loaded items and the current
   * selection ids so the foot can find the intersection.
   */
  update(proposalIds: number[], itemKeys: string[]): void;
}

/**
 * bulkProposalActions creates the accept N / reject N… foot buttons for the
 * proposed view.  Returns { el, update } — the caller must call update()
 * whenever the selection changes or items are reloaded.
 *
 * onDone(keys) is called after a successful accept/reject with the item keys
 * to deselect.
 */
export function bulkProposalActions(
  ctx: Ctx,
  onDone: (keys: string[]) => void,
): BulkProposalActions {
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

// ---- agree with all ----------------------------------------------------------

/** Handle returned by agreeWithAll; call update() when the rows change. */
export interface AgreeWithAll {
  el: HTMLElement;
  update(groups: AgreeGroup[], look?: LookAll | null): void;
}

/** What "look into all N" needs: the attached session's name, serve's
 * words, and the keys a session is looking into already. */
export interface LookAll {
  session: string;
  words: LookIntoVocab;
  looking: (key: string) => boolean;
}

/**
 * agreeWithAll is the list foot's band of groups: for each group of two or
 * more rows whose recommendations agree, "‹agent› recommends ‹label› for
 * ‹n›" and a button that accepts exactly that group's proposals ("agree
 * with all n"; an outward choice reads "send all n to To apply", since
 * accepting it only puts it in To apply). onAgree does the accepting; the
 * button is disabled while it runs.
 *
 * With a session attached (look), each group also offers "ask ‹session›
 * to look into all n": onLook sends that session one message naming the
 * group's keys (serve's message_many). It is disabled while it runs, and
 * while every row of the group is being looked into already.
 */
export function agreeWithAll(
  onAgree: (g: AgreeGroup) => Promise<void>,
  onLook: (g: AgreeGroup, message: string) => Promise<void>,
): AgreeWithAll {
  const el = h('div', { class: 'cb-agree', hidden: true });

  function update(groups: AgreeGroup[], look?: LookAll | null): void {
    el.replaceChildren(
      ...groups.map((g) => {
        const t = agreeText(g);
        const btn = h(
          'button',
          {
            class: 'kit-btn cb-agree-btn',
            type: 'button',
            onclick() {
              btn.disabled = true;
              void onAgree(g).finally(() => {
                btn.disabled = false;
              });
            },
          },
          t.action,
        ) as HTMLButtonElement;
        let lookBtn: HTMLButtonElement | null = null;
        if (look) {
          const w = lookIntoAll(look.words, look.session, g.keys);
          const all = g.keys.every((k) => look.looking(k));
          lookBtn = h(
            'button',
            {
              class: 'kit-btn cb-agree-look',
              type: 'button',
              disabled: all,
              onclick() {
                if (lookBtn!.disabled) return;
                lookBtn!.disabled = true;
                void onLook(g, w.message).finally(() => {
                  lookBtn!.disabled = false;
                });
              },
            },
            w.action,
          ) as HTMLButtonElement;
        }
        return h(
          'div',
          {
            class: 'cb-agree-line',
            'data-d': g.disposition,
            'data-ids': g.ids.join(' '),
          },
          h('span', { class: 'cb-agree-says' }, t.says),
          h('span', { class: 'cb-agree-acts' }, btn, lookBtn),
        );
      }),
    );
    el.hidden = groups.length === 0;
  }

  return { el, update };
}
