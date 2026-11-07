"use client";

import { useEffect, useState } from "react";
import { Check, X } from "lucide-react";
import { HeldBack, HoldBar, Sending, Sent, holdsMoney, useHold } from "@/components/chat/hold";
import { RejectForm } from "@/components/chat/reject-form";
import { Button } from "@/components/ui/button";
import { CodeBlock, CodeBlockContent, CodeBlockCopyButton } from "@/components/ui/code-block";
import { FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Lightbox } from "@/components/ui/lightbox";
import { SecretInput } from "@/components/ui/secret-input";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { isPrivateField, type CardField, type Decision, type Proposal, type ProposalState } from "@/lib/thread";
import { ACTION, MODULE, ofModule } from "@/lib/kinds";
import { isLiteral, readableBody, readableConsequence, readableTitle } from "@/lib/card-body";
import { heardBack, sentOn } from "@/lib/decide";
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
  onDecide: (decision: Decision, edits?: Record<string, string>, reason?: string) => Promise<string | null> | void;
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
  const [asking, setAsking] = useState(false);
  // Taken, and the card not back with it yet (lib/decide.ts): said in the
  // buttons' place until it is, here or wherever the card is drawn again.
  const [sentAs, setSentAs] = useState(() => sentOn(proposal.id));
  const onItsWay = settled ? undefined : sentAs;
  useEffect(() => {
    if (settled) heardBack(proposal.id);
  }, [settled, proposal.id]);
  // A required field left empty (the price of a listing, say) is the one thing
  // a form card cannot be approved without: the operator fills it in, never the model.
  const missing = fields.filter((f) => f.required && !(values[f.key] ?? "").trim());

  async function decide(d: Decision, because?: string) {
    setRefusal(null);
    setBusy(true);
    const reason = await onDecide(d, form && d === "approved" ? values : undefined, because);
    setBusy(false);
    if (reason) {
      setRefusal(reason);
      return;
    }
    setSentAs(sentOn(proposal.id));
    // A typed password is not kept on screen once it has been handed over.
    setValues((cur) => Object.fromEntries(Object.entries(cur).map(([k, v]) => [k, fields.some((f) => f.key === k && isPrivateField(f)) ? "" : v])));
  }
  // Money: the approval waits five seconds with 되돌리기 on the card (chat/hold.tsx).
  const hold = useHold(proposal.id, () => void decide("approved"));
  // A command or a file's content the CLI would run is shown as the literal
  // call; anything else - a module's change, a job's or the browser's
  // arguments - as words (lib/card-body.ts).
  const literal = isLiteral(proposal);

  return (
    <article
      // The thread's standing bar watches for these: a decision on screen
      // does not need announcing at the top of the page.
      data-pending-card={settled ? undefined : ""}
      // A tapped push about this card scrolls here (chat/thread.tsx).
      data-proposal-id={proposal.id}
      className={cn(
        "relative mt-4 w-full max-w-[34rem] overflow-hidden rounded-xl border border-edge bg-glass",
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
          <div className="mb-2 flex items-center gap-2 text-meta text-faint">
            <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
            <span>{busy ? "보내는 중" : onItsWay ? "반영 중" : "결재 대기"}</span>
          </div>
        )}

        <h3 className="text-body leading-snug font-medium text-foreground">
          {readableTitle(proposal.card.title)}
        </h3>

        {proposal.card.images?.length ? (
          <CardPhotos names={proposal.card.images} cover={!ofModule(proposal.action.kind, MODULE.mind)} />
        ) : null}

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
        {form && settled ? null : literal ? (
          <CodeBlock className="mt-3 text-dim">
            <CodeBlockContent>
              <code>{readableBody(proposal.card.body)}</code>
            </CodeBlockContent>
            <CodeBlockCopyButton value={readableBody(proposal.card.body)} label="명령 복사" />
          </CodeBlock>
        ) : (
          <pre className="scrollbar-hairline tnum mt-3 rounded-md bg-well px-3 py-2.5 font-sans text-ui whitespace-pre-wrap text-dim [&_code]:font-sans">
            <code>{readableBody(proposal.card.body)}</code>
          </pre>
        )}

        <p className="mt-2.5 text-aside leading-normal text-dim">
          {readableConsequence(proposal.card.consequence)}
        </p>
      </div>

      <div className="border-t border-edge-soft px-3 py-2.5 sm:px-4">
        {settled ? (
          <p
            className={cn(
              "anim-settle flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-meta",
              settled.tone,
            )}
          >
            <settled.Icon aria-hidden className="size-3.5" />
            {settled.label}
            <span className="tnum text-faint">· {proposal.decidedAt}</span>
            {proposal.note ? <span className="text-faint">· {proposal.note}</span> : null}
            {proposal.reason ? <span className="text-faint">· 이유: {proposal.reason}</span> : null}
            {/* Where a no goes: mind's own question, turned down, is not put
                again for a while (its topic two weeks, an entry like it a
                month, a place told 다시 묻지 마 never). */}
            {proposal.state === "rejected" && proposal.action.kind === ACTION.mindApprove ? (
              <span className="w-full text-faint">같은 제안은 한동안 다시 묻지 않아요.</span>
            ) : null}
          </p>
        ) : hold.holding ? (
          <HoldBar left={hold.left} onCancel={hold.cancel} />
        ) : busy ? (
          <Sending />
        ) : onItsWay ? (
          <Sent decision={onItsWay} />
        ) : asking ? (
          <RejectForm busy={busy} onCancel={() => setAsking(false)} onReject={(why) => void decide("rejected", why)} />
        ) : (
          /* This is the decision the whole product exists to collect, and on
             a phone it was a pair of 32px targets sitting a thumb-width apart.
             Full width, 48px, and a real gap between them: reaching for 승인
             must not be able to land on 반려. */
          <div>
            {hold.interrupted ? <HeldBack /> : null}
            <div className="grid grid-cols-2 gap-2 sm:flex sm:items-center sm:justify-end sm:gap-1.5">
              <Button
                variant="reject"
                size="pair"
                onClick={() => setAsking(true)}
                disabled={busy}
              >
                반려
              </Button>
              <Button
                variant="approve"
                size="pair"
                onClick={() => (holdsMoney(proposal) ? hold.start() : void decide("approved"))}
                disabled={busy || missing.length > 0}
              >
                {hold.interrupted ? "다시 승인" : "승인"}
              </Button>
            </div>
          </div>
        )}
        {!settled && (refusal || missing.length > 0) ? (
          <p role={refusal ? "alert" : undefined} className={cn("mt-2 px-1 text-aside", refusal ? "text-reject" : "text-faint")}>
            {refusal ?? `${missing.map((f) => f.label).join(", ")}을(를) 적으면 승인할 수 있어요.`}
          </p>
        ) : null}
      </div>
    </article>
  );
}

/**
 * The photos a card is about. A listing's are shown as it will show them, the
 * first the cover; the ones a question asks about (cover false) are all alike.
 * A tap opens them full screen, one after another under the finger - small
 * tiles alone are no way to see one whole.
 */
function CardPhotos({ names, cover }: { names: string[]; cover: boolean }) {
  const [open, setOpen] = useState<number | null>(null);
  const alt = (i: number) => (cover && i === 0 ? "대표 사진" : `사진 ${i + 1}`);
  return (
    <>
      <ul className="scrollbar-none -mx-4 mt-3 flex gap-1.5 overflow-x-auto px-4 sm:-mx-5 sm:px-5" aria-label={cover ? "올릴 사진" : "사진"}>
        {names.map((name, i) => (
          <li key={name} className="relative shrink-0">
            <button
              type="button"
              onClick={() => setOpen(i)}
              aria-label={`${alt(i)} 크게 보기`}
              className={cn("block overflow-hidden rounded-lg bg-well focus-ring", cover && i > 0 && "mt-4")}
            >
              {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
              <img
                src={photoUrl(name)}
                alt={alt(i)}
                loading="lazy"
                className={cn("block object-cover", !cover ? "size-24" : i === 0 ? "size-24" : "size-20")}
              />
            </button>
            {cover && i === 0 ? (
              <span className="pointer-events-none absolute bottom-1 left-1 rounded bg-background/80 px-1.5 py-px text-tiny text-dim">대표</span>
            ) : null}
          </li>
        ))}
      </ul>
      {open !== null ? (
        <Lightbox items={names.map((name, i) => ({ src: photoUrl(name), alt: alt(i) }))} start={open} onClose={() => setOpen(null)} />
      ) : null}
    </>
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
            {f.kind === "select" || f.kind === "multi" ? (
              <p id={`${id}-label`} className="mb-1.5 text-aside text-dim">
                {f.label}
              </p>
            ) : (
              <FieldLabel htmlFor={id} className="mb-1.5">
                {f.label}
              </FieldLabel>
            )}
            {f.kind === "number" ? (
              // The price is the decision on this card; it gets the type size.
              <div className="relative">
                <Input
                  id={id}
                  inputMode="numeric"
                  autoComplete="off"
                  value={value ? Number(value.replace(/[^\d]/g, "") || 0).toLocaleString("ko-KR") : ""}
                  onChange={(e) => onChange(f.key, e.target.value.replace(/[^\d]/g, ""))}
                  placeholder="숫자로 적기"
                  className="tnum h-12 pe-9 text-lead font-medium mouse:h-11"
                />
                <span aria-hidden className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-body text-faint">
                  원
                </span>
              </div>
            ) : f.kind === "textarea" ? (
              <Textarea
                id={id}
                rows={6}
                value={value}
                onChange={(e) => onChange(f.key, e.target.value)}
                className="scrollbar-hairline min-h-[11rem] resize-y"
              />
            ) : f.kind === "multi" ? (
              <ToggleGroup
                type="multiple"
                aria-labelledby={`${id}-label`}
                variant="chip"
                size="touch"
                spacing={1.5}
                // Kept in the order the card offers them.
                value={value ? value.split(",").filter(Boolean) : []}
                onValueChange={(on) => onChange(f.key, (f.options ?? []).filter((o) => on.includes(o)).join(","))}
                className="flex-wrap"
              >
                {(f.options ?? []).map((o) => (
                  <ToggleGroupItem key={o} value={o} className="hover:bg-glass">
                    {o}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            ) : f.kind === "select" ? (
              <ToggleGroup
                type="single"
                aria-labelledby={`${id}-label`}
                variant="tile"
                size="touch"
                spacing={1.5}
                value={value}
                onValueChange={(o) => {
                  if (o) onChange(f.key, o);
                }}
                className="flex-wrap"
              >
                {(f.options ?? []).map((o) => (
                  <ToggleGroupItem key={o} value={o}>
                    {o}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            ) : f.kind === "secret" ? (
              <SecretInput id={id} value={value} onChange={(v) => onChange(f.key, v)} />
            ) : (
              <Input
                id={id}
                value={value}
                onChange={(e) => onChange(f.key, e.target.value)}
                // A login's username: no autocorrect, no capitalised first letter.
                {...(isPrivateField(f)
                  ? { autoComplete: "username", autoCapitalize: "none", autoCorrect: "off", spellCheck: false }
                  : {})}
                className="mouse:text-ui"
              />
            )}
            {f.hint ? <p className="mt-1.5 text-meta leading-relaxed whitespace-pre-wrap text-faint">{f.hint}</p> : null}
          </div>
        );
      })}
    </div>
  );
}

/** A decided form card shows what was approved, not the inputs. */
function FormSummary({ fields }: { fields: CardField[] }) {
  const shown = fields.filter((f) => f.value && f.kind !== "textarea" && !isPrivateField(f)).slice(0, 4);
  if (shown.length === 0) return null;
  return (
    <dl className="mt-2 space-y-0.5 text-ui">
      {shown.map((f) => (
        <div key={f.key} className="flex gap-2">
          <dt className="shrink-0 text-faint">{f.label}</dt>
          <dd className="tnum min-w-0 truncate text-ink">
            {f.kind === "number" ? Number(f.value).toLocaleString("ko-KR") : f.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}
