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
        setError(event.error);
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

/**
 * One AudioContext for the page: the cloned voice plays through it, the
 * recorder that teaches it listens through it. Made on first use, so the
 * server render never touches it.
 */
let shared: AudioContext | null = null;
export function audioContext(): AudioContext | null {
  if (typeof window === "undefined") return null;
  if (!shared) {
    try {
      shared = new AudioContext();
    } catch {
      return null;
    }
  }
  return shared;
}

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
  const ctx = audioContext();
  if (ctx) {
    void ctx.resume().catch(() => undefined);
    try {
      const source = ctx.createBufferSource();
      source.buffer = ctx.createBuffer(1, 1, ctx.sampleRate);
      source.connect(ctx.destination);
      source.start();
    } catch {
      // Already unlocked, or no output device: either way nothing to do.
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
  const LIMIT = 200;
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
  /** The cloned voice's live frequency bins while it plays; null otherwise. The ring draws them. */
  const spectrum = useRef<Uint8Array | null>(null);
  const voices = useRef<SpeechSynthesisVoice[]>([]);
  const decay = useRef<number>(0);
  // Each speak() is a run; a cancel or a newer speak() retires the old one,
  // and anything it was still fetching or playing is dropped on arrival.
  const run = useRef(0);
  const playing = useRef<AudioBufferSourceNode | null>(null);
  const analyser = useRef<AnalyserNode | null>(null);
  const meter = useRef(0);

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
    meter.current = 0;
    setPulse(0);
    setProgress(-1);
    spectrum.current = null;
  }, []);

  const cancel = useCallback(() => {
    run.current += 1;
    window.speechSynthesis?.cancel();
    try {
      playing.current?.stop();
    } catch {
      // Not started, or already ended.
    }
    playing.current = null;
    analyser.current?.disconnect();
    analyser.current = null;
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
      const ctx = audioContext();
      const parts = sentences(text);
      if (!ctx || parts.length === 0) {
        speakBrowser(text);
        return;
      }
      const id = ++run.current;
      const alive = () => run.current === id;
      setSpeaking(true);
      setPreparing(true);
      setSpeaker("server");
      setProgress(-1);
      // A context the page has not been allowed to run (no tap since load -
      // say, reopened straight into voice mode) plays nothing and never ends.
      // The browser's own voice is the better bet there; it at least reports.
      try {
        await ctx.resume();
      } catch {
        // Looked at below.
      }
      if (!alive()) return;
      if (ctx.state !== "running") {
        console.warn("[voice] audio context is", ctx.state, "- using the browser's voice");
        speakBrowser(text);
        return;
      }

      const node = ctx.createAnalyser();
      node.fftSize = 1024;
      node.smoothingTimeConstant = 0.6;
      node.connect(ctx.destination);
      analyser.current = node;
      const bins = new Uint8Array(node.frequencyBinCount);
      const wave = new Uint8Array(node.fftSize);

      // Where each piece starts in the answer, so the caption lights the right words.
      const offsets: number[] = [];
      let cursor = 0;
      for (const p of parts) {
        const at = text.indexOf(p, cursor);
        offsets.push(at);
        if (at >= 0) cursor = at + p.length;
      }

      const fetchPiece = async (i: number): Promise<AudioBuffer> => {
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
        return ctx.decodeAudioData(await res.arrayBuffer());
      };

      const play = (buffer: AudioBuffer, i: number) =>
        new Promise<void>((resolve) => {
          const source = ctx.createBufferSource();
          source.buffer = buffer;
          source.connect(node);
          playing.current = source;
          spectrum.current = bins;
          const started = ctx.currentTime;
          let last = -1;
          let raf = 0;
          const tick = () => {
            if (!alive() || playing.current !== source) return;
            node.getByteFrequencyData(bins);
            node.getByteTimeDomainData(wave);
            let sum = 0;
            for (const s of wave) {
              const c = (s - 128) / 128;
              sum += c * c;
            }
            const rms = Math.min(1, Math.sqrt(sum / wave.length) * 4);
            meter.current = rms > meter.current ? rms : meter.current * 0.88 + rms * 0.12;
            setPulse(meter.current);
            if (offsets[i] >= 0) {
              const frac = Math.min(1, (ctx.currentTime - started) / buffer.duration);
              const at = offsets[i] + Math.round(parts[i].length * frac);
              if (at !== last) {
                last = at;
                setProgress(at);
              }
            }
            raf = requestAnimationFrame(tick);
          };
          // Should the end never be reported (a context suspended mid-play by
          // an interruption), the piece is counted as done a little after it
          // should have finished, and the next one is tried.
          const guard = window.setTimeout(() => {
            console.warn("[voice] playback did not report its end; moving on");
            try {
              source.stop();
            } catch {
              // Already stopped.
            }
            done();
          }, buffer.duration * 1000 + 2500);
          let settled = false;
          const done = () => {
            if (settled) return;
            settled = true;
            window.clearTimeout(guard);
            cancelAnimationFrame(raf);
            if (playing.current === source) playing.current = null;
            resolve();
          };
          source.onended = done;
          source.start();
          raf = requestAnimationFrame(tick);
        });

      let next: Promise<AudioBuffer> = fetchPiece(0);
      for (let i = 0; i < parts.length; i += 1) {
        let buffer: AudioBuffer;
        try {
          buffer = await next;
        } catch (err) {
          if (!alive()) return;
          console.warn("[voice] cloned voice unavailable, using the browser's:", err);
          // The rest of the answer in the browser's voice, picking up where this left off.
          node.disconnect();
          analyser.current = null;
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
        await play(buffer, i);
        if (!alive()) return;
      }
      node.disconnect();
      analyser.current = null;
      finish();
    },
    [speakBrowser, finish],
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
