"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { diag } from "@/lib/diag";

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
 * A hands-free reopen whose capture failed (audio-capture) is tried again,
 * after a beat, this many times before a tap is asked for.
 */
const RECAPTURE_TRIES = 2;
/** A beat before that try: the platform has to let go of the audio it was playing. */
const RECAPTURE_MS = 1200;
/**
 * A session opened hands-free that reports no sound at all for this long is
 * taken to be deaf and replaced: an iPhone's recognizer, opened right after the
 * answer played, says it is capturing and hears nothing.
 */
const AUTO_SOUND_MS = 5000;
/** A session that says it started sooner than this after start() opened no microphone (phones, iOS 27). */
const DEAF_START_MS = 150;

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
 * abort can come back saying it listens while it hears nothing (on an
 * iPhone, after a couple of exchanges). Every timer and flag of a session lives
 * in that session, so nothing stuck in one can leak into the next. "Listening"
 * is the engine's own word (onstart), and a session that says so but never
 * opens the mic, or hears sound but never words, is replaced.
 */
export function useRecognition({
  lang = "ko-KR",
  onFinal,
  holding = false,
}: {
  lang?: string;
  onFinal: (text: string) => void;
  /**
   * While Jarvis thinks or talks: the session stays open and what it hears
   * is dropped. Closing the mic and opening it again afterwards is what an
   * iPhone on iOS 27 refuses without a tap - the reopened session says it
   * started and hears nothing (WebKit 321436) - while a session that never
   * closed keeps hearing.
   */
  holding?: boolean;
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
  const holdRef = useRef(holding);
  // A session that ended while holding is opened again once the hold lifts.
  const reopenWhenFree = useRef(false);
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
  // Hands-free reopens that got no audio in a row; reset by words, or a tap.
  const recaptures = useRef(0);
  // Whether this engine has been seen to report its capture (onaudiostart).
  // Only then is a session that never does so taken to be deaf - an engine
  // that never reports it is not punished for that.
  const reportsAudio = useRef(false);
  const attemptRef = useRef<((tries: number) => void) | null>(null);

  useEffect(() => {
    final.current = onFinal;
    langRef.current = lang;
    holdRef.current = holding;
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
      diag("voice", { ev: "rec.new", tries });
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
      let silence: ReturnType<typeof setTimeout> | undefined;
      // When start() was called: a session that says "started" within a few
      // milliseconds opened no microphone (WebKit 321436).
      let asked = 0;
      const quiet = () => {
        clearTimeout(settle);
        clearTimeout(ending);
        clearTimeout(starting);
        clearTimeout(capture);
        clearTimeout(words);
        clearTimeout(silence);
      };

      // The session is over: open the next while one is wanted.
      const finish = () => {
        if (!mine()) return;
        quiet();
        diag("voice", { ev: "rec.end", wanted: wanted.current ? 1 : 0, words: gotWords ? 1 : 0, captured: captured ? 1 : 0 });
        engineRef.current = null;
        opening.current = false;
        setInterim("");
        if (wanted.current && holdRef.current) {
          // Jarvis is still talking: the screen opens the next session when he stops.
          tapped.current = false;
          reopenWhenFree.current = true;
          diag("voice", { ev: "rec.endheld" });
          setState((prev) => (prev === "listening" ? "idle" : prev));
        } else if (wanted.current) {
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
        diag("voice", { ev: "rec.replace", why, deaf: deaf.current + (counts ? 1 : 0) });
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
        if (!wanted.current) {
          // Stopped while it was still opening: it does not get to say "listening".
          quiet();
          retire();
          return;
        }
        opening.current = false;
        setError(null);
        setState("listening");
        const took = Math.round(performance.now() - asked);
        diag("voice", { ev: "rec.start", reports: reportsAudio.current ? 1 : 0, took });
        if (!tapped.current && took < DEAF_START_MS && capturesAlone()) {
          // Said "started" at once: no microphone was opened (WebKit 321436).
          // Not "듣고 있습니다" with nothing behind it - the tap is asked for now.
          diag("voice", { ev: "rec.deaf", took });
          quiet();
          retire();
          wanted.current = false;
          setInterim("");
          setError("mic-silent");
          setState("paused");
          return;
        }
        if (reportsAudio.current && !captured) {
          capture = setTimeout(() => replace("never opened the microphone", true), AUDIO_MS);
        }
        if (!tapped.current) {
          silence = setTimeout(() => {
            primePlayer();
            replace("opened hands-free and heard no sound", true);
          }, AUTO_SOUND_MS);
        }
      };
      engine.onaudiostart = () => {
        if (!mine()) return;
        captured = true;
        reportsAudio.current = true;
        clearTimeout(capture);
        diag("voice", { ev: "rec.audio" });
      };
      const sound = () => {
        if (!mine()) return;
        captured = true;
        clearTimeout(capture);
        setHeard((h) => h + 1);
        clearTimeout(silence);
        diag("voice", { ev: "rec.sound", words: gotWords ? 1 : 0 });
        if (!gotWords) {
          clearTimeout(words);
          words = setTimeout(() => replace("heard sound but no words", false), WORDS_MS);
        }
      };
      engine.onsoundstart = sound;
      engine.onspeechstart = sound;

      engine.onresult = (event) => {
        if (!mine() || dropping) return;
        if (holdRef.current) {
          // Jarvis's own voice, or words said over his answer: not a question.
          setInterim("");
          return;
        }
        clearTimeout(capture);
        clearTimeout(words);
        clearTimeout(silence);
        gotWords = true;
        deaf.current = 0;
        recaptures.current = 0;
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
        diag("voice", { ev: "rec.result", n: event.results.length - event.resultIndex, interim: pending.length });

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
          diag("voice", { ev: "rec.settle", chars: heardText.length });
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
        diag("voice", { ev: "rec.error", error: String(event.error), tapped: tapped.current ? 1 : 0 });
        const refused = event.error === "not-allowed" || event.error === "service-not-allowed";
        // Opened hands-free right after the answer played and got no audio:
        // on an iPhone the element that just spoke can still hold the audio
        // route, and the engine reports it only seconds later. Settle the
        // element as a tap would (primePlayer) and open once more.
        if (!tapped.current && event.error === "audio-capture" && recaptures.current < RECAPTURE_TRIES) {
          recaptures.current += 1;
          diag("voice", { ev: "rec.recapture", n: recaptures.current });
          quiet();
          retire();
          primePlayer();
          if (wanted.current) schedule(RECAPTURE_MS);
          return;
        }
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
        asked = performance.now();
        engine.start();
        opening.current = true;
        diag("voice", { ev: "rec.starting", tries });
        // Listening is announced by onstart, not here: a state that says
        // "listening" with a closed mic is what this replaces.
        setState((prev) => (prev === "listening" ? prev : "idle"));
        starting = setTimeout(() => replace("never started", true), OPEN_MS);
      } catch {
        // Still letting go of the last session: try again shortly.
        quiet();
        retire();
        diag("voice", { ev: "rec.startfail", tries });
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
    reopenWhenFree.current = false;
    // Already asked and waiting for the engine's word: nothing to add.
    if (opening.current && wanted.current) return;
    tapped.current = !opts?.auto;
    if (!opts?.auto) recaptures.current = 0;
    diag("voice", { ev: "rec.want", auto: opts?.auto ? 1 : 0 });
    wanted.current = true;
    deaf.current = 0;
    setError(null);
    if (retry.current) clearTimeout(retry.current);
    attemptRef.current?.(0);
  }, []);

  /**
   * A fresh session in place of the one listening - for a session that has
   * heard nothing in a long while. `tap` when a person asked for it.
   */
  const restart = useCallback((tap = false) => {
    if (!wanted.current && !tap) return;
    tapped.current = tap;
    if (tap) recaptures.current = 0;
    diag("voice", { ev: "rec.restart", tap: tap ? 1 : 0 });
    wanted.current = true;
    deaf.current = 0;
    setError(null);
    retire();
    if (retry.current) clearTimeout(retry.current);
    attemptRef.current?.(0);
  }, [retire]);

  const stop = useCallback(() => {
    wanted.current = false;
    diag("voice", { ev: "rec.stop", had: engineRef.current ? 1 : 0 });
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
    restart,
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
 * The cloned voice plays through Web Audio, not a media element, on purpose.
 * Since iOS 27 a media element that has played leaves the page's speech
 * recognition deaf: the next session says it started and hears nothing
 * (WebKit 321436: "듣고 있습니다" with no words). Web Audio
 * playback does not do that. Each answer gets a context of its own, made when
 * the hardware runs at the rate it will play at and closed after - a context
 * kept from before the microphone was used played everything a fraction high
 * once the hardware had moved to 48 kHz for it.
 */
function newContext(): AudioContext | null {
  if (typeof window === "undefined") return null;
  const AC = window.AudioContext ?? (window as Window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!AC) return null;
  try {
    return new AC();
  } catch {
    return null;
  }
}

/** The context running, or not: iOS keeps one suspended until the page has sounded inside a tap. */
async function resumed(ctx: AudioContext): Promise<boolean> {
  try {
    await Promise.race([ctx.resume(), new Promise<void>((r) => setTimeout(r, 1500))]);
  } catch {
    // Not allowed yet: the state says so.
  }
  return ctx.state === "running";
}

let playerUnlocked = false;
let synthUnlocked = false;
let shared: AudioContext | null = null;

/**
 * The one context the answers play through. A context an iPhone has started
 * inside a tap goes on playing on its own; one made later, outside any tap,
 * stays suspended for good.
 */
export function sharedContext(): AudioContext | null {
  if (shared && shared.state !== "closed") return shared;
  shared = newContext();
  return shared;
}

/**
 * A sound made inside a tap: the shared context started on one silent
 * sample, so the answers may play through it later on their own.
 */
export function primePlayer() {
  const ctx = sharedContext();
  if (!ctx) return;
  try {
    const source = ctx.createBufferSource();
    source.buffer = ctx.createBuffer(1, 1, ctx.sampleRate);
    source.connect(ctx.destination);
    source.start(0);
  } catch {
    // Nothing to prime on this engine.
  }
  void ctx
    .resume()
    .then(() => {
      playerUnlocked = true;
    })
    .catch(() => undefined);
}

/** Whether a context has been started inside a tap yet. */
export function playerReady() {
  return playerUnlocked;
}

/**
 * iOS lets a page make sound on its own only after it has done so once inside
 * a tap. Answers arrive long after any tap, so the voice stage calls this from
 * its buttons. The browser's synthesizer needs it once per page - a silent
 * utterance, never again after, so later taps do not touch it while the mic
 * opens - and a context is started every time (primePlayer).
 */
export function unlockSpeech() {
  const synth = typeof window === "undefined" ? undefined : window.speechSynthesis;
  if (synth && !synthUnlocked) {
    try {
      const u = new SpeechSynthesisUtterance(" ");
      u.volume = 0;
      synth.speak(u);
      synth.resume();
      synthUnlocked = true;
    } catch {
      // Nothing to unlock on this engine.
    }
  }
  primePlayer();
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
        // What went wrong is for the diagnostics; the screen says what to do.
        diag("voice", { ev: "tts.voice-unavailable", status: res.status });
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
  /** The cloned voice's spectrum while it plays (an analyser's bins); null otherwise. */
  const spectrum = useRef<Uint8Array | null>(null);
  const voices = useRef<SpeechSynthesisVoice[]>([]);
  const decay = useRef<number>(0);
  // Each speak() is a run; a cancel or a newer speak() retires the old one,
  // and anything it was still fetching or playing is dropped on arrival.
  const run = useRef(0);
  // The context the answer being read plays through.
  const context = useRef<AudioContext | null>(null);
  // Settles the piece playing now (its promise resolves, its handlers come off).
  const stopPiece = useRef<(() => void) | null>(null);
  // Aborts the fetches of the answer being read.
  const fetches = useRef<AbortController | null>(null);

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
    diag("voice", { ev: "tts.cancel", playing: context.current ? 1 : 0 });
    // The cut answer's pieces still being fetched are not wanted any more.
    fetches.current?.abort();
    fetches.current = null;
    window.speechSynthesis?.cancel();
    // The piece playing is settled, not left pending: a pending piece's
    // timer would fire later and take the handlers of whatever answer was
    // playing by then.
    const piece = stopPiece.current;
    stopPiece.current = null;
    piece?.();
    context.current = null;
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
      diag("voice", { ev: "tts.browser", chars: text.length });
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
      const parts = sentences(text);
      if (parts.length === 0) {
        speakBrowser(text);
        return;
      }
      const id = ++run.current;
      const alive = () => run.current === id;
      setSpeaking(true);
      setPreparing(true);
      setSpeaker("server");
      setProgress(-1);

      const ctx = sharedContext();
      if (!ctx) {
        speakBrowser(text);
        return;
      }
      context.current = ctx;
      const letGo = () => {
        if (context.current === ctx) context.current = null;
      };
      // The hardware's rate now, read off a context made for the purpose: a
      // context kept across the microphone's use plays a fraction high and
      // fast once the hardware moved to 48 kHz for it, and the pieces are
      // slowed by that much to come out right.
      let hw = ctx.sampleRate;
      const probe = newContext();
      if (probe) {
        hw = probe.sampleRate;
        void probe.close().catch(() => undefined);
      }
      const rateFix = hw > 0 && hw !== ctx.sampleRate ? ctx.sampleRate / hw : 1;
      const running = await resumed(ctx);
      diag("voice", { ev: "tts.ctx", rate: ctx.sampleRate, hw, state: ctx.state, running: running ? 1 : 0 });
      if (!alive()) return;
      if (!running) {
        // Not allowed to sound yet (no tap since the page opened): the
        // browser's voice at least reports, and asks for no tap on most.
        letGo();
        speakBrowser(text);
        return;
      }
      const analyser = ctx.createAnalyser();
      analyser.fftSize = 256;
      analyser.smoothingTimeConstant = 0.6;
      analyser.connect(ctx.destination);
      const bins = new Uint8Array(analyser.frequencyBinCount);
      spectrum.current = bins;

      // Where each piece starts in the answer, so the caption lights the right words.
      const offsets: number[] = [];
      let cursor = 0;
      for (const p of parts) {
        const at = text.indexOf(p, cursor);
        offsets.push(at);
        if (at >= 0) cursor = at + p.length;
      }

      const abort = new AbortController();
      fetches.current = abort;
      const fetchPiece = async (i: number): Promise<AudioBuffer> => {
        const res = await fetch("/api/tts/speak", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ text: parts[i] }),
          cache: "no-store",
          signal: abort.signal,
        });
        diag("voice", { ev: "tts.piece", i, status: res.status });
        if (!res.ok) {
          const body = (await res.json().catch(() => ({}))) as { error?: string };
          throw new Error(body.error ?? `목소리 서비스 ${res.status}`);
        }
        return ctx.decodeAudioData(await res.arrayBuffer());
      };

      // One piece, start to end. Resolves false when it would not play at all.
      const play = (buffer: AudioBuffer, i: number) =>
        new Promise<boolean>((resolve) => {
          let settled = false;
          let raf = 0;
          let guard = 0;
          let last = -1;
          const source = ctx.createBufferSource();
          source.buffer = buffer;
          source.playbackRate.value = rateFix;
          source.connect(analyser);
          const done = (ok: boolean) => {
            if (settled) return;
            settled = true;
            window.clearTimeout(guard);
            cancelAnimationFrame(raf);
            if (stopPiece.current === stop) stopPiece.current = null;
            try {
              source.disconnect();
            } catch {
              // Never connected, or gone with its context.
            }
            resolve(ok);
          };
          const stop = () => {
            try {
              source.stop();
            } catch {
              // Not started, or already ended.
            }
            done(true);
          };
          const startedAt = ctx.currentTime;
          const tick = () => {
            if (!alive()) return;
            // The ring reads the sound itself; the caption follows the clock.
            analyser.getByteFrequencyData(bins);
            let sum = 0;
            for (const b of bins) sum += b;
            const heard = Math.min(1, sum / bins.length / 96);
            if (Math.abs(heard - level.current) > 0.03) kick(heard);
            if (offsets[i] >= 0 && buffer.duration > 0) {
              const frac = Math.min(1, (ctx.currentTime - startedAt) / buffer.duration);
              const at = offsets[i] + Math.round(parts[i].length * frac);
              if (at !== last) {
                last = at;
                setProgress(at);
              }
            }
            raf = requestAnimationFrame(tick);
          };
          source.onended = () => {
            diag("voice", { ev: "tts.ended", i });
            done(true);
          };
          stopPiece.current = stop;
          // Should the end never be reported, the piece is counted as done a
          // little after it should have finished, and the next one is tried.
          guard = window.setTimeout(() => {
            console.warn("[voice] playback did not report its end; moving on");
            diag("voice", { ev: "tts.guard", i });
            done(true);
          }, buffer.duration * 1000 + 2500);
          try {
            source.start(0);
            diag("voice", { ev: "tts.playing", i, dur: Math.round(buffer.duration * 1000) });
            raf = requestAnimationFrame(tick);
          } catch (err) {
            console.warn("[voice] the piece would not play:", err);
            diag("voice", { ev: "tts.noplay", i });
            done(false);
          }
        });

      let next: Promise<AudioBuffer> = fetchPiece(0);
      for (let i = 0; i < parts.length; i += 1) {
        let buffer: AudioBuffer;
        try {
          buffer = await next;
        } catch (err) {
          if (!alive()) return;
          console.warn("[voice] cloned voice unavailable, using the browser's:", err);
          diag("voice", { ev: "tts.fallback", i });
          // The rest of the answer in the browser's voice, picking up where this left off.
          letGo();
          speakBrowser(parts.slice(i).join(" "), offsets[i] >= 0 ? offsets[i] : 0);
          return;
        }
        if (!alive()) return;
        if (i + 1 < parts.length) {
          next = fetchPiece(i + 1);
          // Looked at later; an early failure must not surface as unhandled meanwhile.
          next.catch(() => undefined);
        }
        setPreparing(false);
        const played = await play(buffer, i);
        if (!alive()) return;
        if (!played) {
          letGo();
          speakBrowser(parts.slice(i).join(" "), offsets[i] >= 0 ? offsets[i] : 0);
          return;
        }
      }
      diag("voice", { ev: "tts.done" });
      letGo();
      analyser.disconnect();
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
