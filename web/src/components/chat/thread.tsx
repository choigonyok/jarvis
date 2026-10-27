"use client";

import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ArrowDown, Reply } from "lucide-react";
import { Composer } from "@/components/chat/composer";
import { ModeToggle, type ChatMode } from "@/components/chat/mode-toggle";
import { ChecklistCard } from "@/components/chat/checklist";
import { GuideSheet, GuideToggle } from "@/components/chat/guide";
import { NoticeCard } from "@/components/chat/notice-card";
import { ProposalCard } from "@/components/chat/proposal-card";
import { QueuedMessages } from "@/components/chat/queued";
import { SwipeRow } from "@/components/chat/swipe-row";
import { SearchBar, SearchToggle, useThreadSearch } from "@/components/chat/thread-search";
import { readableTitle } from "@/lib/card-body";
import { onFocusProposal } from "@/lib/resume";
import { Markdown } from "@/components/chat/markdown";
import { VoiceStage } from "@/components/chat/voice-stage";
import { Header, TabBar } from "@/components/shell/header";
import { useRole } from "@/components/shell/role";
import { StandingBar } from "@/components/shell/standing-bar";
import {
  isPending,
  plainText,
  quoteText,
  storedForm,
  type Checklist,
  type Draft,
  type Proposal,
  type Turn,
  type TurnRef,
} from "@/lib/thread";
import { diag, diagScroll, takeFrames, takeScrolls, viewOf, watchScrolls } from "@/lib/diag";
import { photoUrl } from "@/lib/uploads";
import { speakable, useSpeech, useVoiceStatus } from "@/lib/use-speech";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";
import { clock, dayLabel, refTime, turnDay, turnTime } from "@/lib/when";

const MODE_KEY = "jarvis:chat-mode";

// The checklist cards opened here (by turn), for as long as the app is
// open: leaving for another tab and coming back finds them as they were.
// A card comes folded - its title, how far along, what is left - and is
// opened by its title when wanted (2026-10-22: "접힌 게 디폴트").
const openedTurns = new Set<string>();

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
      {at ? <span className="tnum text-[11px] text-faint">{at}</span> : null}
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
      className="hidden size-6 items-center justify-center rounded-md text-faint opacity-0 transition-opacity outline-none group-hover/turn:opacity-100 hover:text-foreground focus-visible:opacity-100 focus-visible:ring-3 focus-visible:ring-ring/50 pointer-fine:flex"
    >
      <Reply aria-hidden className="size-3.5" />
    </button>
  );
}

/** Where one day of the conversation ends and the next begins. */
function DayDivider({ day }: { day: string }) {
  return (
    <li className="flex justify-center" data-search-skip>
      <span className="tnum text-[11.5px] text-faint">{dayLabel(day)}</span>
    </li>
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
      className="mb-2 block w-full rounded-xl bg-well px-3 py-2 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span className="tnum block text-[11.5px] text-faint">
        {quote.role === "agent" ? "Jarvis" : "나"}
        {at ? ` · ${at}` : ""}
      </span>
      <span className="line-clamp-2 text-[13px] leading-snug text-dim">{plainText(quote.text) || "사진"}</span>
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
function JobNews({ turn }: { turn: Turn }) {
  const [open, setOpen] = useState(false);
  const text = turn.paragraphs?.join("\n\n") ?? "";
  const long = text.length > 160 || text.includes("\n");
  return (
    <div className="max-w-[36rem] rounded-xl bg-well px-3.5 py-2.5">
      <div className="flex items-baseline justify-between gap-3" data-search-skip>
        <span className="min-w-0 truncate text-[11.5px] text-faint">
          작업 · {turn.origin?.title || "이름 없음"}
        </span>
        <Link
          href="/jobs"
          className="shrink-0 text-[11.5px] text-faint underline-offset-2 outline-none hover:text-dim hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          작업 탭
        </Link>
      </div>
      <div
        className={cn(
          "mt-1 text-[14px] leading-relaxed text-dim [&_p]:my-0 [&_ul]:my-1",
          !open && long && "line-clamp-3",
        )}
      >
        <Markdown>{text}</Markdown>
      </div>
      {long ? (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="tap mt-1 text-[12px] text-faint outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
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
 * Photos sent with a message, above its bubble. One photo is shown whole-ish;
 * several become a square grid, the way a messenger stacks an album. A photo
 * deleted after its listing ended shows as an empty tile, not a broken icon.
 */
function Photos({ names }: { names: string[] }) {
  const single = names.length === 1;
  return (
    <div
      className={cn(
        "grid max-w-[80%] gap-1 sm:max-w-[26rem]",
        single ? "grid-cols-1" : names.length === 2 || names.length === 4 ? "grid-cols-2" : "grid-cols-3",
      )}
    >
      {names.map((name) => (
        <a
          key={name}
          href={photoUrl(name)}
          target="_blank"
          rel="noreferrer"
          className="block overflow-hidden rounded-xl border border-edge-soft bg-well outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
          <img
            src={photoUrl(name)}
            alt="보낸 사진"
            loading="lazy"
            onError={(e) => (e.currentTarget.style.visibility = "hidden")}
            className={cn("block object-cover", single ? "max-h-72 w-auto max-w-full" : "aspect-square w-24 sm:w-28")}
          />
        </a>
      ))}
    </div>
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
  opened,
  onFold,
}: {
  turn: Turn;
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
  // A divider where the day changes: with it, a turn's own stamp only has to
  // say the time, and a reply that came hours later - a suggestion overnight
  // - is plainly not a live one.
  const divider = day ? <DayDivider day={day} /> : null;

  if (turn.role === "agent") {
    return (
      <>
        {divider}
        <li data-turn-id={turn.id} className="group/turn">
          <SwipeRow onRef={quote}>
            <div className={ROW}>
              <Rail at={time} onRef={quote} />
              <div className="min-w-0">
                {turn.origin?.kind === "notice" ? (
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
                  <JobNews turn={turn} />
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
                {/* The rail holds the time on a wide screen; a phone
                    has no rail, so it sits under the message. */}
                {time ? (
                  <p className="tnum mt-1.5 text-[11px] text-faint sm:hidden" data-search-skip>
                    {time}
                  </p>
                ) : null}
              </div>
            </div>
          </SwipeRow>
        </li>
      </>
    );
  }
  return (
    <>
      {divider}
      <li data-turn-id={turn.id} className="group/turn">
        <SwipeRow onRef={quote} className="flex flex-col items-end gap-1.5">
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
            <div className="max-w-[80%] rounded-2xl border border-edge bg-glass-raised px-4 py-2.5 text-[14.5px] leading-relaxed text-foreground sm:max-w-[26rem]">
              {turn.ref ? <Quote quote={turn.ref} at={quoteAt} onJump={() => onJump(turn.ref!.id)} /> : null}
              <p className="break-words whitespace-pre-wrap">{turn.text}</p>
            </div>
          ) : null}
          <span className="flex items-center gap-1.5 pr-1" data-search-skip>
            <RefButton onClick={quote} />
            <span className="tnum text-[11px] text-faint">{time}</span>
          </span>
        </SwipeRow>
      </li>
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
 * lands in one piece - 458 characters in one frame on the phone, 2026-10-27 -
 * spread over SPREAD_MS came out a sentence a redraw, not a few characters.
 * Opus writes about 150 a second, so this keeps up with it.
 */
const MAX_RATE = 0.18;
/**
 * Behind by this much, the ceiling is lifted: better a jump than a lag. Well
 * above a whole answer landing in one piece (458 on the phone), which the
 * ceiling is for - this is for a stream that has run away from the screen.
 */
const BACKLOG = 1200;
/** The least time between redraws: each one parses the markdown again. */
const FRAME_MS = 33;
/** How far each draft has come out, by id: a row built again goes on from there. */
const progress = new Map<string, number>();

/**
 * The words of an answer let out at an even pace. They reach the screen in
 * bursts - from the Mac mini a few dozen characters every half second or so
 * (2026-10-27) - and drawn as they landed they jumped a line at a time. What
 * waits is spread over about the time the next burst takes to come, so it
 * reads as typing, and a burst that lands early speeds it up. One joined
 * midway (Draft.joined: the stream opened to an answer under way) is shown
 * as far as it has come. A row built again for a draft already under way
 * (back from the voice stage) goes on from where it was (progress), not from
 * the start. With reduced motion everything is shown as it lands.
 */
function useTyped(id: string, text: string, atOnce: boolean): string {
  const [still] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches,
  );
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
      if (out || now - drawn >= FRAME_MS) {
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
 * The answer while it is written: where its turn will be, in its words, laid
 * out as the runner stores them so the turn reads the same. Once its turn has
 * come and the last word is out, the turn takes its place (onShown).
 */
const DraftRow = memo(function DraftRow({ draft, onShown }: { draft: Draft; onShown: (id: string) => void }) {
  const typed = useTyped(draft.id, draft.text, Boolean(draft.joined));
  const out = typed.length >= draft.text.length;
  useEffect(() => {
    if (out && draft.turn) {
      progress.delete(draft.id);
      onShown(draft.id);
    }
  }, [out, draft.turn, draft.id, onShown]);
  const text = storedForm(typed);
  return (
    <li className={ROW} aria-busy="true">
      <Rail />
      <div className="min-w-0">{text ? <Markdown>{text}</Markdown> : null}</div>
    </li>
  );
});

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
  onDraftShown,
  error,
  hydrated,
  onQuote,
  onJump,
  onDecide,
  onTick,
  onDrop,
  opened,
  onFold,
}: {
  turns: Turn[];
  proposals: Proposal[];
  byId: Map<string, Proposal>;
  checklists: Record<string, Checklist>;
  /** Every card that waited while this page was open. */
  waited: ReadonlySet<string>;
  thinking: boolean;
  /** The answer being written. */
  draft: Draft | null;
  onDraftShown: (id: string) => void;
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
  // A card with no turn pointing at it - one raised before every card was
  // laid in the conversation - floats below the thread while it waits. One
  // that was waiting on this screen stays once decided: a card answered
  // under your thumb and then gone reads as one that never came (2026-10-21).
  const attached = new Set(turns.map((t) => t.proposalId).filter(Boolean));
  const unattached = proposals.filter((p) => !attached.has(p.id) && (isPending(p) || waited.has(p.id)));
  // When a quoted turn is on the page its own stamp says when; one that has
  // scrolled out of the loaded thread falls back to the agent's.
  const times = new Map(turns.map((t) => [t.id, turnTime(t.atIso, t.at)]));

  return (
    <>
      {hydrated && turns.length === 0 ? (
        <div className={ROW}>
          <Rail />
          <p className="max-w-[32rem] text-[14.5px] leading-[1.75] text-dim">
            무엇을 맡길지 적어주세요. 상태를 바꾸는 일은 실행 전에 승인
            카드로 올라옵니다.
          </p>
        </div>
      ) : null}

      <ol className="space-y-7">
        {turns.map((turn, i) => {
          // A finished answer still coming out holds this place (DraftRow).
          if (turn.id === draft?.turn) return null;
          const day = turnDay(turn.atIso);
          return (
            <TurnRow
              key={turn.id}
              turn={turn}
              proposal={turn.proposalId ? byId.get(turn.proposalId) : undefined}
              list={turn.origin?.checklist ? checklists[turn.origin.checklist] : undefined}
              day={day && day !== turnDay(turns[i - 1]?.atIso) ? day : null}
              quoteAt={turn.ref ? (times.get(turn.ref.id) ?? refTime(turn.ref.at)) : ""}
              onQuote={onQuote}
              onJump={onJump}
              onDecide={onDecide}
              onTick={onTick}
              onDrop={onDrop}
              opened={opened.has(turn.id)}
              onFold={onFold}
            />
          );
        })}

        {unattached.map((proposal) => (
          <li key={proposal.id} className={ROW}>
            <Rail at={proposal.at} />
            <div className="min-w-0">
              <ProposalCard
                proposal={proposal}
                onDecide={(d, edits, reason) => onDecide(proposal.id, d, edits, reason)}
              />
            </div>
          </li>
        ))}

        {/* While the answer grows the dots have nothing to say. */}
        {draft && draft.text.trim() ? (
          <DraftRow key={draft.id} draft={draft} onShown={onDraftShown} />
        ) : thinking ? (
          <li
            className={ROW}
            aria-live="polite"
            aria-label="Jarvis가 작업 중입니다"
          >
            <Rail />
            <span
              aria-hidden
              className="anim-think flex h-6 items-center gap-1"
            >
              <span className="size-[5px] rounded-full bg-dim" />
              <span className="size-[5px] rounded-full bg-dim" />
              <span className="size-[5px] rounded-full bg-dim" />
            </span>
          </li>
        ) : null}

        {error ? (
          <li className={ROW} role="status">
            <Rail />
            <p className="max-w-[36rem] border-l-2 border-reject pl-3 text-[13.5px] leading-relaxed text-dim">
              {error}
            </p>
          </li>
        ) : null}
      </ol>
    </>
  );
});

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
    decide,
  } = useThread();
  const scrollRef = useRef<HTMLElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const search = useThreadSearch(scrollRef);
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

  const waiting = proposals.filter(isPending);
  const pending = waiting.length;
  // The bar speaks for what you cannot see. A card sitting in the transcript
  // in front of you is already saying it.
  const [cardsVisible, setCardsVisible] = useState(false);

  // Opening the tab lands on the newest turn before anything is painted - no
  // scroll from the top. After that the view follows the bottom (pinned) -
  // the dots, a message queued above the composer, an answer as it is
  // written - from a message you send (which takes the view down first), or
  // the reader coming back down to it, until the reader scrolls up themselves. Following is decided
  // by what the reader did, not by where the view happens to be: when being
  // within 80px of the bottom counted as following, a reader who had scrolled
  // a line up to read was pulled back down by the next turn.
  const landed = useRef(false);
  // The scroller it landed in. The voice stage takes this whole screen, the
  // scroller with it, and coming back builds a fresh one at the top: a new
  // scroller lands again, rather than opening on the oldest turns.
  const landedIn = useRef<HTMLElement | null>(null);
  const pinned = useRef(true);
  // Where the last scroll left the view: a scroll up from there is the
  // reader's - the view only ever goes down on its own.
  const lastTop = useRef(0);
  // Until when a send holds the view at the bottom (toNewest): one scrollTop
  // did not stick on the phone - iOS put the view back where it had been
  // within 120ms as the keyboard and the layout moved, and that read as the
  // reader scrolling up (2026-10-27 23:56). A finger on the thread ends it.
  const holdUntil = useRef(0);
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
  // The card a tapped push brought the reader to. While set, nothing pulls
  // the view back to the bottom - not new turns, not photos above finishing
  // their layout - until the reader touches the thread themselves.
  const held = useRef<HTMLElement | null>(null);
  // Whether a finger is on the thread, and the wait for scrolling to settle:
  // the page above is asked for only once the reader has stopped near the
  // top - finger lifted, fling over. On an iPhone the view's position is the
  // system's while a finger or a fling moves it, so the page's height put
  // back mid-fling was overwritten: the view fell into the page just added,
  // was near the top again, asked for the next one - one flick up ran
  // through page after page (2026-10-24).
  const touching = useRef(false);
  const settle = useRef<number | undefined>(undefined);

  // Set while a page of older turns is on its way: how tall the thread was
  // and where the reader was, so once the page is in front of them the view
  // is moved by exactly what was added and nothing seems to jump.
  const above = useRef<{ height: number; top: number } | null>(null);
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el || turns.length === 0) return;
    if (landedIn.current !== el) {
      el.scrollTop = el.scrollHeight;
      landedIn.current = el;
      landed.current = true;
      pinned.current = true;
      return;
    }
    if (above.current) {
      const { height, top } = above.current;
      above.current = null;
      const target = el.scrollHeight - height + top;
      el.scrollTop = target;
      // iOS can still be finishing a fling it owns and put its own position
      // back over this one: look again on the next frame.
      requestAnimationFrame(() => {
        if (!touching.current && Math.abs(el.scrollTop - target) > 2) el.scrollTop = target;
      });
      return;
    }
    if (held.current) return;
    // The very bottom, where landing put it - not an empty div after the last
    // turn: aligning that left the thread's bottom padding (the room the last
    // line has above the composer's fade) out of sight, so every fresh read -
    // coming back to the tab, say - slid the view 48px up, the last line under
    // the fade.
    if (pinned.current && !touching.current) {
      const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      el.scrollTo({ top: el.scrollHeight, behavior: still ? "auto" : "smooth" });
    }
  }, [turns, proposals, thinking, error, mode]);

  // Scrolling up near the top asks for the page above. Asked once per page:
  // the next ask waits for this one to land (loadingOlder) and be measured.
  const askOlder = useCallback(
    (el: HTMLElement, tapped = false) => {
      if (!hasMore || loadingOlder || above.current || !landed.current) return;
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
  const focusCard = useCallback(() => {
    const id = wanted.current;
    const card = id ? scrollRef.current?.querySelector<HTMLElement>(`[data-proposal-id="${CSS.escape(id)}"]`) : null;
    if (!card) return;
    wanted.current = null;
    pinned.current = false;
    held.current = card;
    // At once, not smooth: from the bottom of a long thread a smooth scroll
    // is a long ride, and its first frames still read as "at the bottom".
    card.scrollIntoView({ block: "center" });
    card.classList.remove("card-flash");
    void card.offsetWidth; // restart the animation on a second tap
    card.classList.add("card-flash");
    const url = new URL(window.location.href);
    if (url.searchParams.has("proposal")) {
      url.searchParams.delete("proposal");
      window.history.replaceState(window.history.state, "", url.pathname + url.search);
    }
  }, []);
  useEffect(() => {
    wanted.current = new URLSearchParams(window.location.search).get("proposal");
    return onFocusProposal((id) => {
      wanted.current = id;
      focusCard();
    });
  }, [focusCard]);
  useEffect(() => {
    if (wanted.current) focusCard();
  }, [turns, proposals, focusCard]);
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
  // were reading, and from there it follows what comes back: the dots, the
  // answer as it is written, the turns after it. At once, not smooth: from far
  // up a smooth ride is long, and the message is already on its way.
  const toNewest = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const before = { ...viewOf(el), pinned: pinned.current, held: Boolean(held.current), touching: touching.current };
    held.current = null;
    pinned.current = true;
    el.scrollTop = el.scrollHeight;
    // Held there frame by frame while the keyboard and the layout settle.
    holdUntil.current = performance.now() + 600;
    const hold = () => {
      const now = scrollRef.current;
      if (!now || performance.now() >= holdUntil.current || touching.current) return;
      now.scrollTop = now.scrollHeight;
      requestAnimationFrame(hold);
    };
    requestAnimationFrame(hold);
    // Temporary (lib/diag.ts): where the view went on the phone after a send.
    const after = viewOf(el);
    const t0 = performance.now();
    const samples: unknown[] = [];
    const take = () =>
      samples.push({ dt: Math.round(performance.now() - t0), ...viewOf(scrollRef.current), pinned: pinned.current, touching: touching.current });
    watchScrolls(3000);
    requestAnimationFrame(take);
    for (const ms of [100, 300, 700, 1500, 3000]) window.setTimeout(take, ms);
    window.setTimeout(() => diag("send", { before, after, samples, scrolls: takeScrolls() }), 3100);
  }, []);

  // Down to the newest turn - and staying there as more come.
  const toBottom = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    held.current = null;
    pinned.current = true;
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.scrollTo({ top: el.scrollHeight, behavior: still ? "auto" : "smooth" });
  }, []);

  // A quote tapped in a bubble goes back to the message it quotes, and marks
  // it the way a tapped push marks a card.
  const jumpTo = useCallback((id: string) => {
    const row = scrollRef.current?.querySelector<HTMLElement>(`[data-turn-id="${CSS.escape(id)}"]`);
    if (!row) return;
    pinned.current = false;
    held.current = row;
    row.scrollIntoView({ block: "center" });
    row.classList.remove("turn-flash");
    void row.offsetWidth;
    row.classList.add("turn-flash");
  }, []);

  // Photos and long markdown finish laying out after the first paint, and an
  // answer grows as it is written; while the reader is pinned to the bottom,
  // keep them there as the page grows. The view itself shrinks too - a
  // message queued above the composer, the keyboard - and the bottom stays
  // in sight then as well. Not under a finger: the reader is moving it.
  useEffect(() => {
    const el = scrollRef.current;
    const content = el?.firstElementChild;
    if (!el || !content) return;
    const ro = new ResizeObserver(() => {
      // Photos above a held card push it down as they load: keep it centred.
      if (held.current) held.current.scrollIntoView({ block: "center" });
      else if (pinned.current && !touching.current) el.scrollTop = el.scrollHeight;
    });
    ro.observe(content);
    ro.observe(el);
    return () => ro.disconnect();
    // Again for the scroller the voice stage's return builds.
  }, [mode]);

  // Temporary (lib/diag.ts): while an answer is written, how far it has come
  // out and where the view is, every 0.1s; and once per load, the device.
  const drafting = Boolean(draft);
  useEffect(() => {
    if (!drafting) return;
    const t0 = performance.now();
    const samples: number[][] = [];
    const id = window.setInterval(() => {
      const el = scrollRef.current;
      const row = el?.querySelector("li[aria-busy=true]");
      if (samples.length < 600)
        samples.push([
          Math.round(performance.now() - t0),
          row?.textContent?.length ?? -1,
          el ? Math.round(el.scrollHeight - el.scrollTop - el.clientHeight) : -1,
          pinned.current ? 1 : 0,
          touching.current ? 1 : 0,
        ]);
    }, 100);
    return () => {
      window.clearInterval(id);
      diag("answer", { samples, ...takeFrames() });
    };
  }, [drafting]);
  useEffect(() => {
    const id = window.setTimeout(
      () =>
        diag("load", {
          ua: navigator.userAgent,
          standalone: matchMedia("(display-mode: standalone)").matches,
          motion: matchMedia("(prefers-reduced-motion: reduce)").matches ? "reduce" : "full",
          view: viewOf(scrollRef.current),
        }),
      1500,
    );
    return () => window.clearTimeout(id);
  }, []);

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
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) seen.add(entry.target);
          else seen.delete(entry.target);
        }
        setCardsVisible(seen.size > 0);
      },
      { root, threshold: 0.35 },
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
    if (text) speak(text);
  }, [mode, muted, last, text, hydrated, speak, cancel, refreshVoice]);

  function switchMode(next: ChatMode) {
    setMode(next);
    window.localStorage.setItem(MODE_KEY, next);
  }

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
    <div className="flex h-full min-h-0 flex-col">
      <Header
        connection={connection}
        pending={pending}
        actions={
          <>
            <GuideToggle open={guide} onOpen={() => setGuide(true)} />
            <SearchToggle search={search} />
          </>
        }
      />
      <GuideSheet open={guide} onOpenChange={setGuide} role={role} />
      <SearchBar search={search} />
      <StandingBar pending={waiting} href="/record" muted={cardsVisible} />

      <main
        ref={scrollRef}
        onScroll={(e) => {
          const el = e.currentTarget;
          const gap = el.scrollHeight - el.scrollTop - el.clientHeight;
          // Where the reader is, whoever scrolled: the button is about position.
          setAway(gap > Math.max(320, el.clientHeight * 0.6));
          if (gap < 80) {
            const newest = turns.at(-1)?.id ?? null;
            setSeenAt((prev) => (prev === newest ? prev : newest));
          }
          const rose = el.scrollTop < lastTop.current - 1;
          lastTop.current = el.scrollTop;
          if (held.current) return; // our own scroll to a card, not the reader's
          // Up is the reader taking the view: following stops, even a line
          // above the bottom. Back at the very bottom it starts again.
          // (Content shrinking under a pinned view also moves it up - to the
          // bottom, which is not away from it.)
          if (performance.now() < holdUntil.current && !touching.current) pinned.current = true;
          else if (rose && gap > 1) pinned.current = false;
          else if (gap < 8) pinned.current = true;
          diagScroll(el.scrollTop, gap, rose, pinned.current);
          window.clearTimeout(settle.current);
          const ask = () => {
            if (touching.current) settle.current = window.setTimeout(ask, 120);
            else askOlder(el);
          };
          settle.current = window.setTimeout(ask, 180);
        }}
        onTouchStart={() => {
          touching.current = true;
          holdUntil.current = 0;
        }}
        onTouchEnd={(e) => {
          touching.current = false;
          // What came while a finger rested on the thread, without scrolling it.
          if (pinned.current && !held.current) e.currentTarget.scrollTop = e.currentTarget.scrollHeight;
        }}
        onTouchCancel={() => {
          touching.current = false;
        }}

        className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain"
      >
        <div className="mx-auto w-full max-w-[46rem] px-4 pt-7 pb-12 sm:px-8 sm:pt-10 sm:pb-6">
          {turns.length > 0 && (hasMore || loadingOlder) ? (
            olderFailed && !loadingOlder ? (
              <button
                type="button"
                onClick={() => scrollRef.current && askOlder(scrollRef.current, true)}
                className="tap mx-auto mb-6 block text-center text-[11.5px] text-faint underline-offset-2 outline-none hover:text-dim hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                이전 대화를 불러오지 못했어요. 눌러서 다시
              </button>
            ) : (
              <p className="mb-6 text-center text-[11.5px] text-faint" aria-live="polite">
                {loadingOlder ? "이전 대화를 불러오는 중" : "위로 올리면 이전 대화가 이어집니다"}
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
            onDraftShown={draftShown}
            error={error}
            hydrated={hydrated}
            onQuote={quote}
            onJump={jumpTo}
            onDecide={decide}
            onTick={tick}
            onDrop={dropItem}
            opened={opened}
            onFold={fold}
          />
        </div>
      </main>

      {/* Lifted above the floating tab bar; it drops back down while typing,
          when the bar hides. On a phone it floats a gutter above the bar and
          a gutter in from the edges - the bar's own spacing, so the two read
          as a pair. */}
      <footer className="pb-tabbar relative shrink-0 transition-[padding] duration-300 ease-out motion-reduce:transition-none">
        {/* The transcript dissolves into the composer instead of stopping at a rule. */}
        <div
          aria-hidden
          className="pointer-events-none absolute inset-x-0 -top-10 h-10 bg-gradient-to-b from-transparent to-background"
        />
        {/* Back to the newest: a bead of the composer's own glass, centred
            over it, there only while the newest turn is out of sight. It
            says how many replies came meanwhile, so a reply that landed while
            you were reading back is not missed. */}
        {away ? (
          <div className="pointer-events-none absolute inset-x-0 bottom-full pb-3">
            <div className="inset-x-safe">
              <div className="mx-auto flex w-full max-w-[46rem] justify-center px-4 sm:px-8">
                <button
                  type="button"
                  onClick={toBottom}
                  aria-label={unseen ? `새 메시지 ${unseen}개, 맨 아래로` : "맨 아래로"}
                  className={cn(
                    "anim-settle pointer-events-auto flex h-11 items-center justify-center gap-1.5 rounded-full bg-popover text-dim shadow-lg ring-1 ring-edge transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-10",
                    unseen ? "pr-3.5 pl-4" : "w-11 sm:w-10",
                  )}
                >
                  {unseen ? <span className="tnum text-[13px] text-foreground">새 메시지 {unseen}</span> : null}
                  <ArrowDown aria-hidden className="size-[18px] shrink-0" strokeWidth={1.8} />
                </button>
              </div>
            </div>
          </div>
        ) : null}
        {/* Two boxes: the safe-area padding and the gutter would otherwise
            be one padding, and the safe area (0 on most phones) would win. */}
        <div className="inset-x-safe">
          <div className={cn("mx-auto w-full max-w-[46rem] px-4 sm:px-8 sm:pb-6", typing ? "pb-2" : "pb-0")}>
            <Composer
              onSend={async (text, images) => {
                toNewest();
                const ok = await send(text, images, quoting?.id);
                if (ok) setQuoting(null);
                return ok;
              }}
              allowPhotos
              // The guest's jobs open no website: no selling for them.
              photoHint={role === "guest" ? "사진에 대해 한마디" : undefined}
              onVoice={() => switchMode("voice")}
              onFocusChange={setTyping}
              quoting={
                quoting
                  ? {
                      label: quoting.role === "agent" ? "Jarvis에게 답장" : "내 메시지에 답장",
                      text: quoting.text || "사진",
                    }
                  : null
              }
              onCancelQuote={() => setQuoting(null)}
              inputRef={inputRef}
              busy={thinking}
              draftKey={`jarvis:composer-draft:${role}`}
              queued={
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
              }
            />
            {/* On a phone this switch is the mic inside the composer; spending a
                third bar on it would leave the transcript a strip. */}
            <div className="mt-2 hidden justify-center sm:flex">
              <ModeToggle mode={mode} onChange={switchMode} />
            </div>
          </div>
        </div>
      </footer>

      <TabBar pending={pending} hidden={typing} />
    </div>
  );
}
