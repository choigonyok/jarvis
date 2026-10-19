"use client";

import { useCallback, useEffect, useRef, useState } from "react";

/**
 * Both halves of voice mode. Listening is the browser's SpeechRecognition.
 * Speaking is the operator's own voice when one is on file - Qwen3-TTS on the
 * Mac mini (tts/server.py), reached through /api/tts - and otherwise the
 * browser's speechSynthesis, which needs no server at all. Nothing goes to a
 * speech vendor either way, and there is no key to rotate.
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

/** A beat after a session ends before the next is opened: the platform has to let go of the last one first. */
const REOPEN_MS = 200;
/** How many times a refused start is tried again, 250 ms apart, before asking for a tap. */
const START_TRIES = 8;
/** A start the engine took but never confirmed (no onstart) is given up on after this. */
const OPEN_MS = 4000;
/** An abort is expected to end the session; past this it is treated as ended anyway. */
const END_MS = 1500;
/** Once an engine has been seen to report its capture, a session that does not within this is deaf. */
const AUDIO_MS = 3000;
/** Sound began and no words came in this long: the session is deaf. */
const WORDS_MS = 7000;
/** Sessions given up on in a row (never started, never captured) before asking for a tap. */
const DEAF_LIMIT = 3;

/**
 * Whether this is a phone or a tablet. There the recognizer must be the only
 * thing capturing: an iPhone lets one audio capture run per page and mutes the
 * one before when another starts, and Android's recognizer and a page's own
 * capture contend for the mic. The voice stage's meter does not open its own
 * microphone there; the ring moves on what the recognizer hears instead.
 */
export function capturesAlone(): boolean {
  if (typeof navigator === "undefined") return false;
  const ua = navigator.userAgent;
  return /iPhone|iPad|iPod|Android/i.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
}

/**
 * Listening, one session at a time, each on a fresh engine.
 *
 * A session ends often - a phrase settles, the engine times out on silence,
 * the stage closes it while Jarvis talks - and the next one is a new
 * SpeechRecognition, not the old one started again: an engine reused after an
 * abort can come back saying it listens while it hears nothing (2026-10-19,
 * on an iPhone after two exchanges). Every timer and flag of a session lives
 * in that session, so nothing stuck in one can leak into the next. "Listening"
 * is the engine's own word (onstart), and a session that says so but never
 * opens the mic, or hears sound but never words, is replaced.
 */
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
  /** Rises each time the recognizer hears something - sound began, words came. */
  const [heard, setHeard] = useState(0);
  const engineRef = useRef<SpeechRecognition | null>(null);
  const final = useRef(onFinal);
  const langRef = useRef(lang);
  // Whether a session should be open: set by start, cleared by stop and by
  // what cannot be fixed by trying again (a refusal, a fatal error).
  const wanted = useRef(false);
  // Whether the current session was opened by a tap. iOS Safari - and a
  // home-screen web app most of all - refuses to start recognition without
  // one, and says so as "not-allowed", the same code as a denied mic. Refused
  // on an automatic reopen, that is not a denial: it is the platform asking
  // for a tap, and the screen should ask for one rather than give up.
  const tapped = useRef(false);
  // The pending (re)open, if any.
  const retry = useRef<ReturnType<typeof setTimeout> | null>(null);
  // True from a start() the engine took until it says it is listening (or ends).
  const opening = useRef(false);
  // Sessions given up on in a row; reset by any words heard.
  const deaf = useRef(0);
  // Whether this engine has been seen to report its capture (onaudiostart).
  // Only then is a session that never does so taken to be deaf - an engine
  // that never reports it is not punished for that.
  const reportsAudio = useRef(false);
  const attemptRef = useRef<((tries: number) => void) | null>(null);

  useEffect(() => {
    final.current = onFinal;
    langRef.current = lang;
  });

  // Let the session in use go: its handlers fall silent, and it is told to stop.
  const retire = useCallback(() => {
    const e = engineRef.current;
    engineRef.current = null;
    opening.current = false;
    if (!e) return;
    e.onresult = null;
    e.onerror = null;
    e.onstart = null;
    e.onend = null;
    e.onaudiostart = null;
    e.onsoundstart = null;
    e.onspeechstart = null;
    try {
      e.abort();
    } catch {
      // Already gone.
    }
  }, []);

  const schedule = useCallback((delay: number) => {
    if (retry.current) clearTimeout(retry.current);
    retry.current = setTimeout(() => attemptRef.current?.(0), delay);
  }, []);

  useEffect(() => {
    if (!constructor()) {
      // Set from an effect on purpose: deciding this during render would make
      // the server (no window, so "unsupported") and the hydrating client
      // disagree about the first frame.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setState("unsupported");
      return;
    }
    return () => {
      wanted.current = false;
      if (retry.current) clearTimeout(retry.current);
      retire();
    };
  }, [retire]);

  useEffect(() => {
    // One attempt to open a session; a start the engine refuses (it is still
    // letting go of the last one) is tried again shortly.
    attemptRef.current = (tries: number) => {
      if (!wanted.current) return;
      const Recognition = constructor();
      if (!Recognition) return;
      retire();
      const engine = new Recognition();
      engine.lang = langRef.current;
      // Continuous so a pause mid-sentence does not end the turn; interim so the
      // words appear as they are spoken instead of all at once at the end.
      engine.continuous = true;
      engine.interimResults = true;
      engineRef.current = engine;
      const mine = () => engineRef.current === engine;

      // This session's own state.
      let dropping = false; // a settled phrase was taken; late results of it are not
      let gotWords = false;
      let captured = false; // this session reported its capture (in whatever order)
      let settle: ReturnType<typeof setTimeout> | undefined;
      let ending: ReturnType<typeof setTimeout> | undefined;
      let starting: ReturnType<typeof setTimeout> | undefined;
      let capture: ReturnType<typeof setTimeout> | undefined;
      let words: ReturnType<typeof setTimeout> | undefined;
      const quiet = () => {
        clearTimeout(settle);
        clearTimeout(ending);
        clearTimeout(starting);
        clearTimeout(capture);
        clearTimeout(words);
      };

      // The session is over: open the next while one is wanted.
      const finish = () => {
        if (!mine()) return;
        quiet();
        engineRef.current = null;
        opening.current = false;
        setInterim("");
        if (wanted.current) {
          tapped.current = false;
          schedule(REOPEN_MS);
        } else {
          // A fatal error has already said why it stopped; keep that on screen.
          setState((prev) => (prev === "failed" || prev === "denied" || prev === "paused" ? prev : "idle"));
        }
      };

      // The session is not working: a fresh one in its place - unless that
      // has happened too many times in a row, when a tap is asked for.
      const replace = (why: string, counts: boolean) => {
        if (!mine()) return;
        console.warn("[voice] recognizer", why, "- opening a fresh one");
        quiet();
        retire();
        if (counts) deaf.current += 1;
        if (deaf.current >= DEAF_LIMIT) {
          deaf.current = 0;
          wanted.current = false;
          setInterim("");
          setError("mic-silent");
          setState("paused");
          return;
        }
        if (wanted.current) schedule(REOPEN_MS);
      };

      engine.onstart = () => {
        if (!mine()) return;
        clearTimeout(starting);
        opening.current = false;
        setError(null);
        setState("listening");
        if (reportsAudio.current && !captured) {
          capture = setTimeout(() => replace("never opened the microphone", true), AUDIO_MS);
        }
      };
      engine.onaudiostart = () => {
        if (!mine()) return;
        captured = true;
        reportsAudio.current = true;
        clearTimeout(capture);
      };
      const sound = () => {
        if (!mine()) return;
        captured = true;
        clearTimeout(capture);
        setHeard((h) => h + 1);
        if (!gotWords) {
          clearTimeout(words);
          words = setTimeout(() => replace("heard sound but no words", false), WORDS_MS);
        }
      };
      engine.onsoundstart = sound;
      engine.onspeechstart = sound;

      engine.onresult = (event) => {
        if (!mine() || dropping) return;
        clearTimeout(capture);
        clearTimeout(words);
        gotWords = true;
        deaf.current = 0;
        setHeard((h) => h + 1);
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
        const heardText = pending.trim();
        if (!heardText) return;
        settle = setTimeout(() => {
          if (!mine()) return;
          // Abort rather than stop: stop would finalize these same words and
          // deliver them again. The session's end opens the next one.
          dropping = true;
          setInterim("");
          final.current(heardText);
          try {
            engine.abort();
          } catch {
            // Already ending.
          }
          // An engine that never says it ended is let go anyway.
          ending = setTimeout(finish, END_MS);
        }, SETTLE_MS);
      };

      engine.onerror = (event) => {
        if (!mine()) return;
        opening.current = false;
        console.warn("[voice] recognition error:", event.error, event.message);
        const refused = event.error === "not-allowed" || event.error === "service-not-allowed";
        // On an automatic reopen, a refusal (or the mic still held by the voice
        // that just finished speaking) means "tap to talk", not "broken".
        if (!tapped.current && (refused || event.error === "audio-capture")) {
          wanted.current = false;
          setError(event.error);
          setState("paused");
        } else if (refused) {
          wanted.current = false;
          setState("denied");
        } else if (FATAL.has(event.error)) {
          wanted.current = false;
          setError(event.error);
          setState("failed");
        } else {
          // "no-speech" and "aborted" are ordinary: the session's end opens the next.
          setInterim("");
          return;
        }
        // Stopped for good: the end may or may not come; this session is done either way.
        ending = setTimeout(finish, END_MS);
      };

      engine.onend = finish;

      try {
        engine.start();
        opening.current = true;
        // Listening is announced by onstart, not here: a state that says
        // "listening" with a closed mic is what this replaces.
        setState((prev) => (prev === "listening" ? prev : "idle"));
        starting = setTimeout(() => replace("never started", true), OPEN_MS);
      } catch {
        // Still letting go of the last session: try again shortly.
        quiet();
        retire();
        if (tries < START_TRIES) {
          retry.current = setTimeout(() => attemptRef.current?.(tries + 1), 250);
          return;
        }
        wanted.current = false;
        setError("start-failed");
        setState("paused");
      }
    };
  });

  /** `auto` when nobody tapped for this start - the hands-free reopen after Jarvis speaks. */
  const start = useCallback((opts?: { auto?: boolean }) => {
    // Already asked and waiting for the engine's word: nothing to add.
    if (opening.current && wanted.current) return;
    tapped.current = !opts?.auto;
    wanted.current = true;
    deaf.current = 0;
    setError(null);
    if (retry.current) clearTimeout(retry.current);
    attemptRef.current?.(0);
  }, []);

  const stop = useCallback(() => {
    wanted.current = false;
    if (retry.current) clearTimeout(retry.current);
    setInterim("");
    const e = engineRef.current;
    if (e) {
      try {
        e.stop();
      } catch {
        // Not running.
      }
    }
    opening.current = false;
    setState((prev) => (prev === "listening" ? "idle" : prev));
  }, []);

  return {
    state,
    interim,
    error,
    heard,
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

/**
 * The one element the cloned voice plays through.
 *
 * A media element, not an AudioContext, on purpose. On an iPhone Web Audio
 * is silenced by the ring/silent switch (media playback is not), and a
 * context opened before the microphone was used keeps the old sample rate
 * after it - the hardware moves to 48 kHz for the mic - and plays everything
 * a fraction high and fast from then on. Both were heard as the voice
 * "changing between answers" and "sometimes no sound" (2026-10-19). A media
 * element has neither problem, and once a tap has played it, it plays on
 * its own afterwards.
 */
let player: HTMLAudioElement | null = null;
let playerUnlocked = false;
export function audioPlayer(): HTMLAudioElement | null {
  if (typeof window === "undefined") return null;
  if (!player) {
    player = new Audio();
    player.preload = "auto";
    // Not a background track: keep it out of the lock screen's controls.
    player.setAttribute("playsinline", "");
  }
  return player;
}

/** A WAV of one silent sample: what the element plays inside the tap that unlocks it. */
const SILENT_WAV =
  "data:audio/wav;base64,UklGRiYAAABXQVZFZm10IBAAAAABAAEAQB8AAIA+AAACABAAZGF0YQIAAAAAAA==";

/**
 * iOS lets a page make sound on its own only after it has done so once inside
 * a tap. Answers arrive long after any tap, so the voice stage calls this from
 * its buttons: a silent utterance and a silent sample, there and then, open
 * the way for both engines.
 */
export function unlockSpeech() {
  const synth = typeof window === "undefined" ? undefined : window.speechSynthesis;
  if (synth) {
    try {
      const u = new SpeechSynthesisUtterance(" ");
      u.volume = 0;
      synth.speak(u);
      synth.resume();
    } catch {
      // Nothing to unlock on this engine.
    }
  }
  const el = audioPlayer();
  if (el && !playerUnlocked) {
    try {
      el.muted = true;
      el.src = SILENT_WAV;
      const p = el.play();
      if (p && typeof p.then === "function") {
        p.then(() => {
          playerUnlocked = true;
          el.muted = false;
        }).catch(() => {
          el.muted = false;
        });
      } else {
        playerUnlocked = true;
        el.muted = false;
      }
    } catch {
      el.muted = false;
    }
  }
}

/**
 * An answer, as the pieces it is read in. The Mac mini reads a sentence in
 * about half the time it takes to say it, so the first sentence goes out on
 * its own and the next is made while it plays - the reply starts within a
 * couple of seconds instead of after the whole thing. Fragments too short to
 * be worth a round trip ride with their neighbour; a run-on sentence is cut
 * at a comma so no single piece outruns what the server will read.
 */
export function sentences(text: string): string[] {
  const flat = text.replace(/\s+/g, " ").trim();
  if (!flat) return [];
  const pieces = flat.split(/(?<=[.!?。！？])\s+/);
  const merged: string[] = [];
  for (const raw of pieces) {
    const p = raw.trim();
    if (!p) continue;
    const prev = merged[merged.length - 1];
    if (prev !== undefined && (prev.length < 16 || p.length < 8) && prev.length + p.length < 160) {
      merged[merged.length - 1] = `${prev} ${p}`;
    } else {
      merged.push(p);
    }
  }
  // Read in about the time it takes to say: the Mac mini needs ~0.8 s per
  // second of speech, and the next piece is made while this one plays. A
  // piece of 200 characters (14 s) after a short one left seven seconds of
  // silence; 80 keeps the gap under a breath.
  const LIMIT = 80;
  const split = (s: string): string[] => {
    if (s.length <= LIMIT) return [s];
    const mid = Math.floor(s.length / 2);
    let cut = -1;
    for (const sep of [", ", ",", "; ", " "]) {
      const left = s.lastIndexOf(sep, mid);
      const right = s.indexOf(sep, mid);
      const best = [left, right].filter((i) => i > 0).sort((a, b) => Math.abs(a - mid) - Math.abs(b - mid))[0];
      if (best !== undefined) {
        cut = best + sep.length;
        break;
      }
    }
    if (cut <= 0 || cut >= s.length) return [s];
    return [...split(s.slice(0, cut).trim()), ...split(s.slice(cut).trim())];
  };
  return merged.flatMap(split);
}

/** What the recording on file is, as the console knows it. */
export type VoiceState = "unknown" | "none" | "ready" | "unavailable";
export type VoiceInfo = {
  state: VoiceState;
  /** The script that was read, and how long the kept recording is. */
  text?: string;
  seconds?: number;
  savedAt?: string;
  /** Why the service could not be asked, when it could not. */
  error?: string;
};

/**
 * Whether the operator's voice is on file. Asked once when the thread opens
 * and again after the recording screen changes it; "unavailable" (the Mac
 * mini's voice service cannot be reached) sends speaking to the browser's own
 * voice without a word, the way it worked before there was a server.
 */
export function useVoiceStatus(): VoiceInfo & { refresh: () => Promise<void> } {
  const [info, setInfo] = useState<VoiceInfo>({ state: "unknown" });
  const refresh = useCallback(async () => {
    try {
      const res = await fetch("/api/tts/voice", { cache: "no-store" });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { error?: string };
        setInfo({ state: "unavailable", error: body.error });
        return;
      }
      const data = (await res.json()) as { present?: boolean; text?: string; seconds?: number; savedAt?: string };
      setInfo(
        data.present
          ? { state: "ready", text: data.text, seconds: data.seconds, savedAt: data.savedAt }
          : { state: "none" },
      );
    } catch {
      setInfo({ state: "unavailable" });
    }
  }, []);
  useEffect(() => {
    // Asking a server from an effect is the thing effects are for; the state
    // it sets arrives with the answer, not in this tick.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void refresh();
  }, [refresh]);
  return { ...info, refresh };
}

/** Which engine the current utterance is on, for the status line. */
export type Speaker = "server" | "browser" | null;

/**
 * Speaking. `server` says the operator's voice is on file: the answer is then
 * read by the Mac mini and played here, sentence by sentence; when that
 * cannot be done (no voice, service down, decode failure) the browser's own
 * voice takes over for what is left, so an answer is never silently dropped.
 */
export function useSpeech(lang = "ko-KR", server = false) {
  const [speaking, setSpeaking] = useState(false);
  /** True from asking for the first sentence until it starts to play. */
  const [preparing, setPreparing] = useState(false);
  const [speaker, setSpeaker] = useState<Speaker>(null);
  /**
   * Rises with the sound and decays between words, 0..1.
   *
   * On the cloned voice it is read off the audio itself. The browser's
   * synthesizer plays outside any AudioContext - there is no waveform to tap
   * - so there the word boundary is the only signal tied to what is being
   * said, and the ring moves on that. Engines that do not fire it for Korean
   * fall back to a steady pulse, which is honest about being a heartbeat
   * rather than a reading.
   */
  const [pulse, setPulse] = useState(0);
  /**
   * How far into the current utterance the voice has reached, in characters,
   * or -1 when unknown. The caption lights up to here, so you can read along
   * with what you are hearing.
   */
  const [progress, setProgress] = useState(-1);
  /**
   * Kept for the ring's sake: null now that the cloned voice plays through a
   * media element with no analyser on it. The ring then moves on `pulse`.
   */
  const spectrum = useRef<Uint8Array | null>(null);
  const voices = useRef<SpeechSynthesisVoice[]>([]);
  const decay = useRef<number>(0);
  // Each speak() is a run; a cancel or a newer speak() retires the old one,
  // and anything it was still fetching or playing is dropped on arrival.
  const run = useRef(0);
  const playing = useRef<HTMLAudioElement | null>(null);

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

  // The ring falls back between words a frame at a time - only while it has
  // somewhere to fall. One loop for the life of the hook woke the chat screen
  // every frame, speaking or not.
  const level = useRef(0);
  const frame = useRef(0);
  const kick = useCallback((v: number) => {
    level.current = v;
    setPulse(v);
    if (frame.current) return;
    const tick = () => {
      level.current = level.current > 0.01 ? level.current * 0.9 : 0;
      setPulse(level.current);
      frame.current = level.current > 0 ? requestAnimationFrame(tick) : 0;
    };
    frame.current = requestAnimationFrame(tick);
  }, []);
  useEffect(() => () => cancelAnimationFrame(frame.current), []);

  const finish = useCallback(() => {
    window.clearInterval(decay.current);
    setSpeaking(false);
    setPreparing(false);
    setSpeaker(null);
    level.current = 0;
    setPulse(0);
    setProgress(-1);
    spectrum.current = null;
  }, []);

  const cancel = useCallback(() => {
    run.current += 1;
    window.speechSynthesis?.cancel();
    const el = playing.current;
    if (el) {
      try {
        el.onended = null;
        el.onerror = null;
        el.onplaying = null;
        el.pause();
        el.removeAttribute("src");
        el.load();
      } catch {
        // Not started, or already ended.
      }
    }
    playing.current = null;
    finish();
  }, [finish]);

  /** The browser's own voice. `base` shifts progress when it reads only the tail of an answer. */
  const speakBrowser = useCallback(
    (text: string, base = 0) => {
      const synth =
        typeof window === "undefined" ? undefined : window.speechSynthesis;
      if (!synth || !text.trim()) {
        finish();
        return;
      }
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
        setProgress(base + event.charIndex + length);
        window.clearInterval(decay.current);
        // A word landed: kick the ring. The loop above brings it back down.
        kick(0.55 + Math.random() * 0.45);
      };
      // Engines do not always say when they are done: with no voice installed
      // nothing fires at all, and Chrome's remote voices fall silent mid-way on
      // a long answer. Left waiting, the stage would stay "speaking" with the
      // microphone shut. So the end is also assumed once the words should
      // long have been said.
      const token = ++run.current;
      const expected = 1500 + text.length * 220;
      const guard = window.setTimeout(() => {
        if (run.current !== token) return;
        console.warn("[voice] browser voice did not report its end; assuming it");
        synth.cancel();
        finish();
      }, expected);
      const done = () => {
        window.clearTimeout(guard);
        if (run.current === token) finish();
      };
      utterance.onend = done;
      utterance.onerror = done;

      setSpeaking(true);
      setPreparing(false);
      setSpeaker("browser");
      spectrum.current = null;
      setProgress(base > 0 ? base : -1);
      synth.speak(utterance);
      // Chrome pauses a synthesis started while another was being cancelled;
      // asking it to go on is harmless when it is already going.
      synth.resume();

      // Korean voices on several engines never fire onboundary. Give it a
      // moment, and if nothing arrived, beat steadily instead of standing
      // still while the room is clearly full of speech.
      window.clearInterval(decay.current);
      decay.current = window.setInterval(() => {
        if (sawBoundary) {
          window.clearInterval(decay.current);
          return;
        }
        kick(0.4 + Math.random() * 0.3);
      }, 260);
    },
    [lang, kick, finish],
  );

  /** The operator's voice from the Mac mini, a sentence at a time. */
  const speakServer = useCallback(
    async (text: string) => {
      const el = audioPlayer();
      const parts = sentences(text);
      if (!el || parts.length === 0) {
        speakBrowser(text);
        return;
      }
      const id = ++run.current;
      const alive = () => run.current === id;
      setSpeaking(true);
      setPreparing(true);
      setSpeaker("server");
      setProgress(-1);
      spectrum.current = null;

      // Where each piece starts in the answer, so the caption lights the right words.
      const offsets: number[] = [];
      let cursor = 0;
      for (const p of parts) {
        const at = text.indexOf(p, cursor);
        offsets.push(at);
        if (at >= 0) cursor = at + p.length;
      }

      const fetchPiece = async (i: number): Promise<string> => {
        const res = await fetch("/api/tts/speak", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ text: parts[i] }),
          cache: "no-store",
        });
        if (!res.ok) {
          const body = (await res.json().catch(() => ({}))) as { error?: string };
          throw new Error(body.error ?? `목소리 서비스 ${res.status}`);
        }
        return URL.createObjectURL(await res.blob());
      };

      // One piece, start to end. Resolves false when the element would not
      // play at all (no tap has unlocked it): the browser's voice then.
      const play = (url: string, i: number) =>
        new Promise<boolean>((resolve) => {
          let settled = false;
          let raf = 0;
          let guard = 0;
          let last = -1;
          const done = (ok: boolean) => {
            if (settled) return;
            settled = true;
            window.clearTimeout(guard);
            cancelAnimationFrame(raf);
            window.clearInterval(decay.current);
            el.onended = null;
            el.onerror = null;
            el.onplaying = null;
            if (playing.current === el) playing.current = null;
            URL.revokeObjectURL(url);
            resolve(ok);
          };
          const tick = () => {
            if (!alive()) return;
            if (offsets[i] >= 0 && el.duration > 0) {
              const frac = Math.min(1, el.currentTime / el.duration);
              const at = offsets[i] + Math.round(parts[i].length * frac);
              if (at !== last) {
                last = at;
                setProgress(at);
              }
            }
            raf = requestAnimationFrame(tick);
          };
          el.onplaying = () => {
            // The ring moves to a steady beat: a media element gives no
            // waveform to read without an AudioContext, which this avoids.
            window.clearInterval(decay.current);
            decay.current = window.setInterval(() => kick(0.45 + Math.random() * 0.4), 240);
            // Should the end never be reported (an interruption mid-play),
            // the piece is counted as done a little after it should have
            // finished, and the next one is tried.
            const expect = el.duration > 0 && Number.isFinite(el.duration) ? el.duration * 1000 : 30_000;
            window.clearTimeout(guard);
            guard = window.setTimeout(() => {
              console.warn("[voice] playback did not report its end; moving on");
              done(true);
            }, expect + 2500);
            raf = requestAnimationFrame(tick);
          };
          el.onended = () => done(true);
          el.onerror = () => done(true);
          playing.current = el;
          el.src = url;
          // If play never starts (metadata never comes), do not wait forever.
          guard = window.setTimeout(() => done(true), 20_000);
          const p = el.play();
          if (p && typeof p.catch === "function") {
            p.catch((err: unknown) => {
              console.warn("[voice] the voice element would not play:", err);
              done(false);
            });
          }
        });

      let next: Promise<string> = fetchPiece(0);
      for (let i = 0; i < parts.length; i += 1) {
        let url: string;
        try {
          url = await next;
        } catch (err) {
          if (!alive()) return;
          console.warn("[voice] cloned voice unavailable, using the browser's:", err);
          // The rest of the answer in the browser's voice, picking up where this left off.
          speakBrowser(parts.slice(i).join(" "), offsets[i] >= 0 ? offsets[i] : 0);
          return;
        }
        if (!alive()) {
          URL.revokeObjectURL(url);
          return;
        }
        if (i + 1 < parts.length) {
          next = fetchPiece(i + 1);
          // Looked at later; an early failure must not surface as unhandled meanwhile.
          next.catch(() => undefined);
        }
        setPreparing(false);
        const played = await play(url, i);
        if (!alive()) return;
        if (!played) {
          // Nothing could be heard this way (no tap since the page opened):
          // the browser's voice at least reports, and asks for no tap on most.
          speakBrowser(parts.slice(i).join(" "), offsets[i] >= 0 ? offsets[i] : 0);
          return;
        }
      }
      finish();
    },
    [speakBrowser, finish, kick],
  );

  const speak = useCallback(
    (text: string) => {
      if (!text.trim()) return;
      cancel();
      if (server) void speakServer(text);
      else speakBrowser(text);
    },
    [server, cancel, speakServer, speakBrowser],
  );

  return { speak, cancel, speaking, preparing, speaker, pulse, progress, spectrum };
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
    .replace(/https?:\/\/\S+/g, "링크")
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    .replace(/^\s{0,3}>\s?/gm, "")
    .replace(/^\s*[-*+]\s+/gm, "")
    .replace(/(\*\*|__|\*|_|~~)/g, "")
    .replace(/^\s*\|.*\|\s*$/gm, "")
    .replace(/\n{2,}/g, ". ")
    .replace(/\s+/g, " ")
    // A table or a code block that came out leaves ". ." behind; one stop is enough.
    .replace(/(?:[.。]\s*){2,}/g, ". ")
    .trim();
}
