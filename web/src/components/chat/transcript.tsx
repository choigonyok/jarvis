"use client";

import { memo, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Reply } from "lucide-react";
import { Lightbox } from "@/components/ui/lightbox";
import { Failed } from "@/components/shell/failed";
import { MarkButtons, WrongForm, WrongNote, useAnswerMarks, type MarkAnswer } from "@/components/chat/answer-marks";
import { ChecklistCard, type Fold } from "@/components/chat/checklist";
import { Button } from "@/components/ui/button";
import { NoticeCard } from "@/components/chat/notice-card";
import { ProposalCard } from "@/components/chat/proposal-card";
import { SwipeRow } from "@/components/chat/swipe-row";
import { Working } from "@/components/chat/working";
import { readableTitle } from "@/lib/card-body";
import { raisedAt } from "@/lib/ledger";
import { Markdown } from "@/components/chat/markdown";
import { uploadsIn } from "@/components/chat/inline-photos";
import { Bubble, BubbleContent } from "@/components/ui/bubble";
import { Marker, MarkerContent } from "@/components/ui/marker";
import { Message, MessageContent, MessageFooter } from "@/components/ui/message";
import { MessageScrollerContent, MessageScrollerItem } from "@/components/ui/message-scroller";
import {
  isPending,
  plainText,
  quoteText,
  storedForm,
  type Checklist,
  type Draft,
  type Proposal,
  type Step,
  type Turn,
  type TurnRef,
} from "@/lib/thread";
import { photoUrl } from "@/lib/uploads";
import type { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";
import { clock, dayLabel, refTime, turnDay, turnTime } from "@/lib/when";

/**
 * The conversation's rows, as chat/thread.tsx lays them in its scroller: what
 * each turn is drawn as - a word of Jarvis's, a message of yours, a card, a
 * job's news - and an answer while it is still being written.
 */

/**
 * Timestamps and status live in the rail, so the body stays a clean measure.
 *
 * The rail is a wide-screen luxury: 44px of gutter is an eighth of a phone,
 * spent on a stamp nobody reads while scrolling the last three turns. It
 * collapses below sm. What matters for an audit - when a request was decided -
 * is on the proposal card itself and survives the collapse.
 */
function Rail({ at, onRef }: { at?: string; onRef?: () => void }) {
  return (
    <div className="hidden flex-col items-end gap-1 pt-[3px] pr-3 sm:flex sm:pr-4" data-search-skip>
      {at ? <span className="tnum text-meta text-faint">{at}</span> : null}
      {onRef ? <RefButton onClick={onRef} /> : null}
    </div>
  );
}

/**
 * Quoting with a pointer. On a touch screen the message is swiped instead
 * (SwipeRow), so this is not rendered there at all - an invisible button a
 * thumb could still hit would be worse than none.
 */
function RefButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label="이 메시지 참조"
      title="참조"
      className="hidden size-6 items-center justify-center rounded-md text-faint opacity-0 transition-opacity group-hover/turn:opacity-100 hover:text-foreground focus-visible:opacity-100 focus-ring pointer-fine:flex"
    >
      <Reply aria-hidden className="size-3.5" />
    </button>
  );
}

/** Where one day of the conversation ends and the next begins: the date between two hairlines. */
function DayDivider({ day }: { day: string }) {
  return (
    <MessageScrollerItem data-search-skip>
      <Marker variant="separator" className="text-faint before:bg-edge-soft after:bg-edge-soft">
        <MarkerContent className="tnum text-meta">{dayLabel(day)}</MarkerContent>
      </Marker>
    </MessageScrollerItem>
  );
}

/**
 * The message a message answers, at the top of its bubble the way a
 * messenger shows a reply. Tapping it goes back up to that message.
 */
function Quote({ quote, at, onJump }: { quote: TurnRef; at: string; onJump: () => void }) {
  return (
    <button
      type="button"
      onClick={onJump}
      data-search-skip
      className="mb-2 block w-full rounded-xl bg-well px-3 py-2 text-left focus-ring"
    >
      <span className="tnum block text-meta text-faint">
        {quote.role === "agent" ? "Jarvis" : "나"}
        {at ? ` · ${at}` : ""}
      </span>
      <span className="line-clamp-2 text-ui leading-snug text-dim">{plainText(quote.text) || "사진"}</span>
    </button>
  );
}

/**
 * News from a background job, beside the conversation rather than in it.
 *
 * A job speaks while you may be mid-thought with Jarvis, so its word is not
 * laid out as his: a recessed card names the job and shows its latest line,
 * folded to a few lines until tapped. One run has one card, kept at its
 * newest word - a chatty run never fills the thread. The way to the job
 * itself is the 작업 tab.
 */
function JobNews({ turn, fold }: { turn: Turn; fold: Fold }) {
  const open = fold.open;
  const text = turn.paragraphs?.join("\n\n") ?? "";
  const long = text.length > 160 || text.includes("\n");
  const job = turn.origin?.job;
  // A photo the words name is shown where they name it (chat/markdown.tsx);
  // below them, only the ones they do not.
  const named = uploadsIn(text);
  const shown = (turn.images ?? []).filter((n) => !named.includes(n));
  return (
    <div className="max-w-[36rem] rounded-xl bg-well px-3.5 py-2.5">
      <div className="flex items-baseline justify-between gap-3" data-search-skip>
        <span className="min-w-0 truncate text-meta text-faint">
          작업 · {turn.origin?.title || "이름 없음"}
          {/* Open while a newer word came: written here, not moved to the bottom. */}
          {turn.freshNews ? <span className="ms-1.5 text-ink">· 새 소식</span> : null}
        </span>
        <Link
          // To this job's own sheet, not the top of the list.
          href={job ? `/jobs?focus=${encodeURIComponent(`job:${job}`)}` : "/jobs"}
          className="shrink-0 text-meta text-faint underline-offset-2 hover:text-dim hover:underline focus-ring"
        >
          작업 탭
        </Link>
      </div>
      <div
        className={cn(
          "mt-1 text-body leading-relaxed text-dim [&_p]:my-0 [&_ul]:my-1",
          !open && long && "line-clamp-3",
        )}
      >
        <Markdown>{text}</Markdown>
      </div>
      {/* What the run showed with its word: a screen it took, the job's photo. */}
      {shown.length ? (
        <div className="mt-2">
          <Photos names={shown} alt="작업이 보여 준 사진" />
        </div>
      ) : null}
      {long ? (
        <button
          type="button"
          onClick={fold.onToggle}
          aria-expanded={open}
          className="tap mt-1 text-aside text-faint hover:text-dim focus-ring"
          data-search-skip
        >
          {open ? "접기" : "더 보기"}
        </button>
      ) : null}
    </div>
  );
}

/** The agent's side of the transcript, railed on wide screens, flush on phones. */
const ROW =
  "grid grid-cols-[minmax(0,1fr)] sm:grid-cols-[3.5rem_minmax(0,1fr)]";

/**
 * Photos sent with a message, above its bubble - or shown by the assistant
 * (show_image), on its side. One photo is shown whole-ish; several become a
 * square grid, the way a messenger stacks an album. A photo deleted after its
 * listing ended shows as an empty tile, not a broken icon. A tap opens it
 * full screen here (ui/lightbox.tsx): a link to the file took a home-screen
 * app out to the browser.
 */
function Photos({ names, alt = "보낸 사진" }: { names: string[]; alt?: string }) {
  const single = names.length === 1;
  const [open, setOpen] = useState<number | null>(null);
  return (
    <>
      <div
        className={cn(
          // As wide as the photos: the assistant's sit in a full-width column.
          "grid w-fit max-w-[80%] gap-1 sm:max-w-[26rem]",
          single ? "grid-cols-1" : names.length === 2 || names.length === 4 ? "grid-cols-2" : "grid-cols-3",
        )}
      >
        {names.map((name, i) => (
          <button
            key={name}
            type="button"
            onClick={() => setOpen(i)}
            aria-label={`${alt} 크게 보기`}
            className="block overflow-hidden rounded-xl bg-well focus-ring"
          >
            {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
            <img
              src={photoUrl(name)}
              alt={alt}
              loading="lazy"
              onError={(e) => (e.currentTarget.style.visibility = "hidden")}
              className={cn("block object-cover", single ? "max-h-72 w-auto max-w-full" : "aspect-square w-24 sm:w-28")}
            />
          </button>
        ))}
      </div>
      {open !== null ? (
        <Lightbox items={names.map((name) => ({ src: photoUrl(name), alt }))} start={open} onClose={() => setOpen(null)} />
      ) : null}
    </>
  );
}

type Decide = ReturnType<typeof useThread>["decide"];
type TickList = ReturnType<typeof useThread>["tick"];
type DropItem = ReturnType<typeof useThread>["dropItem"];

/**
 * One message of the transcript, memoized on the turn itself: an update
 * replaces only the turn it changes, so a new message renders one row, not
 * all of them.
 */
const TurnRow = memo(function TurnRow({
  turn,
  proposal,
  list,
  day,
  quoteAt,
  onQuote,
  onJump,
  onDecide,
  onTick,
  onDrop,
  onMark,
  opened,
  onFold,
  typing,
  onShown,
}: {
  turn: Turn;
  /** Marks an answer good or wrong; absent where answers are not marked. */
  onMark?: MarkAnswer;
  /** The answer being written for this row, while it is. */
  typing?: Draft;
  onShown?: (id: string, turn?: string) => void;
  proposal?: Proposal;
  /** The checklist the turn's card shows, as it stands. */
  list?: Checklist;
  /** Whether that list is open on this card (its title row toggles it). */
  opened: boolean;
  onFold: (turnId: string) => void;
  /** Set on the first turn of a day: the divider above it. */
  day: string | null;
  /** When the message this one quotes was said. */
  quoteAt: string;
  onQuote: (ref: TurnRef) => void;
  onJump: (id: string) => void;
  onDecide: Decide;
  onTick: TickList;
  onDrop: DropItem;
}) {
  const time = clock(turn.atIso, turn.at);
  // A turn that is only a card is quoted by what the card asks, or names.
  const quote = () =>
    onQuote({
      id: turn.id,
      role: turn.role,
      at: turnTime(turn.atIso, turn.at),
      text:
        quoteText(turn) ||
        (proposal
          ? `승인 카드: ${readableTitle(proposal.card.title)}`
          : turn.origin?.kind === "checklist"
            ? `체크리스트: ${list?.title ?? turn.origin.title ?? ""}`
            : ""),
    });
  const tick = (item: string, done: boolean) =>
    turn.origin?.checklist ? onTick(turn.origin.checklist, item, done) : Promise.resolve(null);
  const drop = (item: string) => (turn.origin?.checklist ? onDrop(turn.origin.checklist, item) : Promise.resolve(null));
  const fold = { open: opened, onToggle: () => onFold(turn.id) };
  // An answer of Jarvis's own, once written - not a card, a job's word or a
  // notice - is what can be marked good or wrong.
  const answer =
    turn.role === "agent" &&
    !typing &&
    !turn.origin &&
    !turn.proposalId &&
    Boolean(turn.paragraphs?.length || turn.images?.length) &&
    Boolean(onMark);
  const marks = useAnswerMarks(turn.id, onMark);
  const markButtons = (className?: string) => (
    <MarkButtons
      feedback={turn.feedback}
      busy={marks.busy}
      onGood={marks.good}
      onWrong={marks.wrong}
      onClear={marks.clear}
      className={className}
    />
  );
  // A divider where the day changes: with it, a turn's own stamp only has to
  // say the time, and a reply that came hours later - a suggestion overnight
  // - is plainly not a live one.
  const divider = day ? <DayDivider day={day} /> : null;

  if (turn.role === "agent") {
    // Nothing to quote of an answer still being written.
    const ref = typing ? () => {} : quote;
    return (
      <>
        {divider}
        <MessageScrollerItem messageId={turn.id} data-turn-id={turn.id} className="group/turn" aria-busy={typing ? "true" : undefined}>
          <SwipeRow onRef={ref}>
            <Message className={cn(ROW, "gap-0 text-base")}>
              <Rail at={typing ? undefined : time} onRef={ref} />
              {/* Its own height, not the row's: the marks below it sit under
                  the last line, not under the rail's taller stack. */}
              <MessageContent className="relative block self-start">
                {typing ? (
                  <Typed draft={typing} onShown={onShown} />
                ) : turn.origin?.kind === "notice" ? (
                  <NoticeCard turn={turn} list={list} onTick={tick} onDrop={drop} fold={fold} />
                ) : turn.origin?.kind === "checklist" ? (
                  <>
                    {/* The line on why now, said above the list like any word of his. */}
                    {turn.paragraphs?.length ? (
                      <div className="mb-3">
                        <Markdown>{turn.paragraphs.join("\n\n")}</Markdown>
                      </div>
                    ) : null}
                    <ChecklistCard list={list} title={turn.origin.title} onTick={tick} onDrop={drop} fold={fold} />
                  </>
                ) : turn.origin ? (
                  <JobNews turn={turn} fold={fold} />
                ) : turn.images?.length ? (
                  // Pictures the assistant shows (show_image): the photos, and
                  // its line about them under, as a messenger sends a photo.
                  <>
                    <Photos names={turn.images} alt={turn.paragraphs?.[0] || "Jarvis가 보여 준 사진"} />
                    {turn.paragraphs?.length ? (
                      <div className="mt-2">
                        <Markdown>{turn.paragraphs.join("\n\n")}</Markdown>
                      </div>
                    ) : null}
                  </>
                ) : turn.paragraphs?.length ? (
                  // The runner split the message on blank lines; putting
                  // them back is what makes a list or a code fence one
                  // block again.
                  <Markdown>{turn.paragraphs.join("\n\n")}</Markdown>
                ) : null}
                {proposal ? (
                  <ProposalCard
                    proposal={proposal}
                    onDecide={(d, edits, reason, choice) => onDecide(proposal.id, d, edits, reason, choice)}
                  />
                ) : null}
                {answer ? <WrongNote feedback={turn.feedback} /> : null}
                {answer && marks.asking ? (
                  <WrongForm busy={marks.busy} error={marks.error} onCancel={marks.cancel} onSave={marks.saveWrong} />
                ) : null}
                {/* The rail holds the time on a wide screen; a phone
                    has no rail, so it sits under the message - with the
                    marks beside it where there is no pointer to find them
                    in the rail. */}
                {time || answer ? (
                  <MessageFooter
                    className={cn("mt-1.5 gap-1.5 px-0 text-base font-normal", answer ? "sm:pointer-fine:hidden" : "sm:hidden")}
                    data-search-skip
                  >
                    {time ? <span className="tnum text-meta text-faint sm:hidden">{time}</span> : null}
                    {answer ? markButtons("-ml-1") : null}
                    {answer && marks.error && !marks.asking ? (
                      <span className="text-meta text-reject" role="status">
                        {marks.error}
                      </span>
                    ) : null}
                  </MessageFooter>
                ) : null}
                {/* With a pointer the marks wait under the answer, in the gap
                    before the next turn - taking no room of their own - until
                    the answer is hovered, like 참조; set, they stay: they are
                    its status. */}
                {answer
                  ? markButtons(
                      cn(
                        "absolute top-full left-0 mt-1 -ml-1.5 hidden sm:pointer-fine:flex",
                        !turn.feedback && !marks.asking && "opacity-0 group-hover/turn:opacity-100 focus-within:opacity-100",
                      ),
                    )
                  : null}
              </MessageContent>
            </Message>
          </SwipeRow>
        </MessageScrollerItem>
      </>
    );
  }
  return (
    <>
      {divider}
      <MessageScrollerItem messageId={turn.id} data-turn-id={turn.id} className="group/turn">
        <SwipeRow onRef={quote}>
          <Message align="end" className="text-base">
            <MessageContent className="items-end gap-1.5">
              {turn.ref && !turn.text ? (
                <div className="max-w-[80%] sm:max-w-[26rem]">
                  <Quote quote={turn.ref} at={quoteAt} onJump={() => onJump(turn.ref!.id)} />
                </div>
              ) : null}
              {turn.images?.length ? <Photos names={turn.images} /> : null}
              {/* No backdrop blur: behind a bubble is only the flat page, so it
                  showed nothing, and made a phone redraw the bubble's backdrop
                  on every frame it moved. */}
              {turn.text ? (
                <Bubble variant="mine" align="end" className="max-w-[80%] sm:max-w-[26rem]">
                  <BubbleContent>
                    {turn.ref ? <Quote quote={turn.ref} at={quoteAt} onJump={() => onJump(turn.ref!.id)} /> : null}
                    <p className="selectable break-words whitespace-pre-wrap">{turn.text}</p>
                  </BubbleContent>
                </Bubble>
              ) : null}
              <MessageFooter className="gap-1.5 px-0 pr-1 text-base font-normal" data-search-skip>
                <RefButton onClick={quote} />
                <span className="tnum text-meta text-faint">{time}</span>
              </MessageFooter>
            </MessageContent>
          </Message>
        </SwipeRow>
      </MessageScrollerItem>
    </>
  );
});

/** How long words that came at once take to come out: about the gap until the next ones. */
const SPREAD_MS = 600;
/**
 * The slowest they come out, in characters a millisecond (80 a second): the
 * last few words of an answer, after its stream has ended, must not crawl.
 */
const MIN_RATE = 0.08;
/**
 * The fastest, short of falling behind (180 a second): a whole answer that
 * lands in one piece - hundreds of characters in one frame on the phone -
 * spread over SPREAD_MS comes out a sentence a redraw, not a few characters.
 * Opus writes about 150 a second, so this keeps up with it.
 */
const MAX_RATE = 0.18;
/**
 * Behind by this much, the ceiling is lifted: better a jump than a lag. Well
 * above a whole answer landing in one piece (458 on the phone), which the
 * ceiling is for - this is for a stream that has run away from the screen.
 */
const BACKLOG = 1200;
/**
 * The least time between redraws: each one parses the markdown again, all of
 * it, so a long answer is redrawn less often - the words come out as fast.
 */
const frameMs = (length: number) => (length < 1500 ? 33 : length < 4000 ? 66 : 100);
/** How far each draft has come out, by id: a row built again goes on from there. */
const progress = new Map<string, number>();

/**
 * The words of an answer let out at an even pace. They reach the screen in
 * bursts - from the Mac mini a few dozen characters every half second or so
 * - and drawn as they landed they jumped a line at a time. What
 * waits is spread over about the time the next burst takes to come, so it
 * reads as typing, and a burst that lands early speeds it up. One joined
 * midway (Draft.joined: the stream opened to an answer under way) is shown
 * as far as it has come. A row built again for a draft already under way
 * (back from the voice stage) goes on from where it was (progress), not from
 * the start. With reduced motion everything is shown as it lands.
 */
/** Whether the reader asked for less motion. */
export const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;

function useTyped(id: string, text: string, atOnce: boolean): string {
  const [still] = useState(() => typeof window !== "undefined" && reducedMotion());
  const [shown, setShown] = useState(() => (atOnce ? text.length : (progress.get(id) ?? 0)));
  const pos = useRef(shown);
  const goal = useRef(shown);
  const rate = useRef(MIN_RATE);

  useEffect(() => {
    if (still) return;
    if (text.length > goal.current) {
      const behind = text.length - pos.current;
      rate.current =
        behind > BACKLOG ? behind / (SPREAD_MS / 2) : Math.min(MAX_RATE, Math.max(MIN_RATE, behind / SPREAD_MS));
    }
    goal.current = text.length;
    if (pos.current >= text.length) return;
    let raf = 0;
    let last = performance.now();
    let drawn = 0;
    const tick = (now: number) => {
      pos.current = Math.min(text.length, pos.current + rate.current * Math.max(0, now - last));
      last = now;
      const out = pos.current >= text.length;
      if (out || now - drawn >= frameMs(text.length)) {
        drawn = now;
        progress.set(id, pos.current);
        setShown(Math.floor(pos.current));
      }
      if (!out) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [id, text, still]);

  if (still) return text;
  let end = Math.min(shown, text.length);
  // Never half of a character written as two (an emoji).
  const code = text.charCodeAt(end - 1);
  if (code >= 0xd800 && code <= 0xdbff) end += 1;
  return text.slice(0, end);
}

/**
 * The answer while it is written, in the row its turn will keep: its words, laid
 * out as the runner stores them so the turn reads the same. Once its turn has
 * come and the last word is out, it says so (onShown) and the row shows the turn.
 */
function Typed({ draft, onShown }: { draft: Draft; onShown?: (id: string, turn?: string) => void }) {
  const typed = useTyped(draft.id, draft.text, Boolean(draft.joined));
  const out = typed.length >= draft.text.length;
  useEffect(() => {
    if (out && draft.turn) {
      progress.delete(draft.id);
      onShown?.(draft.id, draft.turn);
    }
  }, [out, draft.turn, draft.id, onShown]);
  const text = storedForm(typed);
  return text ? <Markdown>{text}</Markdown> : null;
}

/** The turn an answer will be, while its own has not come. */
const drafted = (d: Draft): Turn => ({ id: d.id, role: "agent", at: "" });

/**
 * The conversation itself, apart from the screen around it: a search being
 * typed, a quote picked or the keyboard coming up re-renders the screen, and
 * on a long thread rebuilding every message each time is what dropped frames.
 * This renders only when the thread does.
 */
export const Transcript = memo(function Transcript({
  turns,
  proposals,
  byId,
  checklists,
  waited,
  thinking,
  draft,
  steps,
  onDraftShown,
  error,
  hydrated,
  hasMore,
  onQuote,
  onJump,
  onDecide,
  onTick,
  onDrop,
  onMark,
  opened,
  onFold,
  onRetry,
  starters,
  onStarter,
  onGuide,
}: {
  turns: Turn[];
  onMark: MarkAnswer;
  /** Reads the thread again, by hand - under "불러오지 못했어요". */
  onRetry: () => void;
  /** The first things to say, on a conversation with nothing in it yet (chat/guide.tsx). */
  starters: string[];
  /** Puts one in the field - not sent: it is a start, to finish in one's own words. */
  onStarter: (text: string) => void;
  /** Opens everything there is to say (the ⓘ's sheet). */
  onGuide: () => void;
  proposals: Proposal[];
  byId: Map<string, Proposal>;
  checklists: Record<string, Checklist>;
  /** Every card that waited while this page was open. */
  waited: ReadonlySet<string>;
  /** The thread goes on above the oldest turn loaded. */
  hasMore: boolean;
  thinking: boolean;
  /** The answer being written. */
  draft: Draft | null;
  /** What the turn under way has done since the last thing said. */
  steps: Step[];
  onDraftShown: (id: string, turn?: string) => void;
  error: string | null;
  hydrated: boolean;
  onQuote: (ref: TurnRef) => void;
  onJump: (id: string) => void;
  onDecide: Decide;
  onTick: TickList;
  onDrop: DropItem;
  /** The checklist cards opened, by turn. */
  opened: ReadonlySet<string>;
  onFold: (turnId: string) => void;
}) {
  // A card with no turn pointing at it - one raised before every card was laid
  // in the conversation - floats below the thread while it waits. One that was
  // waiting on this screen stays once decided: a card answered under your thumb
  // and then gone reads as one that never came. A card raised before the oldest
  // turn loaded is not one of these: its turn is on a page above, and it shows
  // there. Taken for one, it would sit at the bottom of the thread once 60
  // newer turns had passed it - and stay there once decided, on a phone that
  // keeps the page open.
  const attached = new Set(turns.map((t) => t.proposalId).filter(Boolean));
  const oldest = hasMore && turns[0]?.atIso ? Date.parse(turns[0].atIso) : NaN;
  const above = (p: Proposal) => (raisedAt(p)?.getTime() ?? Infinity) < oldest;
  const unattached = proposals.filter((p) => !attached.has(p.id) && !above(p) && (isPending(p) || waited.has(p.id)));
  // When a quoted turn is on the page its own stamp says when; one that has
  // scrolled out of the loaded thread falls back to the agent's.
  const times = new Map(turns.map((t) => [t.id, turnTime(t.atIso, t.at)]));

  // Every row is a direct child of the scroller's content. The room under
  // the last row is the content's own, so the bottom the scroller keeps to is
  // the page's - and it is the composer's and the tab bar's too, which float
  // over the thread's end (--footer-space): the newest row stops above them.
  return (
    <MessageScrollerContent className="gap-7 pb-[calc(var(--footer-space)+1.25rem)] sm:pb-[calc(var(--footer-space)+1.5rem)]">
      {hydrated && turns.length === 0 ? (
        <MessageScrollerItem className={ROW}>
          <Rail />
          {error ? (
            // Nothing could be read: said as that, with the way to read again
            // - not as a conversation yet to start.
            <Failed onRetry={onRetry} className="max-w-[32rem]">
              {error}
            </Failed>
          ) : (
            <div className="max-w-[32rem]">
              <p className="text-read leading-[1.75] text-dim">
                무엇을 맡길지 적어주세요. 상태를 바꾸는 일은 실행 전에 승인 카드로 올라와요.
              </p>
              {/* What to say, shaped as one's own message is: a tap puts it in
                  the field, to finish and send. */}
              <ul className="mt-4 flex flex-wrap gap-2" aria-label="이렇게 말해 보세요">
                {starters.map((text) => (
                  <li key={text}>
                    <button
                      type="button"
                      onClick={() => onStarter(text)}
                      className="flex min-h-11 items-center rounded-2xl border border-edge bg-glass-raised px-4 text-left text-body text-foreground transition-colors hover:bg-foreground/10 focus-ring mouse:min-h-9 mouse:text-ui"
                    >
                      {text}
                    </button>
                  </li>
                ))}
              </ul>
              <Button variant="underline" size="inline" onClick={onGuide} className="tap mt-2 text-ui">
                말로 시킬 수 있는 일 더 보기
              </Button>
            </div>
          )}
        </MessageScrollerItem>
      ) : null}

        {[
          ...turns.map((turn, i) => {
          // A finished answer still coming out writes itself here. The row is
          // keyed by the answer it typed (Turn.rowKey once it is out): keyed by
          // its turn, the row was built again the moment the last word was out.
          const typing = turn.id === draft?.turn ? draft : undefined;
          const day = turnDay(turn.atIso);
          return (
            <TurnRow
              key={typing ? typing.id : (turn.rowKey ?? turn.id)}
              turn={turn}
              typing={typing}
              onShown={onDraftShown}
              proposal={turn.proposalId ? byId.get(turn.proposalId) : undefined}
              list={turn.origin?.checklist ? checklists[turn.origin.checklist] : undefined}
              day={day && day !== turnDay(turns[i - 1]?.atIso) ? day : null}
              quoteAt={turn.ref ? (times.get(turn.ref.id) ?? refTime(turn.ref.at)) : ""}
              onQuote={onQuote}
              onJump={onJump}
              onDecide={onDecide}
              onTick={onTick}
              onDrop={onDrop}
              onMark={onMark}
              opened={opened.has(turn.id)}
              onFold={onFold}
            />
          );
          }),

          ...unattached.map((proposal) => (
          <MessageScrollerItem key={proposal.id} messageId={`card:${proposal.id}`} className={ROW}>
            <Rail at={proposal.at} />
            <div className="min-w-0">
              <ProposalCard
                proposal={proposal}
                onDecide={(d, edits, reason, choice) => onDecide(proposal.id, d, edits, reason, choice)}
              />
            </div>
          </MessageScrollerItem>
          )),

          // The answer before its turn has come: the same row, at the end. In
          // one list with the turns, under the key it keeps, it moves into the
          // turn's place instead of being built again there.
          draft && draft.text.trim() && !draft.turn ? (
            <TurnRow
              key={draft.id}
              turn={drafted(draft)}
              typing={draft}
              onShown={onDraftShown}
              day={null}
              quoteAt=""
              onQuote={onQuote}
              onJump={onJump}
              onDecide={onDecide}
              onTick={onTick}
              onDrop={onDrop}
              opened={false}
              onFold={onFold}
            />
          ) : null,
        ]}

        {/* While the answer grows the steps have nothing to say. */}
        {draft && draft.text.trim() ? null : thinking ? (
          <MessageScrollerItem className={ROW} aria-live="polite" data-search-skip>
            <Rail />
            <Working steps={steps} />
          </MessageScrollerItem>
        ) : null}

        {error && !(hydrated && turns.length === 0) ? (
          <MessageScrollerItem className={ROW}>
            <Rail />
            <Failed className="max-w-[36rem]">{error}</Failed>
          </MessageScrollerItem>
        ) : null}
    </MessageScrollerContent>
  );
});
