export type CheckState = "ok" | "warn" | "fail" | "unknown";

export type CheckResult = {
  id: string;
  group: string;
  name: string;
  state: CheckState;
  summary: string;
  observedAt?: string;
  observedLabel?: string;
  facts?: string[];
  rule: string;
  fix?: string;
  checkedAt: string;
  since: string;
  cause?: string;
  causeName?: string;
};

export type StatusReport = {
  groups: string[];
  results: CheckResult[];
  now: string;
};

/** "방금", "3분 전", "5시간 전", "3일 전". Ages here run to weeks, so days
 *  stay days rather than turning into a date - "3일 전" is the finding. */
export function since(iso: string, now = Date.now()): string {
  const min = Math.floor((now - new Date(iso).getTime()) / 60000);
  if (min < 1) return "방금";
  if (min < 60) return `${min}분 전`;
  const h = Math.floor(min / 60);
  if (h < 48) return `${h}시간 전`;
  return `${Math.floor(h / 24)}일 전`;
}

/** How long a state has held: "12분째", "3일째". */
export function held(iso: string, now = Date.now()): string {
  const min = Math.floor((now - new Date(iso).getTime()) / 60000);
  if (min < 1) return "방금부터";
  if (min < 60) return `${min}분째`;
  const h = Math.floor(min / 60);
  if (h < 48) return `${h}시간째`;
  return `${Math.floor(h / 24)}일째`;
}

/**
 * The faults worth naming at the top: those not explained by another one.
 * A KakaoTalk app that is off also silences card alerts; that is one problem,
 * and the headline should say it once.
 */
export function roots(results: CheckResult[]): CheckResult[] {
  const bad = results.filter((r) => r.state === "fail" || r.state === "warn");
  return bad
    .filter((r) => !r.cause)
    .sort((a, b) => (a.state === b.state ? 0 : a.state === "fail" ? -1 : 1));
}
