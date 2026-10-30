import { asRecord, enqueueEvents } from "./db.js";
import type { Portfolio } from "./types.js";

/**
 * What the portfolio has to say, sent to output-svc after each build. Each
 * event carries a dedupe key, so saying the same thing on every build is
 * harmless: output-svc keeps the first and drops the rest.
 *
 * All of it is "digest" - worth knowing today, none of it needing a hand
 * this minute - except nothing yet; output-svc puts it in tonight's summary.
 */

const URL = process.env.NOTIFY_URL?.trim() ?? "";
const TOKEN = process.env.API_TOKEN ?? "";

type Event = {
  source: "assets";
  kind: string;
  tier: "now" | "digest" | "log";
  level?: "info" | "warn" | "alert";
  title: string;
  body?: string;
  url?: string;
  key: string;
};

const won = (v: number) => `${v < 0 ? "−" : ""}₩${Math.round(Math.abs(v)).toLocaleString("ko-KR")}`;
const signed = (v: number) => `${v > 0 ? "+" : ""}${won(v)}`;

/**
 * Whether an account the allocation rests on could not be read. A bucket's
 * share is only known with every account in; with one missing, every share
 * is wrong and a plan made from them would sell what is there to buy what is
 * only unread.
 */
export function halfRead(problems: readonly string[] | undefined): boolean {
  return (problems ?? []).some((m) => m.startsWith("업비트:") || m.startsWith("한국투자증권:") || m.startsWith("금현물:"));
}

export function eventsOf(p: Portfolio, now = new Date()): Event[] {
  const today = now.toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
  const yesterday = new Date(Date.parse(`${today}T00:00:00+09:00`) - 86_400_000).toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
  const hour = Number(now.toLocaleString("en-US", { timeZone: "Asia/Seoul", hour: "2-digit", hour12: false }));
  const out: Event[] = [];
  const add = (e: Omit<Event, "source" | "url"> & { url?: string }) => out.push({ source: "assets", url: "/assets", ...e });
  // A tap lands on the part of the 자산 screen the notice is about (?focus=,
  // web/src/lib/focus.ts).
  const at = (focus: string) => `/assets?focus=${encodeURIComponent(focus)}`;

  // Out of band: kept in the inbox only. mind-svc reads the event and brings
  // the rebalance up in the evening at home, after work (its moments) - a
  // line in the evening's summary as well would say it twice. Not from a
  // portfolio half read: with a broker timed out, "유망주 0%" is a gap, not
  // a bucket out of band (2026-10-24~27: three such days).
  for (const r of halfRead(p.problems) ? [] : p.allocation.rows) {
    if (r.inBand) continue;
    const pp = Math.round((r.current - r.target) * 100);
    add({
      kind: "assets.band",
      tier: "log",
      level: "warn",
      url: at(`allocation:${r.id}`),
      title: `${r.label} 비중이 범위를 벗어났어요`,
      body: `지금 ${Math.round(r.current * 100)}% · 목표 ${Math.round(r.target * 100)}% (${pp > 0 ? "+" : ""}${pp}%p)`,
      key: `assets:band:${r.id}:${today}`,
    });
  }

  // Deposits found from a KIS account's cash - worth a look, since nothing
  // but the arithmetic recorded them. Told the day after they were found:
  // a trade the broker books a day apart from the cash reads as money out
  // one day and back the next, and the next day's look drops such a pair
  // (kis-flows.ts). An alert at the first look would have gone out already.
  for (const f of p.principal?.flows ?? []) {
    if (f.source !== "auto" || f.venue === "upbit" || f.date !== yesterday) continue;
    add({
      kind: "assets.flow",
      tier: "digest",
      url: at("principal"),
      title: `${f.venue === "gold" ? "금현물 계좌" : "한국투자증권"} ${f.amountKrw > 0 ? "입금" : "출금"} ${won(Math.abs(f.amountKrw))} 감지`,
      body: "현금 변화에서 체결된 매매를 뺀 금액이에요. 맞는지 확인해 주세요.",
      key: `assets:flow:${f.id}:${Math.round(f.amountKrw)}`,
    });
  }

  for (const c of p.principal?.checks ?? []) {
    add({
      kind: "assets.ledger",
      tier: "digest",
      level: "warn",
      url: at("principal"),
      title: "원금 기록이 매매와 맞지 않아요",
      body: c.message,
      key: `assets:check:${c.venue}:${c.kind}:${c.date ?? ""}:${Math.round(c.amountKrw / 1000)}`,
    });
  }

  const overseas = p.realized?.tax.baskets.find((b) => b.kind === "overseas" && b.inForce);
  if (overseas) {
    const year = p.realized!.tax.year;
    if (overseas.taxKrw > 0) {
      add({
        kind: "assets.tax",
        tier: "digest",
        level: "warn",
        url: at("tax"),
        title: "해외주식 양도세 공제를 넘었어요",
        body: `올해 실현손익 ${signed(overseas.gainKrw)} · 예상 양도세 ${won(overseas.taxKrw)}`,
        key: `assets:tax:over:${year}`,
      });
    } else if (overseas.gainKrw >= overseas.deductionKrw * 0.8) {
      add({
        kind: "assets.tax",
        tier: "digest",
        url: at("tax"),
        title: "해외주식 공제가 얼마 안 남았어요",
        body: `남은 공제 ${won(overseas.deductionKrw - overseas.gainKrw)} - 더 팔면 양도세가 붙어요`,
        key: `assets:tax:80:${year}`,
      });
    }
  }

  // The day's total, between eight and nine so it rides tonight's digest.
  if (hour === 20 && p.principal) {
    const prev = p.track.filter((t) => t.date < today).at(-1);
    const rate = p.principal.rate * 100;
    const change = prev ? p.totalKrw - prev.totalKrw - (p.principal.principalKrw - prev.principalKrw) : null;
    add({
      kind: "assets.daily",
      tier: "digest",
      url: at("summary"),
      title: `총자산 ${won(p.totalKrw)} · 수익률 ${rate >= 0 ? "+" : ""}${rate.toFixed(1)}%`,
      body: change === null ? "" : `어제보다 ${signed(change)} (입출금 뺀 값)`,
      key: `assets:daily:${today}`,
    });
  }
  return out;
}

/** Sends one event of this service's own (an order filled); never throws. */
export function tell(e: Omit<Event, "source">): void {
  const event: Event = { source: "assets", ...e };
  void enqueueEvents([event]).then((queued) => {
    if (!queued) post([event]);
  });
}

/** Sends the portfolio's events; never throws, never waits on the caller. */
export function announce(p: Portfolio): void {
  const events = eventsOf(p);
  void enqueueEvents(events).then((queued) => {
    if (!queued) post(events);
  });
}

function post(events: Event[]): void {
  if (!URL) return;
  for (const e of events) {
    void fetch(`${URL.replace(/\/$/, "")}/records`, {
      method: "POST",
      headers: { "content-type": "application/json", ...(TOKEN ? { authorization: `Bearer ${TOKEN}` } : {}) },
      body: JSON.stringify(asRecord(e)),
      signal: AbortSignal.timeout(5_000),
    }).catch((err: Error) => console.error("알림을 보내지 못했습니다:", e.kind, err.message));
  }
}
