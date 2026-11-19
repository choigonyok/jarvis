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
          <div key={t.id} className="toast liquid-glass flex max-w-md items-center gap-2 rounded-full px-4 py-2.5 text-ui text-foreground">
            {t.delta !== undefined && (
              <span className={`tnum flex items-center gap-1 font-medium ${t.delta >= 0 ? "text-foreground" : "text-dim"}`}>
                <Icon name="feather" className="size-4 text-dim" />
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
