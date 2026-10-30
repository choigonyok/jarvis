/**
 * How each account stands, as checks on the stream (gocommon/checks is the same
 * thing in Go): the status tab shows them and has no code of its own for any
 * broker. Said when one changes and once an hour otherwise, so that a silence
 * means this service stopped saying.
 */

export type Check = {
  id: string;
  group: string;
  name: string;
  state: "ok" | "warn" | "fail" | "unknown";
  summary: string;
  observedAt?: string;
  observedLabel?: string;
  rule: string;
  fix: string;
  deps: string[];
};

const VENUES: { id: string; name: string; prefixes: string[]; bad: "warn" | "fail"; rule: string; fix: string }[] = [
  {
    id: "assets.upbit", name: "업비트 잔고", prefixes: ["업비트:"], bad: "fail",
    rule: "자산 서비스가 업비트 잔고를 읽으면 정상, 오류를 돌려주면 이상. 5분마다 확인해요.",
    fix: "업비트 API 키의 허용 IP와 만료일을 확인하세요.",
  },
  {
    id: "assets.kis", name: "한국투자증권 잔고", prefixes: ["한국투자증권:"], bad: "fail",
    rule: "자산 서비스가 주식 계좌 잔고를 읽으면 정상, 오류를 돌려주면 이상. 5분마다 확인해요.",
    fix: "KIS 앱키 만료(1년)와 계좌번호를 확인하세요. 토큰은 1분에 한 번만 발급되니 서비스 재시작 직후 1분은 실패할 수 있어요.",
  },
  {
    id: "assets.gold", name: "금현물 계좌", prefixes: ["금현물:"], bad: "fail",
    rule: "자산 서비스가 금현물 계좌를 읽으면 정상, 오류를 돌려주면 이상.",
    fix: "금현물 계좌용 KIS 앱키(주식 계좌와 별도)를 확인하세요.",
  },
  {
    id: "assets.flows", name: "원금 기록", prefixes: ["업비트 입출금 내역:", "원금 기록:"], bad: "warn",
    rule: "업비트 입출금 내역과 직접 적은 원금 기록을 읽으면 정상, 어느 쪽이든 실패하면 주의(수익률이 틀려져요).",
    fix: "업비트 키에 입출금 조회 권한이 있는지 확인하세요.",
  },
];

/** What a read of the portfolio says of each account: its problems, by the name they start with. */
export function venueChecks(problems: readonly string[], at: Date): Check[] {
  return VENUES.map(({ id, name, prefixes, bad, rule, fix }) => {
    const row = { id, group: "외부 계정", name, rule, fix, deps: ["svc.assets"] };
    for (const message of problems) {
      const prefix = prefixes.find((p) => message.startsWith(p));
      if (!prefix) continue;
      const detail = message.slice(prefix.length).trim();
      if (detail.includes("설정되지 않았습니다")) return { ...row, state: "unknown" as const, summary: "키가 설정되지 않아 연결하지 않았어요." };
      return { ...row, state: bad, summary: [...detail].slice(0, 160).join("") };
    }
    return { ...row, state: "ok" as const, summary: "읽고 있어요.", observedAt: at.toISOString(), observedLabel: "마지막 조회" };
  });
}

const BEAT_MS = 60 * 60_000;
const said = new Map<string, { what: string; at: number }>();

/** The checks worth a record now, as records. */
export function worthSaying(checks: readonly Check[], now: number): unknown[] {
  const out: unknown[] = [];
  for (const c of checks) {
    const what = `${c.state}\0${c.summary}`;
    const last = said.get(c.id);
    if (last && last.what === what && now - last.at < BEAT_MS) continue;
    said.set(c.id, { what, at: now });
    out.push({
      kind: "check.result", source: "assets", source_key: `${c.id}@${now}`, actor: "assets", subject: c.id,
      occurred_at: new Date(now).toISOString(), tier: "raw", schema_version: 1, payload: c,
    });
  }
  return out;
}
