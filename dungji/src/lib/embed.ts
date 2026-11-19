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
