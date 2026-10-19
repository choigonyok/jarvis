"use client";

import { useCallback, useEffect, useState, type ReactNode } from "react";
import { ChevronDown, RotateCw } from "lucide-react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import {
  type CheckResult,
  type CheckState,
  type StatusReport,
  held,
  roots,
  since,
} from "@/lib/status";
import { onResume } from "@/lib/resume";
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
 * a warning, a faint dash for "not measured".
 */
export function Status() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const usage = useUsage();

  const [report, setReport] = useState<StatusReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const [, tick] = useState(0);

  const apply = useCallback((out: Fetched) => {
    if (out.report) {
      setReport(out.report);
      setError(null);
    } else {
      setError(out.error);
    }
  }, []);

  useEffect(() => {
    const load = () => void fetchReport("GET").then(apply);
    load();
    // Only while the tab is visible: a phone left on this screen in a pocket
    // should not keep polling.
    const id = setInterval(() => {
      if (document.visibilityState === "visible") load();
    }, 30_000);
    const clock = setInterval(() => tick((n) => n + 1), 30_000);
    // Back to the app, or a pull to refresh.
    const stop = onResume(() => fetchReport("GET").then(apply));
    return () => {
      clearInterval(id);
      clearInterval(clock);
      stop();
    };
  }, [apply]);

  // A notification about a check lands on its row (lib/focus.ts): open it
  // too, so what is wrong and what to do are on screen.
  useEffect(() => {
    const opened = (e: Event) => {
      const id = (e.target as HTMLElement).dataset.focus?.replace(/^check:/, "");
      if (id) setOpen(id);
    };
    document.addEventListener("jarvis:focused", opened);
    return () => document.removeEventListener("jarvis:focused", opened);
  }, []);

  async function refresh() {
    setRefreshing(true);
    apply(await fetchReport("POST"));
    setRefreshing(false);
  }

  function reveal(id: string) {
    setOpen(id);
    requestAnimationFrame(() =>
      document.getElementById(`check-${id}`)?.scrollIntoView({ block: "center", behavior: "smooth" }),
    );
  }

  const results = report?.results ?? [];
  const spend = usageRows(usage);
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
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      {/* inset-x-safe and px-4 both set padding-left, so they sit on
          different elements: the notch inset outside, the gutter inside. */}
      <main className="scrollbar-hairline inset-x-safe min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[46rem] px-4 pt-6 pb-10 sm:px-8 sm:pt-10">
          {!report ? (
            <p className="pt-10 text-center text-[13px] text-faint">
              {error ?? "상태를 불러오는 중이에요."}
            </p>
          ) : (
            <>
              <Headline results={[...results, ...spend]} onPick={reveal} />

              <div className="mt-3 flex items-center gap-3 text-[11.5px] text-faint">
                {lastChecked ? <span className="tnum">확인 {since(lastChecked)}</span> : null}
                <button
                  type="button"
                  onClick={() => void refresh()}
                  disabled={refreshing}
                  className="tap -my-3 flex items-center gap-1.5 rounded-md px-1 text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:text-faint"
                >
                  <RotateCw aria-hidden className={cn("size-3.5", refreshing && "anim-spin-slow")} />
                  {refreshing ? "확인하는 중" : "다시 확인"}
                </button>
                {error ? <span role="alert">{error}</span> : null}
              </div>

              {order.map((group) => {
                const rows = group === USAGE ? spend : results.filter((r) => r.group === group);
                if (rows.length === 0) return null;
                return (
                  <Group key={group} name={group} rows={rows}>
                    <Rows rows={rows} open={open} setOpen={setOpen} onPick={reveal} />
                  </Group>
                );
              })}

              <Group name="폰 연결">
                <ul>
                  <Row
                    r={{
                      id: "phone",
                      name: "위치·기기 상태 보내기",
                      summary: "아이폰이 위치(OwnTracks)와 기기 상태(단축어)를 jarvis로 보내도록 설정해요.",
                    }}
                    open={open === "phone"}
                    onToggle={() => setOpen(open === "phone" ? null : "phone")}
                    onPick={reveal}
                  >
                    <PhoneSetup />
                  </Row>
                </ul>
              </Group>

              <Group name="로그인 정보">
                <ul>
                  <Row
                    r={{
                      id: "logins",
                      name: "저장된 로그인",
                      summary: "자비스가 로그인 화면에 자동으로 넣는 사이트와, 넣은 기록이에요.",
                    }}
                    open={open === "logins"}
                    onToggle={() => setOpen(open === "logins" ? null : "logins")}
                    onPick={reveal}
                  >
                    {open === "logins" ? <SavedLogins /> : null}
                  </Row>
                </ul>
              </Group>
            </>
          )}
        </div>
      </main>

      <TabBar pending={waiting.length} />
    </div>
  );
}

type Fetched = { report: StatusReport; error?: undefined } | { report?: undefined; error: string };

/** POST makes the service look again before answering; GET reads what it has. */
async function fetchReport(method: "GET" | "POST"): Promise<Fetched> {
  try {
    const res = await fetch(method === "POST" ? "/api/status/refresh" : "/api/status/status", {
      method,
      cache: "no-store",
    });
    const body = await res.json();
    if (!res.ok) return { error: body.error ?? "상태를 불러오지 못했어요." };
    return { report: body as StatusReport };
  } catch {
    return { error: "상태를 불러오지 못했어요." };
  }
}

const USAGE = "사용량";

/**
 * A row, as every line on this screen is drawn. Checks come from status-svc;
 * the 사용량 rows are made here from the same numbers the header's bars read,
 * judged the way the bars warm: 90% of a window is a warning, a closed window
 * a fault.
 */
type Line = Pick<CheckResult, "id" | "name" | "summary"> &
  Partial<CheckResult> & {
    /** The figure right of the name when it is not an age: "43%", "$0.03 / $1.00". */
    figure?: string;
    /** 0-100: drawn as a thin bar under the summary. */
    meter?: number | null;
    /** What opening it shows, when it is not the check's facts and rule. */
    detail?: ReactNode;
  };

function windowRow(id: string, name: string, w: UsageWindow | undefined, status: string | undefined): Line {
  const p = pct(w);
  const state: CheckState = p == null ? "unknown" : p >= 100 || status === "rejected" ? "fail" : p >= 90 ? "warn" : "ok";
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
  if (!u) return [];
  const rows: Line[] = [
    windowRow("usage.five", "Claude 5시간", u.fiveHour, u.status),
    windowRow("usage.week", "Claude 주간", u.sevenDay, u.status),
  ];
  const api = u.api;
  if (api) {
    const p = capPct(api);
    rows.push({
      id: "usage.memory",
      name: "기억 API",
      state: !api.enabled ? "unknown" : p != null && p >= 100 ? "warn" : "ok",
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
  const unmeasured = results.filter((r) => r.state === "unknown").length;

  let sentence = "모두 정상이에요";
  if (fails > 0) sentence = `${fails}개가 이상해요`;
  else if (warns > 0) sentence = `${warns}개를 지켜보고 있어요`;
  const extra = fails > 0 && warns > 0 ? `주의 ${warns}개도 있어요.` : null;

  return (
    <div data-focus="problems">
      <p className="text-[26px] leading-[1.25] font-medium tracking-[-0.015em] text-foreground sm:text-[30px]">
        {sentence}
      </p>
      {top.length > 0 ? (
        <ul className="mt-4 space-y-2.5">
          {top.map((r) => (
            <li key={r.id}>
              <button
                type="button"
                onClick={() => onPick(r.id)}
                className="group -mx-1 flex w-full items-start gap-3 rounded-md px-1 py-0.5 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <Mark state={r.state} className="mt-[7px]" />
                <span className="min-w-0 text-[14.5px] leading-[1.55] text-pretty">
                  <span className="text-foreground">{r.name}</span>
                  <span className="text-dim"> {r.summary}</span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-2 text-[13.5px] text-dim">
          {results.length}개 항목을 확인했어요
          {unmeasured > 0 ? `. ${unmeasured}개는 측정하지 않아요.` : "."}
        </p>
      )}
      {extra ? <p className="mt-2 text-[12.5px] text-faint">{extra}</p> : null}
    </div>
  );
}

/** A group: its name, and on the right how its rows stand, so a glance down the page reads the tally. */
function Group({ name, rows, children }: { name: string; rows?: Line[]; children: ReactNode }) {
  const count = (state: CheckState) => rows?.filter((r) => r.state === state).length ?? 0;
  const [fail, warn, unknown] = [count("fail"), count("warn"), count("unknown")];
  // "모두 정상" only when every row was measured and is fine.
  const tally = !rows
    ? null
    : fail || warn
      ? [fail && `이상 ${fail}`, warn && `주의 ${warn}`].filter(Boolean).join(" · ")
      : unknown
        ? `측정 안 함 ${unknown}`
        : "모두 정상";
  const id = `group-${name}`;
  return (
    <section className="mt-9 sm:mt-11" aria-labelledby={id}>
      <div className="mb-1 flex items-baseline justify-between gap-3">
        <h2 id={id} className="text-[12px] text-faint">
          {name}
        </h2>
        {tally ? <span className={cn("text-[11.5px]", fail || warn ? "text-dim" : "text-faint")}>{tally}</span> : null}
      </div>
      {children}
    </section>
  );
}

/**
 * A group's rows. Services that answer are folded into one line: nine
 * "응답해요" in a row is noise, and the one that stops answering unfolds
 * itself because it is no longer ok.
 */
function Rows({
  rows,
  open,
  setOpen,
  onPick,
}: {
  rows: Line[];
  open: string | null;
  setOpen: (id: string | null) => void;
  onPick: (id: string) => void;
}) {
  const [showServices, setShowServices] = useState(false);
  const healthyServices = rows.filter((r) => r.id.startsWith("svc.") && r.state === "ok");
  // Unfold for a notification that points into the fold.
  const wanted = open && healthyServices.some((r) => r.id === open);
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
            className="flex min-h-12 w-full items-center gap-3 py-3 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <Mark state="ok" />
            <span className="min-w-0 flex-1 text-[14.5px] text-foreground/90">
              서비스 {healthyServices.length}개
              <span className="mt-0.5 block text-[13px] text-faint">모두 응답해요.</span>
            </span>
            <span className="shrink-0 text-[12px] text-faint">펼치기</span>
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
        className="flex min-h-12 w-full items-start gap-3 py-3 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <Mark state={r.state} className="mt-[7px]" />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline justify-between gap-3">
            <span className={cn("text-[14.5px]", trouble ? "text-foreground" : "text-foreground/90")}>{r.name}</span>
            {figure ? (
              <span className={cn("tnum shrink-0", r.figure ? "text-[13px] text-foreground/90" : "text-[11.5px] text-faint")}>
                {/* An age's label rides along on wide screens; on a phone the
                    age alone fits beside the name. */}
                {!r.figure && r.observedLabel ? <span className="hidden sm:inline">{r.observedLabel} </span> : null}
                {figure}
              </span>
            ) : null}
          </span>
          <span className={cn("mt-0.5 block text-[13px] leading-[1.55] text-pretty", trouble ? "text-dim" : "text-faint")}>
            {r.summary}
          </span>
          {r.meter !== undefined ? (
            <span aria-hidden className="mt-2 block h-1 overflow-hidden rounded-full bg-foreground/12">
              <span className={cn("block h-full rounded-full", tone(r.meter ?? null))} style={{ width: `${Math.min(100, r.meter ?? 0)}%` }} />
            </span>
          ) : null}
          {r.cause && r.causeName ? (
            <span className="mt-1 block text-[12.5px] text-faint">
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
                className="cursor-pointer text-dim underline decoration-edge underline-offset-[3px] outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
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
        <div className="anim-settle mb-4 ms-[19px] space-y-3 border-s border-edge ps-4 text-[12.5px] leading-[1.6]">
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
                  className="text-dim underline decoration-edge underline-offset-[3px] outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
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

/** A week of spend as bars, the day under each. */
function Days({ label, days }: { label: string; days: { day: string; usd: number; note: string }[] }) {
  if (days.length === 0) return null;
  const top = Math.max(...days.map((d) => d.usd), 0.000001);
  return (
    <ul className="flex h-12 max-w-[20rem] items-end gap-1.5" aria-label={label}>
      {days.map((d) => (
        <li key={d.day} className="flex flex-1 flex-col items-center gap-1" title={`${d.day} ${usd(d.usd)} · ${d.note}`}>
          <span className="block w-full rounded-sm bg-foreground/45" style={{ height: `${Math.max(2, (d.usd / top) * 30)}px` }} />
          <span className="tnum text-[10px] text-faint">{d.day.slice(8)}</span>
        </li>
      ))}
    </ul>
  );
}

const markLabel: Record<CheckState, string> = {
  ok: "정상",
  warn: "주의",
  fail: "이상",
  unknown: "측정 안 함",
};

/** One 7px box for every state, so the column of marks lines up. A row with no state (a setup) keeps the space. */
function Mark({ state, className }: { state?: CheckState; className?: string }) {
  return (
    <span
      role={state ? "img" : undefined}
      aria-label={state ? markLabel[state] : undefined}
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
