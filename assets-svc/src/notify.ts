import { enqueueEvents } from "./db.js";
import type { Portfolio } from "./types.js";

/**
 * What the portfolio has to say, sent to notify-svc after each build. Each
 * event carries a dedupe key, so saying the same thing on every build is
 * harmless: notify-svc keeps the first and drops the rest.
 *
 * All of it is "digest" - worth knowing today, none of it needing a hand
 * this minute - except nothing yet; notify-svc puts it in tonight's summary.
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

export function eventsOf(p: Portfolio, now = new Date()): Event[] {
  const today = now.toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
  const hour = Number(now.toLocaleString("en-US", { timeZone: "Asia/Seoul", hour: "2-digit", hour12: false }));
  const out: Event[] = [];
  const add = (e: Omit<Event, "source" | "url"> & { url?: string }) => out.push({ source: "assets", url: "/assets", ...e });

  for (const r of p.allocation.rows) {
    if (r.inBand) continue;
    const pp = Math.round((r.current - r.target) * 100);
    add({
      kind: "assets.band",
      tier: "digest",
      level: "warn",
      title: `${r.label} 비중이 범위를 벗어났어요`,
      body: `지금 ${Math.round(r.current * 100)}% · 목표 ${Math.round(r.target * 100)}% (${pp > 0 ? "+" : ""}${pp}%p)`,
      key: `assets:band:${r.id}:${today}`,
    });
  }

  // Deposits found from a KIS account's cash - worth a look, since nothing
  // but the arithmetic recorded them.
  for (const f of p.principal?.flows ?? []) {
    if (f.source !== "auto" || f.venue === "upbit" || f.date < today) continue;
    add({
      kind: "assets.flow",
      tier: "digest",
      title: `${f.venue === "gold" ? "금현물 계좌" : "한국투자증권"} ${f.amountKrw > 0 ? "입금" : "출금"} ${won(Math.abs(f.amountKrw))} 감지`,
      body: "현금 변화에서 결제된 매매를 뺀 금액이에요. 맞는지 확인해 주세요.",
      key: `assets:flow:${f.id}:${Math.round(f.amountKrw)}`,
    });
  }

  for (const c of p.principal?.checks ?? []) {
    add({
      kind: "assets.ledger",
      tier: "digest",
      level: "warn",
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
        title: "해외주식 양도세 공제를 넘었어요",
        body: `올해 실현손익 ${signed(overseas.gainKrw)} · 예상 양도세 ${won(overseas.taxKrw)}`,
        key: `assets:tax:over:${year}`,
      });
    } else if (overseas.gainKrw >= overseas.deductionKrw * 0.8) {
      add({
        kind: "assets.tax",
        tier: "digest",
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
      title: `총자산 ${won(p.totalKrw)} · 수익률 ${rate >= 0 ? "+" : ""}${rate.toFixed(1)}%`,
      body: change === null ? "" : `어제보다 ${signed(change)} (입출금 뺀 값)`,
      key: `assets:daily:${today}`,
    });
  }
  return out;
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
    void fetch(`${URL.replace(/\/$/, "")}/events`, {
      method: "POST",
      headers: { "content-type": "application/json", ...(TOKEN ? { authorization: `Bearer ${TOKEN}` } : {}) },
      body: JSON.stringify(e),
      signal: AbortSignal.timeout(5_000),
    }).catch((err: Error) => console.error("알림을 보내지 못했습니다:", e.kind, err.message));
  }
}
