"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { diag } from "@/lib/diag";

/**
 * Speaking, the second half of voice mode (lib/use-speech.ts): the operator's
 * own voice from the Mac mini when one is on file, the browser's otherwise.
 */

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
