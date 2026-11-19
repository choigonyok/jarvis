// 밤·낮 테마. 콘솔(jarvis) 안에서는 콘솔 머리글의 테마 버튼이 고른 것을 따른다:
// 처음 열 때는 입장권 주소의 ?theme= 로, 그 뒤 바뀌면 콘솔이 보내는 말(postMessage)로.
// 혼자 열면(이웃 커플) 기기의 설정을 따른다. 고른 적 없으면 밤이다(콘솔과 같다).

import { EMBEDDED, PARENTS } from "./embed";

type Theme = "dark" | "light";
const KEY = "dungji:theme";

const BAR: Record<Theme, string> = { dark: "#0c0d0f", light: "#f9fafb" };

function apply(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  document.querySelectorAll('meta[name="theme-color"]').forEach((m) => m.setAttribute("content", BAR[theme]));
}

const isTheme = (v: unknown): v is Theme => v === "dark" || v === "light";

function remember(theme: Theme) {
  try {
    sessionStorage.setItem(KEY, theme);
  } catch {
    // 저장소를 못 써도 이번 화면에는 맞는다
  }
}

export function initTheme() {
  const embedded = EMBEDDED;
  const params = new URLSearchParams(location.search);
  const given = params.get("theme");
  if (isTheme(given)) {
    remember(given);
    params.delete("theme");
    const rest = params.toString();
    history.replaceState(null, "", location.pathname + (rest ? `?${rest}` : "") + location.hash);
  }
  let saved: string | null = null;
  try {
    saved = sessionStorage.getItem(KEY);
  } catch {
    saved = null;
  }

  if (embedded) apply(isTheme(saved) ? saved : "dark");
  else {
    const light = matchMedia("(prefers-color-scheme: light)");
    apply(light.matches ? "light" : "dark");
    light.addEventListener("change", (e) => apply(e.matches ? "light" : "dark"));
  }

  window.addEventListener("message", (e) => {
    if (!PARENTS.test(e.origin)) return;
    const data = e.data as { type?: string; theme?: unknown } | null;
    if (data?.type === "dungji:theme" && isTheme(data.theme)) {
      remember(data.theme);
      apply(data.theme);
    }
  });
}
