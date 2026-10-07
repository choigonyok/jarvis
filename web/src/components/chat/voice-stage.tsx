"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Keyboard, Mic, Volume2, VolumeX, X } from "lucide-react";
import { useMicLevel } from "@/lib/use-mic-level";
import { useRecognition } from "@/lib/use-speech";
import { cn } from "@/lib/utils";

/**
 * The voice surface.
 *
 * Everything else gets out of the way: a ring that shows what is being heard,
 * the words, and the way out. Once you press 시작 it keeps listening - phrases go out as
 * turns on their own, the mic closes while Jarvis is talking so it does not
 * hear itself, and it reopens when he stops. There is no send button because
 * the point of this mode is not touching the machine.
 */
export function VoiceStage({
  onSend,
  speaking,
  pulse,
  onStopSpeaking,
  muted,
  onMutedChange,
  onExit,
  lastSaid,
  thinking,
  progress,
}: {
  onSend: (text: string) => void;
  speaking: boolean;
  /** 0..1, rising on each spoken word. */
  pulse: number;
  onStopSpeaking: () => void;
  muted: boolean;
  onMutedChange: (muted: boolean) => void;
  onExit: () => void;
  /** The newest agent line, for the one-line caption. */
  lastSaid?: string;
  thinking: boolean;
  /** Characters of lastSaid already spoken, or -1 when unknown. */
  progress: number;
}) {
  const [started, setStarted] = useState(false);
  const [draft, setDraft] = useState("");
  /** Your last sent line, kept above Jarvis's answer to it. */
  const [lastSent, setLastSent] = useState<string>();
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The draft lives in a ref as well so sending happens outside a state
  // updater - React may run an updater twice, and that would send twice.
  const pendingDraft = useRef("");

  const flush = useCallback(() => {
    const text = pendingDraft.current.trim();
    pendingDraft.current = "";
    setDraft("");
    if (!text) return;
    setLastSent(text);
    onSend(text);
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

  const { state, interim, error, start, stop, supported } = useRecognition({
    onFinal,
  });
  const listening = state === "listening";
  const { level, spectrum } = useMicLevel(listening);

  useEffect(() => {
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);

  // The loop that makes this hands-free: Jarvis talks, the mic closes; Jarvis
  // stops, it opens again. Without the second half you would have to tap
  // after every single answer, which is the thing this mode exists to avoid.
  useEffect(() => {
    if (!started) return;
    if (speaking) {
      if (listening) stop();
      return;
    }
    // Not after "paused": that device wants a tap, and asking again without
    // one only gets refused again.
    if (
      !listening &&
      state !== "paused" &&
      state !== "denied" &&
      state !== "failed" &&
      state !== "unsupported"
    )
      start({ auto: true });
  }, [started, speaking, listening, state, start, stop]);
  const said = [draft, interim].filter(Boolean).join(" ");

  // One number drives the ring, whichever side of the conversation is live.
  const energy = speaking ? pulse : listening ? level : 0;
  const stopped = state === "denied" || state === "failed";
  const paused = state === "paused";

  /** The tap a device like iOS Safari asks for before it lets the mic reopen. */
  function resume() {
    onStopSpeaking();
    start();
  }

  function begin() {
    setStarted(true);
    onStopSpeaking();
    start();
  }

  // Ending the session keeps you on the stage, so a failed start can be
  // retried in place. Leaving is the two buttons at the top.
  function end() {
    setStarted(false);
    if (timer.current) clearTimeout(timer.current);
    pendingDraft.current = "";
    setDraft("");
    stop();
    onStopSpeaking();
  }

  function leave() {
    end();
    onExit();
  }

  const status: { text: string; tone: Tone } = !started
    ? { text: "대기 중", tone: "idle" }
    : stopped
      ? { text: "음성 인식 멈춤", tone: "error" }
      : paused && !speaking
        ? { text: "눌러서 말하기", tone: "idle" }
      : speaking
        ? { text: "Jarvis가 말하는 중", tone: "speaking" }
        : thinking
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
        text="시작을 누르고 말하세요. 말이 끝나면 Jarvis가 소리로 답합니다."
      />
    );
  } else if (paused && !speaking && !thinking) {
    caption = (
      <Caption
        tone="hint"
        text="이 기기에서는 답을 들은 뒤 말하기를 한 번 눌러야 마이크가 다시 열립니다."
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
  } else if (thinking && lastSent) {
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
    <div className="pb-safe pt-safe inset-x-safe fixed inset-0 z-50 flex flex-col bg-background">
      <div className="flex h-13 shrink-0 items-center justify-between px-3 sm:h-14 sm:px-6">
        <IconButton onClick={leave} label="음성 모드 나가기">
          <X aria-hidden className="size-[18px]" />
        </IconButton>

        <div className="flex items-center gap-1">
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
          bins={spectrum}
          mode={
            !started || stopped
              ? "idle"
              : speaking
                ? "speaking"
                : thinking
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
        </div>
      </main>

      <div className="flex shrink-0 items-center justify-center px-6 pt-2 pb-8">
        {!supported ? (
          <p className="max-w-[26rem] text-center text-[13px] leading-relaxed text-dim">
            이 브라우저는 음성 인식을 지원하지 않습니다. Chrome이나 Safari에서
            열면 쓸 수 있습니다.
          </p>
        ) : started && paused && !speaking ? (
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={resume}
              className="flex h-12 min-w-[8rem] items-center justify-center gap-2 rounded-full bg-foreground px-7 text-[14.5px] font-medium text-background transition-opacity outline-none hover:opacity-90 focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <Mic aria-hidden className="size-4" />
              말하기
            </button>
            <button
              type="button"
              onClick={end}
              className="flex h-12 items-center justify-center rounded-full border border-edge px-5 text-[14px] text-dim transition-colors outline-none hover:border-white/20 hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              끝내기
            </button>
          </div>
        ) : started ? (
          <button
            type="button"
            onClick={end}
            className="flex h-12 min-w-[7.5rem] items-center justify-center rounded-full border border-edge px-6 text-[14px] text-dim transition-colors outline-none hover:border-white/20 hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            끝내기
          </button>
        ) : (
          <button
            type="button"
            onClick={begin}
            className="flex h-12 min-w-[8rem] items-center justify-center gap-2 rounded-full bg-foreground px-7 text-[14.5px] font-medium text-background transition-opacity outline-none hover:opacity-90 focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <Mic aria-hidden className="size-4" />
            시작
          </button>
        )}
      </div>
    </div>
  );
}

type Tone = "idle" | "listening" | "thinking" | "speaking" | "error";

const DOT: Record<Tone, string> = {
  idle: "bg-faint/60",
  listening: "bg-foreground",
  thinking: "bg-dim",
  speaking: "bg-online",
  error: "bg-reject",
};

function IconButton({
  onClick,
  label,
  pressed,
  children,
}: {
  onClick: () => void;
  label: string;
  pressed?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      aria-pressed={pressed}
      className="tap flex items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {children}
    </button>
  );
}

/**
 * The words under the ring.
 *
 * While Jarvis speaks, the words he has already said
 * are lit and the rest wait in grey - you can read along with your ears -
 * and a long answer scrolls itself to keep the spoken edge in view.
 */
function Caption({
  text,
  tone,
  pending,
  progress = -1,
}: {
  text: string;
  tone: "you" | "jarvis" | "hint";
  /** Interim words not yet settled, shown dimmer after the settled ones. */
  pending?: string;
  progress?: number;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const edge = useRef<HTMLSpanElement>(null);
  const long = text.length + (pending?.length ?? 0) > 90;

  useEffect(() => {
    const box = scroller.current;
    const mark = edge.current;
    if (!box || !mark) return;
    box.scrollTo({
      top: Math.max(0, mark.offsetTop - box.clientHeight * 0.4),
      behavior: "smooth",
    });
  }, [progress]);

  const reading = tone === "jarvis" && progress >= 0;

  return (
    <div
      ref={scroller}
      className="max-h-[26dvh] overflow-hidden [mask-image:linear-gradient(to_bottom,transparent,black_1.25rem,black_calc(100%-1.75rem),transparent)] py-5"
    >
      <p
        className={cn(
          "tracking-[-0.012em] text-pretty break-keep transition-colors duration-500",
          long
            ? "text-[clamp(1rem,3vw,1.15rem)] leading-[1.65]"
            : "text-[clamp(1.2rem,4vw,1.5rem)] leading-[1.5]",
          tone === "hint" && "text-faint",
          tone === "you" && "text-foreground",
          tone === "jarvis" && (reading ? "text-faint/70" : "text-dim"),
        )}
      >
        {reading ? (
          <>
            <span className="text-foreground">{text.slice(0, progress)}</span>
            <span ref={edge} />
            {text.slice(progress)}
          </>
        ) : (
          <>
            {text}
            {pending && (
              <span className="text-faint">
                {text ? " " : ""}
                {pending}
              </span>
            )}
          </>
        )}
      </p>
    </div>
  );
}

/**
 * The ring: a circular spectrum.
 *
 * While you talk it is your microphone's actual frequency content - lows at
 * the top, highs running down both sides, mirrored so the shape stays
 * balanced - with a soft wave drawn through the tips. While Jarvis talks the
 * synthesizer gives no audio to analyse, so the ring swells in green on his
 * word boundaries with a moving shape layered on top; that part is a
 * rendering of rhythm, not a reading. Thinking sends one light around the
 * ring; at rest it is a quiet circle of dots that breathes.
 */
function Spectrum({
  energy,
  bins,
  mode,
  className,
}: {
  energy: number;
  bins: React.RefObject<Uint8Array | null>;
  mode: "idle" | "listening" | "thinking" | "speaking";
  className?: string;
}) {
  const canvas = useRef<HTMLCanvasElement>(null);
  // Read inside the draw loop, so the loop is set up once rather than on
  // every level change sixty times a second.
  const live = useRef({ energy, mode });
  useEffect(() => {
    live.current = { energy, mode };
  });

  useEffect(() => {
    const el = canvas.current;
    const ctx = el?.getContext("2d");
    if (!el || !ctx) return;

    // Bars per half; the other half mirrors it.
    const HALF = 44;
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const values = new Float32Array(HALF);
    let size = 0;
    let frame = 0;
    // Colour fades between modes instead of snapping.
    let tint = 0;

    const style = getComputedStyle(el);
    const ink = {
      voice: style.getPropertyValue("--foreground").trim(),
      jarvis: style.getPropertyValue("--online").trim(),
      quiet: style.getPropertyValue("--dim").trim(),
    };

    const resize = () => {
      const ratio = window.devicePixelRatio || 1;
      size = el.clientWidth;
      el.width = Math.round(size * ratio);
      el.height = Math.round(size * ratio);
      ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    };
    const observer = new ResizeObserver(resize);
    observer.observe(el);
    resize();

    // Speech lives between ~90Hz and ~5kHz. Bins are spread on a log scale
    // across that, so the ring is not all bass on one side and silence on
    // the other.
    const binFor = (i: number, count: number) => {
      const lo = 2;
      const hi = Math.min(count - 1, 110);
      return Math.round(lo * Math.pow(hi / lo, i / (HALF - 1)));
    };

    const draw = (now: number) => {
      const { energy: e, mode: m } = live.current;
      const t = now / 1000;
      const data = bins.current;

      for (let i = 0; i < HALF; i += 1) {
        let target = 0;
        if (m === "listening" && data) {
          // Neighbouring bins averaged, so one loud bin is not a spike.
          const b = binFor(i, data.length);
          const raw = (data[b - 1] + data[b] * 2 + data[b + 1]) / (4 * 255);
          // Highs are naturally quieter; tilt them up so the whole ring moves.
          target = Math.min(1, Math.pow(raw, 1.6) * (1 + i / HALF) * 1.15);
        } else if (m === "speaking") {
          const shape =
            0.5 +
            0.25 * Math.sin(i * 0.42 + t * 5.1) +
            0.25 * Math.sin(i * 0.17 - t * 3.3);
          target = e * (0.35 + 0.65 * shape) * (1 - (i / HALF) * 0.45);
        }
        // Fast attack, slow release: the way a level meter reads.
        values[i] += (target - values[i]) * (target > values[i] ? 0.5 : 0.12);
      }

      const goal = m === "speaking" ? 1 : 0;
      tint += (goal - tint) * 0.08;

      ctx.clearRect(0, 0, size, size);
      const c = size / 2;
      const base = size * 0.3;
      const reach = size * 0.18;
      const count = HALF * 2;
      const color =
        tint > 0.5 ? ink.jarvis : m === "listening" ? ink.voice : ink.quiet;

      // Soft light behind the ring, rising with the sound.
      const level = values.reduce((a, v) => a + v, 0) / HALF;
      const glow = ctx.createRadialGradient(c, c, base * 0.2, c, c, base * 1.5);
      glow.addColorStop(0, color);
      glow.addColorStop(1, "transparent");
      ctx.globalAlpha =
        m === "idle" ? 0.035 : m === "thinking" ? 0.06 : 0.06 + level * 0.22;
      ctx.fillStyle = glow;
      ctx.beginPath();
      ctx.arc(c, c, base * 1.5, 0, Math.PI * 2);
      ctx.fill();

      // The inner circle the bars stand on.
      ctx.globalAlpha = m === "idle" ? 0.14 : 0.22;
      ctx.strokeStyle = color;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.arc(c, c, base - 7, 0, Math.PI * 2);
      ctx.stroke();

      const breathe = still ? 0 : (Math.sin(t * 2.4) + 1) / 2;
      const sweep = (t * 0.9) % 1;
      const tips: [number, number][] = [];
      ctx.lineCap = "round";
      ctx.lineWidth = Math.max(1.5, ((Math.PI * 2 * base) / count) * 0.42);

      for (let k = 0; k < count; k += 1) {
        // Index into the mirrored half: 0 at the top, HALF-1 at the bottom.
        const i = k < HALF ? k : count - 1 - k;
        const angle = (k / count) * Math.PI * 2 - Math.PI / 2 + Math.PI / count;
        let length = 2 + values[i] * reach;
        let alpha = 0.35 + values[i] * 0.65;

        if (m === "idle") {
          length = 2 + breathe * 2;
          alpha = 0.22 + breathe * 0.1;
        } else if (m === "thinking") {
          const pos = k / count;
          let d = Math.abs(pos - sweep);
          d = Math.min(d, 1 - d);
          const near = still ? 0 : Math.exp(-(d * d) / 0.004);
          length = 2 + near * reach * 0.35;
          alpha = 0.22 + near * 0.7;
        }

        const cos = Math.cos(angle);
        const sin = Math.sin(angle);
        const x0 = c + cos * base;
        const y0 = c + sin * base;
        const x1 = c + cos * (base + length);
        const y1 = c + sin * (base + length);
        tips.push([
          c + cos * (base + length + 4),
          c + sin * (base + length + 4),
        ]);

        ctx.globalAlpha = alpha;
        ctx.strokeStyle = color;
        ctx.beginPath();
        ctx.moveTo(x0, y0);
        ctx.lineTo(x1, y1);
        ctx.stroke();
      }

      // The wave through the tips, drawn only when there is sound to trace.
      if (level > 0.02 && (m === "listening" || m === "speaking")) {
        ctx.globalAlpha = Math.min(0.4, level * 1.4);
        ctx.lineWidth = 1.25;
        ctx.beginPath();
        for (let k = 0; k <= count; k += 1) {
          const [x, y] = tips[k % count];
          const [nx, ny] = tips[(k + 1) % count];
          const mx = (x + nx) / 2;
          const my = (y + ny) / 2;
          if (k === 0) ctx.moveTo(mx, my);
          else ctx.quadraticCurveTo(x, y, mx, my);
        }
        ctx.closePath();
        ctx.stroke();
      }

      ctx.globalAlpha = 1;
      frame = requestAnimationFrame(draw);
    };
    frame = requestAnimationFrame(draw);

    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
    };
  }, [bins]);

  return (
    <canvas
      ref={canvas}
      role="img"
      aria-label={
        mode === "speaking"
          ? "Jarvis가 말하는 중"
          : mode === "listening"
            ? "듣는 중"
            : "대기 중"
      }
      className={cn("block", className)}
    />
  );
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
