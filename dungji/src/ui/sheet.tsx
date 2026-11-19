// 아래에서 올라오는 판(폰), 가운데 뜨는 창(패드·컴퓨터). 콘솔의 바닥 시트와 같은 손짓:
// 위쪽(손잡이·제목)을 끌어내리면 닫히고, 뒤로가기는 화면을 떠나지 않고 시트만 닫으며,
// 폰 키보드가 올라오면 시트 아래가 키보드만큼 비어 저장 줄이 키보드 위로 온다.

import { useEffect, useRef, useState, type PointerEvent as RPointerEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Icon } from "./icons";

// ── 뒤로가기: 열린 시트마다가 아니라 하나의 기록 칸을 시트들이 넘겨받는다 ──
// (닫히며 바로 다른 시트를 열면 그 칸을 넘긴다. 쓰지 않는 칸이 쌓이면 뒤로가기가 헛돈다.)
interface Holder {
  close: () => void;
}
let entry: { key: string; holder: Holder } | null = null;
let release: ReturnType<typeof setTimeout> | null = null;
let backing = false;
let waiting: (() => void)[] = [];

function settle() {
  const fns = waiting;
  waiting = [];
  fns.forEach((fn) => fn());
}

/** 닫히는 시트의 뒤로가기가 끝난 뒤에 부른다. 그 사이 시작한 부드러운 스크롤은 사파리가 끊는다. */
export function afterSheets(fn: () => void) {
  if (!release && !backing) return fn();
  waiting.push(fn);
  setTimeout(() => {
    backing = false;
    settle();
  }, 600);
}

if (typeof window !== "undefined")
  window.addEventListener("popstate", () => {
    if (backing) {
      backing = false;
      requestAnimationFrame(settle);
    }
    if (entry && (history.state as { dungjiSheet?: string } | null)?.dungjiSheet !== entry.key) {
      const { holder } = entry;
      entry = null;
      holder.close();
    }
  });

function hold(holder: Holder) {
  if (release) {
    clearTimeout(release);
    release = null;
    settle();
  }
  if (entry) {
    entry.holder = holder;
    return;
  }
  const key = Math.random().toString(36).slice(2);
  history.pushState({ ...((history.state as object | null) ?? {}), dungjiSheet: key }, "");
  entry = { key, holder };
}

function letGo(holder: Holder) {
  if (!entry || entry.holder !== holder) return;
  release = setTimeout(() => {
    release = null;
    if (entry && entry.holder === holder) {
      const { key } = entry;
      entry = null;
      // 시트가 열린 사이 화면이 옮겨 갔으면(맨 위가 이 시트의 칸이 아니면) 되돌리지 않는다.
      if ((history.state as { dungjiSheet?: string } | null)?.dungjiSheet === key) {
        backing = true;
        history.back();
        return;
      }
    }
    settle();
  }, 0);
}

/** 폰 키보드가 가린 높이(없으면 0) */
function useKeyboardInset() {
  const [inset, setInset] = useState(0);
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const update = () => setInset(Math.max(0, Math.round(window.innerHeight - vv.height - vv.offsetTop)));
    vv.addEventListener("resize", update);
    vv.addEventListener("scroll", update);
    update();
    return () => {
      vv.removeEventListener("resize", update);
      vv.removeEventListener("scroll", update);
    };
  }, []);
  return inset;
}

const DRAG_CLOSE = 90;

export function Sheet({
  open,
  onClose,
  title,
  children,
  footer,
  dismissible = true,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  /** false 면 닫을 수 없다(처음 이름 정하기처럼 꼭 거쳐야 하는 것) */
  dismissible?: boolean;
}) {
  const ref = useRef<HTMLElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const inset = useKeyboardInset();
  const drag = useRef<{ y: number; dy: number } | null>(null);
  const [dy, setDy] = useState(0);

  useEffect(() => {
    if (!open) return;
    const prev = document.activeElement as HTMLElement | null;
    const holder: Holder = { close: () => closeRef.current() };
    if (dismissible) hold(holder);
    const onKey = (e: KeyboardEvent) => dismissible && e.key === "Escape" && closeRef.current();
    document.addEventListener("keydown", onKey);
    document.body.classList.add("sheet-open");
    requestAnimationFrame(() => ref.current?.focus());
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.classList.remove("sheet-open");
      if (dismissible) letGo(holder);
      prev?.focus?.();
    };
  }, [open, dismissible]);

  if (!open) return null;

  const down = (e: RPointerEvent) => {
    if (!dismissible || e.pointerType === "mouse") return;
    drag.current = { y: e.clientY, dy: 0 };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  };
  const move = (e: RPointerEvent) => {
    if (!drag.current) return;
    drag.current.dy = Math.max(0, e.clientY - drag.current.y);
    setDy(drag.current.dy);
  };
  const up = () => {
    if (!drag.current) return;
    const far = drag.current.dy > DRAG_CLOSE;
    drag.current = null;
    setDy(0);
    if (far) closeRef.current();
  };

  return createPortal(
    <div className="sheet-backdrop" onClick={dismissible ? onClose : undefined}>
      <section
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={typeof title === "string" ? title : undefined}
        tabIndex={-1}
        className="sheet"
        style={{
          transform: dy ? `translateY(${dy}px)` : undefined,
          transition: drag.current ? "none" : "transform 0.2s ease",
          paddingBottom: inset ? `${inset}px` : undefined,
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="touch-none" onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerCancel={up}>
          <div className="sheet-grip" aria-hidden />
          <header className="flex items-center gap-3 px-5 pt-2 pb-3">
            <h2 className="min-w-0 flex-1 truncate text-title font-semibold">{title}</h2>
            {dismissible && (
              <button type="button" onClick={onClose} className="icon-btn -mr-2" aria-label="닫기">
                <Icon name="x" />
              </button>
            )}
          </header>
        </div>
        <div className="sheet-body">{children}</div>
        {footer && <footer className="sheet-footer">{footer}</footer>}
      </section>
    </div>,
    document.body,
  );
}
