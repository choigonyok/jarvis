"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { diag } from "@/lib/diag";
import { primePlayer } from "@/lib/speech-speak";

/**
 * Listening, the first half of voice mode (lib/use-speech.ts): the browser's
 * SpeechRecognition, a session at a time.
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
