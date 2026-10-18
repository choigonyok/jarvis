"use client";

import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import Link from "next/link";
import { Reply } from "lucide-react";
import { Composer } from "@/components/chat/composer";
import { ModeToggle, type ChatMode } from "@/components/chat/mode-toggle";
import { ProposalCard } from "@/components/chat/proposal-card";
import { QueuedMessages } from "@/components/chat/queued";
import { SwipeRow } from "@/components/chat/swipe-row";
import { SearchBar, SearchToggle, useThreadSearch } from "@/components/chat/thread-search";
import { onFocusProposal } from "@/lib/resume";
import { Markdown } from "@/components/chat/markdown";
import { VoiceStage } from "@/components/chat/voice-stage";
import { Header, TabBar } from "@/components/shell/header";
import { useRole } from "@/components/shell/role";
import { StandingBar } from "@/components/shell/standing-bar";
import { isPending, plainText, quoteText, type Proposal, type Turn, type TurnRef } from "@/lib/thread";
import { photoUrl } from "@/lib/uploads";
import { speakable, useSpeech, useVoiceStatus } from "@/lib/use-speech";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";
import { clock, dayLabel, refTime, turnDay, turnTime } from "@/lib/when";

const MODE_KEY = "jarvis:chat-mode";

/** An agent turn as it is read aloud; a job's card is announced as the job's. */
function spokenText(turn: Turn): string {
  const body = speakable(turn.paragraphs?.join("\n\n") ?? "");
  if (!body) return "";
  return turn.origin ? `작업 ${turn.origin.title ? `'${turn.origin.title}'` : ""} 소식. ${body}` : body;
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

/**
 * One message of the transcript, memoized on the turn itself: an update
 * replaces only the turn it changes, so a new message renders one row, not
 * all of them.
 */
const TurnRow = memo(function TurnRow({
  turn,
  proposal,
  day,
  quoteAt,
  onQuote,
  onJump,
  onDecide,
}: {
  turn: Turn;
  proposal?: Proposal;
  /** Set on the first turn of a day: the divider above it. */
  day: string | null;
  /** When the message this one quotes was said. */
  quoteAt: string;
  onQuote: (ref: TurnRef) => void;
  onJump: (id: string) => void;
  onDecide: Decide;
}) {
  const time = clock(turn.atIso, turn.at);
  // A turn that is only a card is quoted by what the card asks.
  const quote = () =>
    onQuote({
      id: turn.id,
      role: turn.role,
      at: turnTime(turn.atIso, turn.at),
      text: quoteText(turn) || (proposal ? `승인 카드: ${proposal.card.title}` : ""),
    });
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
                {turn.origin ? (
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
  thinking,
  error,
  hydrated,
  onQuote,
  onJump,
  onDecide,
}: {
  turns: Turn[];
  proposals: Proposal[];
  byId: Map<string, Proposal>;
  thinking: boolean;
  error: string | null;
  hydrated: boolean;
  onQuote: (ref: TurnRef) => void;
  onJump: (id: string) => void;
  onDecide: Decide;
}) {
  // A proposal nobody asked for has no turn pointing at it. Until a detector
  // exists to raise one, this renders nothing - but it is what makes the
  // chat able to show one the day it does.
  const attached = new Set(turns.map((t) => t.proposalId).filter(Boolean));
  const unattached = proposals.filter((p) => isPending(p) && !attached.has(p.id));
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
          const day = turnDay(turn.atIso);
          return (
            <TurnRow
              key={turn.id}
              turn={turn}
              proposal={turn.proposalId ? byId.get(turn.proposalId) : undefined}
              day={day && day !== turnDay(turns[i - 1]?.atIso) ? day : null}
              quoteAt={turn.ref ? (times.get(turn.ref.id) ?? refTime(turn.ref.at)) : ""}
              onQuote={onQuote}
              onJump={onJump}
              onDecide={onDecide}
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

        {thinking ? (
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
    thinking,
    queue,
    connection,
    error,
    hydrated,
    send,
    steer,
    drop,
    decide,
  } = useThread();
  const bottomRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const search = useThreadSearch(scrollRef);
  // The earlier message the next one quotes: swiped (or, with a pointer, the
  // button beside its time), shown above the composer until sent or dropped.
  const [quoting, setQuoting] = useState<TurnRef | null>(null);
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
  // scroll from the top. After that, new turns pull the view down only while
  // the reader is at the bottom; someone scrolled up to read is left there.
  const landed = useRef(false);
  const pinned = useRef(true);
  // The card a tapped push brought the reader to. While set, nothing pulls
  // the view back to the bottom - not new turns, not photos above finishing
  // their layout - until the reader touches the thread themselves.
  const held = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el || turns.length === 0) return;
    if (!landed.current) {
      el.scrollTop = el.scrollHeight;
      landed.current = true;
      pinned.current = true;
      return;
    }
    if (held.current) return;
    if (pinned.current) bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [turns, proposals, thinking, error]);

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
    const release = () => (held.current = null);
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

  // Photos and long markdown finish laying out after the first paint; while
  // the reader is pinned to the bottom, keep them there as the page grows.
  useEffect(() => {
    const el = scrollRef.current;
    const content = el?.firstElementChild;
    if (!el || !content) return;
    const ro = new ResizeObserver(() => {
      // Photos above a held card push it down as they load: keep it centred.
      if (held.current) held.current.scrollIntoView({ block: "center" });
      else if (pinned.current) el.scrollTop = el.scrollHeight;
    });
    ro.observe(content);
    return () => ro.disconnect();
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
  }, [pending, turns, proposals]);

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
        onSend={(text) => void send(text)}
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
      <Header connection={connection} pending={pending} actions={<SearchToggle search={search} />} />
      <SearchBar search={search} />
      <StandingBar pending={waiting} href="/record" muted={cardsVisible} />

      <main
        ref={scrollRef}
        onScroll={(e) => {
          if (held.current) return; // our own scroll to a card, not the reader's
          const el = e.currentTarget;
          pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
        }}

        className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain"
      >
        <div className="mx-auto w-full max-w-[46rem] px-4 pt-7 pb-12 sm:px-8 sm:pt-10 sm:pb-6">
          <Transcript
            turns={turns}
            proposals={proposals}
            byId={byId}
            thinking={thinking}
            error={error}
            hydrated={hydrated}
            onQuote={quote}
            onJump={jumpTo}
            onDecide={decide}
          />
          <div ref={bottomRef} />
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
        {/* Two boxes: the safe-area padding and the gutter would otherwise
            be one padding, and the safe area (0 on most phones) would win. */}
        <div className="inset-x-safe">
          <div className={cn("mx-auto w-full max-w-[46rem] px-4 sm:px-8 sm:pb-6", typing ? "pb-2" : "pb-0")}>
            <Composer
              onSend={async (text, images) => {
                const ok = await send(text, images, quoting?.id);
                if (ok) setQuoting(null);
                return ok;
              }}
              allowPhotos={role === "owner"}
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
                <QueuedMessages queue={queue} onSteer={steer} onDrop={drop} onRestore={(text, images) => void send(text, images)} />
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
