"use client";

import { useCallback, useEffect, useRef, useState } from "react";

/**
 * Both halves of voice mode come from the browser: SpeechRecognition for
 * listening and speechSynthesis for speaking. Nothing is sent to a speech
 * vendor, there is no key to rotate, and a stack with no network egress past
 * the agent still talks - which is the whole reason not to reach for a hosted
 * voice here.
 *
 * The cost is reach: recognition is Chromium and Safari only. Callers are
 * expected to check `supported` and fall back to typing.
 */

export type Listening =
  "idle" | "listening" | "paused" | "denied" | "failed" | "unsupported";

/**
 * Errors restarting cannot fix. Chrome sends audio to Google's recognizer, so
 * an unreachable service shows up as "network" on every attempt; restarting
 * on it only spins the mic forever while looking like it is listening.
 */
const FATAL = new Set(["network", "audio-capture", "language-not-supported"]);

/**
 * How long interim words may sit unchanged before they count as said.
 *
 * In continuous mode Safari - and Chrome on some platforms - keeps a phrase
 * interim for as long as the session lives and only finalizes it on stop, so
 * waiting for isFinal means waiting forever. Silence is the signal a person
 * would use, so it is the one this uses too.
 */
const SETTLE_MS = 1200;

function constructor() {
  if (typeof window === "undefined") return undefined;
  return window.SpeechRecognition ?? window.webkitSpeechRecognition;
}

export function useRecognition({
  lang = "ko-KR",
  onFinal,
}: {
  lang?: string;
  onFinal: (text: string) => void;
}) {
  const [state, setState] = useState<Listening>("idle");
  const [interim, setInterim] = useState("");
  /** The recognizer's last error code, so the screen can say what broke. */
  const [error, setError] = useState<string | null>(null);
  const recognition = useRef<SpeechRecognition | null>(null);
  const final = useRef(onFinal);
  // The recognizer is built once and lives for the page, so it has to reach
  // fresh state through a ref rather than closing over a stale callback.
  const wanted = useRef(false);
  // Whether the current session was opened by a tap. iOS Safari - and a
  // home-screen web app most of all - refuses to start recognition without
  // one, and says so as "not-allowed", the same code as a denied mic. Refused
  // on an automatic reopen, that is not a denial: it is the platform asking
  // for a tap, and the screen should ask for one rather than give up.
  const tapped = useRef(false);

  useEffect(() => {
    final.current = onFinal;
  });

  useEffect(() => {
    const Recognition = constructor();
    if (!Recognition) {
      // Set from an effect on purpose: deciding this during render would make
      // the server (no window, so "unsupported") and the hydrating client
      // disagree about the first frame.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setState("unsupported");
      return;
    }

    const engine = new Recognition();
    engine.lang = lang;
    // Continuous so a pause mid-sentence does not end the turn; interim so the
    // words appear as they are spoken instead of all at once at the end.
    engine.continuous = true;
    engine.interimResults = true;

    // Set between committing a settled phrase and the session it came from
    // ending, so a late final for the same words is not sent a second time.
    let dropping = false;
    let settle: ReturnType<typeof setTimeout> | undefined;

    engine.onresult = (event) => {
      if (dropping) return;
      let pending = "";
      for (let i = event.resultIndex; i < event.results.length; i += 1) {
        const result = event.results[i];
        const text = result[0]?.transcript ?? "";
        if (result.isFinal) {
          const trimmed = text.trim();
          if (trimmed) final.current(trimmed);
        } else {
          pending += text;
        }
      }
      setInterim(pending);

      clearTimeout(settle);
      const heard = pending.trim();
      if (!heard) return;
      settle = setTimeout(() => {
        // Abort rather than stop: stop would finalize these same words and
        // deliver them again. onend below reopens the mic.
        dropping = true;
        setInterim("");
        final.current(heard);
        engine.abort();
      }, SETTLE_MS);
    };

    engine.onerror = (event) => {
      console.warn("[voice] recognition error:", event.error, event.message);
      const refused =
        event.error === "not-allowed" || event.error === "service-not-allowed";
      // On an automatic reopen, a refusal (or the mic still held by the voice
      // that just finished speaking) means "tap to talk", not "broken".
      if (!tapped.current && (refused || event.error === "audio-capture")) {
        wanted.current = false;
        setState("paused");
        return;
      }
      if (refused) {
        wanted.current = false;
        setState("denied");
        return;
      }
      if (FATAL.has(event.error)) {
        wanted.current = false;
        setError(event.error);
        setState("failed");
        return;
      }
      // "no-speech" and "aborted" are ordinary: onend restarts below.
      setInterim("");
    };

    engine.onend = () => {
      dropping = false;
      clearTimeout(settle);
      setInterim("");
      // Chrome stops the recognizer on its own after a stretch of silence.
      // If the user has not asked to stop, start it again so the mic stays on.
      if (wanted.current) {
        tapped.current = false;
        try {
          engine.start();
        } catch {
          setState("paused");
        }
      } else {
        // A fatal error has already said why it stopped; keep that on screen.
        setState((prev) =>
          prev === "failed" || prev === "denied" || prev === "paused" ? prev : "idle",
        );
      }
    };

    recognition.current = engine;
    return () => {
      wanted.current = false;
      clearTimeout(settle);
      engine.onend = null;
      engine.abort();
      recognition.current = null;
    };
  }, [lang]);

  /** `auto` when nobody tapped for this start - the hands-free reopen after Jarvis speaks. */
  const start = useCallback((opts?: { auto?: boolean }) => {
    const engine = recognition.current;
    if (!engine) return;
    tapped.current = !opts?.auto;
    wanted.current = true;
    setError(null);
    try {
      engine.start();
      setState("listening");
    } catch {
      // Already running - which is the state we wanted anyway. Refused
      // outright (no tap) surfaces through onerror instead.
      setState("listening");
    }
  }, []);

  const stop = useCallback(() => {
    wanted.current = false;
    setInterim("");
    recognition.current?.stop();
    setState((prev) => (prev === "listening" ? "idle" : prev));
  }, []);

  return {
    state,
    interim,
    error,
    start,
    stop,
    supported: state !== "unsupported",
  };
}

/**
 * How natural a voice sounds, judged by the only thing engines expose: its
 * name. Edge's "Online (Natural)" voices are neural; macOS ships Premium and
 * Enhanced downloads next to the robotic compact default. Chrome's "Google"
 * voices sound better than the compact one but are cut off by Chrome after
 * about fifteen seconds, which a long answer always hits - so they rank
 * below any local voice. macOS also lists its old Eloquence character voices
 * under every language; they are the most robotic of all and come last.
 */
const ELOQUENCE = /^(eddy|flo|grandma|grandpa|reed|rocko|sandy|shelley)\b/;

function quality(voice: SpeechSynthesisVoice) {
  const name = voice.name.toLowerCase();
  if (/natural|neural/.test(name)) return 4;
  if (name.includes("premium")) return 3;
  if (name.includes("enhanced")) return 2;
  if (ELOQUENCE.test(name)) return -1;
  if (name.includes("google")) return 0;
  return 1;
}

/** Prefers the most natural Korean voice; the platform default is the fallback. */
function pickVoice(voices: SpeechSynthesisVoice[], lang: string) {
  const base = lang.split("-")[0];
  const best = (candidates: SpeechSynthesisVoice[]) =>
    candidates.reduce<SpeechSynthesisVoice | undefined>(
      (top, v) => (!top || quality(v) > quality(top) ? v : top),
      undefined,
    );
  const normalized = (v: SpeechSynthesisVoice) => v.lang.replace("_", "-");
  return (
    best(voices.filter((v) => normalized(v) === lang)) ??
    best(voices.filter((v) => normalized(v).startsWith(base)))
  );
}

export function useSpeech(lang = "ko-KR") {
  const [speaking, setSpeaking] = useState(false);
  /**
   * Rises on every spoken word and decays between them, 0..1.
   *
   * The synthesizer's audio never reaches an AudioContext - browsers play it
   * outside the graph - so there is no waveform to tap and no library that
   * can conjure one. The word boundary is the only signal tied to what is
   * actually being said, so that is what the ring moves on. Engines that do
   * not fire it for Korean fall back to a steady pulse, which is honest about
   * being a heartbeat rather than a reading.
   */
  const [pulse, setPulse] = useState(0);
  /**
   * How far into the current utterance the voice has reached, in characters,
   * or -1 when no engine has reported a boundary. The caption lights up to
   * here, so you can read along with what you are hearing.
   */
  const [progress, setProgress] = useState(-1);
  const voices = useRef<SpeechSynthesisVoice[]>([]);
  const decay = useRef<number>(0);

  useEffect(() => {
    if (typeof window === "undefined" || !window.speechSynthesis) return;
    const load = () => {
      voices.current = window.speechSynthesis.getVoices();
    };
    load();
    // Chrome populates the list asynchronously, after this first call.
    window.speechSynthesis.addEventListener("voiceschanged", load);
    return () => {
      window.speechSynthesis.removeEventListener("voiceschanged", load);
      window.speechSynthesis.cancel();
    };
  }, []);

  // One decay loop for the life of the hook rather than one per utterance.
  useEffect(() => {
    let frame = 0;
    const tick = () => {
      setPulse((p) => (p > 0.01 ? p * 0.9 : 0));
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, []);

  const cancel = useCallback(() => {
    window.speechSynthesis?.cancel();
    window.clearInterval(decay.current);
    setSpeaking(false);
    setPulse(0);
    setProgress(-1);
  }, []);

  const speak = useCallback(
    (text: string) => {
      const synth =
        typeof window === "undefined" ? undefined : window.speechSynthesis;
      if (!synth || !text.trim()) return;
      synth.cancel();
      const utterance = new SpeechSynthesisUtterance(text);
      utterance.lang = lang;
      const voice = pickVoice(voices.current, lang);
      if (voice) utterance.voice = voice;
      let sawBoundary = false;
      utterance.onboundary = (event) => {
        sawBoundary = true;
        // charLength is missing on some engines; the word itself is the
        // fallback, read up to the next space.
        const rest = text.slice(event.charIndex);
        const length = event.charLength || rest.search(/\s|$/);
        setProgress(event.charIndex + length);
        window.clearInterval(decay.current);
        // A word landed: kick the ring. The loop above brings it back down.
        setPulse(0.55 + Math.random() * 0.45);
      };

      const finish = () => {
        window.clearInterval(decay.current);
        setSpeaking(false);
        setPulse(0);
        setProgress(-1);
      };
      utterance.onend = finish;
      utterance.onerror = finish;

      setSpeaking(true);
      setProgress(-1);
      synth.speak(utterance);

      // Korean voices on several engines never fire onboundary. Give it a
      // moment, and if nothing arrived, beat steadily instead of standing
      // still while the room is clearly full of speech.
      window.clearInterval(decay.current);
      decay.current = window.setInterval(() => {
        if (sawBoundary) {
          window.clearInterval(decay.current);
          return;
        }
        setPulse(0.4 + Math.random() * 0.3);
      }, 260);
    },
    [lang],
  );

  return { speak, cancel, speaking, pulse, progress };
}

/**
 * What the agent wrote, as something worth hearing. Markdown read aloud is
 * unbearable - fences, pipes and asterisks all get pronounced - so the marks
 * come off and code blocks are named rather than recited.
 */
export function speakable(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, " 코드 블록. ")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    .replace(/^\s{0,3}>\s?/gm, "")
    .replace(/^\s*[-*+]\s+/gm, "")
    .replace(/(\*\*|__|\*|_|~~)/g, "")
    .replace(/^\s*\|.*\|\s*$/gm, "")
    .replace(/\n{2,}/g, ". ")
    .replace(/\s+/g, " ")
    .trim();
}
