"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Composer } from "@/components/chat/composer";
import { ModeToggle, type ChatMode } from "@/components/chat/mode-toggle";
import { ProposalCard } from "@/components/chat/proposal-card";
import { onFocusProposal } from "@/lib/resume";
import { Markdown } from "@/components/chat/markdown";
import { VoiceStage } from "@/components/chat/voice-stage";
import { Header, TabBar } from "@/components/shell/header";
import { useRole } from "@/components/shell/role";
import { StandingBar } from "@/components/shell/standing-bar";
import { isPending } from "@/lib/thread";
import { photoUrl } from "@/lib/uploads";
import { speakable, useSpeech } from "@/lib/use-speech";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

const MODE_KEY = "jarvis:chat-mode";

/**
 * Timestamps and status live in the rail, so the body stays a clean measure.
 *
 * The rail is a wide-screen luxury: 44px of gutter is an eighth of a phone,
 * spent on a stamp nobody reads while scrolling the last three turns. It
 * collapses below sm. What matters for an audit - when a request was decided -
 * is on the proposal card itself and survives the collapse.
 */
function Rail({ at }: { at?: string }) {
  return (
    <div className="hidden pt-[3px] pr-3 text-right sm:block sm:pr-4">
      {at ? <span className="tnum text-[11px] text-faint">{at}</span> : null}
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

export function Thread() {
  const role = useRole();
  const {
    turns,
    proposals,
    byId,
    thinking,
    connection,
    error,
    hydrated,
    send,
    decide,
  } = useThread();
  const bottomRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLElement>(null);
  const [mode, setMode] = useState<ChatMode>("text");
  const [muted, setMuted] = useState(false);
  const { speak, cancel, speaking, pulse, progress } = useSpeech();
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

  // A proposal nobody asked for has no turn pointing at it. Until a detector
  // exists to raise one, this renders nothing - but it is what makes the
  // chat able to show one the day it does.
  const attached = new Set(turns.map((t) => t.proposalId).filter(Boolean));
  const unattached = proposals.filter(
    (p) => isPending(p) && !attached.has(p.id),
  );

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

  useEffect(() => {
    if (mode !== "voice") {
      cancel();
      spoken.current = null;
      return;
    }
    // Entering voice mode marks whatever is already on screen as heard.
    if (spoken.current === null) {
      spoken.current = last?.id ?? "";
      return;
    }
    if (muted) return;
    if (!last || last.role !== "agent" || last.id === spoken.current) return;
    spoken.current = last.id;
    const text = speakable(last.paragraphs?.join("\n\n") ?? "");
    if (text) speak(text);
  }, [mode, muted, last, speak, cancel]);

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
        pulse={pulse}
        progress={progress}
        onStopSpeaking={cancel}
        muted={muted}
        onMutedChange={setMuted}
        onExit={() => switchMode("text")}
        lastSaid={
          last?.role === "agent"
            ? speakable(last.paragraphs?.join("\n\n") ?? "")
            : undefined
        }
        thinking={thinking}
      />
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={pending} />
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
            {turns.map((turn) => {
              const proposal = turn.proposalId
                ? byId.get(turn.proposalId)
                : undefined;
              return turn.role === "agent" ? (
                <li key={turn.id} className={ROW}>
                  <Rail at={turn.at} />
                  <div className="min-w-0">
                    {turn.paragraphs?.length ? (
                      // The runner split the message on blank lines; putting
                      // them back is what makes a list or a code fence one
                      // block again.
                      <Markdown>{turn.paragraphs.join("\n\n")}</Markdown>
                    ) : null}
                    {proposal ? (
                      <ProposalCard
                        proposal={proposal}
                        onDecide={(d, edits) => decide(proposal.id, d, edits)}
                      />
                    ) : null}
                  </div>
                </li>
              ) : (
                <li key={turn.id} className="flex flex-col items-end gap-1.5">
                  {turn.images?.length ? <Photos names={turn.images} /> : null}
                  {turn.text ? (
                    <p className="max-w-[80%] rounded-2xl border border-edge bg-glass-raised px-4 py-2.5 text-[14.5px] leading-relaxed text-foreground backdrop-blur-md sm:max-w-[26rem]">
                      {turn.text}
                    </p>
                  ) : null}
                  <span className="tnum pr-1 text-[11px] text-faint">
                    {turn.at}
                  </span>
                </li>
              );
            })}

            {unattached.map((proposal) => (
              <li key={proposal.id} className={ROW}>
                <Rail at={proposal.at} />
                <div className="min-w-0">
                  <ProposalCard
                    proposal={proposal}
                    onDecide={(d, edits) => decide(proposal.id, d, edits)}
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
          <div ref={bottomRef} />
        </div>
      </main>

      {/* Lifted above the floating tab bar; it drops back down while typing,
          when the bar hides. */}
      <footer className="pb-tabbar relative shrink-0 transition-[padding] duration-300 ease-out motion-reduce:transition-none">
        {/* The transcript dissolves into the composer instead of stopping at a rule. */}
        <div
          aria-hidden
          className="pointer-events-none absolute inset-x-0 -top-10 h-10 bg-gradient-to-b from-transparent to-background"
        />
        <div className="inset-x-safe mx-auto w-full max-w-[46rem] px-4 pb-3 sm:px-8 sm:pb-6">
          <Composer
            onSend={(text, images) => send(text, images)}
            allowPhotos={role === "owner"}
            onVoice={() => switchMode("voice")}
            onFocusChange={setTyping}
          />
          {/* On a phone this switch is the mic inside the composer; spending a
              third bar on it would leave the transcript a strip. */}
          <div className="mt-2 hidden justify-center sm:flex">
            <ModeToggle mode={mode} onChange={switchMode} />
          </div>
        </div>
      </footer>

      <TabBar pending={pending} hidden={typing} />
    </div>
  );
}
