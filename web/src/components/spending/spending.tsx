"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { ChevronLeft, ChevronRight, Plus, Search, X } from "lucide-react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { Categories } from "@/components/spending/categories";
import { Pace } from "@/components/spending/pace";
import {
  AddSheet,
  BudgetSheet,
  type Draft,
  TxSheet,
  UnparsedSheet,
} from "@/components/spending/sheets";
import { Transactions } from "@/components/spending/transactions";
import { krw } from "@/lib/portfolio";
import {
  type Book,
  type Tx,
  ago,
  clock,
  dayKey,
  dayLabel,
  monthLabel,
  send,
  shiftMonth,
} from "@/lib/spending";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

const thisMonth = () =>
  new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" }).slice(0, 7);

/**
 * The household book: what the cards were used for, read from the alerts
 * card companies send over KakaoTalk.
 *
 * Unlike 자산 this surface writes - categories, memos, budgets - because
 * those are bookkeeping, not money moving. Nothing here can spend anything.
 */
export function Spending() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

  const [month, setMonth] = useState(thisMonth);
  const [book, setBook] = useState<Book | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const [day, setDay] = useState<string | null>(null);
  const [category, setCategory] = useState<string | null>(null);
  const [query, setQuery] = useState("");

  const [open, setOpen] = useState<Tx | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [budgetOpen, setBudgetOpen] = useState(false);
  const [unparsedOpen, setUnparsedOpen] = useState(false);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(async (m: string) => {
    setLoading(true);
    try {
      const res = await fetch(`/api/spending/spending?month=${m}`, { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      setBook((await res.json()) as Book);
      setError(null);
    } catch {
      setError("가계부를 불러오지 못했어요. 잠시 뒤 다시 시도하세요.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    // load sets state only after its fetch resolves; the rule cannot see that.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load(month);
  }, [load, month]);

  // A charge made while the tab is open should appear without a pull: the
  // collector reads alerts within seconds, so a minute is the slow side.
  useEffect(() => {
    if (month !== thisMonth()) return;
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") void load(month);
    }, 60_000);
    return () => window.clearInterval(id);
  }, [load, month]);

  useEffect(() => {
    if (!note) return;
    const id = window.setTimeout(() => setNote(null), 3500);
    return () => window.clearTimeout(id);
  }, [note]);

  const done = (message: string) => {
    setOpen(null);
    setDraft(null);
    setBudgetOpen(false);
    setUnparsedOpen(false);
    setNote(message);
    void load(month);
  };

  const goMonth = (m: string) => {
    setMonth(m);
    setDay(null);
    setCategory(null);
  };

  const shown = useMemo(() => {
    if (!book) return [];
    const q = query.trim().toLowerCase();
    return book.transactions.filter(
      (t) =>
        (!day || dayKey(t.approvedAt) === day) &&
        (!category || t.category === category) &&
        (!q || t.merchant.toLowerCase().includes(q) || t.memo.toLowerCase().includes(q)),
    );
  }, [book, day, category, query]);
  const shownTotal = shown.reduce((sum, t) => sum + t.signedKrw, 0);
  const filtered = Boolean(day || category || query.trim());

  const current = month === thisMonth();
  const today = new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-4 pb-12 sm:px-8 sm:pt-7 sm:pb-8">
          <div className="-ms-2 mb-3 flex items-center gap-1">
            <button
              type="button"
              onClick={() => goMonth(shiftMonth(month, -1))}
              aria-label="지난달"
              className="tap flex items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <ChevronLeft aria-hidden className="size-4" />
            </button>
            <h2 className="min-w-14 text-center text-[13px] text-dim" aria-live="polite">
              {monthLabel(month, month.slice(0, 4) !== thisMonth().slice(0, 4))}
            </h2>
            <button
              type="button"
              onClick={() => goMonth(shiftMonth(month, 1))}
              disabled={current}
              aria-label="다음 달"
              className="tap flex items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30"
            >
              <ChevronRight aria-hidden className="size-4" />
            </button>
            {!current ? (
              <button
                type="button"
                onClick={() => goMonth(thisMonth())}
                className="tap ms-1 rounded-lg px-2 text-[12px] text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                이번 달
              </button>
            ) : null}
          </div>

          {error && !book ? (
            <p className="py-16 text-center text-[13px] text-dim">{error}</p>
          ) : null}

          {book ? (
            <div className={cn("transition-opacity", loading && "opacity-60")}>
              <Headline book={book} current={current} day={day} />
              <Status book={book} onOpenUnparsed={() => setUnparsedOpen(true)} onRetried={done} />

              <section className="mt-6" aria-label="이번 달 속도">
                <Pace
                  book={book}
                  selectedDay={day}
                  onSelectDay={setDay}
                  onEditBudget={() => setBudgetOpen(true)}
                />
              </section>

              {book.categories.length > 0 ? (
                <section className="mt-6 border-t border-edge-soft pt-5" aria-label="분류별">
                  <h2 className="mb-2 px-2 text-[13px] text-dim">어디에 썼나</h2>
                  <Categories book={book} selected={category} onSelect={setCategory} />
                </section>
              ) : null}

              {book.recurring.length > 0 ? (
                <section className="mt-6 border-t border-edge-soft pt-5" aria-label="정기 결제">
                  <div className="mb-2 flex items-baseline justify-between px-2">
                    <h2 className="text-[13px] text-dim">매달 나가는 돈</h2>
                    <span className="tnum text-[11.5px] text-faint">
                      월 {krw(book.recurring.reduce((s, r) => s + r.amountKrw, 0))}
                    </span>
                  </div>
                  <ul className="space-y-px">
                    {book.recurring.map((r) => (
                      <li key={r.merchant} className="flex items-center gap-3 px-2 py-2">
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-[13.5px] text-foreground/90">{r.merchant}</span>
                          <span className="block text-[11.5px] text-faint">
                            매달 {r.day}일 무렵 · {r.months}개월째
                          </span>
                        </span>
                        <span className="shrink-0 text-right">
                          <span className="tnum block text-[13.5px] text-foreground/90">{krw(r.amountKrw)}</span>
                          <span className="tnum block text-[11px] text-faint">
                            {r.nextAt.slice(5).replace("-", "/")} 예정
                          </span>
                        </span>
                      </li>
                    ))}
                  </ul>
                </section>
              ) : null}

              <section className="mt-6 border-t border-edge-soft pt-5" aria-label="내역">
                <div className="mb-3 flex items-center gap-2 px-2">
                  <h2 className="text-[13px] text-dim">내역</h2>
                  <label className="relative ms-auto min-w-0 flex-1 sm:max-w-56">
                    <span className="sr-only">사용처나 메모로 찾기</span>
                    <Search aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-faint" />
                    <input
                      type="search"
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                      placeholder="찾기"
                      className="h-10 w-full rounded-lg bg-glass ps-8 pe-2 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-8 sm:text-[12.5px]"
                    />
                  </label>
                  <button
                    type="button"
                    onClick={() =>
                      setDraft({ date: day ?? (current ? today : `${month}-01`), time: current ? clock(new Date().toISOString()) : "12:00" })
                    }
                    className="tap flex shrink-0 items-center gap-1 rounded-lg px-2 text-[12.5px] text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
                  >
                    <Plus aria-hidden className="size-3.5" />
                    직접 적기
                  </button>
                </div>

                {filtered ? (
                  <div className="mb-3 flex flex-wrap items-center gap-1.5 px-2 text-[12px]">
                    {day ? <Chip label={dayLabel(day)} onClear={() => setDay(null)} /> : null}
                    {category ? <Chip label={category} onClear={() => setCategory(null)} /> : null}
                    {query.trim() ? <Chip label={`“${query.trim()}”`} onClear={() => setQuery("")} /> : null}
                    <span className="tnum ms-auto text-faint">
                      {shown.length}건 · {krw(shownTotal)}
                    </span>
                  </div>
                ) : null}

                {shown.length > 0 ? (
                  <Transactions txs={shown} onOpen={setOpen} />
                ) : (
                  <p className="px-2 py-8 text-center text-[13px] text-faint">
                    {filtered
                      ? "조건에 맞는 내역이 없어요."
                      : current
                        ? "이번 달 카드 알림이 아직 없어요. 결제하면 몇 초 안에 여기 나타나요."
                        : "이 달에는 기록된 내역이 없어요."}
                  </p>
                )}
              </section>
            </div>
          ) : loading ? (
            <p className="py-16 text-center text-[13px] text-faint">불러오는 중</p>
          ) : null}
        </div>

        {note ? (
          <div className="pointer-events-none sticky bottom-3 flex justify-center px-4" role="status">
            <p className="rounded-full bg-popover px-4 py-2 text-[12.5px] text-foreground shadow-lg">{note}</p>
          </div>
        ) : null}
      </main>

      <TabBar pending={waiting.length} />

      {book ? (
        <>
          <TxSheet tx={open} names={book.categoryNames} onClose={() => setOpen(null)} onSaved={done} />
          <AddSheet draft={draft} names={book.categoryNames} onClose={() => setDraft(null)} onSaved={done} />
          <BudgetSheet book={book} open={budgetOpen} onClose={() => setBudgetOpen(false)} onSaved={done} />
          <UnparsedSheet
            items={book.unparsed}
            open={unparsedOpen}
            onClose={() => setUnparsedOpen(false)}
            onDismissed={done}
            onWrite={(u) => {
              setUnparsedOpen(false);
              setDraft({ date: dayKey(u.sentAt), time: clock(u.sentAt), from: u });
            }}
          />
        </>
      ) : null}
    </div>
  );
}

function Headline({ book, current, day }: { book: Book; current: boolean; day: string | null }) {
  // A day picked on the strip takes over the headline: its total is what
  // the person is now looking at.
  const picked = day ? book.daily.find((d) => d.date === day) : undefined;
  if (picked) {
    return (
      <div>
        <p className="tnum text-[34px] leading-none font-semibold tracking-tight text-foreground sm:text-[40px]">
          {krw(picked.totalKrw)}
        </p>
        <p className="mt-2 text-[13px] text-dim">
          {dayLabel(picked.date)}에 쓴 돈
          <span className="tnum text-faint"> · {picked.count}건</span>
        </p>
        <p className="tnum mt-0.5 text-[13px] text-faint">
          {current ? "이번 달" : monthLabel(book.month)} 전체 {krw(book.totalKrw)}
        </p>
      </div>
    );
  }
  // While the month runs, the fair comparison is last month up to the same
  // moment; a finished month compares whole with whole.
  const base = current ? book.prev.sameDayKrw : book.prev.totalKrw;
  const diff = book.totalKrw - base;
  const prevLabel = monthLabel(book.prev.month);
  let compare: string | null = null;
  if (base > 0) {
    const when = current ? `${prevLabel} 이맘때` : prevLabel;
    compare =
      Math.abs(diff) < 1000
        ? `${when}와 비슷해요`
        : `${when}보다 ${krw(Math.abs(diff))} ${diff > 0 ? "더" : "덜"} 썼어요`;
  }
  return (
    <div>
      <p className="tnum text-[34px] leading-none font-semibold tracking-tight text-foreground sm:text-[40px]">
        {krw(book.totalKrw)}
      </p>
      <p className="mt-2 text-[13px] text-dim">
        {current ? "이번 달 쓴 돈" : `${monthLabel(book.month)}에 쓴 돈`}
        <span className="tnum text-faint"> · {book.count}건</span>
      </p>
      {compare ? <p className="mt-0.5 text-[13px] text-dim">{compare}</p> : null}
    </div>
  );
}

/**
 * Whether alerts are arriving at all. The book can only be as complete as the
 * KakaoTalk app on the Mac is awake, and a quiet book looks the same as a
 * frugal week unless something says which one it is.
 */
function Status({
  book,
  onOpenUnparsed,
  onRetried,
}: {
  book: Book;
  onOpenUnparsed: () => void;
  onRetried: (note: string) => void;
}) {
  const c = book.collector;
  const trouble = Boolean(c.lastError) || c.stale;
  return (
    <div className="mt-3 space-y-1.5">
      <p className="flex items-start gap-1.5 text-[11.5px] leading-[1.5] text-faint">
        <span
          aria-hidden
          className={cn("mt-[6px] size-[5px] shrink-0 rounded-full", trouble ? "bg-faint" : "bg-online")}
        />
        {c.lastError
          ? "카톡 수집기에 연결되지 않아요. 그동안의 결제는 연결되면 들어와요."
          : c.stale
            ? `카톡이 ${c.latestMessageAt ? ago(c.latestMessageAt) : "한동안"} 이후로 조용해요. 맥에서 카카오톡이 켜져 있는지 확인하세요.`
            : c.lastAlertAt
              ? `마지막 카드 알림 ${ago(c.lastAlertAt)}`
              : "카드 알림을 기다리는 중"}
      </p>
      {book.enrichBlocked.map((site) => (
        <Blocked key={site.source} source={site.source} label={site.label} onRetried={onRetried} />
      ))}
      {book.unparsed.length > 0 ? (
        <button
          type="button"
          onClick={onOpenUnparsed}
          className="tap -mx-1 flex items-center gap-1.5 rounded-md px-1 text-left text-[12.5px] text-foreground/90 outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <span aria-hidden className="anim-breathe size-[5px] shrink-0 rounded-full bg-dim" />
          읽지 못한 카드 알림 {book.unparsed.length}건이 합계에서 빠져 있어요
          <span className="text-faint">확인</span>
        </button>
      ) : null}
    </div>
  );
}

/**
 * Jarvis reads marketplace orders in its own browser, and a site that has
 * logged it out stops that until someone signs in again there. Said where the
 * missing items would otherwise be a mystery, with the two steps to fix it.
 */
function Blocked({
  source,
  label,
  onRetried,
}: {
  source: string;
  label: string;
  onRetried: (note: string) => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const retry = async () => {
    const err = await send("enrich/retry", "POST", { source });
    if (err) setError(err);
    else onRetried(`${label} 주문을 다시 확인할게요`);
  };
  return (
    <div className="text-[12.5px]">
      <p className="flex items-start gap-1.5 leading-[1.5] text-foreground/90">
        <span aria-hidden className="anim-breathe mt-[7px] size-[5px] shrink-0 rounded-full bg-dim" />
        {label} 로그인이 풀려 산 상품을 못 읽고 있어요
      </p>
      <div className="-ms-1 flex items-center gap-1 ps-3">
        <Link
          href="/screen"
          className="tap flex items-center rounded-md px-1 text-dim underline decoration-edge underline-offset-4 outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          화면 탭에서 로그인
        </Link>
        <span aria-hidden className="text-faint">·</span>
        <button
          type="button"
          onClick={() => void retry()}
          className="tap flex items-center rounded-md px-1 text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          로그인했으면 다시 시도
        </button>
      </div>
      {error ? <p className="ps-3 text-reject">{error}</p> : null}
    </div>
  );
}

function Chip({ label, onClear }: { label: string; onClear: () => void }) {
  return (
    <button
      type="button"
      onClick={onClear}
      aria-label={`${label} 조건 지우기`}
      className="flex min-h-8 items-center gap-1 rounded-full bg-glass-raised ps-3 pe-2 text-foreground/90 outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {label}
      <X aria-hidden className="size-3 text-faint" />
    </button>
  );
}
