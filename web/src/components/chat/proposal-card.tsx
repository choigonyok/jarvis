"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Check, Minus, X } from "lucide-react";
import { CardAnswers, CardFacts, answersOf } from "@/components/chat/card-answers";
import { Sending, Sent } from "@/components/chat/hold";
import { CodeBlock, CodeBlockContent, CodeBlockCopyButton } from "@/components/ui/code-block";
import { FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Lightbox } from "@/components/ui/lightbox";
import { SecretInput } from "@/components/ui/secret-input";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { isPrivateField, type CardChoice, type CardField, type Decision, type Proposal } from "@/lib/thread";
import { ACTION, MODULE, ofModule } from "@/lib/kinds";
import { isLiteral, readableBody, readableConsequence, readableTitle } from "@/lib/card-body";
import { sentChoice, typedOn, useDecision } from "@/lib/decide";
import { keepValue, keptValue } from "@/lib/keep";
import { asksYesNo, chosenOf, outcomeOf, turnedDown, type Outcome } from "@/lib/outcome";
import { photoUrl } from "@/lib/uploads";
import { cn } from "@/lib/utils";

// Red is 반려 and 실패 only. A card taken down without anyone deciding it (its
// job's turn came) and a question's 아니요 are neither: they read in the
// quiet grey of everything that is simply over.
const settledCopy: Record<Exclude<Outcome, "pending">, { label: string; tone: string; edge: string; Icon: typeof Check }> = {
  approved: { label: "승인함", tone: "text-approve", edge: "bg-approve", Icon: Check },
  executed: { label: "실행함", tone: "text-approve", edge: "bg-approve", Icon: Check },
  rejected: { label: "반려함", tone: "text-reject", edge: "bg-reject", Icon: X },
  failed: { label: "실패함", tone: "text-reject", edge: "bg-reject", Icon: X },
  yes: { label: "예", tone: "text-approve", edge: "bg-approve", Icon: Check },
  no: { label: "아니요", tone: "text-dim", edge: "bg-dim", Icon: Minus },
  closed: { label: "닫힘", tone: "text-dim", edge: "bg-dim", Icon: Minus },
};

export function ProposalCard({
  proposal,
  onDecide,
}: {
  proposal: Proposal;
  /** May resolve to a reason the decision was refused (a form card missing a value). */
  onDecide: (
    decision: Decision,
    edits?: Record<string, string>,
    reason?: string,
    choice?: Pick<CardChoice, "id" | "label">,
  ) => Promise<string | null> | void;
}) {
  const outcome = outcomeOf(proposal);
  // An answer of the card's own says how it ended in its own words; one that
  // is the other answer (아니요, 순서대로) reads in grey, as a question's 아니요.
  const chosen = chosenOf(proposal);
  const settled =
    outcome === "pending"
      ? null
      : chosen && outcome !== "failed" && outcome !== "closed"
        ? { ...settledCopy[chosen.decision === "rejected" ? (chosen.tone === "neutral" ? "no" : "rejected") : outcome === "yes" ? "yes" : outcome], label: chosen.label }
        : settledCopy[outcome];
  // A question with two answers (the 먼저 card): 예 and 아니요, no reason asked.
  const yesNo = asksYesNo(proposal);
  const fields = proposal.card.fields ?? [];
  const form = fields.length > 0;
  // What was typed into the fields is kept while the app is open, as an order
  // half typed is (lib/keep.ts): the card is drawn again after another tab,
  // and a price typed and a description rewritten on a phone were gone. A
  // private field's value (a login) is not - it is held no longer than it has
  // to be - and nothing is once the card is decided.
  const typedKey = typedOn(proposal.id, "values");
  const [values, setValues] = useState<Record<string, string>>(() => ({
    ...Object.fromEntries(fields.map((f) => [f.key, f.value])),
    ...keptValue<Record<string, string>>(typedKey),
  }));
  const waiting = outcome === "pending";
  const cardFields = proposal.card.fields;
  useEffect(() => {
    const typed = (cardFields ?? []).filter((f) => !isPrivateField(f) && values[f.key] !== f.value);
    keepValue(typedKey, waiting && typed.length > 0 ? Object.fromEntries(typed.map((f) => [f.key, values[f.key]])) : undefined);
  }, [typedKey, cardFields, values, waiting]);
  // Taken, and the card not back with it yet (lib/decide.ts): said in the
  // buttons' place until it is, here or wherever the card is drawn again.
  const decision = useDecision(proposal, (d, reason, choice) =>
    onDecide(d, form && d === "approved" ? values : undefined, reason, choice),
  );
  // A required field left empty (the price of a listing, say) is the one thing
  // a form card cannot be approved without: the operator fills it in, never the
  // model - or an answer of the card's own that fills it in.
  const missingFor = (c?: CardChoice) =>
    fields.filter((f) => f.required && !(c?.values?.[f.key] ?? values[f.key] ?? "").trim());
  const missing = missingFor();
  // Said under the buttons only when no answer that runs can be chosen yet.
  const runs = answersOf(proposal).filter((c) => c.decision === "approved");
  const stuck = runs.length > 0 && runs.every((c) => missingFor(c).length > 0);
  // A card that offers only answers, none of them a turn-down, is a question.
  const question = yesNo || Boolean(proposal.card.choices?.length && proposal.card.choices.every((c) => c.tone !== "reject"));

  async function decide(c: CardChoice, because?: string) {
    if (!(await decision.answer(c, because))) return;
    // A typed password is not kept on screen once it has been handed over.
    setValues((cur) => Object.fromEntries(Object.entries(cur).map(([k, v]) => [k, fields.some((f) => f.key === k && isPrivateField(f)) ? "" : v])));
  }
  // A command or a file's content the CLI would run is shown as the literal
  // call; anything else - a module's change, a job's or the browser's
  // arguments - as words (lib/card-body.ts).
  const literal = isLiteral(proposal);
  // What Jarvis says and what the code knows, apart: the words in prose, the
  // facts in the well. A card from before cards had facts keeps its body in
  // the well, as it was drawn.
  const facts = proposal.card.facts ?? [];
  const apart = facts.length > 0 || Boolean(proposal.card.writer);
  const body = readableBody(proposal.card.body).trim();

  return (
    <article
      // The thread's standing bar watches for these: a decision on screen
      // does not need announcing at the top of the page.
      data-pending-card={settled ? undefined : ""}
      // A tapped push about this card scrolls here (chat/thread-scroll.tsx).
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
            <span>{decision.busy ? "보내는 중" : decision.sent ? "반영 중" : question ? "답 대기" : "결재 대기"}</span>
          </div>
        )}

        <h3 className="text-body leading-snug font-medium text-foreground">
          {readableTitle(proposal.card.title)}
        </h3>

        {apart && body ? <p className="mt-1.5 text-ui leading-relaxed whitespace-pre-wrap text-ink">{body}</p> : null}
        {apart ? <CardFacts facts={facts} className="mt-3" /> : null}

        {proposal.card.images?.length ? (
          <CardPhotos
            names={proposal.card.images}
            cover={!proposal.card.screen && !ofModule(proposal.action.kind, MODULE.mind)}
            screen={Boolean(proposal.card.screen)}
          />
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
        {form && settled ? null : apart ? null : literal ? (
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

        {proposal.card.consequence ? (
          <p className="mt-2.5 text-aside leading-normal text-dim">{readableConsequence(proposal.card.consequence)}</p>
        ) : null}
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
            {turnedDown(proposal) && proposal.action.kind === ACTION.mindApprove ? (
              <span className="w-full text-faint">같은 제안은 한동안 다시 묻지 않아요.</span>
            ) : null}
          </p>
        ) : decision.busy ? (
          <Sending />
        ) : decision.sent ? (
          <Sent decision={decision.sent} yesNo={yesNo} label={sentChoice(proposal.id)} />
        ) : (
          /* This is the decision the whole product exists to collect, and on
             a phone it was a pair of 32px targets sitting a thumb-width apart.
             Full width, 48px, and a real gap between them: reaching for 승인
             must not be able to land on 반려. */
          <CardAnswers
            proposal={proposal}
            size="pair"
            busy={decision.busy}
            blocked={(c) => c.decision === "approved" && missingFor(c).length > 0}
            onAnswer={(c, why) => void decide(c, why)}
          />
        )}
        {/* What a field still needs is said with the buttons, not under a
            decision on its way: a login's typed values are cleared once sent. */}
        {!settled && (decision.refusal || (stuck && !decision.busy && !decision.sent)) ? (
          <p role={decision.refusal ? "alert" : undefined} className={cn("mt-2 px-1 text-aside", decision.refusal ? "text-reject" : "text-faint")}>
            {decision.refusal ?? `${missing.map((f) => (f.kind === "number" ? money(f).label : f.label)).join(", ")}을(를) 적으면 승인할 수 있어요.`}
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
 * tiles alone are no way to see one whole. With `screen` the first is the
 * browser's screen the card asks about - a 보안문자 to read, a page waiting
 * for a code - drawn whole at the card's width, since the answer is read off
 * it; the rest stay tiles.
 */
function CardPhotos({ names, cover, screen = false }: { names: string[]; cover: boolean; screen?: boolean }) {
  const [open, setOpen] = useState<number | null>(null);
  const alt = (i: number) => (screen && i === 0 ? "물어보는 화면" : cover && i === 0 ? "대표 사진" : `사진 ${i + 1}`);
  const thumbs = screen ? names.slice(1) : names;
  const from = names.length - thumbs.length;
  return (
    <>
      {screen ? (
        <button type="button" onClick={() => setOpen(0)} aria-label={`${alt(0)} 크게 보기`} className="mt-3 block w-full overflow-hidden rounded-lg bg-well focus-ring">
          {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
          <img src={photoUrl(names[0])} alt={alt(0)} className="block h-auto max-h-[60vh] w-full object-contain" />
        </button>
      ) : null}
      {thumbs.length ? (
        <ul className="scrollbar-none -mx-4 mt-3 flex gap-1.5 overflow-x-auto px-4 sm:-mx-5 sm:px-5" aria-label={cover ? "올릴 사진" : "사진"}>
          {thumbs.map((name, t) => {
            const i = t + from;
            return (
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
            );
          })}
        </ul>
      ) : null}
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
        const { label, won } = f.kind === "number" ? money(f) : { label: f.label, won: false };
        return (
          <div key={f.key}>
            {f.kind === "select" || f.kind === "multi" ? (
              <p id={`${id}-label`} className="mb-1.5 text-aside text-dim">
                {f.label}
              </p>
            ) : (
              <FieldLabel htmlFor={id} className="mb-1.5">
                {label}
                {/* The 원 after the number is not read out: the label says it. */}
                {won ? <span className="sr-only">(원)</span> : null}
              </FieldLabel>
            )}
            {f.kind === "number" ? (
              // The price is the decision on this card; it gets the type size.
              <NumberField id={id} value={value} won={won} onChange={(v) => onChange(f.key, v)} />
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

/**
 * Whether a number field is money, and its label then. To the agent a field
 * is only a number (core/action Field), so this goes by its words: a label
 * ending in "(원)" - taken off, so 원 is said once, after the number - one
 * that says 금액 or 가격, or a price's or an amount's key. A label with
 * brackets of another kind ("수량(개)") says what it needs to, and anything
 * else stands bare: a count with 원 after it reads as a price.
 */
function money(f: CardField): { label: string; won: boolean } {
  const own = /\s*\(원\)\s*$/;
  if (own.test(f.label)) return { label: f.label.replace(own, ""), won: true };
  if (/\)\s*$/.test(f.label)) return { label: f.label, won: false };
  return { label: f.label, won: /금액|가격/.test(f.label) || /price|amount/i.test(f.key) };
}

/**
 * A whole number, its thousands marked as it is typed. Marked again after
 * every key, the field had its caret thrown to the end: a digit changed in the
 * middle of 1,250,000 went on after the last 0, and the price approved was
 * another one. The caret is put back after as many digits as stood before it.
 */
function NumberField({
  id,
  value,
  won,
  onChange,
}: {
  id: string;
  value: string;
  /** Money: 원 after the number. */
  won: boolean;
  onChange: (digits: string) => void;
}) {
  const ref = useRef<HTMLInputElement>(null);
  // The digits before the caret as it was typed, until the number is drawn marked.
  const caret = useRef<number | null>(null);
  const shown = grouped(value);
  useLayoutEffect(() => {
    const el = ref.current;
    const n = caret.current;
    caret.current = null;
    if (!el || n === null || document.activeElement !== el) return;
    const at = afterDigits(shown, n);
    el.setSelectionRange(at, at);
  });
  return (
    <div className="relative">
      <Input
        ref={ref}
        id={id}
        inputMode="numeric"
        autoComplete="off"
        value={shown}
        onChange={(e) => {
          const el = e.target;
          caret.current = el.value.slice(0, el.selectionStart ?? el.value.length).replace(/\D/g, "").length;
          onChange(el.value.replace(/\D/g, ""));
        }}
        // Leading zeros stay while typing - 1,050,000 with its 1 taken out is
        // 050,000 until a 2 goes in front - and go once the field is left.
        onBlur={() => {
          const digits = value.replace(/\D/g, "");
          const bare = digits.replace(/^0+(?=\d)/, "");
          if (bare !== digits) onChange(bare);
        }}
        placeholder="숫자로 적기"
        className={cn("tnum h-12 text-lead font-medium mouse:h-11", won && "pe-9")}
      />
      {won ? (
        <span aria-hidden className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-body text-faint">
          원
        </span>
      ) : null}
    </div>
  );
}

/** The digits of a value with their thousands marked - by hand: Number rounds past 16 digits. */
function grouped(value: string): string {
  return value.replace(/\D/g, "").replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/** Where the caret stands after the first n digits of a marked number. */
function afterDigits(text: string, n: number): number {
  let seen = 0;
  for (let i = 0; i < text.length; i++) {
    if (seen === n) return i;
    if (/\d/.test(text[i])) seen++;
  }
  return text.length;
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
