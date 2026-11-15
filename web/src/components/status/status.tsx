"use client";

import { useEffect, useState, type ReactNode } from "react";
import { ChevronDown, LogOut, RotateCw } from "lucide-react";
import { AsOf } from "@/components/shell/as-of";
import { Failed } from "@/components/shell/failed";
import { Header, PageTop } from "@/components/shell/header";
import { useSignOut } from "@/components/shell/sign-out";
import { TabBar } from "@/components/shell/tab-bar";
import { StandingBar } from "@/components/shell/standing-bar";
import { Progress } from "@/components/ui/progress";
import { DetailRow } from "@/components/ui/detail-sheet";
import { onFocusWanted, wantedFocus } from "@/lib/focus";
import {
  type CheckResult,
  type CheckState,
  type StatusReport,
  held,
  roots,
  since,
} from "@/lib/status";
import { HttpError, useResource } from "@/lib/use-resource";
import { SAY } from "@/lib/say";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import {
  type Usage,
  type UsageWindow,
  PURPOSE,
  capPct,
  freeLabel,
  freePct,
  pct,
  resetLabel,
  tokens,
  tone,
  usd,
  useUsage,
} from "@/lib/use-usage";
import { cn } from "@/lib/utils";
import { monthDay } from "@/lib/when";
import { PhoneSetup } from "./phone-setup";
import { SavedLogins } from "./saved-logins";

/**
 * Is anything broken - answered per feature, not per container.
 *
 * The screen opens on one sentence, because the question that brings someone
 * here is yes-or-no. Below it everything is the same row, in groups: a mark
 * for its state, a name, a figure on the right, one line saying what it is
 * doing; opening a row shows how it is judged and what to do. The checks,
 * what the subscription and the API keys have used, and setting up the phone
 * all read that way - one grammar, so a row is found where it was last time
 * and read the same wherever it is. Rows keep a fixed order; the headline is
 * where faults are lifted out.
 *
 * Faults that follow from another fault say so ("카톡 수집기 때문일 수 있어요")
 * instead of standing as independent problems: KakaoTalk going quiet and card
 * alerts going quiet are the same event, and the screen counts it once.
 *
 * States reuse the product's marks rather than adding colours: --online for
 * ok, --reject for a fault, the breathing hollow ring ("waiting on you") for
 * a warning, a faint dash for one with no measure: not read yet (읽지 못함),
 * or with no key to read with (측정 안 함) - counted apart, as they are not
 * the same news.
 */
export function Status() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const usage = useUsage();

  // Read on opening, on coming back to the app (a pull to refresh too) and
  // every 30 seconds - only while the tab is visible: a phone left on this
  // screen in a pocket should not keep polling.
  const status = useResource("status", (signal) => reportOf("GET", signal), { interval: 30_000 });
  const report = status.data ?? null;
  const error = status.error?.message ?? null;
  const [refreshing, setRefreshing] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  // The group whose sheet is up, and the check a notification named: its group
  // opens once the report says which that is.
  const [sheet, setSheet] = useState<string | null>(null);
  const [aim, setAim] = useState<string | null>(null);
  const [, tick] = useState(0);

  // "확인 3분 전" moves on while the screen stays.
  useEffect(() => {
    const clock = setInterval(() => tick((n) => n + 1), 30_000);
    return () => clearInterval(clock);
  }, []);

  // A notification about a check lands on its row (lib/focus.ts): open it
  // too, so what is wrong and what to do are on screen. The row is in its
  // group's sheet, so the sheet opens when it is wanted.
  useEffect(() => {
    const wanted = (id: string) => {
      if (id.startsWith("check:")) setAim(id.slice("check:".length));
    };
    const first = wantedFocus();
    if (first) wanted(first);
    const stop = onFocusWanted(wanted);
    const opened = (e: Event) => {
      const id = (e.target as HTMLElement).dataset.focus?.replace(/^check:/, "");
      if (id) setOpen(id);
    };
    document.addEventListener("jarvis:focused", opened);
    return () => {
      stop();
      document.removeEventListener("jarvis:focused", opened);
    };
  }, []);

  // An action, which nothing cuts short - not leaving the tab, coming back or
  // another read: status-svc runs the checks again for it, and a request cut
  // short could cut them short too, to be kept as checks that did not answer.
  async function refresh() {
    setRefreshing(true);
    await status.reloadWith(() => reportOf("POST"));
    setRefreshing(false);
  }

  const results = report?.results ?? [];
  const spend = usageRows(usage);
  // The setups under the groups are sheets of their own, not checks: a link
  // to one (위치 수신's 폰 연결 설정 열기, the 위치 screen's ?focus=check:phone)
  // opens it. Looked up as a check, it had only closed the sheet it was in.
  const groupOf = (id: string) =>
    SETUPS.includes(id)
      ? id
      : (results.find((r) => r.id === id)?.group ?? (spend.some((r) => r.id === id) ? USAGE : null));

  function reveal(id: string) {
    setOpen(id);
    setSheet(groupOf(id));
    // The sheet lays it out on the next frame.
    window.setTimeout(() => document.getElementById(`check-${id}`)?.scrollIntoView({ block: "center" }), 120);
  }

  const shown = sheet ?? (aim ? groupOf(aim) : null);
  const show = (group: string) => (v: boolean) => {
    setAim(null);
    setSheet(v ? group : null);
  };
  const lastChecked = results.reduce<string | null>(
    (latest, r) => (!latest || r.checkedAt > latest ? r.checkedAt : latest),
    null,
  );
  // 사용량 sits before 기반: the subscription is the ground the rest stands on,
  // but it is read more often than the services are.
  const groups = report?.groups ?? [];
  const at = groups.indexOf("기반");
  const order = at < 0 ? [...groups, USAGE] : [...groups.slice(0, at), USAGE, ...groups.slice(at)];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={waiting.length} />
        <StandingBar pending={waiting} />
      </PageTop>

      {/* inset-x-safe and px-4 both set padding-left, so they sit on
          different elements: the notch inset outside, the gutter inside. */}
      <main className="scrollbar-hairline inset-x-safe min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar">
        <div className="mx-auto w-full max-w-page px-4 pt-6 pb-10 sm:px-8 sm:pt-10">
          {/* Never read: the failure in its place, with the way back - it had
              been the loading line's faint centred words, with nothing to
              press, on the screen that is opened to see what is broken. A read
              again that failed keeps the report and says when it is from. */}
          {!report ? (
            error ? (
              <Failed onRetry={() => status.reload()} className="pt-4">
                {error}
              </Failed>
            ) : (
              <Placeholder />
            )
          ) : (
            <>
              {error ? <AsOf at={status.readAt} failed onRetry={() => void status.reload()} className="mb-3" /> : null}
              <Headline results={[...results, ...spend]} onPick={reveal} />

              <div className="mt-3 flex items-center gap-3 text-meta text-faint">
                {lastChecked ? <span className="tnum">확인 {since(lastChecked)}</span> : null}
                <button
                  type="button"
                  onClick={() => void refresh()}
                  disabled={refreshing}
                  className="tap -my-3 flex items-center gap-1.5 rounded-md px-1 text-dim transition-colors hover:text-foreground focus-ring disabled:text-faint"
                >
                  <RotateCw aria-hidden className={cn("size-3.5", refreshing && "anim-spin-slow")} />
                  {refreshing ? "확인하는 중" : "다시 확인"}
                </button>
              </div>

              <ul className="mt-7 divide-y divide-edge-soft border-y border-edge-soft">
                {order.map((group) => {
                  const rows = group === USAGE ? spend : results.filter((r) => r.group === group);
                  if (rows.length === 0) return null;
                  const { worst, off, tally, trouble } = standing(rows);
                  return (
                    <DetailRow
                      key={group}
                      title={group}
                      mark={<Mark state={worst} off={off} />}
                      line={trouble}
                      figure={<span className={worst === "fail" || worst === "warn" ? "text-dim" : undefined}>{tally}</span>}
                      open={shown === group}
                      onOpenChange={show(group)}
                    >
                      <Rows rows={rows} open={open} aim={aim} setOpen={setOpen} onPick={reveal} />
                    </DetailRow>
                  );
                })}

                <DetailRow title="폰 연결" line="아이폰이 위치와 기기 상태를 보내게 설정해요." open={shown === "phone"} onOpenChange={show("phone")}>
                  <PhoneSetup />
                </DetailRow>

                <DetailRow title="로그인 정보" line="Jarvis가 자동으로 넣는 사이트와 넣은 기록이에요." open={shown === "logins"} onOpenChange={show("logins")}>
                  <SavedLogins />
                </DetailRow>
              </ul>
            </>
          )}
          <SignOutRow />
        </div>
      </main>

      <TabBar pending={waiting.length} />
    </div>
  );
}

/**
 * 로그아웃 on a phone, at the foot of the screen the account and the device
 * are looked after on. Its place is the foot of 더보기's sheet, and from in
 * here that sheet does not open - the slot goes round its screens - so it had
 * been out of reach from all three. Even with the status unread: signing out
 * is wanted most when something is wrong. A wide screen has it in the header.
 */
function SignOutRow() {
  const out = useSignOut();
  return (
    <div className="mt-8 sm:hidden">
      <button
        type="button"
        disabled={out.busy}
        onClick={() => void out.signOut()}
        className="-mx-3 flex min-h-12 items-center gap-3 rounded-xl px-3 text-body text-dim focus-ring"
      >
        <LogOut aria-hidden className="size-[18px]" strokeWidth={1.8} />
        {out.busy ? "로그아웃하는 중…" : "로그아웃"}
      </button>
      {out.failed ? (
        <p role="status" className="text-aside text-reject">
          {out.failed}
        </p>
      ) : null}
    </div>
  );
}

/** A failure carries the status the service answered with, if it did: a refusal (4xx) is not read again. */
type Fetched = { report: StatusReport; error?: undefined } | { report?: undefined; error: string; status?: number };

/** POST makes the service look again before answering; GET reads what it has. */
async function fetchReport(method: "GET" | "POST", signal?: AbortSignal): Promise<Fetched> {
  let status: number | undefined;
  try {
    const res = await fetch(method === "POST" ? "/api/status/refresh" : "/api/status/status", {
      method,
      cache: "no-store",
      signal,
    });
    if (!res.ok) status = res.status;
    const body = await res.json();
    if (!res.ok) return { error: body.error ?? "상태를 불러오지 못했어요.", status };
    return { report: body as StatusReport };
  } catch {
    return { error: "상태를 불러오지 못했어요.", status };
  }
}

/**
 * The report as useResource reads it: a failure, in the service's words when
 * it has them, is thrown - an HttpError when the service answered.
 */
async function reportOf(method: "GET" | "POST", signal?: AbortSignal): Promise<StatusReport> {
  const out = await fetchReport(method, signal);
  if (out.error === undefined) return out.report;
  throw out.status === undefined ? new Error(out.error) : new HttpError(out.status, out.error);
}

const USAGE = "사용량";

/** The sheets of the rows under the groups: 폰 연결, 로그인 정보. */
const SETUPS = ["phone", "logins"];

/**
 * A row, as every line on this screen is drawn. Checks come from status-svc;
 * the 사용량 rows are made here from the same numbers the header's bars read,
 * judged the way the bars warm: 90% of a window is a warning, a closed window
 * a fault.
 */
type Line = Pick<CheckResult, "id" | "name" | "summary"> &
  Partial<CheckResult> & {
    /** What opening it shows, when it is not the check's facts and rule. */
    detail?: ReactNode;
    /** Unknown because there is nothing to measure with (no key), not because it is not read yet. */
    off?: boolean;
  };

/**
 * The window a refusal is said on. The CLI's status is the subscription's,
 * not a window's: given to both, one window running out was two faults in
 * the headline, and the week a red mark beside "남은 60%". A full window says
 * it by itself; with neither full the refusal goes on the fuller one - and on
 * none once a window has started over since it was read, as it may be over.
 */
function refusedWindow(u: Usage): "five" | "week" | null {
  if (u.status !== "rejected" || u.fiveHour?.reset || u.sevenDay?.reset) return null;
  const five = pct(u.fiveHour);
  const week = pct(u.sevenDay);
  if (five === null && week === null) return null;
  if ((five ?? 0) >= 100 || (week ?? 0) >= 100) return null;
  return (week ?? -1) > (five ?? -1) ? "week" : "five";
}

function windowRow(id: string, name: string, w: UsageWindow | undefined, refused: boolean): Line {
  const p = pct(w);
  const state: CheckState = p == null ? "unknown" : p >= 100 || refused ? "fail" : p >= 90 ? "warn" : "ok";
  return {
    id,
    name,
    state,
    figure: w?.reset ? "측정 전" : p == null ? "-" : `${p}%`,
    meter: p,
    summary: w?.reset
      ? "초기화된 뒤로 아직 읽지 못했어요. 다음 대화나 작업 때 채워져요."
      : p == null
        ? "아직 읽지 못했어요. 대화나 작업이 한 번 돌면 채워져요."
        : w?.resetsAt
          ? `남은 ${100 - p}% · ${resetLabel(w.resetsAt)} 초기화`
          : `남은 ${100 - p}%`,
    rule: "대화나 작업이 돌 때마다 Claude Code 가 알려 주는 구독 사용량이에요. 90%부터 주의, 다 쓰면 초기화될 때까지 Jarvis가 답하지 못해요.",
  };
}

function usageRows(u: Usage | null): Line[] {
  // Not come yet, or not coming - the agent's /usage failing is never said
  // (lib/use-usage.ts): either way the group keeps its place and says it is
  // not read, rather than missing, which counted the screen as all fine.
  if (!u) return [{ id: "usage", name: "Claude 사용량", state: "unknown", summary: "아직 읽지 못했어요." }];
  const refused = refusedWindow(u);
  const rows: Line[] = [
    windowRow("usage.five", "Claude 5시간", u.fiveHour, refused === "five"),
    windowRow("usage.week", "Claude 주간", u.sevenDay, refused === "week"),
  ];
  const api = u.api;
  if (api) {
    const p = capPct(api);
    rows.push({
      id: "usage.memory",
      name: "기억 API",
      state: !api.enabled ? "unknown" : p != null && p >= 100 ? "warn" : "ok",
      off: !api.enabled,
      figure: `${usd(api.todayUsd)} / ${usd(api.dailyUsd)}`,
      meter: p,
      summary: !api.enabled
        ? "API 키가 없어 대화에서 기억을 뽑지 않아요."
        : p != null && p >= 100
          ? "오늘 상한에 닿아 내일 이어서 기억해요."
          : `이번 달 ${usd(api.month.usd)} · ${api.month.calls.toLocaleString()}회`,
      rule: `대화에서 기억할 사실을 뽑는 ${api.model}의 사용료예요. 하루 상한에 닿으면 그날은 멈추고 다음 날 이어서 해요.`,
      detail: <Days label="최근 7일 기억 API 사용료" days={[...api.days].reverse().map((d) => ({ day: d.day, usd: d.usd, note: `${d.calls}회` }))} />,
    });
  }
  const v = u.api?.voyage;
  if (v) {
    const p = freePct(v);
    rows.push({
      id: "usage.search",
      name: "검색 API",
      state: !v.enabled ? "unknown" : v.billedUsd > 0 || (p ?? 0) >= 90 ? "warn" : "ok",
      off: !v.enabled,
      figure: usd(v.todayUsd),
      meter: p,
      summary: !v.enabled ? "API 키가 없어요." : `무료 토큰 ${freeLabel(v)} 사용 · 청구 ${usd(v.billedUsd)}`,
      rule: "기억을 찾을 때 쓰는 Voyage 사용료(정가)예요. 모델마다 처음 2억 토큰은 무료이고, 넘은 만큼만 청구돼요. 막대는 가장 많이 쓴 모델의 무료 토큰을 쓴 비율이에요.",
      facts: [
        `이번 달 ${usd(v.month.usd)} 정가 · ${v.month.calls.toLocaleString()}회`,
        ...v.models.map((m) => `${m.model} ${tokens(m.tokens)} 토큰`),
      ],
      detail: <Days label="최근 7일 검색 API 사용료" days={v.days.map((d) => ({ day: d.day, usd: d.usd, note: `${tokens(d.tokens)} 토큰` }))} />,
    });
  }
  const mind = u.mind;
  if (mind) {
    const p = capPct(mind);
    rows.push({
      id: "usage.mind",
      name: "판단 API",
      state: p != null && p >= 100 ? "warn" : "ok",
      figure: `${usd(mind.todayUsd)} / ${usd(mind.dailyUsd)}`,
      meter: p,
      summary: `이번 달 ${usd(mind.month.usd)} · ${mind.month.calls.toLocaleString()}번`,
      rule: `대화 해석·상식 판정·밤 성찰·답장 초안에 쓰는 ${mind.model}의 사용료예요. 하루 상한에 닿으면 그날은 멈춰요.`,
      facts: mind.purposes.map((x) => `${PURPOSE[x.purpose] ?? x.purpose} ${x.calls}번 · ${usd(x.usd)}`),
    });
  }
  return rows;
}

function Headline({ results, onPick }: { results: Line[]; onPick: (id: string) => void }) {
  const top = roots(results as CheckResult[]);
  const fails = top.filter((r) => r.state === "fail").length;
  const warns = top.length - fails;
  // Not read yet - a window just started over, a check not heard from - is
  // not "not measured": that is only what has no key to measure with.
  const off = results.filter((r) => r.state === "unknown" && r.off).length;
  const unread = results.filter((r) => r.state === "unknown").length - off;

  let sentence = "모두 정상이에요";
  if (fails > 0) sentence = `${fails}개가 이상해요`;
  else if (warns > 0) sentence = `${warns}개를 지켜보고 있어요`;
  const extra = fails > 0 && warns > 0 ? `주의 ${warns}개도 있어요.` : null;

  return (
    <div data-focus="problems">
      <p className="text-display leading-[1.25] font-semibold tracking-tight text-foreground">
        {sentence}
      </p>
      {top.length > 0 ? (
        <ul className="mt-4 space-y-2.5">
          {top.map((r) => (
            <li key={r.id}>
              <button
                type="button"
                onClick={() => onPick(r.id)}
                className="group -mx-1 flex w-full items-start gap-3 rounded-md px-1 py-0.5 text-left focus-ring"
              >
                <Mark state={r.state} className="mt-[7px]" />
                <span className="min-w-0 text-body leading-[1.55] text-pretty">
                  <span className="text-foreground">{r.name}</span>
                  <span className="text-dim"> {r.summary}</span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-2 text-ui text-dim">
          {results.length}개 항목을 확인했어요.
          {unread > 0 ? ` ${unread}개는 아직 읽지 못했어요.` : ""}
          {off > 0 ? ` ${off}개는 측정하지 않아요.` : ""}
        </p>
      )}
      {extra ? <p className="mt-2 text-aside text-faint">{extra}</p> : null}
    </div>
  );
}

/**
 * The screen's place while the report is read: the headline, the line under
 * it and the groups' lines, as still blocks of their size.
 */
function Placeholder() {
  return (
    <div>
      <p role="status" className="sr-only">
        {SAY.loading}
      </p>
      <div aria-hidden>
        <div className="h-8 w-44 rounded-lg bg-well" />
        <div className="mt-3 h-4 w-56 rounded bg-well" />
        <div className="mt-7">
          {["w-28", "w-20", "w-24", "w-16", "w-24"].map((w, i) => (
            <div key={i} className="flex h-14 items-center">
              <div className={cn("h-4 rounded bg-well", w)} />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

/** How a group stands: its worst mark, the tally for the right of its line, and what is wrong by name. */
function standing(rows: Line[]) {
  const count = (state: CheckState) => rows.filter((r) => r.state === state).length;
  const [fail, warn, unknown] = [count("fail"), count("warn"), count("unknown")];
  const off = rows.filter((r) => r.state === "unknown" && r.off).length;
  // "모두 정상" only when every row was measured and is fine.
  const tally =
    fail || warn
      ? [fail && `이상 ${fail}`, warn && `주의 ${warn}`].filter(Boolean).join(" · ")
      : unknown
        ? [unknown > off && `읽지 못함 ${unknown - off}`, off && `측정 안 함 ${off}`].filter(Boolean).join(" · ")
        : "모두 정상";
  const worst: CheckState = fail ? "fail" : warn ? "warn" : unknown ? "unknown" : "ok";
  const trouble = rows.filter((r) => r.state === "fail" || r.state === "warn").map((r) => r.name).join(", ");
  return { worst, off: unknown > 0 && unknown === off, tally, trouble: trouble || undefined };
}

/**
 * A group's rows. Services that answer are folded into one line: nine
 * "응답해요" in a row is noise, and the one that stops answering unfolds
 * itself because it is no longer ok.
 */
function Rows({
  rows,
  open,
  aim,
  setOpen,
  onPick,
}: {
  rows: Line[];
  open: string | null;
  /** The check a notification named, not found on screen yet. */
  aim: string | null;
  setOpen: (id: string | null) => void;
  onPick: (id: string) => void;
}) {
  const [showServices, setShowServices] = useState(false);
  const healthyServices = rows.filter((r) => r.id.startsWith("svc.") && r.state === "ok");
  // Unfold for a notification that points into the fold - by what it named,
  // too: a service that answers again by the time it is tapped is folded,
  // and a folded row is not there for the notification to find and open.
  const wanted = [open, aim].some((id) => id && healthyServices.some((r) => r.id === id));
  const fold = healthyServices.length > 1 && !showServices && !wanted;
  const visible = fold ? rows.filter((r) => !healthyServices.includes(r)) : rows;

  return (
    <ul className="divide-y divide-edge-soft">
      {visible.map((r) => (
        <Row
          key={r.id}
          r={r}
          open={open === r.id}
          onToggle={() => setOpen(open === r.id ? null : r.id)}
          onPick={onPick}
        />
      ))}
      {fold ? (
        <li>
          <button
            type="button"
            onClick={() => setShowServices(true)}
            className="flex min-h-12 w-full items-center gap-3 py-3 text-left focus-ring"
          >
            <Mark state="ok" />
            <span className="min-w-0 flex-1 text-body text-ink">
              서비스 {healthyServices.length}개
              <span className="mt-0.5 block text-ui text-faint">모두 응답해요.</span>
            </span>
            <span className="shrink-0 text-aside text-faint">펼치기</span>
          </button>
        </li>
      ) : null}
    </ul>
  );
}

function Row({
  r,
  open,
  onToggle,
  onPick,
  children,
}: {
  r: Line;
  open: boolean;
  onToggle: () => void;
  onPick: (id: string) => void;
  /** What opening it shows, instead of a check's facts and rule. */
  children?: ReactNode;
}) {
  const trouble = r.state === "fail" || r.state === "warn";
  const figure = r.figure ?? (r.observedAt ? since(r.observedAt) : undefined);
  return (
    <li id={`check-${r.id}`} data-focus={`check:${r.id}`} className="scroll-mt-24">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex min-h-12 w-full items-start gap-3 py-3 text-left focus-ring"
      >
        <Mark state={r.state} off={r.off} className="mt-[7px]" />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline justify-between gap-3">
            <span className={cn("text-body", trouble ? "text-foreground" : "text-ink")}>{r.name}</span>
            {figure ? (
              <span className={cn("tnum shrink-0", r.figure ? "text-ui text-ink" : "text-meta text-faint")}>
                {/* An age's label rides along on wide screens; on a phone the
                    age alone fits beside the name. */}
                {!r.figure && r.observedLabel ? <span className="hidden sm:inline">{r.observedLabel} </span> : null}
                {figure}
              </span>
            ) : null}
          </span>
          <span className={cn("mt-0.5 block text-ui leading-[1.55] text-pretty", trouble ? "text-dim" : "text-faint")}>
            {r.summary}
          </span>
          {r.meter !== undefined ? (
            <Progress aria-hidden value={r.meter ?? 0} className="mt-2" indicatorClassName={tone(r.meter ?? null)} />
          ) : null}
          {r.cause && r.causeName ? (
            <span className="mt-1 block text-aside text-faint">
              {/* A span, not a nested button: the whole row is already one. */}
              <span
                role="link"
                tabIndex={0}
                onClick={(e) => {
                  e.stopPropagation();
                  onPick(r.cause!);
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.stopPropagation();
                    onPick(r.cause!);
                  }
                }}
                className="tap-y cursor-pointer text-dim underline decoration-edge underline-offset-[3px] hover:text-foreground focus-ring"
              >
                {r.causeName}
              </span>{" "}
              때문일 수 있어요
            </span>
          ) : null}
        </span>
        <ChevronDown aria-hidden className={cn("mt-1 size-4 shrink-0 text-faint transition-transform", open && "rotate-180")} />
      </button>

      {open ? (
        <div className="anim-settle mb-4 ms-[19px] space-y-3 border-s border-edge ps-4 text-aside leading-[1.6]">
          {children ?? (
            <>
              {r.facts && r.facts.length > 0 ? (
                <ul className="space-y-0.5 text-dim">
                  {r.facts.map((f) => (
                    <li key={f}>{f}</li>
                  ))}
                </ul>
              ) : null}
              {r.detail}
              {r.rule ? <Detail term="판정 기준">{r.rule}</Detail> : null}
              {r.fix && r.state !== "ok" ? <Detail term="조치">{r.fix}</Detail> : null}
              {r.id === "context.location" ? (
                <button
                  type="button"
                  onClick={() => onPick("phone")}
                  className="tap-y text-dim underline decoration-edge underline-offset-[3px] hover:text-foreground focus-ring"
                >
                  폰 연결 설정 열기
                </button>
              ) : null}
              {r.since && r.checkedAt ? (
                <p className="tnum text-faint">
                  이 상태로 {held(r.since)} · 확인 {since(r.checkedAt)}
                </p>
              ) : null}
            </>
          )}
        </div>
      ) : null}
    </li>
  );
}

function Detail({ term, children }: { term: string; children: ReactNode }) {
  return (
    <div>
      <p className="text-faint">{term}</p>
      <p className="text-pretty text-dim">{children}</p>
    </div>
  );
}

/**
 * A week of spend as bars, the day under each, and under them what the day
 * picked came to - today's, until another bar is pressed, as a day's bar is
 * pressed in 가계부. The figures had been in a hover title only, and a phone
 * has no hover: there it was seven heights and no amounts.
 */
function Days({ label, days }: { label: string; days: { day: string; usd: number; note: string }[] }) {
  const [picked, setPicked] = useState<string | null>(null);
  if (days.length === 0) return null;
  const top = Math.max(...days.map((d) => d.usd), 0.000001);
  const chosen = days.find((d) => d.day === picked) ?? days[days.length - 1];
  return (
    <div className="max-w-[20rem]">
      <ul role="group" aria-label={label} className="flex h-12">
        {days.map((d) => {
          const on = d === chosen;
          return (
            <li key={d.day} className="flex-1">
              {/* The whole column is the button, the bars kept apart inside it. */}
              <button
                type="button"
                aria-pressed={on}
                aria-label={`${monthDay(d.day)} ${usd(d.usd)} · ${d.note}`}
                onClick={() => setPicked(d.day)}
                className="flex h-full w-full flex-col items-center justify-end gap-1 rounded-sm px-[3px] focus-ring"
              >
                <span
                  className={cn("block w-full rounded-sm", on ? "bg-foreground/70" : "bg-foreground/45")}
                  style={{ height: `${Math.max(2, (d.usd / top) * 30)}px` }}
                />
                <span className={cn("tnum text-tiny", on ? "text-dim" : "text-faint")}>{d.day.slice(8)}</span>
              </button>
            </li>
          );
        })}
      </ul>
      <p className="tnum mt-1.5 text-meta text-faint">
        {monthDay(chosen.day)} {usd(chosen.usd)} · {chosen.note}
      </p>
    </div>
  );
}

const markLabel: Record<CheckState, string> = {
  ok: "정상",
  warn: "주의",
  fail: "이상",
  unknown: "읽지 못함",
};

/** One 7px box for every state, so the column of marks lines up. A row with no state (a setup) keeps the space. */
function Mark({ state, off, className }: { state?: CheckState; off?: boolean; className?: string }) {
  return (
    <span
      role={state ? "img" : undefined}
      aria-label={state ? (state === "unknown" && off ? "측정 안 함" : markLabel[state]) : undefined}
      className={cn("flex size-[7px] shrink-0 items-center justify-center", className)}
    >
      {state === "ok" ? (
        <span className="size-[6px] rounded-full bg-online" />
      ) : state === "fail" ? (
        <span className="size-[7px] rounded-full bg-reject" />
      ) : state === "warn" ? (
        <span className="anim-breathe size-[7px] rounded-full border border-dim" />
      ) : state === "unknown" ? (
        <span className="h-px w-[7px] bg-faint" />
      ) : null}
    </span>
  );
}
