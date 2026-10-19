"use client";

import { useEffect, useRef } from "react";
import { cn } from "@/lib/utils";

/**
 * The pieces the voice screens are built from - the stage that talks with
 * Jarvis and the one that teaches him your voice share the same ring, the same
 * big caption and the same quiet icon buttons, so the second reads as a room
 * in the same house rather than a form that opened over it.
 */

export type Tone = "idle" | "listening" | "thinking" | "speaking" | "error";

export const DOT: Record<Tone, string> = {
  idle: "bg-faint/60",
  listening: "bg-foreground",
  thinking: "bg-dim",
  speaking: "bg-online",
  error: "bg-reject",
};

export function IconButton({
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

/** The one filled button on a voice screen: whatever the next thing to do is. */
export function PrimaryButton({
  onClick,
  children,
  disabled,
}: {
  onClick: () => void;
  children: React.ReactNode;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className="flex h-12 min-w-[8rem] items-center justify-center gap-2 rounded-full bg-foreground px-7 text-[14.5px] font-medium text-background transition-opacity outline-none hover:opacity-90 focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-40"
    >
      {children}
    </button>
  );
}

/** The outlined one beside it: the way back, or the smaller choice. */
export function QuietButton({
  onClick,
  children,
  disabled,
}: {
  onClick: () => void;
  children: React.ReactNode;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className="flex h-12 items-center justify-center gap-2 rounded-full border border-edge px-5 text-[14px] text-dim transition-colors outline-none hover:border-white/20 hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-40"
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
export function Caption({
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
 * balanced - with a soft wave drawn through the tips. While Jarvis talks in
 * your own voice the same analysis runs on his audio, so the ring is a real
 * reading of the sound; on the browser's built-in voice there is no audio to
 * tap, so it swells in green on word boundaries instead - a rendering of
 * rhythm, not a reading. Thinking sends one light around the ring; at rest it
 * is a quiet circle of dots that breathes.
 */
export function Spectrum({
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
        if ((m === "listening" || m === "speaking") && data) {
          // Neighbouring bins averaged, so one loud bin is not a spike.
          const b = binFor(i, data.length);
          const raw = (data[b - 1] + data[b] * 2 + data[b + 1]) / (4 * 255);
          // Highs are naturally quieter; tilt them up so the whole ring moves.
          target = Math.min(1, Math.pow(raw, 1.6) * (1 + i / HALF) * 1.15);
        } else if (m === "speaking" || m === "listening") {
          // No waveform to read (the cloned voice through a media element, a
          // phone's recognizer that owns the mic): the ring moves on energy.
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
