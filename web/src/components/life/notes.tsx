"use client";

import { useMemo, useState, type ReactNode } from "react";
import { Check, ChevronDown, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Pager } from "@/components/ui/pager";
import { Textarea } from "@/components/ui/textarea";
import {
  type Note,
  type NoteStatus,
  decide,
  moment,
  pct,
  RULE_SCOPE,
  scopeLabel,
  versions as readVersions,
} from "@/lib/life";
import { usePaged } from "@/lib/paging";
import { SAY } from "@/lib/say";
import { cn } from "@/lib/utils";

/**
 * What mind believes about the operator, as sentences they can answer.
 *
 * A note asking for an answer is drawn as a request waiting for a decision
 * is everywhere else in the console - the breathing hollow ring - and is
 * answered the same way: 맞아요 is the brightest control, 아니에요 asks once
 * why. A note let go is not removed but struck through, as a cancelled
 * charge is in 가계부, and folded away below the rest.
 */
export function Notes({ notes, onChange }: { notes: Note[]; onChange: (n: Note) => void }) {
  const [open, setOpen] = useState<string | null>(null);
  const [showRejected, setShowRejected] = useState(false);
  // A note let go a moment ago stays where it was until the next visit: a
  // row that jumps away on its own answer reads as an undo. The ones let go
  // before are folded.
  const [answered, setAnswered] = useState<Set<string>>(new Set());
  const tucked = notes.filter((n) => n.status === "rejected" && !answered.has(n.id) && n.id !== open);
  const fold = tucked.length > 0 && !showRejected;
  const visible = useMemo(
    () => (fold ? notes.filter((n) => !(n.status === "rejected" && !answered.has(n.id) && n.id !== open)) : notes),
    [notes, fold, answered, open],
  );
  // Twenty a page; a note a notification names opens its page. The folded
  // ones wait under the last page, where the list ends.
  const paged = usePaged(visible, { focusKey: (n) => `note:${n.id}` });

  return (
    <div id={paged.anchor} className="scroll-mt-4">
    <ul className="divide-y divide-edge-soft">
      {paged.items.map((n) => (
        <NoteRow
          key={n.id}
          note={n}
          open={open === n.id}
          onToggle={() => setOpen(open === n.id ? null : n.id)}
          onChange={(next) => {
            setAnswered((cur) => new Set(cur).add(next.id));
            onChange(next);
          }}
        />
      ))}
      {fold && paged.page === paged.pageCount ? (
        <li>
          <button
            type="button"
            onClick={() => setShowRejected(true)}
            className="flex min-h-12 w-full items-center gap-3 py-3 text-left focus-ring"
          >
            <Mark status="rejected" />
            <span className="min-w-0 flex-1 text-body text-ink">
              아니라고 한 노트 {tucked.length}개
              <span className="mt-0.5 block text-ui text-faint">다시 만들지 않아요.</span>
            </span>
            <span className="shrink-0 text-aside text-faint">펼치기</span>
          </button>
        </li>
      ) : null}
    </ul>
    <Pager {...paged} unit="개" />
    </div>
  );
}

const statusLabel: Record<NoteStatus, string> = { hypothesis: "확인 전", confirmed: "맞음", rejected: "아님" };

function NoteRow({
  note,
  open,
  onToggle,
  onChange,
}: {
  note: Note;
  open: boolean;
  onToggle: () => void;
  onChange: (n: Note) => void;
}) {
  // What the row is doing: answering "아니에요" (with why), rewording, or neither.
  const [mode, setMode] = useState<"idle" | "why" | "edit">("idle");
  const [why, setWhy] = useState("");
  const [text, setText] = useState(note.text);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The answer given on this visit, said where the buttons were.
  const [said, setSaid] = useState<string | null>(null);
  const [history, setHistory] = useState<Note[] | null | "loading">(null);

  function loadHistory() {
    setHistory("loading");
    void readVersions(note.id).then((vs) => setHistory(vs ?? []));
  }

  async function answer(change: { status?: NoteStatus; text?: string; reason?: string }, done: string) {
    setBusy(true);
    setError(null);
    const out = await decide(note.id, change);
    setBusy(false);
    if (out.error) {
      setError(out.error);
      return;
    }
    setMode("idle");
    setSaid(done);
    onChange(out.note!);
    if (open) loadHistory();
    else setHistory(null);
  }

  function toggle() {
    if (!open && history === null) loadHistory();
    onToggle();
  }

  const asking = note.status === "hypothesis";
  const gone = note.status === "rejected";
  const scope = scopeLabel(note.scope);
  // A rule the operator gave the assistant (mind's rule scope): not a guess,
  // so no sureness to show - it stands until taken back or reworded here.
  const rule = note.scope === RULE_SCOPE && note.status === "confirmed";

  const buttons =
    mode !== "idle" ? null : rule ? (
      <div className="grid grid-cols-2 gap-2 sm:flex sm:items-center sm:gap-1.5">
        <Button variant="reject" size="pair-sm" onClick={() => setMode("why")} disabled={busy}>
          거두기
        </Button>
        <Button variant="quiet" size="pair-sm" onClick={() => setMode("edit")} disabled={busy} className={later}>
          고치기
        </Button>
      </div>
    ) : asking ? (
      <div className="grid grid-cols-[1fr_1fr_auto] gap-2 sm:flex sm:items-center sm:gap-1.5">
        <Button variant="reject" size="pair-sm" onClick={() => setMode("why")} disabled={busy}>
          아니에요
        </Button>
        <Button
          variant="approve"
          size="pair-sm"
          onClick={() => void answer({ status: "confirmed" }, "맞다고 했어요. 예측과 제안에 그대로 써요.")}
          disabled={busy}
        >
          맞아요
        </Button>
        <Button
          variant="quiet"
          size="pair-sm"
          onClick={() => setMode("edit")}
          disabled={busy}
          className={later}
        >
          고치기
        </Button>
      </div>
    ) : null;

  return (
    <Collapsible asChild open={open} onOpenChange={toggle}>
    <li data-focus={`note:${note.id}`} className="scroll-mt-24 py-3">
      <CollapsibleTrigger asChild>
      <button
        type="button"
        className="flex w-full items-start gap-3 text-left focus-ring"
      >
        <Mark status={note.status} className="mt-[8px]" />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline justify-between gap-3">
            <span
              className={cn(
                "text-title leading-[1.5] break-keep text-pretty",
                asking ? "text-foreground" : gone ? "text-faint line-through decoration-faint/60" : "text-ink",
              )}
            >
              {note.text}
            </span>
            {note.scope === RULE_SCOPE ? null : (
              <span className={cn("tnum shrink-0 text-ui", gone ? "text-faint" : "text-ink")}>
                <span className="hidden text-meta text-faint sm:inline">확신 </span>
                {pct(note.confidence)}
              </span>
            )}
          </span>
          <span className="mt-0.5 block text-aside leading-[1.55] text-faint">
            {[
              scope,
              note.scope === RULE_SCOPE
                ? { confirmed: "지키는 중", rejected: "거둠" }[note.status as string] ?? statusLabel[note.status]
                : statusLabel[note.status],
              note.reason,
            ]
              .filter(Boolean)
              .join(" · ")}
          </span>
          {!gone && note.scope !== RULE_SCOPE ? (
            <span aria-hidden className="mt-2 block h-[3px] max-w-[14rem] overflow-hidden rounded-full bg-foreground/10">
              <span className="block h-full rounded-full bg-foreground/45" style={{ width: pct(note.confidence) }} />
            </span>
          ) : null}
        </span>
        <ChevronDown aria-hidden className={cn("mt-1 size-4 shrink-0 text-faint transition-transform", open && "rotate-180")} />
      </button>
      </CollapsibleTrigger>

      <div className="ms-[19px] me-7">
        {said && mode === "idle" ? (
          <p role="status" className="anim-settle mt-2 flex items-center gap-1.5 text-aside text-dim">
            {note.status === "rejected" ? (
              <X aria-hidden className="size-3.5 text-reject" />
            ) : (
              <Check aria-hidden className={cn("size-3.5", note.status === "confirmed" ? "text-approve" : "text-dim")} />
            )}
            {said}
          </p>
        ) : null}

        {buttons ? <div className="mt-2.5">{buttons}</div> : null}

        {mode === "why" ? (
          <div className="anim-settle mt-2.5 space-y-2">
            <p className="text-aside text-dim">
              {rule
                ? "거두면 Jarvis가 더는 따르지 않아요. 이유가 있으면 적어 주세요(비워도 돼요)."
                : "왜 아닌가요? 비워도 돼요. 적어 두면 다음 노트를 만들 때 읽어요."}
            </p>
            <Input
              value={why}
              onChange={(e) => setWhy(e.target.value)}
              placeholder={rule ? "예: 이제는 넣어도 돼" : "예: 그날은 비가 와서 그랬어"}
              aria-label="아닌 이유"
              maxLength={300}
              className="mouse:h-8 mouse:text-ui"
            />
            <div className="grid grid-cols-2 gap-2 sm:flex sm:justify-end sm:gap-1.5">
              <Button variant="quiet" size="pair-sm" onClick={() => setMode("idle")} disabled={busy}>
                취소
              </Button>
              <Button
                variant="refuse"
                size="pair-sm"
                onClick={() =>
                  void answer(
                    { status: "rejected", reason: why.trim() },
                    rule ? "거뒀어요. Jarvis가 더는 따르지 않아요." : "아니라고 했어요. 다시 만들지 않아요.",
                  )
                }
                disabled={busy}
              >
                {rule ? "거두기" : "아니에요"}
              </Button>
            </div>
          </div>
        ) : null}

        {mode === "edit" ? (
          <div className="anim-settle mt-2.5 space-y-2">
            <Textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              aria-label="노트 문장"
              rows={2}
              maxLength={300}
              className="min-h-[calc(2lh+18px)] resize-none py-2 leading-[1.5]"
            />
            <div className="grid grid-cols-2 gap-2 sm:flex sm:justify-end sm:gap-1.5">
              <Button
                variant="quiet"
                size="pair-sm"
                onClick={() => {
                  setMode("idle");
                  setText(note.text);
                }}
                disabled={busy}
              >
                취소
              </Button>
              <Button
                variant="raised"
                size="pair-sm"
                onClick={() => void answer({ text: text.trim(), reason: "문장을 고침" }, "고쳐 두었어요.")}
                disabled={busy || !text.trim() || text.trim() === note.text}
              >
                저장
              </Button>
            </div>
          </div>
        ) : null}

        {error ? (
          <p role="alert" className="mt-2 text-aside text-reject">
            {error}
          </p>
        ) : null}
      </div>

      <CollapsibleContent className="anim-settle mt-3 mb-1 ms-[19px] space-y-3 border-s border-edge ps-4 text-aside leading-[1.6] break-keep">
          {note.evidence && note.evidence.length > 0 ? (
            <div>
              <p className="text-faint">근거</p>
              <ul className="space-y-0.5 text-dim">
                {note.evidence.map((e) => (
                  <li key={e} className="text-pretty">
                    {e}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}

          <div>
            <p className="text-faint">고친 이력</p>
            {history === "loading" || history === null ? (
              <p className="text-faint">{SAY.loading}</p>
            ) : history.length === 0 ? (
              <p className="text-faint">이력을 불러오지 못했어요.</p>
            ) : (
              <ol className="space-y-1 text-dim">
                {[...history].reverse().map((v) => (
                  <li key={v.version} className="text-pretty">
                    <span className="tnum text-faint">
                      {v.version}번째 · {moment(v.updatedAt)} ·{" "}
                    </span>
                    {v.reason || statusLabel[v.status]}
                    {v.text !== note.text ? <span className="block text-faint">“{v.text}”</span> : null}
                  </li>
                ))}
              </ol>
            )}
          </div>

          {!asking && mode === "idle" ? (
            <div className="flex flex-wrap gap-x-4 gap-y-1">
              {gone ? (
                <TextButton onClick={() => void answer({ status: "hypothesis", reason: "아니라고 한 것을 거둠" }, "다시 확인 전으로 두었어요.")} disabled={busy}>
                  아니라고 한 것 거두기
                </TextButton>
              ) : (
                <>
                  <TextButton onClick={() => setMode("edit")} disabled={busy}>
                    고치기
                  </TextButton>
                  <TextButton onClick={() => setMode("why")} disabled={busy}>
                    아니에요
                  </TextButton>
                </>
              )}
            </div>
          ) : null}
      </CollapsibleContent>
    </li>
    </Collapsible>
  );
}

/** A word to press among the notes' history: underlined, the height of the line, a thumb's band around it. */
function TextButton({ onClick, disabled, children }: { onClick: () => void; disabled?: boolean; children: ReactNode }) {
  return (
    <Button variant="underline" size="inline" onClick={onClick} disabled={disabled} className="tap -my-3">
      {children}
    </Button>
  );
}

/** The answer that can wait (고치기): fainter than the two that settle the note. */
const later = "px-3 text-ui text-faint";

const markLabel: Record<NoteStatus, string> = { hypothesis: "확인 전", confirmed: "맞음", rejected: "아님" };

/**
 * The note's state in the console's marks: waiting on the operator is the
 * breathing hollow ring, as a request is; a yes is the approve dot, a no the
 * reject dot.
 */
function Mark({ status, className }: { status: NoteStatus; className?: string }) {
  return (
    <span role="img" aria-label={markLabel[status]} className={cn("flex size-[7px] shrink-0 items-center justify-center", className)}>
      {status === "hypothesis" ? (
        <span className="anim-breathe size-[7px] rounded-full border border-dim" />
      ) : status === "confirmed" ? (
        <span className="size-[6px] rounded-full bg-approve" />
      ) : (
        <span className="size-[6px] rounded-full bg-reject-fill" />
      )}
    </span>
  );
}
