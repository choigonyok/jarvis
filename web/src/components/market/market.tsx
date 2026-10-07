"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Eye, Heart, MessageCircle, RefreshCw } from "lucide-react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { ListingSheet } from "@/components/market/listing-sheet";
import { krw } from "@/lib/portfolio";
import {
  type Listing,
  type ListingStatus,
  type Market as MarketData,
  STATUS_LABEL,
  STATUS_TONE,
  send,
  taskLabel,
} from "@/lib/market";
import { ago } from "@/lib/spending";
import { isPending } from "@/lib/thread";
import { photoUrl } from "@/lib/uploads";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

type Filter = "all" | "active" | "reserved" | "sold";

const FILTERS: { key: Filter; label: string }[] = [
  { key: "all", label: "전체" },
  { key: "active", label: "판매중" },
  { key: "reserved", label: "예약중" },
  { key: "sold", label: "판매완료" },
];

/** What needs a look first, then what is live, then what is over. */
const ORDER: Record<ListingStatus, number> = {
  failed: 0,
  queued: 1,
  active: 2,
  reserved: 3,
  sold: 4,
  deleted: 5,
};

/**
 * The operator's Joongna listings: what is for sale, how it is doing, and the
 * changes on their way there. Nothing here talks to Joongna directly - a
 * change is queued, and the agent carries it over in the background with the
 * shared browser, which is why every action reads "…하는 중" until it lands.
 */
export function Market() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

  const [data, setData] = useState<MarketData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [openId, setOpenId] = useState<number | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await fetch("/api/market/listings", { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      setData((await res.json()) as MarketData);
      setError(null);
    } catch {
      setError("중고나라 현황을 불러오지 못했습니다. 잠시 뒤 다시 시도하세요.");
    }
  }, []);

  useEffect(() => {
    // load sets state only after its fetch resolves; the rule cannot see that.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
  }, [load]);

  // While a change is on its way the tab checks often, so "…하는 중" turns
  // into the result without a pull; otherwise once a minute is plenty.
  const busy = data?.listings.some((l) => l.tasks.some((t) => t.state === "pending")) ?? false;
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, busy ? 10_000 : 60_000);
    return () => window.clearInterval(id);
  }, [load, busy]);

  useEffect(() => {
    if (!note) return;
    const id = window.setTimeout(() => setNote(null), 3500);
    return () => window.clearTimeout(id);
  }, [note]);

  const listings = useMemo(() => data?.listings ?? [], [data]);
  const counts = useMemo(() => {
    const c: Record<Filter, number> = { all: 0, active: 0, reserved: 0, sold: 0 };
    for (const l of listings) {
      if (l.status === "deleted") continue;
      c.all++;
      if (l.status === "active" || l.status === "reserved" || l.status === "sold") c[l.status]++;
    }
    return c;
  }, [listings]);
  const shown = useMemo(
    () =>
      listings
        .filter((l) => (filter === "all" ? l.status !== "deleted" : l.status === filter))
        .sort((a, b) => ORDER[a.status] - ORDER[b.status]),
    [listings, filter],
  );
  const open = listings.find((l) => l.id === openId) ?? null;

  const act = async (path: string, method: "POST" | "DELETE", body: unknown, done: string) => {
    const err = await send(path, method, body);
    setNote(err ?? done);
    await load();
    return err;
  };

  const state = data?.state;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-4 pb-12 sm:px-8 sm:pt-7 sm:pb-8">
          {error && !data ? <p className="py-16 text-center text-[13px] text-dim">{error}</p> : null}

          {state?.loginRequired ? (
            <section
              role="alert"
              className="mb-5 rounded-xl border border-reject/25 bg-reject/[0.06] px-4 py-3.5"
            >
              <p className="text-[13.5px] text-foreground/90">중고나라 로그인이 풀려 작업이 멈췄습니다.</p>
              <p className="mt-1 text-[12px] leading-relaxed text-dim">
                화면 탭에서 브라우저로 중고나라에 다시 로그인한 뒤, 아래 버튼을 누르면 멈춘 작업을 이어서 합니다.
                {state.loginNote ? ` (${state.loginNote})` : ""}
              </p>
              <div className="mt-3 flex gap-2">
                <Link
                  href="/screen"
                  className="tap flex min-h-10 items-center rounded-lg border border-edge px-3 text-[13px] text-foreground outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-8"
                >
                  화면 탭 열기
                </Link>
                <button
                  type="button"
                  onClick={() => void act("login/resolved", "POST", undefined, "작업을 이어서 합니다.")}
                  className="tap min-h-10 rounded-lg bg-glass-raised px-3 text-[13px] text-foreground outline-none hover:bg-foreground/15 focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-8"
                >
                  다시 로그인했어요
                </button>
              </div>
            </section>
          ) : null}

          {data ? (
            <>
              <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2">
                <div role="tablist" aria-label="상태로 거르기" className="-ms-1 flex gap-1">
                  {FILTERS.map((f) => {
                    const on = filter === f.key;
                    return (
                      <button
                        key={f.key}
                        type="button"
                        role="tab"
                        aria-selected={on}
                        onClick={() => setFilter(f.key)}
                        className={cn(
                          "tap flex min-h-9 items-center gap-1.5 rounded-lg px-2.5 text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-8 sm:text-[12.5px]",
                          on ? "bg-glass-raised text-foreground" : "text-dim hover:text-foreground",
                        )}
                      >
                        {f.label}
                        <span className={cn("tnum text-[11.5px]", on ? "text-dim" : "text-faint")}>{counts[f.key]}</span>
                      </button>
                    );
                  })}
                </div>
                <button
                  type="button"
                  onClick={() =>
                    void act("sync/request", "POST", undefined, "다음 차례에 중고나라에서 다시 확인합니다.")
                  }
                  disabled={state?.syncRequested || state?.loginRequired}
                  className="tap ms-auto flex min-h-9 items-center gap-1.5 rounded-lg px-2 text-[12px] text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-60 sm:min-h-8"
                >
                  <RefreshCw
                    aria-hidden
                    className={cn("size-3.5", state?.syncRequested && "animate-spin motion-reduce:animate-none")}
                  />
                  {state?.syncRequested
                    ? "확인 대기 중"
                    : state?.lastSyncAt
                      ? `${ago(state.lastSyncAt)} 확인`
                      : "지금 확인"}
                </button>
              </div>

              {shown.length > 0 ? (
                <ul className="space-y-1">
                  {shown.map((l) => (
                    <li key={l.id}>
                      <Row listing={l} onOpen={() => setOpenId(l.id)} />
                    </li>
                  ))}
                </ul>
              ) : counts.all === 0 ? (
                <div className="py-14 text-center">
                  <p className="text-[14px] text-foreground/90">아직 올린 글이 없습니다.</p>
                  <p className="mx-auto mt-2 max-w-[22rem] text-[12.5px] leading-relaxed text-dim">
                    대화에서 팔 물건 사진을 첨부하고 &ldquo;중고나라에 올려줘&rdquo;라고 하면, 시세를 보고 판매 글 초안을
                    만들어 승인 카드로 올립니다.
                  </p>
                  <Link
                    href="/"
                    className="tap mt-4 inline-flex min-h-10 items-center rounded-lg border border-edge px-4 text-[13px] text-foreground outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
                  >
                    대화로 가기
                  </Link>
                </div>
              ) : (
                <p className="py-12 text-center text-[13px] text-dim">
                  {STATUS_LABEL[filter as ListingStatus]}인 글이 없습니다.
                </p>
              )}
            </>
          ) : null}
        </div>
      </main>

      {note ? (
        <p
          role="status"
          className="pointer-events-none fixed inset-x-0 bottom-[calc(var(--tabbar-space,0px)+1rem)] z-40 mx-auto w-fit max-w-[90vw] rounded-full border border-edge bg-background/90 px-4 py-2 text-[12.5px] text-foreground backdrop-blur-md sm:bottom-6"
        >
          {note}
        </p>
      ) : null}

      <ListingSheet
        listing={open}
        onOpenChange={(v) => !v && setOpenId(null)}
        onAct={act}
      />

      <TabBar pending={waiting.length} />
    </div>
  );
}

function Row({ listing: l, onOpen }: { listing: Listing; onOpen: () => void }) {
  const pending = l.tasks.find((t) => t.state === "pending");
  const failed = l.tasks.find((t) => t.state === "failed");
  const over = l.status === "sold" || l.status === "deleted";
  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex w-full items-center gap-3.5 rounded-xl px-2 py-2.5 text-left transition-colors outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <Thumb listing={l} />
      <span className="min-w-0 flex-1">
        <span className={cn("block truncate text-[14px]", over ? "text-dim" : "text-foreground/90")}>{l.title}</span>
        <span className="mt-0.5 flex items-baseline gap-2">
          <span className={cn("tnum text-[15.5px] font-medium", over ? "text-dim" : "text-foreground")}>
            {krw(l.priceKrw)}
          </span>
          <span className={cn("text-[12px]", STATUS_TONE[l.status])}>{STATUS_LABEL[l.status]}</span>
        </span>
        {pending ? (
          <span className="mt-1 flex items-center gap-1.5 text-[11.5px] text-dim">
            <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
            {taskLabel(pending)}
          </span>
        ) : failed ? (
          <span className="mt-1 block truncate text-[11.5px] text-reject">못 했습니다: {failed.note || taskLabel(failed)}</span>
        ) : l.joongnaId ? (
          <span className="tnum mt-1 flex items-center gap-3 text-[11.5px] text-faint">
            <Stat Icon={Eye} n={l.views} label="조회" />
            <Stat Icon={Heart} n={l.likes} label="찜" />
            <Stat Icon={MessageCircle} n={l.chats} label="채팅" />
          </span>
        ) : l.status === "failed" && l.note ? (
          <span className="mt-1 block truncate text-[11.5px] text-reject">{l.note}</span>
        ) : null}
      </span>
    </button>
  );
}

function Stat({ Icon, n, label }: { Icon: typeof Eye; n: number; label: string }) {
  return (
    <span className="flex items-center gap-1" aria-label={`${label} ${n}`}>
      <Icon aria-hidden className="size-3" />
      {n}
    </span>
  );
}

export function Thumb({ listing: l, size = "size-16" }: { listing: Listing; size?: string }) {
  const cover = l.photos[0];
  const over = l.status === "sold" || l.status === "deleted";
  return (
    <span className={cn("relative block shrink-0 overflow-hidden rounded-lg border border-edge-soft bg-well", size)}>
      {cover && !l.photosGone ? (
        // eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy
        <img
          src={photoUrl(cover)}
          alt=""
          loading="lazy"
          onError={(e) => (e.currentTarget.style.visibility = "hidden")}
          className={cn("size-full object-cover", over && "opacity-50 grayscale")}
        />
      ) : null}
      {l.status === "sold" ? (
        <span className="absolute inset-0 flex items-center justify-center bg-background/35 text-[11px] font-medium text-foreground/85">
          판매완료
        </span>
      ) : null}
    </span>
  );
}
