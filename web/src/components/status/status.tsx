"use client";

import { useCallback, useEffect, useState } from "react";
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
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { type UsageWindow, pct, resetLabel, tone, useUsage } from "@/lib/use-usage";
import { cn } from "@/lib/utils";

/**
 * Is anything broken - answered per feature, not per container.
 *
 * The screen opens on one sentence, because the question that brings someone
 * here is yes-or-no. Below it the checks keep a fixed order, grouped by what
 * they are for, so a row is where it was last time; sorting faults to the top
 * would move rows under the thumb every time a state flipped. The headline is
 * where faults are lifted out.
 *
 * Faults that follow from another fault say so ("카톡 수집기 때문일 수 있어요")
 * instead of standing as independent problems. That is the one idea this
 * surface adds: KakaoTalk going quiet and card alerts going quiet are the
 * same event, and the screen should count it once.
 *
 * States reuse the product's existing marks rather than adding colours:
 * --online for ok, --reject for a fault, the breathing hollow ring (already
 * "waiting on you") for a warning, and a faint dash for "not measured".
 */
export function Status() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

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
    const onVisible = () => {
      if (document.visibilityState === "visible") load();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(id);
      clearInterval(clock);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [apply]);

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
  const lastChecked = results.reduce<string | null>(
    (latest, r) => (!latest || r.checkedAt > latest ? r.checkedAt : latest),
    null,
  );

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
              <Headline results={results} onPick={reveal} />

              <div className="mt-4 flex items-center gap-3 text-[11.5px] text-faint">
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

              <ClaudeUsage />

              {report.groups.map((group) => {
                const rows = results.filter((r) => r.group === group);
                if (rows.length === 0) return null;
                return (
                  <section key={group} className="mt-9 sm:mt-11" aria-labelledby={`group-${group}`}>
                    <h2 id={`group-${group}`} className="mb-1.5 text-[12px] text-faint">
                      {group}
                    </h2>
                    <Rows rows={rows} open={open} setOpen={setOpen} onPick={reveal} />
                  </section>
                );
              })}
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

function Headline({ results, onPick }: { results: CheckResult[]; onPick: (id: string) => void }) {
  const top = roots(results);
  const fails = top.filter((r) => r.state === "fail").length;
  const warns = top.length - fails;
  const unmeasured = results.filter((r) => r.state === "unknown").length;

  let sentence = "모두 정상이에요";
  if (fails > 0) sentence = `${fails}개가 이상해요`;
  else if (warns > 0) sentence = `${warns}개를 지켜보고 있어요`;
  const extra = fails > 0 && warns > 0 ? `주의 ${warns}개도 있어요.` : null;

  return (
    <div>
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
                className="group -mx-1 flex w-full items-start gap-2.5 rounded-md px-1 py-0.5 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
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

/**
 * A group's rows. In 기반, services that answer are folded into one line:
 * six "응답해요" in a row is noise, and the one that stops answering unfolds
 * itself because it is no longer ok.
 */
function Rows({
  rows,
  open,
  setOpen,
  onPick,
}: {
  rows: CheckResult[];
  open: string | null;
  setOpen: (id: string | null) => void;
  onPick: (id: string) => void;
}) {
  const [showServices, setShowServices] = useState(false);
  const healthyServices = rows.filter((r) => r.id.startsWith("svc.") && r.state === "ok");
  const fold = healthyServices.length > 1 && !showServices;
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
            className="flex min-h-12 w-full items-center gap-3 py-2.5 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <Mark state="ok" />
            <span className="text-[14px] text-dim">
              서비스 {healthyServices.length}개가 모두 응답해요
            </span>
            <span className="ms-auto text-[12px] text-faint">펼치기</span>
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
}: {
  r: CheckResult;
  open: boolean;
  onToggle: () => void;
  onPick: (id: string) => void;
}) {
  const trouble = r.state === "fail" || r.state === "warn";
  return (
    <li id={`check-${r.id}`} className="scroll-mt-24">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex min-h-12 w-full items-start gap-3 py-3 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <Mark state={r.state} className="mt-[7px]" />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline justify-between gap-3">
            <span className={cn("text-[14.5px]", trouble ? "text-foreground" : "text-foreground/90")}>
              {r.name}
            </span>
            {r.observedAt ? (
              <span className="tnum shrink-0 text-[11.5px] text-faint">
                {/* The label rides along on wide screens; on a phone the
                    age alone fits beside the name. */}
                <span className="hidden sm:inline">{r.observedLabel} </span>
                {since(r.observedAt)}
              </span>
            ) : null}
          </span>
          <span
            className={cn(
              "mt-0.5 block text-[13px] leading-[1.55] text-pretty",
              trouble ? "text-dim" : "text-faint",
            )}
          >
            {r.summary}
          </span>
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
        <ChevronDown
          aria-hidden
          className={cn("mt-1 size-4 shrink-0 text-faint transition-transform", open && "rotate-180")}
        />
      </button>

      {open ? (
        <div className="anim-settle mb-4 ms-[17px] space-y-3 border-s border-edge ps-4 text-[12.5px] leading-[1.6]">
          {r.facts && r.facts.length > 0 ? (
            <ul className="space-y-0.5 text-dim">
              {r.facts.map((f) => (
                <li key={f}>{f}</li>
              ))}
            </ul>
          ) : null}
          <Detail term="판정 기준">{r.rule}</Detail>
          {r.fix && r.state !== "ok" ? <Detail term="조치">{r.fix}</Detail> : null}
          <p className="tnum text-faint">
            이 상태로 {held(r.since)} · 확인 {since(r.checkedAt)}
          </p>
        </div>
      ) : null}
    </li>
  );
}

function Detail({ term, children }: { term: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="text-faint">{term}</p>
      <p className="text-pretty text-dim">{children}</p>
    </div>
  );
}

const markLabel: Record<CheckState, string> = {
  ok: "정상",
  warn: "주의",
  fail: "이상",
  unknown: "측정 안 함",
};

function Mark({ state, className }: { state: CheckState; className?: string }) {
  return (
    <span role="img" aria-label={markLabel[state]} className={cn("flex size-[5px] shrink-0 items-center justify-center", className)}>
      {state === "ok" ? (
        <span className="size-[5px] rounded-full bg-online" />
      ) : state === "fail" ? (
        <span className="size-[7px] rounded-full bg-reject" />
      ) : state === "warn" ? (
        <span className="anim-breathe size-[7px] rounded-full border border-dim" />
      ) : (
        <span className="h-px w-[7px] bg-faint" />
      )}
    </span>
  );
}

/** The Claude subscription: how much of the 5-hour and weekly windows is gone. */
function ClaudeUsage() {
  const usage = useUsage();
  return (
    <section className="mt-9 sm:mt-11" aria-labelledby="group-claude">
      <h2 id="group-claude" className="mb-2.5 text-[12px] text-faint">
        Claude 사용량
      </h2>
      {usage?.fiveHour || usage?.sevenDay ? (
        <div className="grid gap-3 sm:grid-cols-2">
          <UsageCard name="5시간" w={usage.fiveHour} />
          <UsageCard name="주간" w={usage.sevenDay} />
        </div>
      ) : (
        <p className="text-[13px] text-dim">아직 읽지 못했습니다. 대화나 작업이 한 번 돌면 채워집니다.</p>
      )}
      {usage?.at ? (
        <p className="tnum mt-2 text-[11.5px] text-faint">
          {new Date(usage.at).toLocaleString("ko-KR", { timeZone: "Asia/Seoul", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false })}{" "}
          기준{usage.status && usage.status !== "allowed" ? ` · ${usage.status === "rejected" ? "한도에 걸림" : "한도에 가까움"}` : ""}
        </p>
      ) : null}
    </section>
  );
}

function UsageCard({ name, w }: { name: string; w?: UsageWindow }) {
  const p = pct(w);
  return (
    <div className="rounded-xl bg-glass px-4 py-3.5">
      <div className="flex items-baseline justify-between">
        <span className="text-[13px] text-dim">{name}</span>
        <span className="tnum text-[20px] font-medium text-foreground">
          {p == null ? "-" : p}
          <span className="ms-0.5 text-[13px] text-dim">%</span>
        </span>
      </div>
      <span aria-hidden className="mt-2.5 block h-1.5 overflow-hidden rounded-full bg-foreground/12">
        <span className={cn("block h-full rounded-full", tone(p))} style={{ width: `${p ?? 0}%` }} />
      </span>
      <p className="tnum mt-2 text-[11.5px] text-faint">
        {p == null ? "" : `남은 ${100 - p}%`}
        {w?.resetsAt ? ` · ${resetLabel(w.resetsAt)} 초기화` : ""}
      </p>
    </div>
  );
}
