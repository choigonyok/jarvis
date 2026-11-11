"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useCardsVisible } from "@/components/chat/cards-visible";
import { Composer } from "@/components/chat/composer";
import { ModeToggle, type ChatMode } from "@/components/chat/mode-toggle";
import { GuideSheet, starters } from "@/components/chat/guide";
import { QueuedMessages } from "@/components/chat/queued";
import { RunningJobs, useRunningJobs } from "@/components/chat/running-jobs";
import { ThreadFooter, useFooterSpace } from "@/components/chat/thread-footer";
import { JobSheet } from "@/components/jobs/job-sheet";
import { ScrollerMoves, useThreadScroll } from "@/components/chat/thread-scroll";
import { SearchBar, SearchToggle, useThreadSearch } from "@/components/chat/thread-search";
import { Transcript } from "@/components/chat/transcript";
import { VoiceStage } from "@/components/chat/voice-stage";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { useRole } from "@/components/shell/role";
import { StandingBar } from "@/components/shell/standing-bar";
import {
  MessageScroller,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller";
import type { Job } from "@/lib/jobs";
import { isPending, type Turn, type TurnRef } from "@/lib/thread";
import { diag } from "@/lib/diag";
import { speakable, useSpeech, useVoiceStatus } from "@/lib/use-speech";
import { openedTurns, useThread } from "@/lib/use-thread";

const MODE_KEY = "jarvis:chat-mode";

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
 * The conversation screen: the transcript (chat/transcript.tsx) in its
 * scroller, how its view moves (chat/thread-scroll.tsx), and the composer
 * floating over its end (chat/thread-footer.tsx) - or, in voice mode, the
 * stage in their place.
 */
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

  // The jobs working behind the conversation, and the one whose sheet is
  // open here. The sheet keeps the job it opened with: a run that ends while
  // it is read leaves the line, not the sheet.
  const newsAt = useMemo(() => {
    for (let i = turns.length - 1; i >= 0; i--) if (turns[i].origin?.kind === "job") return turns[i].atIso ?? "";
    return "";
  }, [turns]);
  const { jobs: running, reload: rereadRunning } = useRunningJobs({ thinking, newsAt });
  const [sheetJob, setSheetJob] = useState<Job | null>(null);
  const openJob = useCallback((id: number) => setSheetJob(running.find((j) => j.id === id) ?? null), [running]);

  const scroll = useThreadScroll({ scrollRef, turns, proposals, byId, hasMore, loadingOlder, olderFailed, loadOlder, mode });
  const { toNewest } = scroll;

  const quote = useCallback((ref: TurnRef) => {
    setQuoting(ref);
    // In the same gesture as the swipe, so a phone raises its keyboard - and
    // lets it scroll the composer into view above the keyboard, the way a
    // tap on the field does: with preventScroll, iOS left it underneath.
    inputRef.current?.focus();
  }, []);

  useFooterSpace(rootRef, footerRef, mode);

  // The bar speaks for what you cannot see. A card sitting in the transcript
  // in front of you is already saying it.
  const cardsVisible = useCardsVisible(scrollRef, { pending, turns, proposals, mode });

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
        <StandingBar pending={waiting} onGo={scroll.goToCard} muted={cardsVisible} />
        <RunningJobs jobs={running} onOpen={openJob} />
      </PageTop>
      <GuideSheet open={guide} onOpenChange={setGuide} role={role} />
      {/* Outside the header, as the guide's: a tap on its backdrop would
          otherwise reach the header's own tap. */}
      <JobSheet
        job={sheetJob}
        proposals={proposals}
        onDecide={decide}
        onOpenChange={(open) => !open && setSheetJob(null)}
        onChanged={() => void rereadRunning()}
      />

      <MessageScrollerProvider autoScroll defaultScrollPosition="end">
        <ScrollerMoves toRef={scroll.scroller} />
        {/* The thread runs the whole height of the screen: under the glass
            top, and under the composer and the tab bar floating over its end,
            as every other screen's page runs under its bars. */}
        <main className="-mt-(--top-space) -mb-(--footer-space) flex min-h-0 flex-1 flex-col">
          <MessageScroller>
            <MessageScrollerViewport
              ref={scrollRef}
              aria-label="대화"
              preserveScrollOnPrepend={false}
              {...scroll.viewport}
              // What is brought into view - a card, a quoted message - lands
              // between the bars, not under them.
              className="scrollbar-hairline inset-x-safe relative scroll-pt-[calc(var(--top-space)+var(--top-fade))] scroll-pb-(--footer-space)"
            >
              {/* Only a move up or down of the reader's reaches the scroller
                  (chat/thread-scroll.tsx). */}
              <div {...scroll.content}>
                <div className="mx-auto w-full max-w-page px-4 pt-[calc(var(--top-space)+1.75rem)] sm:px-8 sm:pt-[calc(var(--top-space)+2.5rem)]">
                  {turns.length > 0 && (hasMore || loadingOlder) ? (
                    olderFailed && !loadingOlder ? (
                      <button
                        type="button"
                        onClick={() => scrollRef.current && scroll.askOlder(scrollRef.current, true)}
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
                    onJump={scroll.jumpTo}
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

      <ThreadFooter
        footerRef={footerRef}
        seeking={scroll.seeking}
        away={scroll.away}
        unseen={scroll.unseen}
        onBottom={scroll.toBottom}
        typing={typing}
      >
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
      </ThreadFooter>

      <TabBar pending={pending} hidden={typing} />
    </div>
  );
}
