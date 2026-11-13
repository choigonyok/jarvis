"use client";

import Link from "next/link";
import { useMemo, useState, type ReactNode } from "react";
import { Check as CheckMark, ChevronRight, ListOrdered, Plus } from "lucide-react";
import { AddBlock } from "@/components/couple/add-block";
import { EditBlocks } from "@/components/couple/edit-blocks";
import { KoreaMap, percent, useKoreaMap, type Face, type KoreaMapData } from "@/components/couple/korea-map";
import { NewsLine } from "@/components/couple/news";
import { CoupleScreen } from "@/components/couple/screen";
import { useChecks, useCover, useSpace } from "@/components/couple/use-space";
import { WeekStrip, useCheckToday } from "@/components/couple/habit";
import { Failed } from "@/components/shell/failed";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import {
  BLOCK_NAME,
  OPENS,
  between,
  checkedDays,
  dMark,
  milestones,
  nextOn,
  photoSrc,
  today,
  together,
  todayIs,
  weekOf,
  writeCouple,
  type Block,
  type Bucket,
  type Space,
} from "@/lib/couple";
import { gridBounds, monthCells, cursorOf } from "@/lib/month";
import { SAY } from "@/lib/say";
import { covers, type CalendarEvent } from "@/lib/thread";
import { useResource } from "@/lib/use-resource";
import { dateLabel } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * 우리: the couple's space at a glance - how long we have been together, what
 * the other just did, and every block of the home in the order the two of us
 * keep them. A block opens to its own page.
 */
export function CoupleHome() {
  const space = useSpace();
  const { map } = useKoreaMap();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState(false);
  const blocks = useMemo(() => (space.data?.blocks ?? []).filter((b) => b.type !== "dday"), [space.data]);
  const dday = space.data?.blocks.find((b) => b.type === "dday");

  return (
    <CoupleScreen wide>
      {space.error && !space.data ? (
        <div>
          <p className="text-display leading-[1.25] font-semibold tracking-tight text-foreground">우리 공간을 불러오지 못했어요</p>
          <Failed onRetry={space.reload} className="mt-3">
            {space.error.message === "502" || /uniple/.test(space.error.message)
              ? "uniple 에 닿지 못했어요. 로그인이 풀렸으면 상태 탭의 캘린더 줄을 보세요."
              : space.error.message}
          </Failed>
        </div>
      ) : !space.data ? (
        <p className="pt-10 text-center text-ui text-faint">{SAY.loading}</p>
      ) : (
        <>
          <Together space={space.data} dday={dday} />
          <div className="mt-3 max-w-[34rem]">
            <NewsLine space={space.data} map={map} />
          </div>

          <div className="mt-6 grid grid-cols-1 gap-3 md:grid-cols-2">
            {blocks.map((b) => (
              <BlockCard key={b.id} block={b} space={space.data!} map={map} onChanged={space.reload} />
            ))}
          </div>

          <div className="mt-4 -ml-2 flex flex-wrap gap-1">
            <Button variant="quiet" size="touch-sm" onClick={() => setAdding(true)} className="gap-1.5">
              <Plus aria-hidden className="size-4" />
              블록 추가
            </Button>
            <Button variant="quiet" size="touch-sm" onClick={() => setEditing(true)} className="gap-1.5">
              <ListOrdered aria-hidden className="size-4" />
              블록 편집
            </Button>
          </div>
          <AddBlock open={adding} onOpenChange={setAdding} space={space.data} onAdded={space.reload} />
          <EditBlocks open={editing} onOpenChange={setEditing} blocks={space.data.blocks} onChanged={space.reload} />
        </>
      )}
    </CoupleScreen>
  );
}

/** The first line: the day count, and the next day worth counting - or, as the D-day block asks, the next three. */
function Together({ space, dday }: { space: Space; dday?: Block }) {
  const start = space.anniversary;
  const now = today();
  const coming =
    start && dday?.config?.showUpcoming === true
      ? [
          ...milestones(start, 3, now).map((m) => ({ key: m.key, title: m.label, date: m.date })),
          ...(dday.dday ?? []).filter((d) => !d.start).map((d) => ({ key: d.id, title: d.title, date: nextOn(d.date, d.repeat, now) })),
        ]
          .filter((d) => d.date >= now)
          .sort((a, b) => a.date.localeCompare(b.date))
          .slice(0, 3)
      : [];
  const body = start ? (
    <>
      <p className="text-display-lg leading-none font-semibold tracking-tight text-foreground tabular-nums">
        D+{together(start, now)}
      </p>
      {coming.length ? (
        <ul className="mt-3 max-w-[22rem] space-y-1">
          {coming.map((d) => (
            <li key={d.key} className="flex items-baseline justify-between gap-3 text-ui">
              <span className="min-w-0 truncate text-ink">{d.title}</span>
              <span className="shrink-0 font-semibold text-foreground tabular-nums">{dMark(d.date, now)}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-2 text-ui text-dim">
          {todayIs(start, now) ? (
            <span className="text-ink">오늘 {todayIs(start, now)}</span>
          ) : (
            (() => {
              const next = milestones(start, 1, now)[0];
              return `${next.label}까지 ${between(now, next.date)}일 · ${dateLabel(next.date)}`;
            })()
          )}
        </p>
      )}
    </>
  ) : (
    <>
      <p className="text-display leading-[1.25] font-semibold tracking-tight text-foreground">만난 날이 아직 없어요</p>
      <p className="mt-2 text-ui text-dim">uniple 앱의 디데이에서 정하면 여기서 날을 세요.</p>
    </>
  );
  if (!dday) return <div>{body}</div>;
  return (
    <Link href={`/couple/${dday.id}`} className="-mx-2 block rounded-xl px-2 py-1 transition-colors hover:bg-glass focus-ring" aria-label={`${dday.title} 디데이 열기`}>
      {body}
    </Link>
  );
}

/** A block of the home: its name and figure on one line, what it holds under. */
function Card({ block, figure, children }: { block: Block; figure?: ReactNode; children?: ReactNode }) {
  const opens = OPENS.has(block.type);
  const head = (
    <>
      <span className="min-w-0 flex-1 truncate text-title font-medium tracking-tight text-foreground">{block.title}</span>
      {figure ? <span className="shrink-0 text-ui text-dim tabular-nums">{figure}</span> : null}
      {opens ? <ChevronRight aria-hidden className="size-4 shrink-0 text-faint" /> : null}
    </>
  );
  return (
    <section className="rounded-2xl bg-glass p-4 sm:p-5" aria-label={block.title}>
      {opens ? (
        <Link
          href={block.type === "calendar" ? "/calendar" : `/couple/${block.id}`}
          className="-mx-2 -mt-1 flex min-h-11 items-center gap-2 rounded-lg px-2 focus-ring"
        >
          {head}
        </Link>
      ) : (
        <div className="-mt-1 flex min-h-11 items-center gap-2">{head}</div>
      )}
      {children ? <div className="mt-2">{children}</div> : null}
    </section>
  );
}

function BlockCard({ block, space, map, onChanged }: { block: Block; space: Space; map: KoreaMapData | null; onChanged: () => unknown }) {
  switch (block.type) {
    case "calendar":
      return <CalendarCard block={block} space={space} />;
    case "workout":
      return <HabitCard block={block} space={space} />;
    case "bucket":
      return <BucketCard block={block} onChanged={onChanged} />;
    case "map":
      return <MapCard block={block} map={map} />;
    case "free":
      return (
        <Card block={block} figure={`${block.free?.length ?? 0}개`}>
          {block.free?.length ? (
            <ul className="space-y-1.5">
              {block.free.slice(-3).map((f) => (
                <li key={f.id} className="flex items-start gap-2 text-ui text-ink">
                  <span aria-hidden className={cn("mt-[7px] size-[5px] shrink-0 rounded-full", f.by === "me" ? "bg-mine" : "bg-partner")} />
                  <span className="line-clamp-2 break-keep">{f.content}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-ui text-faint">아무거나 적어도 괜찮아요.</p>
          )}
        </Card>
      );
    default:
      return (
        <Card block={block}>
          <p className="text-aside text-faint">{BLOCK_NAME[block.type] ?? "블록"} · uniple 앱에서 열어요</p>
        </Card>
      );
  }
}

/* ───────────────────────────── calendar ───────────────────────────── */

/** This month, small: the days with something on them, and the day's photo. */
function CalendarCard({ block, space }: { block: Block; space: Space }) {
  const now = today();
  const cursor = cursorOf(now);
  const { from, to } = gridBounds(cursor);
  const month = useResource<CalendarEvent[]>(`couple:month:${now.slice(0, 7)}`, async (signal) => {
    const res = await fetch(`/api/calendar/events?from=${from}&to=${to}`, { cache: "no-store", signal });
    if (!res.ok) throw new Error(String(res.status));
    return ((await res.json()) as { events: CalendarEvent[] }).events ?? [];
  });
  const cover = useCover(now);
  const cells = monthCells(cursor).filter((c) => c.inMonth);
  const lead = (new Date(`${cells[0].date}T00:00:00Z`).getUTCDay() - (space.weekStart === "mon" ? 1 : 0) + 7) % 7;
  const names = space.weekStart === "mon" ? ["월", "화", "수", "목", "금", "토", "일"] : ["일", "월", "화", "수", "목", "금", "토"];
  const owners = (day: string) => {
    const set = new Set((month.data ?? []).filter((e) => covers(e, day)).map((e) => e.owner ?? "me"));
    return (["me", "shared", "partner"] as const).filter((o) => set.has(o));
  };

  return (
    <Card block={block} figure={`${cursor.month + 1}월`}>
      <div className="flex gap-4">
        {cover.data ? (
          <div className="relative aspect-square w-[38%] shrink-0 self-start overflow-hidden rounded-xl bg-glass-raised">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={photoSrc(cover.data.asset)} alt={`${dateLabel(cover.data.date)}의 사진`} className="size-full object-cover" loading="lazy" />
          </div>
        ) : null}
        <div className="min-w-0 flex-1">
          <div className="grid grid-cols-7 text-center text-tiny text-faint">
            {names.map((n) => (
              <span key={n}>{n}</span>
            ))}
          </div>
          <div className="mt-1 grid grid-cols-7 gap-y-0.5 text-center">
            {Array.from({ length: lead }, (_, i) => (
              <span key={`p${i}`} />
            ))}
            {cells.map((c) => {
              const marks = owners(c.date);
              const isToday = c.date === now;
              return (
                <span key={c.date} className="flex flex-col items-center py-0.5">
                  <span
                    className={cn(
                      "flex size-6 items-center justify-center rounded-full text-meta tabular-nums",
                      isToday ? "bg-foreground font-semibold text-background" : "text-ink",
                    )}
                  >
                    {Number(c.date.slice(8))}
                  </span>
                  <span className="mt-0.5 flex h-[5px] gap-[2px]" aria-hidden>
                    {marks.map((o) => (
                      <span
                        key={o}
                        className={cn("size-[5px] rounded-full", o === "me" ? "bg-mine" : o === "partner" ? "bg-partner" : "owner-shared")}
                      />
                    ))}
                  </span>
                </span>
              );
            })}
          </div>
        </div>
      </div>
    </Card>
  );
}

/* ───────────────────────────── habit ───────────────────────────── */

function HabitCard({ block, space }: { block: Block; space: Space }) {
  const now = today();
  const week = weekOf(now, space.weekStart);
  const checks = useChecks(block.id, week[0], week[6]);
  const days = checkedDays(checks.data ?? []);
  const { mine, busy, error, toggle } = useCheckToday(block.id, checks);
  return (
    <Card block={block}>
      <p className="mb-3 flex items-center gap-3 text-aside" aria-label="이번 주">
        <Count dot="bg-mine" label="나" n={days.me.size} />
        <Count dot="bg-partner" label="상대" n={days.partner.size} />
        <Count dot="owner-shared" label="함께" n={days.both.size} />
      </p>
      <WeekStrip days={week} checks={checks.data ?? []} today={now} weekStart={space.weekStart} />
      <Button
        variant={mine ? "soft" : "raised"}
        size="touch"
        disabled={busy || !checks.data}
        onClick={() => void toggle()}
        className="mt-3 w-full gap-1.5"
      >
        {mine ? null : <CheckMark aria-hidden className="size-4" />}
        {busy ? "보내는 중…" : mine ? "오늘 체크 취소" : "오늘 체크하기"}
      </Button>
      {error ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {error}
        </p>
      ) : null}
    </Card>
  );
}

function Count({ dot, label, n }: { dot: string; label: string; n: number }) {
  return (
    <span className="inline-flex items-center gap-1 text-dim">
      <span aria-hidden className={cn("size-[6px] rounded-full", dot)} />
      {label}
      <span className="text-ink tabular-nums">{n}</span>
    </span>
  );
}

/* ───────────────────────────── bucket ───────────────────────────── */

function BucketCard({ block, onChanged }: { block: Block; onChanged: () => unknown }) {
  const items = block.bucket ?? [];
  const done = items.filter((b) => b.done).length;
  const share = items.length ? Math.round((done / items.length) * 100) : 0;
  const [ticked, setTicked] = useState<ReadonlySet<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const open = items.filter((b) => !b.done && !ticked.has(b.id)).slice(0, 3);

  async function tick(b: Bucket) {
    setError(null);
    setTicked((s) => new Set(s).add(b.id));
    const sent = await writeCouple("PATCH", `/blocks/${block.id}/bucket/${b.id}`, { done: true });
    if (!sent.ok) {
      setTicked((s) => {
        const next = new Set(s);
        next.delete(b.id);
        return next;
      });
      setError(sent.error);
      return;
    }
    await onChanged();
  }

  return (
    <Card block={block} figure={items.length ? `${done}/${items.length} · ${share}%` : undefined}>
      {items.length ? <Progress value={share} className="h-1.5" indicatorClassName="bg-foreground/70" aria-label={`${share}% 이뤘어요`} /> : null}
      {open.length ? (
        <ul className="mt-3 space-y-0.5">
          {open.map((b) => (
            <li key={b.id} className="flex min-h-10 items-center gap-3">
              <button
                type="button"
                onClick={() => void tick(b)}
                aria-label={`${b.title} 이뤘어요로 표시`}
                className="tap -m-2.5 flex shrink-0 items-center justify-center rounded-full p-2.5 focus-ring"
              >
                <span aria-hidden className="size-5 rounded-full border-[1.5px] border-dim transition-colors hover:border-foreground" />
              </button>
              <span className="min-w-0 truncate text-ui text-ink">{b.title}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-3 text-ui text-faint">{items.length ? "전부 이뤘어요. 다음 버킷을 적어 볼까요?" : "적어 두면 다 이루게 돼요."}</p>
      )}
      {error ? (
        <p role="alert" className="mt-1 text-aside text-reject">
          {error}
        </p>
      ) : null}
    </Card>
  );
}

/* ───────────────────────────── map ───────────────────────────── */

/** Each region's face, from the block's photos. */
export function facesOf(block: Block): Map<string, Face> {
  const out = new Map<string, Face>();
  for (const v of block.visits ?? []) {
    if (!v.asset) continue;
    const cur = out.get(v.region);
    if (!cur || v.rep) out.set(v.region, { asset: v.asset, rotation: v.rotation });
  }
  return out;
}

/** The date map, as the two pinned it to the card: the whole country, or one 시/도 near. */
function MapCard({ block, map }: { block: Block; map: KoreaMapData | null }) {
  const faces = facesOf(block);
  const pinned = typeof block.config?.card_region === "string" ? (block.config.card_region as string) : "all";
  const view = map && pinned !== "all" && map.provinces.some((p) => p.code === pinned) ? pinned : "all";
  const figure = !map
    ? undefined
    : view === "all"
      ? `전국 ${percent(faces.size, map.regions.length)}`
      : (() => {
          const rs = map.regions.filter((r) => r.sido === view);
          return `${map.provinces.find((p) => p.code === view)?.name} ${rs.filter((r) => faces.has(r.code)).length}/${rs.length}`;
        })();
  return (
    <Card block={block} figure={figure}>
      {map ? (
        <KoreaMap map={map} view={view} faces={faces} className="mx-auto h-auto max-h-80 w-full" />
      ) : (
        <div aria-hidden className="mx-auto aspect-[3/5] max-h-80 rounded-xl bg-glass-raised" />
      )}
    </Card>
  );
}
