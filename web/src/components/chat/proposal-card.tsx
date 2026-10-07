"use client";

import { useState } from "react";
import { Check, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { CardField, Decision, Proposal, ProposalState } from "@/lib/thread";
import { photoUrl } from "@/lib/uploads";
import { cn } from "@/lib/utils";

const settledCopy: Record<
  Exclude<ProposalState, "pending">,
  { label: string; tone: string; edge: string; Icon: typeof Check }
> = {
  approved: { label: "승인함", tone: "text-approve", edge: "bg-approve", Icon: Check },
  executed: { label: "실행함", tone: "text-approve", edge: "bg-approve", Icon: Check },
  rejected: { label: "반려함", tone: "text-reject", edge: "bg-reject", Icon: X },
  failed: { label: "실패함", tone: "text-reject", edge: "bg-reject", Icon: X },
};

export function ProposalCard({
  proposal,
  onDecide,
}: {
  proposal: Proposal;
  /** May resolve to a reason the decision was refused (a form card missing a value). */
  onDecide: (decision: Decision, edits?: Record<string, string>) => Promise<string | null> | void;
}) {
  const settled =
    proposal.state === "pending" ? null : settledCopy[proposal.state];
  const fields = proposal.card.fields ?? [];
  const form = fields.length > 0;
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(fields.map((f) => [f.key, f.value])),
  );
  const [refusal, setRefusal] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // A number field left empty is the one thing a form card cannot be approved
  // without (the price of a listing): the operator fills it in, never the model.
  const missing = fields.some((f) => f.kind === "number" && !(values[f.key] ?? "").replace(/[^\d]/g, ""));

  async function decide(d: Decision) {
    setRefusal(null);
    setBusy(true);
    const reason = await onDecide(d, form && d === "approved" ? values : undefined);
    setBusy(false);
    if (reason) setRefusal(reason);
  }
  // Claude Code's own tools are shown as the literal call; a module's action
  // is shown as the change it makes.
  const literal = proposal.action.kind.startsWith("claude.");

  return (
    <article
      // The thread's standing bar watches for these: a decision on screen
      // does not need announcing at the top of the page.
      data-pending-card={settled ? undefined : ""}
      className={cn(
        "relative mt-4 w-full max-w-[34rem] overflow-hidden rounded-xl border border-edge bg-glass backdrop-blur-md",
        "transition-colors duration-300",
        settled && "border-edge-soft",
      )}
    >
      {settled ? (
        <span
          aria-hidden
          className={cn("anim-edge absolute inset-y-0 left-0 w-[2px]", settled.edge)}
        />
      ) : null}

      <div className="px-4 pt-3.5 pb-4 sm:px-5">
        {/* Only an undecided proposal announces itself; a settled one is read by its edge. */}
        {settled ? null : (
          <div className="mb-2 flex items-center gap-2 text-[11px] text-faint">
            <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
            <span>결재 대기</span>
          </div>
        )}

        <h3 className="text-[14.5px] leading-snug font-medium text-foreground">
          {proposal.card.title}
        </h3>

        {proposal.card.images?.length ? <CardPhotos names={proposal.card.images} /> : null}

        {form && !settled ? (
          <FormFields fields={fields} values={values} onChange={(k, v) => setValues((cur) => ({ ...cur, [k]: v }))} />
        ) : null}
        {form && settled ? <FormSummary fields={fields} /> : null}

        {/* Mono is reserved for a literal command the CLI would run. A
            module renders its change as a sentence, and setting that in mono
            would blur the line between "this is text" and "this is code".

            That split decides wrapping too: a command must not be re-wrapped,
            because a broken line changes what it says, so it scrolls. A
            sentence has no such constraint and wraps, which is what a narrow
            screen needs. */}
        {form && settled ? null : (
        <pre
          className={cn(
            "scrollbar-hairline mt-3 rounded-md bg-well px-3 py-2.5 leading-relaxed text-dim",
            literal
              ? "overflow-x-auto font-mono text-[12px]"
              : "tnum font-sans text-[13px] whitespace-pre-wrap",
          )}
        >
          <code>{proposal.card.body}</code>
        </pre>
        )}

        <p className="mt-2.5 text-[12px] leading-normal text-dim">
          {proposal.card.consequence}
        </p>
      </div>

      <div className="border-t border-edge-soft px-3 py-2.5 sm:px-4">
        {settled ? (
          <p
            className={cn(
              "anim-settle flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[11.5px]",
              settled.tone,
            )}
          >
            <settled.Icon aria-hidden className="size-3.5" />
            {settled.label}
            <span className="tnum text-faint">· {proposal.decidedAt}</span>
            {proposal.note ? <span className="text-faint">· {proposal.note}</span> : null}
          </p>
        ) : (
          /* This is the decision the whole product exists to collect, and on
             a phone it was a pair of 32px targets sitting a thumb-width apart.
             Full width, 48px, and a real gap between them: reaching for 승인
             must not be able to land on 반려. */
          <div className="grid grid-cols-2 gap-2 sm:flex sm:items-center sm:justify-end sm:gap-1.5">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void decide("rejected")}
              disabled={busy}
              className="h-12 text-[13.5px] text-dim hover:bg-reject/10 hover:text-reject sm:h-8 sm:px-3 sm:text-[0.8rem]"
            >
              반려
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void decide("approved")}
              disabled={busy || missing}
              className="h-12 border-edge bg-glass-raised text-[13.5px] text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve sm:h-8 sm:px-3.5 sm:text-[0.8rem] dark:bg-glass-raised dark:hover:bg-approve/12"
            >
              승인
            </Button>
          </div>
        )}
        {!settled && (refusal || missing) ? (
          <p role={refusal ? "alert" : undefined} className={cn("mt-2 px-1 text-[12px]", refusal ? "text-reject" : "text-faint")}>
            {refusal ?? "가격을 적으면 승인할 수 있습니다."}
          </p>
        ) : null}
      </div>
    </article>
  );
}

const input =
  "w-full rounded-lg border border-edge-soft bg-well px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:border-edge focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-[13.5px]";

/** The listing's photos as it will show them: the first one is the cover. */
function CardPhotos({ names }: { names: string[] }) {
  return (
    <ul className="scrollbar-none -mx-4 mt-3 flex gap-1.5 overflow-x-auto px-4 sm:-mx-5 sm:px-5" aria-label="올릴 사진">
      {names.map((name, i) => (
        <li key={name} className="relative shrink-0">
          {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
          <img
            src={photoUrl(name)}
            alt={i === 0 ? "대표 사진" : `사진 ${i + 1}`}
            loading="lazy"
            className={cn("rounded-lg border border-edge-soft bg-well object-cover", i === 0 ? "size-24" : "size-20 mt-4")}
          />
          {i === 0 ? (
            <span className="absolute bottom-1 left-1 rounded bg-background/80 px-1.5 py-px text-[10.5px] text-dim">대표</span>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

function FormFields({
  fields,
  values,
  onChange,
}: {
  fields: CardField[];
  values: Record<string, string>;
  onChange: (key: string, value: string) => void;
}) {
  return (
    <div className="mt-4 space-y-3.5">
      {fields.map((f) => {
        const id = `field-${f.key}`;
        const value = values[f.key] ?? "";
        return (
          <div key={f.key}>
            <label htmlFor={f.kind === "select" ? undefined : id} className="mb-1.5 block text-[12px] text-dim">
              {f.label}
            </label>
            {f.kind === "number" ? (
              // The price is the decision on this card; it gets the type size.
              <div className="relative">
                <input
                  id={id}
                  inputMode="numeric"
                  autoComplete="off"
                  value={value ? Number(value.replace(/[^\d]/g, "") || 0).toLocaleString("ko-KR") : ""}
                  onChange={(e) => onChange(f.key, e.target.value.replace(/[^\d]/g, ""))}
                  placeholder="얼마에 팔까요"
                  className={cn(input, "tnum h-12 pe-9 text-[20px] font-medium sm:h-11 sm:text-[18px]")}
                />
                <span aria-hidden className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-[14px] text-faint">
                  원
                </span>
              </div>
            ) : f.kind === "textarea" ? (
              <textarea
                id={id}
                rows={6}
                value={value}
                onChange={(e) => onChange(f.key, e.target.value)}
                className={cn(input, "scrollbar-hairline resize-y py-2.5 leading-relaxed")}
              />
            ) : f.kind === "select" ? (
              <div role="radiogroup" aria-label={f.label} className="flex flex-wrap gap-1.5">
                {(f.options ?? []).map((o) => {
                  const on = value === o;
                  return (
                    <button
                      key={o}
                      type="button"
                      role="radio"
                      aria-checked={on}
                      onClick={() => onChange(f.key, o)}
                      className={cn(
                        "min-h-10 rounded-lg border px-3 text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-8 sm:text-[12.5px]",
                        on ? "border-edge bg-glass-raised text-foreground" : "border-edge-soft text-dim hover:bg-glass",
                      )}
                    >
                      {o}
                    </button>
                  );
                })}
              </div>
            ) : (
              <input
                id={id}
                value={value}
                onChange={(e) => onChange(f.key, e.target.value)}
                className={cn(input, "h-11 sm:h-9")}
              />
            )}
          </div>
        );
      })}
    </div>
  );
}

/** A decided form card shows what was approved, not the inputs. */
function FormSummary({ fields }: { fields: CardField[] }) {
  const get = (k: string) => fields.find((f) => f.key === k)?.value ?? "";
  const price = get("priceKrw");
  return (
    <p className="mt-2 text-[13.5px] text-dim">
      <span className="text-foreground/90">{get("title")}</span>
      {price ? <span className="tnum ms-2">{Number(price).toLocaleString("ko-KR")}원</span> : null}
    </p>
  );
}
