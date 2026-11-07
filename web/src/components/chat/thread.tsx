"use client";

import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type RefObject } from "react";
import Link from "next/link";
import { ArrowDown, Reply } from "lucide-react";
import { Lightbox } from "@/components/ui/lightbox";
import { Failed } from "@/components/shell/failed";
import { Spinner } from "@/components/ui/spinner";
import { MarkButtons, WrongForm, WrongNote, useAnswerMarks, type MarkAnswer } from "@/components/chat/answer-marks";
import { Composer } from "@/components/chat/composer";
import { ModeToggle, type ChatMode } from "@/components/chat/mode-toggle";
import { ChecklistCard, type Fold } from "@/components/chat/checklist";
import { GuideSheet, starters } from "@/components/chat/guide";
import { Button } from "@/components/ui/button";
import { NoticeCard } from "@/components/chat/notice-card";
import { ProposalCard } from "@/components/chat/proposal-card";
import { QueuedMessages } from "@/components/chat/queued";
import { SwipeRow } from "@/components/chat/swipe-row";
import { SearchBar, SearchToggle, useThreadSearch } from "@/components/chat/thread-search";
import { Working } from "@/components/chat/working";
import { readableTitle } from "@/lib/card-body";
import { raisedAt } from "@/lib/ledger";
import { onFocusProposal } from "@/lib/resume";
import { Markdown } from "@/components/chat/markdown";
import { uploadsIn } from "@/components/chat/inline-photos";
import { VoiceStage } from "@/components/chat/voice-stage";
import { Header, PageTop, TabBar } from "@/components/shell/header";
import { useRole } from "@/components/shell/role";
import { StandingBar } from "@/components/shell/standing-bar";
import { Bubble, BubbleContent } from "@/components/ui/bubble";
import { Marker, MarkerContent } from "@/components/ui/marker";
import { Message, MessageContent, MessageFooter } from "@/components/ui/message";
import {
  MessageScroller,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
  useMessageScroller,
} from "@/components/ui/message-scroller";
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
import { diag } from "@/lib/diag";
import { photoUrl } from "@/lib/uploads";
import { speakable, useSpeech, useVoiceStatus } from "@/lib/use-speech";
import { openedTurns, useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";
import { clock, dayLabel, refTime, turnDay, turnTime } from "@/lib/when";

const MODE_KEY = "jarvis:chat-mode";

/**
 * Where the reader was in the conversation when they left it for another tab:
 * the row at the top of the view, how far into the view it stood, and the
 * newest turn then. Coming back lands there, not at the bottom - unless they
 * had been at the bottom - and what came meanwhile is counted on the way
 * down (새 메시지 N).
 */
let readingSpot: { row: string; offset: number; newest: string | null } | null = null;

// The cards opened here (by turn) - checklists, a job's news - live in
// lib/use-thread.ts (openedTurns), for as long as the app is open. A card
// comes folded - its title, how far along, what is left - and is opened by
// its title when wanted.

/**
 * An agent turn as it is read aloud: a job's card is announced as the job's,
 * a notice as what it is, a checklist by its name.
 */
function spokenText(turn: Turn): string {
  const body = speakable(turn.paragraphs?.join("\n\n") ?? "");
  const o = turn.origin;
  if (!o) return body;
  const name = o.title ? `'${o.title}'` : "";
  if (o.kind === "checklist") return `체크리스트 ${name}. ${body}`.trim();
  if (!body) return "";
  return o.kind === "notice" ? `알림. ${body}` : `작업 ${name} 소식. ${body}`;
}

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
                    onDecide={(d, edits, reason) => onDecide(proposal.id, d, edits, reason)}
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
/** How long a send holds the view at the bottom, at least, and at most. */
const HOLD_MS = 800;
const HOLD_MAX_MS = 3000;
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
const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;

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
const Transcript = memo(function Transcript({
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
                onDecide={(d, edits, reason) => onDecide(proposal.id, d, edits, reason)}
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

/** The scroller's moves: to the end, to a row. */
type Scroller = ReturnType<typeof useMessageScroller>;

/**
 * Hands the scroller's moves to the screen around it: a send from the
 * composer, the 맨 아래로 button and a push landing on a card all live
 * outside the scroller.
 */
function ScrollerMoves({ toRef }: { toRef: RefObject<Scroller | null> }) {
  const moves = useMessageScroller();
  useLayoutEffect(() => {
    toRef.current = moves;
    return () => {
      if (toRef.current === moves) toRef.current = null;
    };
  }, [toRef, moves]);
  return null;
}

export function Thread() {
  const role = useRole();
  const {
    turns,
    proposals,
    byId,
    checklists,
    tick,
    dropItem,
    waited,
    thinking,
    draft,
    steps,
    draftShown,
    queue,
    connection,
    error,
    hydrated,
    hasMore,
    loadingOlder,
    olderFailed,
    loadOlder,
    resync,
    send,
    steer,
    drop,
    stop,
    mark,
    decide,
  } = useThread();
  const scrollRef = useRef<HTMLDivElement>(null);
  // The screen's root, which carries --footer-space, and the footer it is
  // measured from.
  const rootRef = useRef<HTMLDivElement>(null);
  const footerRef = useRef<HTMLElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const search = useThreadSearch(scrollRef, { hasMore, failed: olderFailed, load: loadOlder });
  // What can be asked of Jarvis in words (ⓘ, left of search). Its sheet is
  // drawn outside the header: a tap on the backdrop would otherwise reach the
  // header's own tap, which scrolls the thread to the top and loads older turns.
  const [guide, setGuide] = useState(false);
  // The earlier message the next one quotes: swiped (or, with a pointer, the
  // button beside its time), shown above the composer until sent or dropped.
  const [quoting, setQuoting] = useState<TurnRef | null>(null);
  const [opened, setOpened] = useState<ReadonlySet<string>>(() => new Set(openedTurns));
  const fold = useCallback((turnId: string) => {
    setOpened((cur) => {
      const next = new Set(cur);
      if (next.has(turnId)) next.delete(turnId);
      else next.add(turnId);
      openedTurns.clear();
      for (const id of next) openedTurns.add(id);
      return next;
    });
  }, []);
  const [mode, setMode] = useState<ChatMode>("text");
  const [muted, setMuted] = useState(false);
  // Your own voice, when it is on file on the Mac mini; the browser's otherwise.
  const voice = useVoiceStatus();
  const refreshVoice = voice.refresh;
  const { speak, cancel, speaking, preparing, speaker, pulse, progress, spectrum } = useSpeech(
    "ko-KR",
    voice.state === "ready",
  );
  // The bottom bar gives up its space while the keyboard is up: it is covered
  // anyway, and the transcript needs those 44px more than the tabs do.
  const [typing, setTyping] = useState(false);
  // Only replies that land while you are listening get read out. Without this
  // the backlog would be recited from the top the moment you switch modes.
  const spoken = useRef<string | null>(null);

  const waiting = useMemo(() => proposals.filter(isPending), [proposals]);
  const pending = waiting.length;
  // The bar speaks for what you cannot see. A card sitting in the transcript
  // in front of you is already saying it.
  const [cardsVisible, setCardsVisible] = useState(false);

  // The scroller (ui/message-scroller.tsx) lands on the newest turn before
  // anything is painted and follows the bottom on its own - the steps being
  // taken, a message queued above the composer, an answer as it is written -
  // until the reader scrolls themselves, and again once they are back at the
  // very bottom. Following is decided by what the reader did, not by where
  // the view happens to be: a reader who scrolls a line up to read is not
  // pulled back down by the next turn. Its moves are reached from here.
  const scroller = useRef<Scroller | null>(null);
  // Until when a send holds the view at the bottom (toNewest): one scrollTop
  // does not stick on the phone - iOS puts the view back where it had been
  // within 120ms as the keyboard and the layout move. A finger on the
  // thread ends it.
  const holdUntil = useRef(0);
  // When that send was: the hold is stretched while the view is still moving
  // (iOS momentum, the keyboard) but never past HOLD_MAX_MS from it.
  const holdFrom = useRef(0);
  // Up far enough that the newest turn is out of sight: the way back down is
  // offered (the button over the composer).
  const [away, setAway] = useState(false);
  // The newest turn when the reader was last at the bottom; what Jarvis said
  // after it is what the button counts as new.
  const [seenAt, setSeenAt] = useState<string | null>(null);
  const unseen = useMemo(() => {
    if (!away || !seenAt) return 0;
    const at = turns.findIndex((t) => t.id === seenAt);
    if (at === -1) return 0;
    return turns.slice(at + 1).filter((t) => t.role === "agent").length;
  }, [away, seenAt, turns]);
  // The card a tapped push brought the reader to. While set it is kept in the
  // middle - photos above finishing their layout would push it down - until
  // the reader touches the thread themselves.
  const held = useRef<HTMLElement | null>(null);
  // Whether a finger is on the thread, and the wait for scrolling to settle:
  // the page above is asked for only once the reader has stopped near the
  // top - finger lifted, fling over. On an iPhone the view's position is the
  // system's while a finger or a fling moves it, so the page's height put
  // back mid-fling was overwritten: the view fell into the page just added,
  // was near the top again, asked for the next one - one flick up ran
  // through page after page.
  const touching = useRef(false);
  // When the finger last moved, or came down. A touch whose end never came -
  // the row under it replaced as it was touched (a draft turning into its
  // turn), its touchend dispatched to a node no longer in the page - would
  // otherwise read as a finger forever.
  const touchAt = useRef(0);
  const finger = useCallback(() => touching.current && performance.now() - touchAt.current < 1500, []);
  const settle = useRef<number | undefined>(undefined);
  // Where the finger came down: a move of less than a few pixels up or down
  // from there is not scrolling.
  const touchY = useRef<number | null>(null);
  // When the reader last scrolled the thread themselves (a move up or down,
  // a wheel turn).
  const intentAt = useRef(0);
  // Set while a page of older turns is on its way: how tall the thread was
  // and where the reader was, so once the page is in front of them the view
  // is moved by exactly what was added and nothing seems to jump. This is
  // the screen's and not the scroller's: the scroller tells a page put in
  // front by its first row changing, and the first row is often the day's
  // divider, which goes when older turns of the same day come in front of it
  // - on an iPhone the reader was dropped onto the oldest turn of the new
  // page.
  const above = useRef<{ height: number; top: number } | null>(null);
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el || !above.current) return;
    const { height, top } = above.current;
    above.current = null;
    const target = el.scrollHeight - height + top;
    el.scrollTop = target;
    // iOS can still be finishing a fling it owns and put its own position
    // back over this one: look again on the next frame.
    requestAnimationFrame(() => {
      if (!finger() && Math.abs(el.scrollTop - target) > 2) el.scrollTop = target;
    });
  }, [turns, finger]);

  // Leaving for another tab: where the reader was. Coming back: there again.
  const newest = useRef<string | null>(null);
  useEffect(() => {
    newest.current = turns.at(-1)?.id ?? null;
  });
  useLayoutEffect(() => {
    const el = scrollRef.current;
    const spot = readingSpot;
    readingSpot = null;
    const row = el && spot ? el.querySelector<HTMLElement>(`[data-message-id="${CSS.escape(spot.row)}"]`) : null;
    if (el && spot && row) {
      // The reader's own place, as a scroll would take it: the scroller lets go of the bottom.
      el.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
      el.scrollTop += row.getBoundingClientRect().top - el.getBoundingClientRect().top - spot.offset;
      setSeenAt(spot.newest);
      setAway(true);
    }
    return () => {
      // The view on screen as the reader leaves - voice mode builds it anew -
      // not the one there on arriving.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      const view = scrollRef.current;
      if (!view || view.scrollHeight - view.scrollTop - view.clientHeight < 80) return;
      const top = view.getBoundingClientRect().top;
      for (const r of view.querySelectorAll<HTMLElement>("[data-message-id]")) {
        const box = r.getBoundingClientRect();
        if (box.bottom > top + 1 && r.dataset.messageId) {
          readingSpot = { row: r.dataset.messageId, offset: box.top - top, newest: newest.current };
          break;
        }
      }
    };
  }, []);

  // Back at the very bottom by the reader's own hand: following again. The
  // scroller looks only when the view moves, and a finger or a wheel still
  // pushing at the bottom moves nothing - it would stay let go of, and the
  // answer would grow out of sight under a reader who is right there.
  const wheelEnd = useRef<number | undefined>(undefined);
  // When something in the thread was last opened or closed to be read (a
  // job's 더 보기, a checklist, a notice): the reader took the view then.
  const expanding = useRef(0);
  const repin = useCallback(() => {
    const el = scrollRef.current;
    if (!el || held.current || performance.now() - expanding.current < 1500) return;
    if (el.scrollHeight - el.scrollTop - el.clientHeight <= 2) scroller.current?.scrollToEnd();
  }, []);

  // Scrolling up near the top asks for the page above.
  const askOlder = useCallback(
    (el: HTMLElement, tapped = false) => {
      if (!hasMore || loadingOlder || above.current) return;
      // A page that failed is asked again only on a tap, not by every scroll.
      if (olderFailed && !tapped) return;
      if (!tapped && el.scrollTop > 320) return;
      above.current = { height: el.scrollHeight, top: el.scrollTop };
      void loadOlder().then(() => {
        // Nothing came (or it failed): nothing to compensate for.
        window.setTimeout(() => {
          if (above.current) above.current = null;
        }, 1500);
      });
    },
    [hasMore, loadingOlder, olderFailed, loadOlder],
  );

  // A tapped push about a card lands on that card - not the bottom of the
  // thread - and marks it for a moment. From ?proposal= when the tap opened
  // this screen, or from the service worker when it was already open. The
  // card may arrive after the tap (the thread is still loading): it is
  // looked for again as turns and cards come in.
  const wanted = useRef<string | null>(null);
  // Pages above being read for the card: said over the composer meanwhile - the view
  // sat still at the bottom with nothing to show for it.
  const [seeking, setSeeking] = useState(false);
  const focusCard = useCallback(() => {
    const id = wanted.current;
    const card = id ? scrollRef.current?.querySelector<HTMLElement>(`[data-proposal-id="${CSS.escape(id)}"]`) : null;
    if (!card) {
      // Older than what is loaded: its turn is on a page above. The view stays
      // where it is and the turns come in behind it until the card is among them.
      if (id && hasMore && !loadingOlder && !olderFailed && byId.has(id)) {
        setSeeking(true);
        void loadOlder();
      } else if (id && (!hasMore || olderFailed)) {
        // Nowhere further to look.
        wanted.current = null;
        setSeeking(false);
      }
      return;
    }
    wanted.current = null;
    setSeeking(false);
    held.current = card;
    // To its row through the scroller, so it stops following the bottom,
    // then the card itself to the middle - as near as the thread's ends let
    // it: the scroller's own centring would pad the bottom with blank room
    // to put a card just raised in the middle. At once, not smooth: from the
    // bottom of a long thread a smooth scroll is a long ride, and its first
    // frames still read as "at the bottom".
    const row = card.closest<HTMLElement>("[data-message-id]")?.dataset.messageId;
    if (row) scroller.current?.scrollToMessage(row, { align: "nearest" });
    card.scrollIntoView({ block: "center" });
    card.classList.remove("card-flash");
    void card.offsetWidth; // restart the animation on a second tap
    card.classList.add("card-flash");
    const url = new URL(window.location.href);
    if (url.searchParams.has("proposal")) {
      url.searchParams.delete("proposal");
      window.history.replaceState(window.history.state, "", url.pathname + url.search);
    }
  }, [hasMore, loadingOlder, olderFailed, byId, loadOlder]);
  // 결재하기 here: to the card where it stands, not to another screen.
  const goToCard = useCallback(
    (id: string) => {
      wanted.current = id;
      focusCard();
    },
    [focusCard],
  );
  useEffect(() => {
    wanted.current = new URLSearchParams(window.location.search).get("proposal");
    return onFocusProposal((id) => {
      wanted.current = id;
      focusCard();
    });
  }, [focusCard]);
  useEffect(() => {
    if (!wanted.current) return;
    // A frame later: the first rows a page brings are landed at the bottom
    // by the scroller as they come in, and that landing must not come after
    // this one.
    const frame = requestAnimationFrame(focusCard);
    return () => cancelAnimationFrame(frame);
  }, [turns, proposals, loadingOlder, focusCard]);
  // The reader taking over - a touch, a wheel, typing, anywhere on the page
  // (the composer is outside the thread) - lets go of a held card.
  useEffect(() => {
    const release = () => {
      held.current = null;
      holdUntil.current = 0;
    };
    const opts = { capture: true, passive: true } as const;
    document.addEventListener("pointerdown", release, opts);
    document.addEventListener("wheel", release, opts);
    document.addEventListener("keydown", release, true);
    return () => {
      document.removeEventListener("pointerdown", release, opts);
      document.removeEventListener("wheel", release, opts);
      document.removeEventListener("keydown", release, true);
    };
  }, []);

  const quote = useCallback((ref: TurnRef) => {
    setQuoting(ref);
    // In the same gesture as the swipe, so a phone raises its keyboard - and
    // lets it scroll the composer into view above the keyboard, the way a
    // tap on the field does: with preventScroll, iOS left it underneath.
    inputRef.current?.focus();
  }, []);

  // A message of yours - typed and sent, a waiting one put into the running
  // turn, one taken back - takes the view to the newest turn, wherever you
  // were reading, and from there it follows what comes back: the steps, the
  // answer as it is written, the turns after it. At once, not smooth: from far
  // up a smooth ride is long, and the message is already on its way.
  const toNewest = useCallback(() => {
    held.current = null;
    scroller.current?.scrollToEnd();
    // Held there frame by frame while the keyboard and the layout settle, and
    // while the view still moves on its own (onScroll stretches it).
    holdFrom.current = performance.now();
    holdUntil.current = holdFrom.current + HOLD_MS;
    const hold = () => {
      const now = scrollRef.current;
      if (!now || performance.now() >= holdUntil.current || finger()) return;
      if (now.scrollHeight - now.scrollTop - now.clientHeight > 1) scroller.current?.scrollToEnd();
      requestAnimationFrame(hold);
    };
    requestAnimationFrame(hold);
  }, [finger]);

  // Down to the newest turn - and staying there as more come.
  const toBottom = useCallback(() => {
    held.current = null;
    const smooth = !reducedMotion();
    scroller.current?.scrollToEnd({ behavior: smooth ? "smooth" : "auto" });
    if (!smooth) return;
    // A smooth ride is stopped by anything else that moves the view - a page
    // of older turns landing in front is put back where the reader was.
    // Where it stopped short and the reader has not taken the view since,
    // the rest of the way is gone at once.
    const asked = performance.now();
    window.setTimeout(() => {
      const el = scrollRef.current;
      if (!el || held.current || finger() || intentAt.current > asked) return;
      if (el.scrollHeight - el.scrollTop - el.clientHeight > 2) scroller.current?.scrollToEnd();
    }, 700);
  }, [finger]);

  // A quote tapped in a bubble goes back to the message it quotes, and marks
  // it the way a tapped push marks a card.
  const jumpTo = useCallback((id: string) => {
    const row = scrollRef.current?.querySelector<HTMLElement>(`[data-turn-id="${CSS.escape(id)}"]`);
    if (!row) return;
    held.current = row;
    scroller.current?.scrollToMessage(id, { align: "nearest" });
    row.scrollIntoView({ block: "center" });
    row.classList.remove("turn-flash");
    void row.offsetWidth;
    row.classList.add("turn-flash");
  }, []);

  // The bottom is the scroller's to keep as the page grows. A card a push
  // landed on is this screen's: photos above it finishing their layout push
  // it down, and the view itself changes height (the bar over the thread
  // folds once the card is in sight), so it is put back in the middle.
  useEffect(() => {
    const el = scrollRef.current;
    const content = el?.firstElementChild;
    if (!el || !content) return;
    const ro = new ResizeObserver(() => {
      held.current?.scrollIntoView({ block: "center" });
    });
    ro.observe(content);
    ro.observe(el);
    return () => ro.disconnect();
    // Again for the scroller the voice stage's return builds.
  }, [mode]);

  // The composer and the tab bar float over the thread's end: how tall they
  // stand is the room under its last row (--footer-space), kept as it changes
  // - a long message, a quote, the bar stepping away while typing.
  useLayoutEffect(() => {
    const root = rootRef.current;
    const footer = footerRef.current;
    if (!root || !footer) return;
    const put = () => root.style.setProperty("--footer-space", `${footer.getBoundingClientRect().height}px`);
    put();
    const watch = new ResizeObserver(put);
    // Its border box: the tab bar's room is the footer's own padding, which
    // a content-box watch never sees change.
    watch.observe(footer, { box: "border-box" });
    return () => watch.disconnect();
    // Again for the screen the voice stage's return builds.
  }, [mode]);

  // Watch the waiting cards themselves rather than guessing from scroll
  // position: the bar exists to speak for a decision you cannot see, so what
  // it needs to know is literally whether one is on screen.
  useEffect(() => {
    const root = scrollRef.current;
    if (!root || pending === 0) {
      setCardsVisible(false);
      return;
    }
    const cards = root.querySelectorAll("[data-pending-card]");
    if (cards.length === 0) {
      setCardsVisible(false);
      return;
    }
    const seen = new Set<Element>();
    // Under the glass top or under the composer a card is not one you can read.
    const style = getComputedStyle(root);
    const inset = (name: string) => Math.round(parseFloat(style.getPropertyValue(name)) || 0);
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) seen.add(entry.target);
          else seen.delete(entry.target);
        }
        setCardsVisible(seen.size > 0);
      },
      { root, threshold: 0.35, rootMargin: `-${inset("--top-space")}px 0px -${inset("--footer-space")}px 0px` },
    );
    for (const card of cards) observer.observe(card);
    return () => observer.disconnect();
  }, [pending, turns, proposals, mode]);

  // The chosen mode should survive a reload; reading it in an effect keeps the
  // server render and the first client render identical.
  useEffect(() => {
    const saved = window.localStorage.getItem(MODE_KEY);
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (saved === "voice" || saved === "text") setMode(saved);
  }, []);

  const last = turns.at(-1);
  // What the newest agent line says when read aloud: a job's news is
  // introduced as the job's, so it is not mistaken for Jarvis answering.
  const text = last?.role === "agent" ? spokenText(last) : "";

  useEffect(() => {
    if (mode !== "voice") {
      cancel();
      spoken.current = null;
      return;
    }
    // Not before the thread has loaded: a page reopened straight into voice
    // mode would otherwise take an empty thread as "heard" and then read the
    // last old answer aloud the moment the real one arrived.
    if (!hydrated) return;
    // Entering voice mode marks whatever is already on screen as heard, and
    // asks again whether your voice is on file - the Mac mini may have come
    // back since the page loaded.
    if (spoken.current === null) {
      spoken.current = last?.id ?? "";
      void refreshVoice();
      return;
    }
    if (muted) return;
    if (!last || last.role !== "agent" || last.id === spoken.current) return;
    spoken.current = last.id;
    diag("voice", { ev: "thread.speak", chars: text.length, origin: last.origin?.kind ?? "" });
    if (text) speak(text);
  }, [mode, muted, last, text, hydrated, speak, cancel, refreshVoice]);

  const switchMode = useCallback((next: ChatMode) => {
    setMode(next);
    window.localStorage.setItem(MODE_KEY, next);
  }, []);

  // What the header and the composer are handed stays the same while it means
  // the same: they are drawn again only when it changes, not on every frame of
  // an answer.
  const openGuide = useCallback(() => setGuide(true), []);
  // The empty conversation's first things to say go into the field, as typed.
  const fillRef = useRef<((text: string) => void) | null>(null);
  const startWith = useCallback((text: string) => fillRef.current?.(text), []);
  const firstSays = useMemo(() => starters(role), [role]);
  // Search alone: what can be asked moved from here into the empty field
  // (the composer's ⓘ) - five grey icons in a phone's header had no order.
  const headerActions = useMemo(() => <SearchToggle search={search} />, [search]);
  const sendFromComposer = useCallback(
    async (text: string, images: string[]) => {
      toNewest();
      const ok = await send(text, images, quoting?.id);
      if (ok) setQuoting(null);
      return ok;
    },
    [toNewest, send, quoting],
  );
  const toVoice = useCallback(() => switchMode("voice"), [switchMode]);
  const cancelQuote = useCallback(() => setQuoting(null), []);
  const quotingLine = useMemo(
    () =>
      quoting
        ? { label: quoting.role === "agent" ? "Jarvis에게 답장" : "내 메시지에 답장", text: quoting.text || "사진" }
        : null,
    [quoting],
  );
  const queued = useMemo(
    () => (
      <QueuedMessages
        queue={queue}
        onSteer={(id) => {
          toNewest();
          return steer(id);
        }}
        onDrop={drop}
        onRestore={(text, images) => {
          toNewest();
          void send(text, images);
        }}
      />
    ),
    [queue, toNewest, steer, drop, send],
  );

  // The transcript is the same one either way - the stage is a different
  // window onto it, not a second store. Leaving voice mode drops you back
  // into the thread with everything that was said sitting in it as text.
  if (mode === "voice") {
    return (
      <VoiceStage
        onSend={(text) => send(text)}
        onStall={resync}
        speaking={speaking}
        preparing={preparing}
        speaker={speaker}
        pulse={pulse}
        speechBins={spectrum}
        progress={progress}
        onStopSpeaking={cancel}
        muted={muted}
        onMutedChange={setMuted}
        onExit={() => switchMode("text")}
        lastSaid={text || undefined}
        thinking={thinking}
        voice={voice}
        onVoiceChanged={voice.refresh}
      />
    );
  }

  return (
    <div ref={rootRef} className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={pending} actions={headerActions} />
        <SearchBar search={search} />
        <StandingBar pending={waiting} onGo={goToCard} muted={cardsVisible} />
      </PageTop>
      <GuideSheet open={guide} onOpenChange={setGuide} role={role} />

      <MessageScrollerProvider autoScroll defaultScrollPosition="end">
        <ScrollerMoves toRef={scroller} />
        {/* The thread runs the whole height of the screen: under the glass
            top, and under the composer and the tab bar floating over its end,
            as every other screen's page runs under its bars. */}
        <main className="-mt-(--top-space) -mb-(--footer-space) flex min-h-0 flex-1 flex-col">
          <MessageScroller>
            <MessageScrollerViewport
              ref={scrollRef}
              aria-label="대화"
              preserveScrollOnPrepend={false}
              onScroll={(e) => {
                const el = e.currentTarget;
                const gap = el.scrollHeight - el.scrollTop - el.clientHeight;
                // Where the reader is, whoever scrolled: the button is about position.
                setAway(gap > Math.max(320, el.clientHeight * 0.6));
                if (gap < 80) {
                  const newest = turns.at(-1)?.id ?? null;
                  setSeenAt((prev) => (prev === newest ? prev : newest));
                }
                if (held.current) return; // our own scroll to a card, not the reader's
                // Still moving without a finger after a send - iOS momentum,
                // the keyboard - so the hold lasts until it has stopped.
                const now = performance.now();
                if (now < holdUntil.current && !finger() && gap > 1) {
                  holdUntil.current = Math.min(holdFrom.current + HOLD_MAX_MS, Math.max(holdUntil.current, now + 300));
                }
                window.clearTimeout(settle.current);
                const ask = () => {
                  if (finger()) settle.current = window.setTimeout(ask, 120);
                  else askOlder(el);
                };
                settle.current = window.setTimeout(ask, 180);
              }}
              onTouchStart={() => {
                touching.current = true;
                touchAt.current = performance.now();
                holdUntil.current = 0;
              }}
              onTouchMove={() => {
                touchAt.current = performance.now();
              }}
              onTouchEnd={() => {
                touching.current = false;
                repin();
              }}
              onTouchCancel={() => {
                touching.current = false;
                repin();
              }}
              onKeyUp={repin}
              // What is brought into view - a card, a quoted message - lands
              // between the bars, not under them.
              className="scrollbar-hairline inset-x-safe relative scroll-pt-(--top-space) scroll-pb-(--footer-space)"
            >
              {/* The scroller takes any touch move or wheel turn as the reader
                  scrolling and stops following the bottom. A finger resting
                  on the thread, or a table or a command scrolled sideways, is
                  not that: only a move up or down reaches it. */}
              <div
                onPointerDownCapture={(e) => {
                  if (!(e.target as Element).closest("[aria-expanded]")) return;
                  // Opening or closing something to read it is the reader taking the
                  // view, as a scroll is: left to the scroller, the view went on
                  // following the bottom as the card grew - and the finger let go at
                  // the bottom put it back there - and the head of what was opened
                  // went up out of sight. The scroller lets go on the keys it scrolls
                  // by; one is said here, not pressed.
                  expanding.current = performance.now();
                  scrollRef.current?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
                }}
                onTouchStart={(e) => {
                  touchY.current = e.touches[0]?.clientY ?? null;
                }}
                onTouchMove={(e) => {
                  const y = e.touches[0]?.clientY;
                  if (touchY.current !== null && y !== undefined && Math.abs(y - touchY.current) < 8) {
                    e.stopPropagation();
                    return;
                  }
                  intentAt.current = performance.now();
                }}
                onWheel={(e) => {
                  if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) {
                    e.stopPropagation();
                    return;
                  }
                  intentAt.current = performance.now();
                  window.clearTimeout(wheelEnd.current);
                  wheelEnd.current = window.setTimeout(repin, 150);
                }}
              >
                <div className="mx-auto w-full max-w-page px-4 pt-[calc(var(--top-space)+1.75rem)] sm:px-8 sm:pt-[calc(var(--top-space)+2.5rem)]">
                  {turns.length > 0 && (hasMore || loadingOlder) ? (
                    olderFailed && !loadingOlder ? (
                      <button
                        type="button"
                        onClick={() => scrollRef.current && askOlder(scrollRef.current, true)}
                        className="tap mx-auto mb-6 block text-center text-meta text-faint underline-offset-2 hover:text-dim hover:underline focus-ring"
                      >
                        이전 대화를 불러오지 못했어요. 눌러서 다시
                      </button>
                    ) : (
                      <p className="mb-6 text-center text-meta text-faint" aria-live="polite">
                        {loadingOlder ? "이전 대화를 불러오는 중…" : "위로 올리면 이전 대화가 이어져요"}
                      </p>
                    )
                  ) : null}
                  <Transcript
                    turns={turns}
                    proposals={proposals}
                    byId={byId}
                    checklists={checklists}
                    waited={waited}
                    thinking={thinking}
                    draft={draft}
                    steps={steps}
                    onDraftShown={draftShown}
                    error={error}
                    hydrated={hydrated}
                    hasMore={hasMore}
                    onQuote={quote}
                    onJump={jumpTo}
                    onDecide={decide}
                    onTick={tick}
                    onDrop={dropItem}
                    onMark={mark}
                    opened={opened}
                    onFold={fold}
                    onRetry={resync}
                    starters={firstSays}
                    onStarter={startWith}
                    onGuide={openGuide}
                  />
                </div>
              </div>
            </MessageScrollerViewport>
          </MessageScroller>
        </main>
      </MessageScrollerProvider>

      {/* Lifted above the floating tab bar; it drops back down while typing,
          when the bar hides. On a phone it floats a gutter above the bar and
          a gutter in from the edges - the bar's own spacing, so the two read
          as a pair. It floats over the thread's end rather than cutting it
          short: what it does not cover is the thread's, to see and to scroll
          by (its height is --footer-space, the room under the last row). */}
      <footer
        ref={footerRef}
        className="pb-tabbar pointer-events-none relative z-20 shrink-0 transition-[padding] duration-300 ease-out motion-reduce:transition-none"
      >
        {/* Back to the newest: a bead of the composer's own glass, centred
            over it, there only while the newest turn is out of sight. It
            says how many replies came meanwhile, so a reply that landed while
            you were reading back is not missed. */}
        {seeking ? (
          <div className="pointer-events-none absolute inset-x-0 bottom-full pb-3">
            <div className="inset-x-safe">
              <div className="mx-auto flex w-full max-w-page justify-center px-4 sm:px-8">
                <p
                  role="status"
                  className="anim-settle flex h-11 items-center gap-2 rounded-full bg-popover px-4 text-ui text-dim shadow-lg ring-1 ring-edge mouse:h-10"
                >
                  <Spinner className="size-3.5" />
                  카드를 찾는 중…
                </p>
              </div>
            </div>
          </div>
        ) : away ? (
          <div className="pointer-events-none absolute inset-x-0 bottom-full pb-3">
            <div className="inset-x-safe">
              <div className="mx-auto flex w-full max-w-page justify-center px-4 sm:px-8">
                <button
                  type="button"
                  onClick={toBottom}
                  aria-label={unseen ? `새 메시지 ${unseen}개, 맨 아래로` : "맨 아래로"}
                  className={cn(
                    "anim-settle pointer-events-auto flex h-11 items-center justify-center gap-1.5 rounded-full bg-popover text-dim shadow-lg ring-1 ring-edge transition-colors hover:text-foreground focus-ring mouse:h-10",
                    unseen ? "pr-3.5 pl-4" : "w-11 sm:w-10",
                  )}
                >
                  {unseen ? <span className="tnum text-ui text-foreground">새 메시지 {unseen}</span> : null}
                  <ArrowDown aria-hidden className="size-[18px] shrink-0" strokeWidth={1.8} />
                </button>
              </div>
            </div>
          </div>
        ) : null}
        {/* Two boxes: the safe-area padding and the gutter would otherwise
            be one padding, and the safe area (0 on most phones) would win. */}
        <div className="inset-x-safe">
          <div className={cn("mx-auto w-full max-w-page px-4 sm:px-8 sm:pb-6", typing ? "pb-2" : "pb-0")}>
            <Composer
              className="pointer-events-auto"
              onSend={sendFromComposer}
              allowPhotos
              // The guest's jobs open no website: no selling for them.
              photoHint={role === "guest" ? "사진에 대해 한마디" : undefined}
              onVoice={toVoice}
              onFocusChange={setTyping}
              quoting={quotingLine}
              onCancelQuote={cancelQuote}
              inputRef={inputRef}
              fillRef={fillRef}
              onGuide={openGuide}
              busy={thinking}
              onStop={stop}
              draftKey={`jarvis:composer-draft:${role}`}
              queued={queued}
            />
            {/* On a phone this switch is the mic inside the composer; spending a
                third bar on it would leave the transcript a strip. */}
            <div className="pointer-events-auto mx-auto mt-2 hidden w-fit justify-center sm:flex">
              <ModeToggle mode={mode} onChange={switchMode} />
            </div>
          </div>
        </div>
      </footer>

      <TabBar pending={pending} hidden={typing} />
    </div>
  );
}
