/**
 * What every screen says - reading, a read that did not come, a save or a
 * removal that did not go, the agent out of reach - said one way. The
 * console speaks 해요체 (web/DESIGN.md): the same failure read
 * "저장하지 못했어요" on one screen and "저장하지 못했습니다" on the next, and
 * reading was said three ways (2026-11-07 review). A screen with something
 * more particular to say ("이전 대화를 불러오는 중…") says it in the same form.
 */
export const SAY = {
  /** A screen, or a part of one, on its way. */
  loading: "불러오는 중…",
  /** The way back from a read that did not come (shell/retry.tsx). */
  retry: "다시 시도",
  couldNotLoad: "불러오지 못했어요.",
  couldNotSave: "저장하지 못했어요.",
  couldNotDelete: "지우지 못했어요.",
  /** The agent, or what stands behind it, out of reach. */
  unreachable: "에이전트에 연결하지 못했어요.",
} as const;
