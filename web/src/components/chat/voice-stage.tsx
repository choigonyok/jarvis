"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AudioLines, Keyboard, Mic, Volume2, VolumeX, X } from "lucide-react";
import { Caption, DOT, IconButton, PrimaryButton, QuietButton, Spectrum, type Tone } from "@/components/chat/voice-parts";
import { VoiceSetup } from "@/components/chat/voice-setup";
import { useMicLevel } from "@/lib/use-mic-level";
import { capturesAlone, unlockSpeech, useRecognition, type Speaker, type VoiceInfo } from "@/lib/use-speech";
import { cn } from "@/lib/utils";

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
   * meanwhile - before "생각하는 중" (2026-10-19). It looks the same as
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
    // The question is asked: the mic closes now, not when the answer begins.
    stopRef.current();
    const sent = onSend(text);
    if (sent && typeof sent.then === "function") {
      void sent.then((ok) => {
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

  const { state, interim, error, heard, start, stop, supported } = useRecognition({
    onFinal,
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
      // eslint-disable-next-line react-hooks/set-state-in-effect -- following the thread's state, not causing it
      setSending(false);
      return;
    }
    // Nothing back at all: the stream may have died quietly. Re-read, and
    // give the mic back so the question can be asked again.
    const stalled = setTimeout(() => {
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
    // Closed while Jarvis thinks as well as while it talks: an open mic keeps
    // an iPhone in call mode, and the answer then plays through the earpiece -
    // too quietly to hear. It reopens once the answer has been spoken.
    if (speaking || thinking || sending) {
      if (listening) stop();
      return;
    }
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
    // A beat after the voice stops before the mic opens: an iPhone has to
    // switch its audio from playing to recording, and asked too soon it
    // refuses - which is what turned the stage into "tap to talk".
    const reopen = setTimeout(() => start({ auto: true }), 600);
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
            ? { text: "듣고 있습니다", tone: "listening" }
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
            ? "시작을 누르고 말하세요. 말이 끝나면 Jarvis가 내 목소리로 답합니다."
            : "시작을 누르고 말하세요. 말이 끝나면 Jarvis가 소리로 답합니다."
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
            : `이 기기가 마이크를 자동으로 다시 열지 못하게 했습니다. 말하기를 눌러 이어서 말하세요.${error ? ` (${error})` : ""}`
        }
      />
    );
  } else if (stopped) {
    caption = (
      <Caption
        tone="hint"
        text={
          state === "denied"
            ? "마이크 권한이 필요합니다. 브라우저 설정에서 허용한 뒤 다시 시작하세요."
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
      <div className="flex h-13 shrink-0 items-center justify-between px-3 sm:h-14 sm:px-6">
        <IconButton onClick={leave} label="음성 모드 나가기">
          <X aria-hidden className="size-[18px]" />
        </IconButton>

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
            className="flex items-center gap-2 text-[13px] text-dim"
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
            <p className="mt-3 line-clamp-1 text-[13.5px] leading-relaxed text-faint">
              {echo}
            </p>
          )}
          <div className={cn("min-h-0 w-full", echo ? "mt-0.5" : "mt-2")}>
            {caption}
          </div>
          {notice && !said ? (
            <p className="mt-1 text-[13px] leading-relaxed text-faint" role="status">
              {notice}
            </p>
          ) : null}
        </div>
      </main>

      <div className="flex shrink-0 flex-col items-center justify-center gap-4 px-6 pt-2 pb-8">
        {!supported ? (
          <p className="max-w-[26rem] text-center text-[13px] leading-relaxed text-dim">
            이 브라우저는 음성 인식을 지원하지 않습니다. Chrome이나 Safari에서
            열면 쓸 수 있습니다.
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
                className="tap text-[13px] text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
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

/** What a fatal recognizer error means, in words that say what to do. */
function failure(code: string | null) {
  switch (code) {
    case "network":
      return "음성 인식 서버에 연결하지 못했습니다. 끝내기를 누른 뒤 다시 시작해 보세요. (network)";
    case "audio-capture":
      return "마이크 소리를 받지 못했습니다. 입력 장치를 확인해 주세요. (audio-capture)";
    case "language-not-supported":
      return "이 브라우저가 한국어 인식을 지원하지 않습니다. (language-not-supported)";
    default:
      return `음성 인식이 멈췄습니다. (${code ?? "unknown"})`;
  }
}
