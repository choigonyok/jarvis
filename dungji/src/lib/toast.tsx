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

export function ToastProvider({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([]);
  const seq = useRef(0);
  const push = useCallback((t: Omit<Toast, "id">) => {
    const id = ++seq.current;
    setList((l) => [...l.slice(-2), { ...t, id }]);
    setTimeout(() => setList((l) => l.filter((x) => x.id !== id)), 2800);
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
      <div className="toasts pointer-events-none fixed inset-x-0 z-[60] flex flex-col items-center gap-2 px-4" aria-live="polite">
        {list.map((t) => (
          <div key={t.id} className="toast flex max-w-md items-center gap-2 rounded-full bg-ink px-4 py-2.5 text-[15px] text-paper shadow-[0_8px_24px_rgba(0,0,0,.2)]">
            {t.delta !== undefined && (
              <span className={`flex items-center gap-1 font-bold ${t.delta >= 0 ? "text-straw" : "text-paper/70"}`}>
                <Icon name="feather" className="size-4" />
                {t.delta >= 0 ? `+${t.delta}` : t.delta}
              </span>
            )}
            <span>{t.text}</span>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}
