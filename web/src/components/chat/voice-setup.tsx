"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ArrowLeft, Check, Mic, Play, RotateCcw, Square } from "lucide-react";
import { Caption, DOT, IconButton, PrimaryButton, QuietButton, Spectrum, type Tone } from "@/components/chat/voice-parts";
import { audioContext, unlockSpeech, type VoiceInfo } from "@/lib/use-speech";
import { concatSamples, encodeWav, toBase64 } from "@/lib/wav";
import { cn } from "@/lib/utils";

/**
 * Teaching Jarvis your voice.
 *
 * The same room as the stage - the ring, the big line, one button - turned
 * around: this time the ring listens to you reading, and the line is a script
 * rather than an answer. One recording of about fifteen seconds is all the
 * clone needs, and the exact words matter (the server is told what was said),
 * so the script is fixed and shown the whole time. Afterwards you hear it back
 * before deciding; Jarvis answers in this voice from the next reply on.
 */

/**
 * What to read. Numbers, a time, a place name, a question - the shapes Jarvis
 * says most - in plain spoken Korean, long enough to take about fifteen seconds
 * at a natural pace and no longer.
 */
export const SCRIPT =
  "안녕하세요, 지금 제 목소리를 자비스에게 알려 주고 있어요. 오늘 오후 세 시 반에 회의가 있고, 저녁에는 친구와 합주 연습을 하기로 했습니다. 내일 날씨는 맑고 기온은 십팔 도쯤이라고 하네요. 이 정도면 충분할까요?";

/** Past this the recorder stops by itself; the clone keeps twenty seconds at most anyway. */
const MAX_SECONDS = 25;
const MIN_SECONDS = 3;

type Step = "idle" | "recording" | "recorded" | "saving" | "saved";

type Take = { samples: Float32Array; rate: number; seconds: number };

/**
 * The microphone as raw samples. Not MediaRecorder: Safari would hand back
 * AAC and Chrome Opus, and the Mac mini has no decoder for either - so the
 * samples are taken straight off the graph and sent as a plain WAV. The same
 * graph feeds the ring, so what you see is what is being kept.
 */
function useRecorder(onFull: () => void) {
  const [recording, setRecording] = useState(false);
  const [seconds, setSeconds] = useState(0);
  const [level, setLevel] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const spectrum = useRef<Uint8Array | null>(null);
  const chunks = useRef<Float32Array[]>([]);
  const rate = useRef(0);
  const teardown = useRef<(() => void) | null>(null);
  // Read when the recording hits its ceiling - from inside the audio callback,
  // so it has to reach the current handler through a ref.
  const full = useRef(onFull);
  useEffect(() => {
    full.current = onFull;
  }, [onFull]);

  const stop = useCallback((): Take | null => {
    teardown.current?.();
    teardown.current = null;
    setRecording(false);
    setLevel(0);
    spectrum.current = null;
    if (chunks.current.length === 0 || !rate.current) return null;
    const samples = concatSamples(chunks.current);
    chunks.current = [];
    return { samples, rate: rate.current, seconds: samples.length / rate.current };
  }, []);

  const start = useCallback(async (): Promise<boolean> => {
    setError(null);
    if (!navigator.mediaDevices?.getUserMedia || typeof AudioContext === "undefined") {
      setError("이 브라우저에서는 녹음할 수 없습니다. Chrome이나 Safari에서 열어 주세요.");
      return false;
    }
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
    } catch (err) {
      const name = (err as { name?: string }).name;
      setError(
        name === "NotAllowedError"
          ? "마이크 권한이 필요합니다. 브라우저 설정에서 허용한 뒤 다시 시도하세요."
          : "마이크를 열지 못했습니다. 다른 앱이 쓰고 있지 않은지 확인해 주세요.",
      );
      return false;
    }
    // Its own context, made after the mic opened: on an iPhone opening the
    // microphone can change the hardware rate, and a context made earlier
    // (the one the answers play through) would then capture at the wrong one.
    const ctx = new AudioContext();
    await ctx.resume().catch(() => undefined);
    const source = ctx.createMediaStreamSource(stream);
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 1024;
    analyser.smoothingTimeConstant = 0.6;
    // ScriptProcessor is old but it is the one capture path every browser
    // still ships without a worklet file to serve; 4096 frames is ~90ms.
    const tap = ctx.createScriptProcessor(4096, 1, 1);
    // Safari only runs the processor when it leads somewhere; a silent gain
    // gives it a destination without putting the mic on the speakers.
    const sink = ctx.createGain();
    sink.gain.value = 0;
    source.connect(analyser);
    analyser.connect(tap);
    tap.connect(sink);
    sink.connect(ctx.destination);

    chunks.current = [];
    rate.current = ctx.sampleRate;
    let kept = 0;
    tap.onaudioprocess = (e) => {
      const data = e.inputBuffer.getChannelData(0);
      chunks.current.push(new Float32Array(data));
      kept += data.length;
      const s = kept / ctx.sampleRate;
      setSeconds(s);
      if (s >= MAX_SECONDS) full.current();
    };

    const bins = new Uint8Array(analyser.frequencyBinCount);
    const wave = new Uint8Array(analyser.fftSize);
    spectrum.current = bins;
    let smoothed = 0;
    let frame = 0;
    const tick = () => {
      analyser.getByteFrequencyData(bins);
      analyser.getByteTimeDomainData(wave);
      let sum = 0;
      for (const v of wave) {
        const c = (v - 128) / 128;
        sum += c * c;
      }
      const rms = Math.min(1, Math.sqrt(sum / wave.length) * 4);
      smoothed = rms > smoothed ? rms : smoothed * 0.88 + rms * 0.12;
      setLevel(smoothed);
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);

    teardown.current = () => {
      cancelAnimationFrame(frame);
      tap.onaudioprocess = null;
      try {
        source.disconnect();
        analyser.disconnect();
        tap.disconnect();
        sink.disconnect();
      } catch {
        // Already torn down.
      }
      for (const track of stream.getTracks()) track.stop();
      void ctx.close().catch(() => undefined);
    };
    setSeconds(0);
    setRecording(true);
    return true;
  }, []);

  useEffect(() => () => teardown.current?.(), []);

  return { recording, seconds, level, spectrum, error, start, stop };
}

export function VoiceSetup({
  voice,
  onBack,
  onChanged,
}: {
  voice: VoiceInfo;
  onBack: () => void;
  /** The recording on file changed: saved anew, or forgotten. */
  onChanged: () => void | Promise<void>;
}) {
  // The ceiling handler needs the recorder, and the recorder needs the handler:
  // the handler goes in through a ref that is filled in once both exist.
  const onFull = useRef<() => void>(() => undefined);
  const recorder = useRecorder(() => onFull.current());
  const [step, setStep] = useState<Step>("idle");
  const [take, setTake] = useState<Take | null>(null);
  const [playing, setPlaying] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const preview = useRef<AudioBufferSourceNode | null>(null);

  const finishTake = useCallback(() => {
    const t = recorder.stop();
    if (!t || t.seconds < MIN_SECONDS) {
      setTake(null);
      setStep("idle");
      setProblem(t ? "녹음이 너무 짧습니다. 대본을 끝까지 읽어 주세요." : "녹음된 소리가 없습니다.");
      return;
    }
    setTake(t);
    setStep("recorded");
  }, [recorder]);
  useEffect(() => {
    onFull.current = finishTake;
  }, [finishTake]);

  const stopPreview = useCallback(() => {
    try {
      preview.current?.stop();
    } catch {
      // Not playing.
    }
    preview.current = null;
    setPlaying(false);
  }, []);
  useEffect(() => stopPreview, [stopPreview]);

  async function begin() {
    setProblem(null);
    stopPreview();
    // Inside the tap: iOS opens the audio graph only for a gesture.
    unlockSpeech();
    if (await recorder.start()) setStep("recording");
  }

  function listen() {
    const ctx = audioContext();
    if (!ctx || !take) return;
    if (playing) {
      stopPreview();
      return;
    }
    unlockSpeech();
    const buffer = ctx.createBuffer(1, take.samples.length, take.rate);
    buffer.getChannelData(0).set(take.samples);
    const source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(ctx.destination);
    source.onended = () => {
      if (preview.current === source) {
        preview.current = null;
        setPlaying(false);
      }
    };
    preview.current = source;
    setPlaying(true);
    source.start();
  }

  async function save() {
    if (!take) return;
    stopPreview();
    setStep("saving");
    setProblem(null);
    try {
      const res = await fetch("/api/tts/voice", {
        method: "PUT",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ text: SCRIPT, audio: toBase64(encodeWav(take.samples, take.rate)) }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { error?: string };
        throw new Error(body.error ?? `저장에 실패했습니다 (${res.status}).`);
      }
      await onChanged();
      setStep("saved");
    } catch (err) {
      setStep("recorded");
      setProblem(err instanceof Error ? err.message : "저장에 실패했습니다.");
    }
  }

  async function forget() {
    setProblem(null);
    try {
      const res = await fetch("/api/tts/voice", { method: "DELETE" });
      if (!res.ok) throw new Error(`지우지 못했습니다 (${res.status}).`);
      await onChanged();
    } catch (err) {
      setProblem(err instanceof Error ? err.message : "지우지 못했습니다.");
    }
  }

  function leave() {
    if (recorder.recording) recorder.stop();
    stopPreview();
    onBack();
  }

  const recording = step === "recording";
  const kept = voice.state === "ready";
  const unavailable = voice.state === "unavailable";

  const status: { text: string; tone: Tone } = recorder.error || problem
    ? { text: "다시 해 주세요", tone: "error" }
    : unavailable && step === "idle"
      ? { text: "목소리 서비스에 닿지 못했습니다", tone: "error" }
      : recording
        ? { text: `읽는 중 ${clock(recorder.seconds)}`, tone: "listening" }
        : step === "recorded"
          ? { text: playing ? "들어보는 중" : `녹음 ${clock(take?.seconds ?? 0)}`, tone: "idle" }
          : step === "saving"
            ? { text: "목소리를 익히는 중", tone: "thinking" }
            : step === "saved"
              ? { text: "이제 이 목소리로 답합니다", tone: "speaking" }
              : kept
                ? { text: `등록된 목소리 ${voice.seconds ? `· ${Math.round(voice.seconds)}초` : ""}`, tone: "speaking" }
                : { text: "아직 등록된 목소리가 없습니다", tone: "idle" };

  const hint =
    recorder.error ??
    problem ??
    (unavailable && step === "idle"
      ? `맥미니의 목소리 서비스가 꺼져 있거나 닿지 않습니다. 켜진 뒤 다시 열어 주세요.${voice.error ? ` (${voice.error})` : ""}`
      : step === "idle"
        ? kept
          ? "다시 녹음하면 새 목소리로 바뀝니다. 조용한 곳에서 아래 대본을 평소 말투로 읽어 주세요."
          : "조용한 곳에서, 아래 대본을 평소 말투로 끝까지 읽어 주세요. 한 번이면 됩니다."
        : step === "saved"
          ? "다음 답부터 이 목소리로 들립니다."
          : null);

  return (
    <div className="pb-safe pt-safe inset-x-safe screen-h fixed inset-x-0 top-0 z-50 flex flex-col bg-background">
      <div className="flex h-13 shrink-0 items-center justify-between px-3 sm:h-14 sm:px-6">
        <IconButton onClick={leave} label="음성 모드로 돌아가기">
          <ArrowLeft aria-hidden className="size-[18px]" />
        </IconButton>
        <span className="text-[13px] text-dim">내 목소리</span>
        {/* Same width as the button on the left, so the title sits centred. */}
        <span aria-hidden className="size-9" />
      </div>

      <main className="flex min-h-0 flex-1 flex-col items-center justify-center gap-6 px-6 sm:gap-8 sm:px-10">
        <Spectrum
          energy={recording ? recorder.level : 0}
          bins={recorder.spectrum}
          mode={recording ? "listening" : step === "saving" ? "thinking" : "idle"}
          className="aspect-square w-[min(84vw,24rem,46dvh)] shrink-0"
        />

        <div className="flex min-h-0 w-full max-w-[32rem] flex-col items-center text-center">
          <p className="flex items-center gap-2 text-[13px] text-dim" role="status" aria-live="polite">
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
          {hint && (
            <p className="mt-3 max-w-[26rem] text-[13.5px] leading-relaxed text-faint">{hint}</p>
          )}
          <div className="mt-1 w-full">
            <Caption text={SCRIPT} tone={recording ? "you" : "jarvis"} />
          </div>
        </div>
      </main>

      <div className="flex shrink-0 items-center justify-center gap-3 px-6 pt-2 pb-8">
        {recording ? (
          <PrimaryButton onClick={finishTake}>
            <Square aria-hidden className="size-3.5 fill-current" />
            다 읽었어요
          </PrimaryButton>
        ) : step === "recorded" ? (
          <>
            <QuietButton onClick={() => void begin()}>
              <RotateCcw aria-hidden className="size-4" />
              다시
            </QuietButton>
            <QuietButton onClick={listen}>
              {playing ? <Square aria-hidden className="size-3.5 fill-current" /> : <Play aria-hidden className="size-4" />}
              {playing ? "멈추기" : "들어보기"}
            </QuietButton>
            <PrimaryButton onClick={() => void save()}>
              <Check aria-hidden className="size-4" />
              이 목소리로
            </PrimaryButton>
          </>
        ) : step === "saving" ? (
          <PrimaryButton onClick={() => undefined} disabled>
            익히는 중
          </PrimaryButton>
        ) : step === "saved" ? (
          <PrimaryButton onClick={leave}>음성 모드로</PrimaryButton>
        ) : (
          <>
            {kept && (
              <QuietButton onClick={() => void forget()}>지우기</QuietButton>
            )}
            <PrimaryButton onClick={() => void begin()} disabled={unavailable}>
              <Mic aria-hidden className="size-4" />
              {kept ? "다시 녹음" : "녹음 시작"}
            </PrimaryButton>
          </>
        )}
      </div>
    </div>
  );
}

function clock(seconds: number) {
  const s = Math.floor(seconds);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}
