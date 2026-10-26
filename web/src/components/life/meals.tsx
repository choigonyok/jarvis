"use client";

import { useMemo, useState } from "react";
import { X } from "lucide-react";
import { Pager } from "@/components/ui/pager";
import { usePaged } from "@/lib/paging";
import { krw } from "@/lib/portfolio";
import { type Meal, type MealWay, type Slot, SLOTS, WAYS, moment, wayOf } from "@/lib/life";
import { cn } from "@/lib/utils";

/** How each way is drawn: the one grayscale ramp, brightest for what was delivered - the count worth watching. */
const shade: Record<MealWay, string> = {
  delivery: "bg-foreground/70",
  home: "bg-foreground/38",
  out: "bg-foreground/16",
};

/**
 * The last month's meals as the card saw them. The four meals of the day
 * are columns, each split by how it was had; tapping one - or a way in the
 * key - narrows the list below to it, as the categories and days do in
 * 가계부: where you look to ask "what is this" is where you ask for only it.
 */
export function Meals({ meals }: { meals: Meal[] }) {
  const [slot, setSlot] = useState<Slot | null>(null);
  const [way, setWay] = useState<MealWay | null>(null);

  const counts = useMemo(() => {
    const out = Object.fromEntries(SLOTS.map((s) => [s, { delivery: 0, home: 0, out: 0, all: 0 }])) as Record<
      Slot,
      Record<MealWay | "all", number>
    >;
    for (const m of meals) {
      const c = out[m.slot];
      if (!c) continue;
      c[wayOf(m)]++;
      c.all++;
    }
    return out;
  }, [meals]);
  const top = Math.max(1, ...SLOTS.map((s) => counts[s].all));

  const shown = useMemo(
    () =>
      meals
        .filter((m) => (!slot || m.slot === slot) && (!way || wayOf(m) === way))
        .sort((a, b) => b.at.localeCompare(a.at)),
    [meals, slot, way],
  );
  // Twenty a page; another meal or way starts at the first.
  const paged = usePaged(shown, { reset: `${slot ?? ""}|${way ?? ""}` });
  const total = shown.reduce((s, m) => s + m.amount, 0);
  const delivered = meals.filter((m) => m.delivery);

  if (meals.length === 0) {
    return (
      <p className="py-3 text-[13px] leading-[1.55] break-keep text-pretty text-faint">
        최근 30일에 끼니로 읽힌 결제가 없어요. 식비로 분류된 카드 결제와 배달앱 결제가 여기 모여요.
      </p>
    );
  }

  return (
    <div>
      <p className="text-[13.5px] leading-[1.55] text-dim">
        끼니 결제 {meals.length}번 · 그중 배달 {delivered.length}번
        {delivered.length > 0 ? ` (${krw(delivered.reduce((s, m) => s + m.amount, 0))})` : ""}
      </p>

      {/* The categories' rows from 가계부 at the same size: a meal of the
          day, how often, and a bar split by how it was had. A row is also
          the filter for the list below. */}
      <ul role="group" aria-label="끼니별" className="mt-3 space-y-px">
        {SLOTS.map((s) => {
          const c = counts[s];
          const active = slot === s;
          const how = WAYS.filter(({ way: w }) => c[w] > 0)
            .map(({ way: w, label }) => `${label} ${c[w]}`)
            .join(" · ");
          return (
            <li key={s}>
              <button
                type="button"
                aria-pressed={active}
                aria-label={`${s} ${c.all}번${how ? `, ${how}` : ""}`}
                onClick={() => setSlot(active ? null : s)}
                className={cn(
                  "w-full rounded-lg px-2 py-2.5 text-left transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  active ? "bg-glass-raised" : "hover:bg-glass",
                  slot && !active && "opacity-55",
                )}
              >
                <span className="flex items-baseline justify-between gap-3">
                  <span className="text-[13.5px] text-foreground/90">
                    {s}
                    <span className="tnum ms-2 text-[11.5px] text-faint">{how}</span>
                  </span>
                  <span className="tnum shrink-0 text-[13.5px] text-foreground/90">{c.all}번</span>
                </span>
                <span aria-hidden className="mt-2 flex h-1 w-full overflow-hidden rounded-full bg-glass">
                  <span className="flex h-full" style={{ width: `${(c.all / top) * 100}%` }}>
                    {WAYS.map(({ way: w }) =>
                      c[w] ? <span key={w} className={cn("block h-full", shade[w])} style={{ flexGrow: c[w] }} /> : null,
                    )}
                  </span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>

      <div role="group" aria-label="어떻게 먹었나" className="mt-3 flex flex-wrap items-center gap-x-1 gap-y-1">
        {WAYS.map(({ way: w, label }) => {
          const active = way === w;
          return (
            <button
              key={w}
              type="button"
              aria-pressed={active}
              onClick={() => setWay(active ? null : w)}
              className={cn(
                "tap -my-2 flex items-center gap-1.5 rounded-md px-1.5 text-[12px] outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50",
                active ? "text-foreground" : "text-faint hover:text-dim",
              )}
            >
              <span aria-hidden className={cn("size-2 rounded-[2px]", shade[w])} />
              {label}
            </button>
          );
        })}
      </div>

      {slot || way ? (
        <div className="mt-3 flex flex-wrap items-center gap-1.5 text-[12px]">
          {slot ? <Chip label={slot} onClear={() => setSlot(null)} /> : null}
          {way ? <Chip label={WAYS.find((w) => w.way === way)!.label} onClear={() => setWay(null)} /> : null}
          <span className="tnum ms-auto text-faint">
            {shown.length}번 · {krw(total)}
          </span>
        </div>
      ) : null}

      {shown.length === 0 ? (
        <p className="py-6 text-center text-[13px] text-faint">이 조건의 끼니가 없어요.</p>
      ) : (
        <div id={paged.anchor} className="scroll-mt-4">
        <ul className="mt-2 space-y-px">
          {paged.items.map((m) => (
            <li key={m.at + m.merchant} className="flex items-center gap-3 py-2">
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[13.5px] text-foreground/90">{m.merchant}</span>
                <span className="tnum block text-[11.5px] text-faint">
                  {moment(m.at)} · {m.slot} · {WAYS.find((w) => w.way === wayOf(m))!.label}
                </span>
              </span>
              <span className="tnum shrink-0 text-[13.5px] text-foreground/90">{krw(m.amount)}</span>
            </li>
          ))}
        </ul>
        <Pager {...paged} unit="번" />
        </div>
      )}
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
