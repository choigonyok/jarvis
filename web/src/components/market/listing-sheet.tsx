"use client";

import { useState } from "react";
import { ExternalLink } from "lucide-react";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { krw } from "@/lib/portfolio";
import { type Listing, type MarketTask, STATUS_LABEL, STATUS_TONE, taskLabel } from "@/lib/market";
import { ago } from "@/lib/spending";
import { photoUrl } from "@/lib/uploads";
import { cn } from "@/lib/utils";

type Act = (path: string, method: "POST" | "DELETE", body: unknown, done: string) => Promise<string | null>;

const field =
  "h-11 w-full rounded-lg border border-edge-soft bg-glass px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-9 sm:text-[13px]";

const quiet =
  "tap min-h-11 rounded-lg border border-edge-soft px-3 text-[13.5px] text-dim transition-colors outline-none hover:bg-glass hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-40 sm:min-h-9 sm:text-[13px]";

/**
 * One listing: its photos, how it is doing, and what can be changed. Every
 * change is queued for the background worker, so each button says what it
 * will do on Joongna and the sheet shows it as waiting until it lands.
 */
export function ListingSheet({
  listing,
  onOpenChange,
  onAct,
}: {
  listing: Listing | null;
  onOpenChange: (open: boolean) => void;
  onAct: Act;
}) {
  return (
    <Sheet open={listing !== null} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        onOpenAutoFocus={(e) => e.preventDefault()}
        className="inset-x-safe pb-safe max-h-[90dvh] gap-0 border-edge-soft bg-background sm:mx-auto sm:max-w-lg sm:rounded-t-2xl sm:border-x"
      >
        {listing ? <Body key={listing.id} listing={listing} onAct={onAct} /> : null}
      </SheetContent>
    </Sheet>
  );
}

function Body({ listing: l, onAct }: { listing: Listing; onAct: Act }) {
  const [price, setPrice] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const pending = l.tasks.filter((t) => t.state === "pending");
  const failed = l.tasks.filter((t) => t.state === "failed");
  const posted = Boolean(l.joongnaId) && l.status !== "deleted";
  const waitingKind = (k: MarketTask["kind"]) => pending.some((t) => t.kind === k);
  const task = (body: unknown, done: string) => onAct(`listings/${l.id}/tasks`, "POST", body, done);
  const newPrice = Number(price.replace(/[^\d]/g, "")) || 0;

  return (
    <>
      <SheetHeader className="px-4 pt-4 pb-1 sm:px-5">
        <SheetTitle className="pr-8 text-[15px] leading-snug font-medium">{l.title}</SheetTitle>
        <SheetDescription className="flex items-baseline gap-2">
          <span className="tnum text-[18px] font-medium text-foreground">{krw(l.priceKrw)}</span>
          <span className={cn("text-[12.5px]", STATUS_TONE[l.status])}>{STATUS_LABEL[l.status]}</span>
        </SheetDescription>
      </SheetHeader>

      <div className="scrollbar-hairline min-h-0 overflow-y-auto px-4 pb-5 sm:px-5">
        {l.photos.length && !l.photosGone ? (
          <ul className="scrollbar-none -mx-4 mt-2 flex snap-x snap-mandatory scroll-px-4 gap-2 overflow-x-auto px-4 sm:-mx-5 sm:scroll-px-5 sm:px-5">
            {l.photos.map((name, i) => (
              <li key={name} className="shrink-0 snap-start">
                {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
                <img
                  src={photoUrl(name)}
                  alt={`사진 ${i + 1}`}
                  loading="lazy"
                  className="size-40 rounded-xl border border-edge-soft bg-well object-cover sm:size-44"
                />
              </li>
            ))}
          </ul>
        ) : l.photosGone ? (
          <p className="mt-2 text-[12px] text-faint">판매가 끝나고 30일이 지나 사진을 지웠습니다.</p>
        ) : null}

        <dl className="mt-4 grid grid-cols-3 gap-2 text-center">
          {[
            ["조회", l.views],
            ["찜", l.likes],
            ["채팅", l.chats],
          ].map(([label, n]) => (
            <div key={label} className="rounded-lg bg-glass py-2">
              <dt className="text-[11.5px] text-faint">{label}</dt>
              <dd className="tnum text-[16px] text-foreground/90">{n}</dd>
            </div>
          ))}
        </dl>

        <p className="mt-3 text-[12px] leading-relaxed text-faint">
          {l.postedAt ? `${ago(l.postedAt)} 올림` : `${ago(l.createdAt)} 승인`}
          {l.syncedAt ? ` · ${ago(l.syncedAt)} 확인` : ""}
          {l.note ? ` · ${l.note}` : ""}
        </p>
        {l.url ? (
          <a
            href={l.url}
            target="_blank"
            rel="noreferrer"
            className="tap mt-1 inline-flex items-center gap-1.5 rounded-lg text-[13px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <ExternalLink aria-hidden className="size-3.5" />
            중고나라에서 보기
          </a>
        ) : null}

        {pending.length > 0 || failed.length > 0 ? (
          <ul className="mt-4 space-y-1.5">
            {pending.map((t) => (
              <li key={t.id} className="flex items-center gap-2 rounded-lg bg-glass px-3 py-2.5 text-[13px] text-dim">
                <span aria-hidden className="anim-breathe size-[5px] shrink-0 rounded-full bg-dim" />
                <span className="min-w-0 flex-1">
                  {taskLabel(t)}
                  {t.attempts > 0 ? <span className="block text-[11.5px] text-faint">다시 시도 예정 · {t.note}</span> : null}
                </span>
                {t.attempts === 0 ? (
                  <button
                    type="button"
                    onClick={() =>
                      void onAct(`tasks/${t.id}`, "DELETE", undefined, t.kind === "post" ? "올리지 않기로 했습니다." : "취소했습니다.")
                    }
                    className="tap shrink-0 rounded-lg px-2 text-[12.5px] text-faint outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
                  >
                    취소
                  </button>
                ) : null}
              </li>
            ))}
            {failed.map((t) => (
              <li key={t.id} className="rounded-lg border border-reject/20 px-3 py-2.5 text-[12.5px] text-reject">
                {taskLabel(t).replace(/ 중$/, "")}지 못했습니다{t.note ? `: ${t.note}` : "."}
              </li>
            ))}
          </ul>
        ) : null}

        {l.status === "failed" && !waitingKind("post") ? (
          <button
            type="button"
            onClick={() => void task({ kind: "post" }, "다시 올립니다.")}
            className={cn(quiet, "mt-4 w-full text-foreground")}
          >
            다시 올리기
          </button>
        ) : null}

        {posted ? (
          <div className="mt-5 space-y-5 border-t border-edge-soft pt-5">
            {l.status !== "sold" ? (
              <section>
                <h3 className="mb-2 text-[12.5px] text-dim">가격 바꾸기</h3>
                <div className="flex gap-2">
                  <label className="relative flex-1">
                    <span className="sr-only">새 가격</span>
                    <input
                      inputMode="numeric"
                      value={newPrice ? newPrice.toLocaleString("ko-KR") : ""}
                      onChange={(e) => setPrice(e.target.value)}
                      placeholder={l.priceKrw.toLocaleString("ko-KR")}
                      className={cn(field, "tnum pe-8")}
                    />
                    <span aria-hidden className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-[13px] text-faint">
                      원
                    </span>
                  </label>
                  <button
                    type="button"
                    disabled={!newPrice || newPrice === l.priceKrw}
                    onClick={async () => {
                      const err = await task({ kind: "price", priceKrw: newPrice }, `${krw(newPrice)}으로 바꿉니다.`);
                      if (!err) setPrice("");
                    }}
                    className={cn(quiet, "shrink-0 text-foreground")}
                  >
                    바꾸기
                  </button>
                </div>
              </section>
            ) : null}

            <section>
              <h3 className="mb-2 text-[12.5px] text-dim">상태</h3>
              <div role="radiogroup" aria-label="판매 상태" className="grid grid-cols-3 gap-1.5">
                {(["active", "reserved", "sold"] as const).map((s) => {
                  const on = l.status === s;
                  const queued = pending.some((t) => t.kind === "status" && t.toStatus === s);
                  return (
                    <button
                      key={s}
                      type="button"
                      role="radio"
                      aria-checked={on}
                      disabled={on || queued}
                      onClick={() => void task({ kind: "status", toStatus: s }, `${STATUS_LABEL[s]}으로 바꿉니다.`)}
                      className={cn(
                        "min-h-11 rounded-lg border text-[13.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-9 sm:text-[13px]",
                        on ? "border-edge bg-glass-raised text-foreground" : "border-edge-soft text-dim hover:bg-glass",
                        queued && "anim-breathe",
                      )}
                    >
                      {STATUS_LABEL[s]}
                    </button>
                  );
                })}
              </div>
            </section>

            <div className="flex gap-2">
              {l.status === "active" ? (
                <button
                  type="button"
                  disabled={waitingKind("bump")}
                  onClick={() => void task({ kind: "bump" }, "끌어올립니다.")}
                  className={cn(quiet, "flex-1")}
                >
                  끌어올리기
                </button>
              ) : null}
              <button
                type="button"
                disabled={waitingKind("delete")}
                onClick={() => {
                  if (!confirmDelete) {
                    setConfirmDelete(true);
                    return;
                  }
                  setConfirmDelete(false);
                  void task({ kind: "delete" }, "중고나라에서 지웁니다.");
                }}
                onBlur={() => setConfirmDelete(false)}
                className={cn(
                  quiet,
                  "flex-1",
                  confirmDelete && "border-reject/40 bg-reject/10 text-reject hover:bg-reject/15 hover:text-reject",
                )}
              >
                {confirmDelete ? "한 번 더 누르면 삭제" : "글 삭제"}
              </button>
            </div>
          </div>
        ) : null}

        {l.description ? (
          <details className="mt-5 border-t border-edge-soft pt-4">
            <summary className="cursor-pointer text-[12.5px] text-dim outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
              올린 글 내용
            </summary>
            <p className="mt-2 text-[12px] text-faint">
              {[l.category, l.condition, l.shipping === "included" ? "택배비 포함" : "택배비 별도"].filter(Boolean).join(", ")}
            </p>
            <p className="mt-2 text-[13px] leading-relaxed whitespace-pre-wrap text-dim">{l.description}</p>
          </details>
        ) : null}
      </div>
    </>
  );
}
