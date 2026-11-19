// 되돌릴 수 없는 일 앞의 확인(콘솔과 같은 모양): 제목은 "…을 지울까요?", 설명은 무엇이 사라지는지,
// 버튼은 `두기` 와 그 동사(빨강). 브라우저의 confirm() 은 콘솔 프레임 안에서 둥지 주소를 달고 떠서 쓰지 않는다.

import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

interface Ask {
  title: string;
  detail?: string;
  verb: string;
}

const ConfirmCtx = createContext<(ask: Ask) => Promise<boolean>>(async () => false);
export const useConfirm = () => useContext(ConfirmCtx);

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [ask, setAsk] = useState<Ask | null>(null);
  const resolver = useRef<((ok: boolean) => void) | null>(null);

  const confirm = useCallback(
    (a: Ask) =>
      new Promise<boolean>((resolve) => {
        resolver.current?.(false);
        resolver.current = resolve;
        setAsk(a);
      }),
    [],
  );

  const answer = (ok: boolean) => {
    resolver.current?.(ok);
    resolver.current = null;
    setAsk(null);
  };

  return (
    <ConfirmCtx.Provider value={confirm}>
      {children}
      {ask &&
        createPortal(
          <div className="sheet-backdrop" style={{ zIndex: 70 }} onClick={() => answer(false)}>
            <section
              role="alertdialog"
              aria-modal="true"
              aria-label={ask.title}
              className="sheet px-5 pt-5 pb-[max(env(safe-area-inset-bottom),1.25rem)]"
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => e.key === "Escape" && answer(false)}
            >
              <p className="text-title font-semibold">{ask.title}</p>
              {ask.detail && <p className="mt-1.5 text-body text-dim">{ask.detail}</p>}
              <div className="mt-5 grid grid-cols-2 gap-2">
                <button type="button" className="btn btn-raised" onClick={() => answer(false)} autoFocus>
                  두기
                </button>
                <button type="button" className="btn btn-refuse" onClick={() => answer(true)}>
                  {ask.verb}
                </button>
              </div>
            </section>
          </div>,
          document.body,
        )}
    </ConfirmCtx.Provider>
  );
}
