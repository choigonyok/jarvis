// 콘솔(jarvis) 안에 띄워져 있을 때: 콘솔 쪽으로 보내는 말(준비됨, 통화 상태).
// 받는 쪽은 web/src/components/nest/host.tsx 다.

/** 다른 화면(jarvis) 안에 띄워져 있나 */
export const EMBEDDED = typeof window !== "undefined" && window.self !== window.top;

/** 말을 보낼 수 있는 곳: 콘솔과, 시험하는 이 기기의 콘솔 */
export const PARENTS = /^(https:\/\/jarvis\.choigonyok\.com|http:\/\/localhost(:\d+)?)$/;

/** 띄운 쪽의 주소. 콘솔이 아니면 null(아무 데도 보내지 않는다). */
function parentOrigin(): string | null {
  if (!EMBEDDED) return null;
  const ancestors = (location as Location & { ancestorOrigins?: DOMStringList }).ancestorOrigins;
  const candidate = ancestors && ancestors.length ? ancestors[0] : document.referrer ? new URL(document.referrer).origin : null;
  return candidate && PARENTS.test(candidate) ? candidate : null;
}

export function tellParent(msg: { type: string; [k: string]: unknown }) {
  const origin = parentOrigin();
  if (origin) window.parent.postMessage(msg, origin);
}

const APP_KEY = "dungji:app";

/**
 * 콘솔이 홈 화면 앱으로 떠 있으면(입장권 주소의 ?app=1) 이 프레임 안에서도 두 손가락 확대를 막는다.
 * 프레임 안의 손짓은 콘솔의 확대 막기(web/src/components/shell/no-zoom.tsx)에 닿지 않고,
 * 확대는 콘솔 화면 전체를 키워 옆으로 밀리는 고장 난 화면이 된다. 브라우저에서는 확대를 그대로 둔다(콘솔과 같다).
 */
export function initEmbed() {
  if (!EMBEDDED) return;
  const params = new URLSearchParams(location.search);
  let app = false;
  try {
    if (params.get("app") === "1") sessionStorage.setItem(APP_KEY, "1");
    app = sessionStorage.getItem(APP_KEY) === "1";
  } catch {
    app = params.get("app") === "1";
  }
  if (params.has("app")) {
    params.delete("app");
    const rest = params.toString();
    history.replaceState(history.state, "", location.pathname + (rest ? `?${rest}` : "") + location.hash);
  }
  if (app) document.documentElement.dataset.app = "";
}
