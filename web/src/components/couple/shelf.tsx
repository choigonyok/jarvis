"use client";

import { useMemo, useState } from "react";
import { MessageCircle } from "lucide-react";
import { PhotoTiles, SaveAction } from "@/components/couple/photos";
import { BlockTitle, useViewOnly } from "@/components/couple/screen";
import { Lightbox } from "@/components/ui/lightbox";
import { photoSrc, type Block, type BudgetDate, type Space, type Who } from "@/lib/couple";
import { dateLabel, dayRange, monthDay } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * The blocks this console shows but leaves to the app to write: the 문화
 * 기록장, the 맛집 기록장, the 커플 일기장 and the 데이트 가계부. Everything
 * the two wrote is here to read; a new one is written in uniple, whose form
 * for them this console has not seen.
 */

export const LOG_KINDS: Record<string, { emoji: string; name: string }> = {
  movie: { emoji: "🎬", name: "영화" },
  drama: { emoji: "📺", name: "드라마" },
  variety: { emoji: "🎤", name: "예능" },
  book: { emoji: "📖", name: "책" },
  comic: { emoji: "📚", name: "만화" },
  exhibition: { emoji: "🖼️", name: "전시" },
  performance: { emoji: "🎭", name: "공연" },
  game: { emoji: "🎮", name: "게임" },
  sports: { emoji: "⚾", name: "스포츠" },
  escape_room: { emoji: "🗝️", name: "방탈출" },
  workshop: { emoji: "⚱️", name: "공방" },
};
export const logKind = (k: string) => LOG_KINDS[k] ?? { emoji: "🏷️", name: k };

export const MEALS: Record<string, { emoji: string; name: string }> = {
  dining_out: { emoji: "🍴", name: "음식점" },
  cafe: { emoji: "☕", name: "카페" },
  home_cooking: { emoji: "🍚", name: "요리" },
  bar: { emoji: "🍺", name: "술집" },
};
export const meal = (k?: string) => (k ? (MEALS[k] ?? { emoji: "🍴", name: k }) : { emoji: "🍴", name: "" });

const FOOD_TAGS: Record<string, string> = { mood: "분위기", revisit: "또갈집" };

export const SPENDS: Record<string, { emoji: string; name: string }> = {
  meal: { emoji: "🍽️", name: "식사" },
  cafe: { emoji: "☕", name: "카페/디저트" },
  alcohol: { emoji: "🍶", name: "술" },
  culture: { emoji: "🎬", name: "영화/공연" },
  activity: { emoji: "🎢", name: "액티비티" },
  transport: { emoji: "🚕", name: "교통" },
  stay: { emoji: "🏨", name: "숙박" },
  shopping: { emoji: "🛍️", name: "쇼핑" },
  gift: { emoji: "🎁", name: "선물" },
  etc: { emoji: "🧾", name: "기타" },
};

/** A spending category: one of the app's, or one the two made (config.custom_categories). */
export function spend(block: Block, key?: string): { emoji: string; name: string } {
  if (!key) return { emoji: "💞", name: "" };
  if (SPENDS[key]) return SPENDS[key];
  const custom = (block.config?.custom_categories as { name?: string; emoji?: string }[] | undefined)?.find((c) => c.name === key);
  return { emoji: custom?.emoji ?? "🏷️", name: key };
}

export const won = (n: number) => `${new Intl.NumberFormat("ko-KR").format(Math.round(n))}원`;

/** Where these are written: one line under the title. */
function WrittenInTheApp() {
  // The guest's screen says it once for every block.
  const viewOnly = useViewOnly();
  return viewOnly ? null : <p className="mt-1 text-aside text-faint">여기서는 보기만 해요. 쓰기와 고치기는 uniple 앱에서 해요.</p>;
}

function useViewer() {
  const [viewing, setViewing] = useState<{ list: string[]; at: number; title?: string } | null>(null);
  const viewer = viewing ? (
    <Lightbox
      items={viewing.list.map((a) => ({ src: photoSrc(a, "display"), title: viewing.title }))}
      start={viewing.at}
      onClose={() => setViewing(null)}
      actions={(i) => (viewing.list[i] ? <SaveAction asset={viewing.list[i]} /> : null)}
    />
  ) : null;
  return { open: setViewing, viewer };
}

function Stars({ rating }: { rating?: number }) {
  if (rating === undefined || rating === null) return null;
  return (
    <span className="text-aside text-dim tabular-nums" aria-label={`별점 ${rating}`}>
      ★ {Math.round(rating * 10) / 10}
    </span>
  );
}

function whoName(by: Who | undefined, space: Space): string {
  if (!by) return "";
  return by === "me" ? space.me.name || "나" : space.partner?.name || "상대";
}

/* ───────────────────────────── 문화 기록장 ───────────────────────────── */

export function LogPage({ block, space, onChanged }: { block: Block; space: Space; onChanged: () => Promise<void> }) {
  const entries = useMemo(() => block.log ?? [], [block.log]);
  const [kind, setKind] = useState<string>("all");
  const kinds = useMemo(() => [...new Set(entries.map((e) => e.category))], [entries]);
  const shown = kind === "all" ? entries : entries.filter((e) => e.category === kind);
  const { open, viewer } = useViewer();
  return (
    <div>
      <BlockTitle title={block.title} block={block} onRenamed={onChanged} />
      <WrittenInTheApp />
      {kinds.length > 1 ? (
        <div className="scrollbar-none -mx-1 mt-3 flex gap-1.5 overflow-x-auto px-1" role="group" aria-label="갈래">
          {["all", ...kinds].map((k) => (
            <button
              key={k}
              type="button"
              aria-pressed={kind === k}
              onClick={() => setKind(k)}
              className={cn(
                "tap-y h-9 shrink-0 rounded-full border px-3 text-aside transition-colors focus-ring",
                kind === k ? "border-edge bg-glass-raised text-foreground" : "border-edge-soft text-faint hover:text-dim",
              )}
            >
              {k === "all" ? `전체 ${entries.length}` : `${logKind(k).emoji} ${logKind(k).name} ${entries.filter((e) => e.category === k).length}`}
            </button>
          ))}
        </div>
      ) : null}
      {shown.length === 0 ? (
        <p className="mt-6 text-ui text-faint">아직 기록이 없어요.</p>
      ) : (
        <ul className="mt-3 divide-y divide-edge-soft">
          {shown.map((e) => (
            <li key={e.id} data-focus={`log:${e.id}`} className="flex gap-3 py-3">
              {e.cover ? (
                <button type="button" onClick={() => open({ list: e.photos?.length ? e.photos : [e.cover!], at: 0, title: e.title })} className="shrink-0 focus-ring rounded-lg">
                  {/* eslint-disable-next-line @next/next/no-img-element -- through the console's own proxy */}
                  <img src={photoSrc(e.cover)} alt="" loading="lazy" className="size-16 rounded-lg object-cover" />
                </button>
              ) : (
                <span aria-hidden className="flex size-16 shrink-0 items-center justify-center rounded-lg bg-glass-raised text-title">
                  {logKind(e.category).emoji}
                </span>
              )}
              <div className="min-w-0 flex-1">
                <p className="text-body break-keep text-foreground">{e.title || logKind(e.category).name}</p>
                <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-aside text-faint">
                  <span>
                    {logKind(e.category).emoji} {logKind(e.category).name}
                  </span>
                  {e.from ? <span>{e.to && e.to !== e.from ? dayRange(e.from, e.to) : monthDay(e.from)}</span> : null}
                  <Stars rating={e.rating} />
                  {e.by ? <span>{whoName(e.by, space)}</span> : null}
                </p>
                {e.note ? <p className="mt-1 line-clamp-3 text-ui break-keep whitespace-pre-line text-dim">{e.note}</p> : null}
              </div>
            </li>
          ))}
        </ul>
      )}
      {viewer}
    </div>
  );
}

/* ───────────────────────────── 맛집 기록장 ───────────────────────────── */

export function FoodPage({ block, space, onChanged }: { block: Block; space: Space; onChanged: () => Promise<void> }) {
  const entries = useMemo(() => block.food ?? [], [block.food]);
  const { open, viewer } = useViewer();
  return (
    <div>
      <BlockTitle title={block.title} block={block} onRenamed={onChanged} />
      <WrittenInTheApp />
      <p className="mt-3 text-ui text-dim">
        <span className="tabular-nums">{entries.length}</span>곳
      </p>
      {entries.length === 0 ? (
        <p className="mt-6 text-ui text-faint">아직 기록한 곳이 없어요.</p>
      ) : (
        <ul className="mt-2 divide-y divide-edge-soft">
          {entries.map((e) => {
            const m = meal(e.meal);
            const photos = e.photos?.length ? e.photos : e.cover ? [e.cover] : [];
            return (
              <li key={e.id} data-focus={`food:${e.id}`} className="py-3">
                <p className="text-body break-keep text-foreground">
                  <span aria-hidden>{m.emoji} </span>
                  {e.title || m.name || "이름 없는 곳"}
                </p>
                <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-aside text-faint">
                  {m.name ? <span>{m.name}</span> : null}
                  {e.day ? <span>{monthDay(e.day)}</span> : null}
                  <Stars rating={e.rating} />
                  {e.by ? <span>{whoName(e.by, space)}</span> : null}
                </p>
                {e.tags?.length ? (
                  <p className="mt-1 flex flex-wrap gap-1">
                    {e.tags.map((t) => (
                      <span key={t} className="rounded-full bg-glass-raised px-2 py-0.5 text-meta text-dim">
                        {FOOD_TAGS[t] ?? t}
                      </span>
                    ))}
                  </p>
                ) : null}
                {e.note ? <p className="mt-1 line-clamp-3 text-ui break-keep whitespace-pre-line text-dim">{e.note}</p> : null}
                {photos.length ? (
                  <PhotoTiles className="mt-1" size="sm" assets={photos.map((a) => ({ key: a, asset: a }))} onOpen={(i) => open({ list: photos, at: i, title: e.title })} />
                ) : null}
              </li>
            );
          })}
        </ul>
      )}
      {viewer}
    </div>
  );
}

/* ───────────────────────────── 커플 일기장 ───────────────────────────── */

export function DiaryPage({ block, space, onChanged }: { block: Block; space: Space; onChanged: () => Promise<void> }) {
  const entries = useMemo(() => block.diary ?? [], [block.diary]);
  const { open, viewer } = useViewer();
  return (
    <div>
      <BlockTitle title={block.title} block={block} onRenamed={onChanged} />
      <WrittenInTheApp />
      {entries.length === 0 ? (
        <p className="mt-6 text-ui text-faint">아직 쓴 일기가 없어요.</p>
      ) : (
        <ol className="mt-4 space-y-3">
          {entries.map((e) => (
            <li key={e.id} data-focus={`diary:${e.id}`} className="rounded-2xl bg-glass p-4">
              <p className="flex items-center gap-2 text-aside text-faint">
                <span aria-hidden className={cn("size-[7px] rounded-full", e.by === "me" ? "bg-mine" : "bg-partner")} />
                {whoName(e.by, space)} · {dateLabel(e.at.slice(0, 10))}
              </p>
              {e.title ? <h2 className="mt-1.5 text-title font-medium break-keep text-foreground">{e.title}</h2> : null}
              <p className="mt-1 text-body leading-relaxed break-keep whitespace-pre-line text-ink">{e.body}</p>
              {e.photos?.length ? (
                <PhotoTiles className="mt-2" size="sm" assets={e.photos.map((a) => ({ key: a, asset: a }))} onOpen={(i) => open({ list: e.photos!, at: i, title: e.title })} />
              ) : null}
              {e.replies?.length ? (
                <ul className="mt-3 space-y-2 border-t border-edge-soft pt-3">
                  {e.replies.map((r) => (
                    <li key={r.id} className="flex gap-2 text-ui">
                      <span aria-hidden className={cn("mt-[7px] size-[5px] shrink-0 rounded-full", r.by === "me" ? "bg-mine" : "bg-partner")} />
                      <span className="min-w-0 break-keep whitespace-pre-line text-dim">
                        <span className="text-ink">{whoName(r.by, space)}</span> {r.body}
                      </span>
                    </li>
                  ))}
                </ul>
              ) : e.comments ? (
                <p className="mt-2 flex items-center gap-1 text-aside text-faint">
                  <MessageCircle aria-hidden className="size-3.5" />
                  댓글 {e.comments}개 - uniple 앱에서 봐요
                </p>
              ) : null}
            </li>
          ))}
        </ol>
      )}
      {viewer}
    </div>
  );
}

/* ───────────────────────────── 데이트 가계부 ───────────────────────────── */

/** This month's outings, as the app's card sums them. */
export function monthTotal(dates: BudgetDate[], month: string): number {
  return dates.filter((d) => d.from.startsWith(month)).reduce((sum, d) => sum + d.total, 0);
}

export function BudgetPage({ block, space, onChanged }: { block: Block; space: Space; onChanged: () => Promise<void> }) {
  const dates = useMemo(() => block.budget ?? [], [block.budget]);
  const [open, setOpen] = useState<string | null>(null);
  const months = useMemo(() => {
    const out = new Map<string, BudgetDate[]>();
    for (const d of dates) out.set(d.from.slice(0, 7), [...(out.get(d.from.slice(0, 7)) ?? []), d]);
    return [...out.entries()];
  }, [dates]);
  const payer = (p?: string) => (p === "me" ? whoName("me", space) : p === "partner" ? whoName("partner", space) : p === "shared" ? "함께" : (p ?? ""));
  return (
    <div>
      <BlockTitle title={block.title} block={block} onRenamed={onChanged} />
      <WrittenInTheApp />
      {dates.length === 0 ? (
        <p className="mt-6 text-ui text-faint">아직 기록한 데이트가 없어요.</p>
      ) : (
        months.map(([month, list]) => (
          <section key={month} className="mt-5" aria-label={`${month} 데이트 가계부`}>
            <h2 className="flex items-baseline justify-between text-ui text-dim">
              <span>
                {Number(month.slice(0, 4))}년 {Number(month.slice(5, 7))}월
              </span>
              <span className="text-heading font-semibold text-foreground tabular-nums">{won(list.reduce((s, d) => s + d.total, 0))}</span>
            </h2>
            <ul className="mt-1 divide-y divide-edge-soft">
              {list.map((d) => {
                const top = spend(block, d.top);
                const shown = open === d.id;
                return (
                  <li key={d.id} data-focus={`budget:${d.id}`}>
                    <button
                      type="button"
                      aria-expanded={shown}
                      onClick={() => setOpen(shown ? null : d.id)}
                      className="-mx-2 flex w-[calc(100%+1rem)] items-center gap-3 rounded-lg px-2 py-3 text-left transition-colors hover:bg-glass focus-ring"
                    >
                      <span aria-hidden className="text-title">
                        {top.emoji}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-body text-foreground">{d.title || "데이트"}</span>
                        <span className="block text-aside text-faint">
                          {d.to ? dayRange(d.from, d.to) : monthDay(d.from)} · {d.count}건
                        </span>
                      </span>
                      <span className="shrink-0 text-body text-ink tabular-nums">{won(d.total)}</span>
                    </button>
                    {shown ? (
                      <div className="pb-3 pl-9">
                        {d.memo ? <p className="mb-2 text-ui break-keep text-dim">{d.memo}</p> : null}
                        {d.expenses?.length ? (
                          <ul className="space-y-1.5">
                            {d.expenses.map((x) => {
                              const c = spend(block, x.category);
                              return (
                                <li key={x.id} className="flex items-baseline gap-2 text-ui">
                                  <span aria-hidden>{c.emoji}</span>
                                  <span className="min-w-0 flex-1 truncate text-ink">{x.memo || c.name}</span>
                                  <span className="shrink-0 text-meta text-faint">{payer(x.paidBy)}</span>
                                  <span className="shrink-0 text-ink tabular-nums">{won(x.amount)}</span>
                                </li>
                              );
                            })}
                          </ul>
                        ) : (
                          <p className="text-aside text-faint">지출 내역은 uniple 앱에서 봐요.</p>
                        )}
                      </div>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          </section>
        ))
      )}
    </div>
  );
}
