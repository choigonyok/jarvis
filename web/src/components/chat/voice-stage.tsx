"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AudioLines, Keyboard, Mic, Volume2, VolumeX } from "lucide-react";
import { Caption, DOT, IconButton, PrimaryButton, QuietButton, Spectrum, type Tone } from "@/components/chat/voice-parts";
import { VoiceSetup } from "@/components/chat/voice-setup";
import { useMicLevel } from "@/lib/use-mic-level";
import { capturesAlone, primePlayer, unlockSpeech, useRecognition, type Speaker, type VoiceInfo } from "@/lib/use-speech";
import { cn } from "@/lib/utils";
import { diag } from "@/lib/diag";

/**
 * The voice surface.
 *
 * Everything else gets out of the way: a ring that shows what is being heard,
 * the words, and the way out. Once you press 시작 it keeps listening - phrases go out as
 * turns on their own, the mic closes while Jarvis is talking so it does not
 * hear itself, and it reopens when he stops. There is no send button because
 * the point of this mode is not touching the machine.
 *
 * Jarvis answers in your own voice once you have taught it to him (the 내 목소리
 * button at the top opens that room); until then, in the browser's.
 */
export function VoiceStage({
  onSend,
  speaking,
  preparing,
  speaker,
  pulse,
  speechBins,
  onStopSpeaking,
  muted,
  onMutedChange,
  onExit,
  lastSaid,
  thinking,
  progress,
  voice,
  onVoiceChanged,
  onStall,
}: {
  /** Resolves false when the message could not be sent. */
  onSend: (text: string) => Promise<boolean> | void;
  /** Sent, and nothing came back in a while: re-read the thread and reconnect. */
  onStall?: () => void;
  speaking: boolean;
  /** Speaking, but the first sentence is still being made on the Mac mini. */
  preparing: boolean;
  speaker: Speaker;
  /** 0..1, the level of the voice as it speaks. */
  pulse: number;
  /** The cloned voice's live spectrum while it plays; the ring draws it. */
  speechBins: React.RefObject<Uint8Array | null>;
  onStopSpeaking: () => void;
  muted: boolean;
  onMutedChange: (muted: boolean) => void;
  onExit: () => void;
  /** The newest agent line, for the one-line caption. */
  lastSaid?: string;
  thinking: boolean;
  /** Characters of lastSaid already spoken, or -1 when unknown. */
  progress: number;
  /** Whether your voice is on file. */
  voice: VoiceInfo;
  onVoiceChanged: () => void | Promise<void>;
}) {
  const [started, setStarted] = useState(false);
  const [setup, setSetup] = useState(false);
  const [draft, setDraft] = useState("");
  /** Your last sent line, kept above Jarvis's answer to it. */
  const [lastSent, setLastSent] = useState<string>();
  /**
   * Sent, and Jarvis has not yet said he is on it. Without this the moment
   * between the two showed the previous answer again - and the open mic
   * meanwhile - before "생각하는 중". It looks the same as
   * thinking, and ends when thinking begins, when the answer comes, or after
   * a while with nothing back (then the thread is re-read).
   */
  const [sending, setSending] = useState(false);
  /** A line under the caption when something needs saying (a send that failed). */
  const [notice, setNotice] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The draft lives in a ref as well so sending happens outside a state
  // updater - React may run an updater twice, and that would send twice.
  const pendingDraft = useRef("");
  const stopRef = useRef<() => void>(() => undefined);

  const flush = useCallback(() => {
    const text = pendingDraft.current.trim();
    pendingDraft.current = "";
    setDraft("");
    if (!text) return;
    setLastSent(text);
    setNotice(null);
    setSending(true);
    diag("voice", { ev: "stage.send", chars: text.length });
    // The question is asked. The mic stays open - what it hears from here
    // until the answer is over is dropped (holding) - because an iPhone on
    // iOS 27 opens no microphone for a session started without a tap.
    const sent = onSend(text);
    if (sent && typeof sent.then === "function") {
      void sent.then((ok) => {
        diag("voice", { ev: "stage.sent", ok: ok ? 1 : 0 });
        if (ok) return;
        setSending(false);
        setNotice("보내지 못했어요. 다시 말해 주세요.");
      });
    }
  }, [onSend]);

  const onFinal = useCallback(
    (text: string) => {
      const prev = pendingDraft.current;
      pendingDraft.current = prev ? `${prev} ${text}` : text;
      setDraft(pendingDraft.current);
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(flush, 1000);
    },
    [flush],
  );

  const { state, interim, error, heard, start, restart, stop, supported } = useRecognition({
    onFinal,
    holding: speaking || thinking || sending,
  });
  useEffect(() => {
    stopRef.current = stop;
  }, [stop]);
  const listening = state === "listening";
  // On a phone the recognizer must be the only capture (capturesAlone): the
  // meter does not open a mic of its own there, and the ring moves on what
  // the recognizer hears instead.
  const [alone] = useState(capturesAlone);
  const { level: micLevel, spectrum: micBins } = useMicLevel(listening && !alone);
  const heardLevel = useHeard(heard + interim.length);
  const level = alone ? heardLevel : micLevel;

  // Listening and hearing nothing at all for a long while. A session can say
  // it listens with a mic that is not delivering (an iPhone, after the voice
  // was cut off mid-sentence): the first time, a fresh session takes its
  // place on its own; if that hears nothing either, a button asks for a tap,
  // which the platform may need to open the mic again.
  const [round, setRound] = useState(0);
  const [quietAt, setQuietAt] = useState<string | null>(null);
  const refreshedAt = useRef<number | null>(null);
  useEffect(() => {
    if (!listening) return;
    const t = setTimeout(() => {
      if (refreshedAt.current !== heard) {
        refreshedAt.current = heard;
        primePlayer();
        restart();
        setRound((r) => r + 1);
      } else {
        setQuietAt(`${heard}|${round}`);
      }
    }, QUIET_MS);
    return () => clearTimeout(t);
  }, [listening, heard, round, restart]);
  const quietLong = listening && quietAt === `${heard}|${round}`;

  function listenAgain() {
    setQuietAt(null);
    primePlayer();
    restart(true);
  }
  // One ring, two sources: your microphone while you talk, the cloned voice's
  // audio while Jarvis does. Each is refreshed in place every frame; this only
  // decides which one the ring reads, and decides it when the ring asks (the
  // mic's bins appear a moment after listening starts, so a value copied at
  // render time would be stale).
  const talking = useRef(speaking);
  useEffect(() => {
    talking.current = speaking;
  }, [speaking]);
  const bins = useMemo<React.RefObject<Uint8Array | null>>(
    () => ({
      get current() {
        return talking.current ? speechBins.current : micBins.current;
      },
    }),
    [speechBins, micBins],
  );

  useEffect(() => {
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);

  // Jarvis took it (thinking) or answered: the sending phase is over.
  useEffect(() => {
    if (!sending) return;
    if (thinking || speaking) {
      diag("voice", { ev: "stage.taken", thinking: thinking ? 1 : 0, speaking: speaking ? 1 : 0 });
      // eslint-disable-next-line react-hooks/set-state-in-effect -- following the thread's state, not causing it
      setSending(false);
      return;
    }
    // Nothing back at all: the stream may have died quietly. Re-read, and
    // give the mic back so the question can be asked again.
    const stalled = setTimeout(() => {
      diag("voice", { ev: "stage.stall" });
      setSending(false);
      setNotice("답이 오지 않아 연결을 다시 확인했어요.");
      onStall?.();
    }, 8000);
    return () => clearTimeout(stalled);
  }, [sending, thinking, speaking, onStall]);

  // The loop that makes this hands-free: Jarvis talks, the mic closes; Jarvis
  // stops, it opens again. Without the second half you would have to tap
  // after every single answer, which is the thing this mode exists to avoid.
  useEffect(() => {
    if (!started || setup) return;
    // Left open while Jarvis thinks and talks, its words held back
    // (holding): on iOS 27 a session opened again afterwards without a tap
    // hears nothing (WebKit 321436). The answer plays through Web Audio, which
    // an iPhone sends to the speaker even with the microphone open.
    if (speaking || thinking || sending) return;
    // Not after "paused": that device wants a tap, and asking again without
    // one only gets refused again.
    if (
      listening ||
      state === "paused" ||
      state === "denied" ||
      state === "failed" ||
      state === "unsupported"
    )
      return;
    // The element that just spoke is settled first - played to the end of
    // a silent clip, as every tap does - and the mic opens a beat after: an
    // iPhone has to switch its audio from playing to recording, and asked
    // too soon it refuses - which is what turned the stage into "tap to talk".
    primePlayer();
    const reopen = setTimeout(() => {
      diag("voice", { ev: "stage.reopen", state });
      start({ auto: true });
    }, 600);
    return () => clearTimeout(reopen);
  }, [started, setup, speaking, thinking, sending, listening, state, start, stop]);
  const said = [draft, interim].filter(Boolean).join(" ");

  // One number drives the ring, whichever side of the conversation is live.
  const energy = speaking ? pulse : listening ? level : 0;
  const stopped = state === "denied" || state === "failed";
  const paused = state === "paused";
  const ownVoice = voice.state === "ready";

  /** The tap a device like iOS Safari asks for before it lets the mic reopen. */
  function resume() {
    onStopSpeaking();
    // After the stop, not before: stopping cancels whatever is queued.
    unlockSpeech();
    start();
  }

  function begin() {
    setStarted(true);
    onStopSpeaking();
    // After the stop, not before: stopping cancels whatever is queued.
    unlockSpeech();
    start();
  }

  // Ending the session keeps you on the stage, so a failed start can be
  // retried in place. Leaving is the two buttons at the top.
  function end() {
    setStarted(false);
    if (timer.current) clearTimeout(timer.current);
    pendingDraft.current = "";
    setDraft("");
    setSending(false);
    setNotice(null);
    stop();
    onStopSpeaking();
  }

  function leave() {
    end();
    onExit();
  }

  // Teaching your voice needs the microphone to itself, and silence from Jarvis.
  function openSetup() {
    end();
    setSetup(true);
  }

  if (setup) {
    return <VoiceSetup voice={voice} onBack={() => setSetup(false)} onChanged={onVoiceChanged} />;
  }

  const status: { text: string; tone: Tone } = !started
    ? { text: "대기 중", tone: "idle" }
    : stopped
      ? { text: "음성 인식 멈춤", tone: "error" }
      : paused && !speaking
        ? { text: "눌러서 말하기", tone: "idle" }
      : speaking && preparing
        ? { text: "답을 소리로 만드는 중", tone: "thinking" }
      : speaking
        ? {
            text:
              speaker === "server"
                ? "Jarvis가 내 목소리로 말하는 중"
                : ownVoice
                  ? "Jarvis가 말하는 중 (브라우저 음성으로 대신)"
                  : "Jarvis가 말하는 중",
            tone: "speaking",
          }
        : thinking || sending
          ? { text: "생각하는 중", tone: "thinking" }
          : listening
            ? { text: "듣고 있어요", tone: "listening" }
            : { text: "마이크를 여는 중", tone: "idle" };

  // What takes the big line. Whoever is talking right now owns it; between
  // turns it holds Jarvis's last answer, with your question above it.
  let caption: React.ReactNode;
  let echo: string | undefined;
  if (!started) {
    caption = (
      <Caption
        tone="hint"
        text={
          ownVoice
            ? "시작을 누르고 말하세요. 말이 끝나면 Jarvis가 내 목소리로 답해요."
            : "시작을 누르고 말하세요. 말이 끝나면 Jarvis가 소리로 답해요."
        }
      />
    );
  } else if (paused && !speaking && !thinking && !sending) {
    caption = (
      <Caption
        tone="hint"
        text={
          error === "mic-silent"
            ? "마이크가 열렸는데 소리가 들어오지 않았어요. 말하기를 눌러 다시 열어 주세요."
            : "이 기기가 마이크를 자동으로 다시 열지 못하게 했어요. 말하기를 눌러 이어서 말하세요."
        }
      />
    );
  } else if (stopped) {
    caption = (
      <Caption
        tone="hint"
        text={
          state === "denied"
            ? "마이크 권한이 필요해요. 브라우저 설정에서 허용한 뒤 다시 시작하세요."
            : failure(error)
        }
      />
    );
  } else if (said) {
    caption = <Caption tone="you" text={draft} pending={interim} />;
  } else if ((thinking || sending) && lastSent) {
    caption = <Caption tone="you" text={lastSent} />;
  } else if (lastSaid && (speaking || lastSent)) {
    echo = lastSent;
    caption = (
      <Caption
        tone="jarvis"
        text={lastSaid}
        progress={speaking ? progress : -1}
      />
    );
  } else {
    caption = <Caption tone="hint" text="편하게 말해 보세요." />;
  }

  return (
    <div className="pb-safe pt-safe inset-x-safe screen-h fixed inset-x-0 top-0 z-50 flex flex-col bg-background">
      {/* One way out, the keyboard on the right: an ✕ on the left did the
          same thing under another name. */}
      <div className="flex h-13 shrink-0 items-center justify-end px-3 sm:h-14 sm:px-6">
        <div className="flex items-center gap-1">
          <IconButton onClick={openSetup} label={ownVoice ? "내 목소리 (등록됨)" : "내 목소리 등록"}>
            <AudioLines aria-hidden className={cn("size-[18px]", ownVoice && "text-online/80")} />
          </IconButton>
          <IconButton
            onClick={() => {
              if (!muted) onStopSpeaking();
              onMutedChange(!muted);
            }}
            label={muted ? "음성 답변 켜기" : "음성 답변 끄기"}
            pressed={muted}
          >
            {muted ? (
              <VolumeX aria-hidden className="size-[18px]" />
            ) : (
              <Volume2 aria-hidden className="size-[18px]" />
            )}
          </IconButton>
          <IconButton onClick={leave} label="채팅으로 돌아가기">
            <Keyboard aria-hidden className="size-[18px]" />
          </IconButton>
        </div>
      </div>

      <main className="flex min-h-0 flex-1 flex-col items-center justify-center gap-6 px-6 sm:gap-8 sm:px-10">
        <Spectrum
          energy={energy}
          bins={bins}
          mode={
            !started || stopped
              ? "idle"
              : speaking && preparing
                ? "thinking"
              : speaking
                ? "speaking"
                : thinking || sending
                  ? "thinking"
                  : listening
                    ? "listening"
                    : "idle"
          }
          className="aspect-square w-[min(84vw,24rem,46dvh)] shrink-0"
        />

        <div className="flex min-h-0 w-full max-w-[32rem] flex-col items-center text-center">
          <p
            className="flex items-center gap-2 text-ui text-dim"
            role="status"
            aria-live="polite"
          >
            <span
              aria-hidden
              className={cn(
                "size-1.5 rounded-full transition-colors duration-300",
                DOT[status.tone],
                status.tone === "thinking" && "anim-breathe",
              )}
            />
            {status.text}
          </p>
          {echo && (
            <p className="mt-3 line-clamp-1 text-ui leading-relaxed text-faint">
              {echo}
            </p>
          )}
          <div className={cn("min-h-0 w-full", echo ? "mt-0.5" : "mt-2")}>
            {caption}
          </div>
          {notice && !said ? (
            <p className="mt-1 text-ui leading-relaxed text-faint" role="status">
              {notice}
            </p>
          ) : null}
          {quietLong && !said && !notice ? (
            <p className="mt-1 text-ui leading-relaxed text-faint">
              말해도 반응이 없나요?{" "}
              <button
                type="button"
                onClick={listenAgain}
                className="tap text-dim underline underline-offset-2 hover:text-foreground focus-ring"
              >
                다시 듣기
              </button>
            </p>
          ) : null}
        </div>
      </main>

      <div className="flex shrink-0 flex-col items-center justify-center gap-4 px-6 pt-2 pb-8">
        {!supported ? (
          <p className="max-w-[26rem] text-center text-ui leading-relaxed text-dim">
            이 브라우저는 음성 인식을 지원하지 않아요. Chrome이나 Safari에서
            열면 쓸 수 있어요.
          </p>
        ) : started && paused && !speaking ? (
          <div className="flex items-center gap-3">
            <PrimaryButton onClick={resume}>
              <Mic aria-hidden className="size-4" />
              말하기
            </PrimaryButton>
            <QuietButton onClick={end}>끝내기</QuietButton>
          </div>
        ) : started ? (
          <QuietButton onClick={end}>끝내기</QuietButton>
        ) : (
          <>
            <PrimaryButton onClick={begin}>
              <Mic aria-hidden className="size-4" />
              시작
            </PrimaryButton>
            {voice.state === "none" && (
              <button
                type="button"
                onClick={openSetup}
                className="tap text-ui text-faint transition-colors hover:text-dim focus-ring"
              >
                내 목소리로 답하게 하기
              </button>
            )}
          </>
        )}
      </div>
    </div>
  );
}

/** Hearing nothing at all this long while listening is taken for a mic that is not delivering. */
const QUIET_MS = 12_000;

/**
 * A level that jumps when `signal` changes and falls back between - the
 * ring's reading on a phone, where nothing may open the mic beside the
 * recognizer: each thing it hears (sound beginning, a word) kicks it.
 */
function useHeard(signal: number): number {
  const [level, setLevel] = useState(0);
  const value = useRef(0);
  const frame = useRef(0);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    value.current = 0.55 + Math.random() * 0.35;
    const tick = () => {
      value.current = value.current > 0.01 ? value.current * 0.92 : 0;
      setLevel(value.current);
      frame.current = value.current > 0 ? requestAnimationFrame(tick) : 0;
    };
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(tick);
  }, [signal]);
  useEffect(() => () => cancelAnimationFrame(frame.current), []);
  return level;
}

/**
 * What a fatal recognizer error means, in words that say what to do. The
 * code itself is the diagnostics' (lib/speech-listen.ts, rec.error): "(network)"
 * at the end of a sentence told nobody anything.
 */
function failure(code: string | null) {
  switch (code) {
    case "network":
      return "음성 인식 서버에 연결하지 못했어요. 끝내기를 누른 뒤 다시 시작해 보세요.";
    case "audio-capture":
      return "마이크 소리를 받지 못했어요. 입력 장치를 확인해 주세요.";
    case "language-not-supported":
      return "이 브라우저는 한국어를 알아듣지 못해요. Safari나 Chrome에서 열어 주세요.";
    default:
      return "음성 인식이 멈췄어요. 끝내기를 누른 뒤 다시 시작해 보세요.";
  }
}
