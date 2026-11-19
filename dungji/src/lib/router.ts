// 아주 작은 주소 이동: 주소창과 뒤로 가기를 그대로 쓴다.

import { useEffect, useState, type MouseEvent } from "react";

const EVENT = "dungji:navigate";

// 화면 위치는 이 앱이 정한다: 새 주소와 뒤로 가서 바뀐 주소는 맨 위, 시트를 닫는 뒤로가기는 그 자리 그대로.
// 브라우저가 스스로 되돌리게 두면 시트를 닫으며 방으로 내려가던 화면이 시트를 연 자리로 튄다.
if (typeof history !== "undefined" && "scrollRestoration" in history) history.scrollRestoration = "manual";
let shownPath = typeof location !== "undefined" ? location.pathname : "";

export function go(to: string, opts: { replace?: boolean } = {}) {
  if (to === location.pathname + location.search) return;
  history[opts.replace ? "replaceState" : "pushState"](null, "", to);
  window.dispatchEvent(new Event(EVENT));
  if (!opts.replace) window.scrollTo({ top: 0 });
}

export function useLocation() {
  const read = () => ({ path: location.pathname, query: new URLSearchParams(location.search) });
  const [loc, setLoc] = useState(read);
  useEffect(() => {
    const on = (e: Event) => {
      if (e.type === "popstate" && location.pathname !== shownPath) window.scrollTo({ top: 0 });
      shownPath = location.pathname;
      setLoc(read());
    };
    window.addEventListener("popstate", on);
    window.addEventListener(EVENT, on);
    return () => {
      window.removeEventListener("popstate", on);
      window.removeEventListener(EVENT, on);
    };
  }, []);
  return loc;
}

/** <a> 를 누르면 새로 고치지 않고 옮겨 간다. */
export function linkTo(to: string) {
  return {
    href: to,
    onClick: (e: MouseEvent) => {
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
      e.preventDefault();
      go(to);
    },
  };
}
