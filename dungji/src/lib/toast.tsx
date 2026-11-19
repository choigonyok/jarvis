// 알림 한 장: 위에서 내려오고, 한 번에 하나(새 알림이 앞의 것을 바꾼다).
// 불투명한 바탕이라 시트나 방 위에 떠도 글자가 섞이지 않고, 아래쪽의 버튼과 탭을 가리지 않는다.

import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { Icon } from "../ui/icons";

interface Toast {
  id: number;
  text: string;
  delta?: number;
}

interface ToastApi {
  show: (text: string) => void;
  feathers: (delta: number, reason: string) => void;
}

const ToastCtx = createContext<ToastApi>({ show: () => undefined, feathers: () => undefined });
export const useToast = () => useContext(ToastCtx);

const SHOW_MS = 2400;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null);
  const seq = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const push = useCallback((t: Omit<Toast, "id">) => {
    const id = ++seq.current;
    setToast({ ...t, id });
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setToast((cur) => (cur?.id === id ? null : cur)), SHOW_MS);
  }, []);
  const api = useMemo<ToastApi>(
    () => ({
      show: (text) => push({ text }),
      feathers: (delta, reason) => push({ text: reason, delta }),
    }),
    [push],
  );
  return (
    <ToastCtx.Provider value={api}>
      {children}
      <div className="toasts pointer-events-none fixed inset-x-0 z-[60] flex justify-center px-4" aria-live="polite">
        {toast && (
          <div
            key={toast.id}
            className="toast flex max-w-md items-center gap-2 rounded-full border border-edge bg-popover px-4 py-2.5 text-ui text-foreground shadow-[0_8px_24px_oklch(0_0_0/35%)]"
          >
            {toast.delta !== undefined && (
              <span className={`tnum flex shrink-0 items-center gap-1 font-medium ${toast.delta >= 0 ? "text-foreground" : "text-dim"}`}>
                <Icon name="feather" className="size-4 text-dim" />
                {toast.delta >= 0 ? `+${toast.delta}` : toast.delta}
              </span>
            )}
            <span className="min-w-0">{toast.text}</span>
          </div>
        )}
      </div>
    </ToastCtx.Provider>
  );
}
