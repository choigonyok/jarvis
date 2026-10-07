"use client";

import { useEffect, useRef, useState } from "react";
import { Keyboard, MousePointer2, MousePointerClick, Move } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * The parts of noVNC's RFB this uses. sendKey and clipboardPasteFrom are
 * public; _sendMouse is not, but it is the one place that turns canvas
 * coordinates into a pointer event, and the version is pinned (1.7).
 */
export type RemoteRFB = {
  viewOnly: boolean;
  disconnect: () => void;
  sendKey: (keysym: number, code: string | null, down?: boolean) => void;
  clipboardPasteFrom: (text: string) => void;
  _sendMouse: (x: number, y: number, mask: number) => void;
};

const LEFT = 0x1;
const RIGHT = 0x4;
const WHEEL_UP = 0x8;
const WHEEL_DOWN = 0x10;

const KEYS: { label: string; keysym: number; code: string; aria: string }[] = [
  { label: "Enter", keysym: 0xff0d, code: "Enter", aria: "Enter" },
  { label: "⌫", keysym: 0xff08, code: "Backspace", aria: "지우기" },
  { label: "Tab", keysym: 0xff09, code: "Tab", aria: "Tab" },
  { label: "Esc", keysym: 0xff1b, code: "Escape", aria: "Esc" },
  { label: "←", keysym: 0xff51, code: "ArrowLeft", aria: "왼쪽" },
  { label: "↑", keysym: 0xff52, code: "ArrowUp", aria: "위" },
  { label: "↓", keysym: 0xff54, code: "ArrowDown", aria: "아래" },
  { label: "→", keysym: 0xff53, code: "ArrowRight", aria: "오른쪽" },
];

/**
 * Text into the remote browser. Plain ASCII goes as keystrokes. Anything
 * else (Korean above all) cannot be typed through X keysyms reliably, so it
 * goes through the clipboard and a Ctrl+V.
 */
export function typeText(rfb: RemoteRFB, text: string) {
  if (/^[\x20-\x7e]*$/.test(text)) {
    for (const ch of text) rfb.sendKey(ch.charCodeAt(0), null);
    return;
  }
  rfb.clipboardPasteFrom(text);
  rfb.sendKey(0xffe3, "ControlLeft", true);
  rfb.sendKey(0x76, "KeyV");
  rfb.sendKey(0xffe3, "ControlLeft", false);
}

type Mode = "click" | "drag";

/**
 * Touch control of the remote screen, laid over the canvas on touch devices.
 *
 * noVNC has gestures of its own (two-finger tap for a right click, long press
 * to drag), but nobody finds them. Here the modes are buttons:
 * - 클릭: tap = click, hold = right click, slide = move the pointer,
 *   two fingers = scroll.
 * - 드래그: press, move, let go = a drag with the button held.
 * - 우클릭: the next tap is a right click.
 */
export function RemoteTouch({
  rfb,
  canvasHost,
}: {
  rfb: React.RefObject<RemoteRFB | null>;
  canvasHost: React.RefObject<HTMLDivElement | null>;
}) {
  const [mode, setMode] = useState<Mode>("click");
  const [rightNext, setRightNext] = useState(false);
  const [typing, setTyping] = useState(false);
  const [text, setText] = useState("");
  const input = useRef<HTMLInputElement>(null);

  // Per-gesture state, read only in handlers.
  const g = useRef<{
    id: number;
    x: number;
    y: number;
    moved: boolean;
    timer: number | null;
    held: boolean;
    pressed: boolean;
    second?: { id: number; y: number };
    scrollY?: number;
  } | null>(null);

  useEffect(() => {
    if (typing) input.current?.focus();
  }, [typing]);

  const pos = (clientX: number, clientY: number) => {
    const canvas = canvasHost.current?.querySelector("canvas");
    const r = canvas?.getBoundingClientRect();
    if (!r) return null;
    return {
      x: Math.min(r.width - 1, Math.max(0, clientX - r.left)),
      y: Math.min(r.height - 1, Math.max(0, clientY - r.top)),
    };
  };
  const send = (clientX: number, clientY: number, mask: number) => {
    const p = pos(clientX, clientY);
    if (p && rfb.current) rfb.current._sendMouse(p.x, p.y, mask);
  };
  const click = (clientX: number, clientY: number, button: number) => {
    send(clientX, clientY, 0);
    send(clientX, clientY, button);
    send(clientX, clientY, 0);
  };

  return (
    <>
      <div
        className="absolute inset-0 touch-none select-none [-webkit-touch-callout:none]"
        onContextMenu={(e) => e.preventDefault()}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          const cur = g.current;
          if (cur && cur.id !== e.pointerId) {
            // A second finger: this is a scroll, not a click.
            if (cur.timer) window.clearTimeout(cur.timer);
            cur.timer = null;
            cur.moved = true;
            if (cur.pressed) send(cur.x, cur.y, 0);
            cur.pressed = false;
            cur.second = { id: e.pointerId, y: e.clientY };
            cur.scrollY = e.clientY;
            return;
          }
          const state = {
            id: e.pointerId,
            x: e.clientX,
            y: e.clientY,
            moved: false,
            timer: null as number | null,
            held: false,
            pressed: false,
          };
          g.current = state;
          if (mode === "drag") {
            send(e.clientX, e.clientY, 0);
            send(e.clientX, e.clientY, LEFT);
            state.pressed = true;
            return;
          }
          state.timer = window.setTimeout(() => {
            state.held = true;
            state.timer = null;
            navigator.vibrate?.(10);
            click(state.x, state.y, RIGHT);
          }, 500);
        }}
        onPointerMove={(e) => {
          const cur = g.current;
          if (!cur) return;
          if (cur.second && e.pointerId === cur.second.id) {
            const dy = e.clientY - (cur.scrollY ?? e.clientY);
            if (Math.abs(dy) > 18) {
              const wheel = dy > 0 ? WHEEL_UP : WHEEL_DOWN;
              send(cur.x, cur.y, wheel);
              send(cur.x, cur.y, 0);
              cur.scrollY = e.clientY;
            }
            return;
          }
          if (e.pointerId !== cur.id || cur.second) return;
          if (!cur.moved && Math.hypot(e.clientX - cur.x, e.clientY - cur.y) > 8) {
            cur.moved = true;
            if (cur.timer) window.clearTimeout(cur.timer);
            cur.timer = null;
          }
          if (cur.moved) {
            cur.x = e.clientX;
            cur.y = e.clientY;
            send(e.clientX, e.clientY, cur.pressed ? LEFT : 0);
          }
        }}
        onPointerUp={(e) => {
          const cur = g.current;
          if (!cur) return;
          if (cur.second && e.pointerId === cur.second.id) {
            cur.second = undefined;
            return;
          }
          if (e.pointerId !== cur.id) return;
          if (cur.timer) window.clearTimeout(cur.timer);
          if (cur.pressed) {
            send(e.clientX, e.clientY, 0);
          } else if (!cur.moved && !cur.held) {
            click(e.clientX, e.clientY, rightNext ? RIGHT : LEFT);
            if (rightNext) setRightNext(false);
          }
          g.current = null;
        }}
        onPointerCancel={() => {
          const cur = g.current;
          if (cur?.timer) window.clearTimeout(cur.timer);
          if (cur?.pressed) send(cur.x, cur.y, 0);
          g.current = null;
        }}
      />

      <div className="absolute inset-x-2 bottom-2 z-10 space-y-1.5">
        {typing ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!rfb.current) return;
              if (text) typeText(rfb.current, text);
              rfb.current.sendKey(0xff0d, "Enter");
              setText("");
            }}
            className="flex gap-1.5 rounded-xl border border-edge bg-background/90 p-1.5 backdrop-blur-md"
          >
            <input
              ref={input}
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder="입력할 글자"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              className="h-10 min-w-0 flex-1 rounded-lg bg-glass px-3 text-[16px] text-foreground outline-none placeholder:text-faint"
            />
            <button
              type="button"
              disabled={!text}
              onClick={() => {
                if (rfb.current && text) typeText(rfb.current, text);
                setText("");
                input.current?.focus();
              }}
              className="h-10 shrink-0 rounded-lg bg-glass-raised px-3 text-[13px] text-foreground disabled:opacity-40"
            >
              넣기
            </button>
            <button type="submit" className="h-10 shrink-0 rounded-lg bg-glass-raised px-3 text-[13px] text-foreground">
              넣고 ↵
            </button>
          </form>
        ) : null}
        {typing ? (
          <div className="scrollbar-none flex gap-1 overflow-x-auto">
            {KEYS.map((k) => (
              <button
                key={k.code}
                type="button"
                aria-label={k.aria}
                // Keep the text field focused, so the phone keyboard stays up.
                onPointerDown={(e) => e.preventDefault()}
                onClick={() => rfb.current?.sendKey(k.keysym, k.code)}
                className="tnum h-9 min-w-11 shrink-0 rounded-lg border border-edge bg-background/90 px-2.5 text-[13px] text-foreground backdrop-blur-md"
              >
                {k.label}
              </button>
            ))}
          </div>
        ) : null}
        <div className="flex items-center gap-1.5 rounded-xl border border-edge bg-background/90 p-1 backdrop-blur-md">
          <div role="radiogroup" aria-label="터치 방식" className="flex gap-1">
            <ModeButton on={mode === "click"} onClick={() => setMode("click")} Icon={MousePointer2} label="클릭" />
            <ModeButton on={mode === "drag"} onClick={() => setMode("drag")} Icon={Move} label="드래그" />
          </div>
          <button
            type="button"
            aria-pressed={rightNext}
            onClick={() => setRightNext((v) => !v)}
            className={cn(
              "flex h-9 items-center gap-1 rounded-lg px-2.5 text-[12.5px]",
              rightNext ? "bg-foreground text-background" : "text-dim",
            )}
          >
            <MousePointerClick aria-hidden className="size-4" />
            우클릭
          </button>
          <button
            type="button"
            aria-pressed={typing}
            onClick={() => setTyping((v) => !v)}
            className={cn(
              "ms-auto flex h-9 items-center gap-1 rounded-lg px-2.5 text-[12.5px]",
              typing ? "bg-glass-raised text-foreground" : "text-dim",
            )}
          >
            <Keyboard aria-hidden className="size-4" />
            키보드
          </button>
        </div>
      </div>
    </>
  );
}

function ModeButton({
  on,
  onClick,
  Icon,
  label,
}: {
  on: boolean;
  onClick: () => void;
  Icon: typeof Move;
  label: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      onClick={onClick}
      className={cn("flex h-9 items-center gap-1 rounded-lg px-2.5 text-[12.5px]", on ? "bg-glass-raised text-foreground" : "text-dim")}
    >
      <Icon aria-hidden className="size-4" />
      {label}
    </button>
  );
}
