"use client";

import { type ReactNode, useState } from "react";
import { BusFront, LocateFixed, Minus, Plus, Search, TrainFront } from "lucide-react";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import type { Who } from "@/lib/track";
import { DAY_NAMES, type Line, type Route, type Stop, findLines, findStops, walkMinutes } from "@/lib/transit";
import { cn } from "@/lib/utils";

const NAMES = ["출근", "퇴근", "학원"];

const action =
  "inline-flex h-11 items-center justify-center gap-1.5 rounded-lg bg-glass-raised px-4 text-[13.5px] text-foreground outline-none transition-colors hover:bg-white/12 focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-45 mouse:h-9";
const chip = (on: boolean) =>
  cn(
    "flex h-9 items-center rounded-full border px-3.5 text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-7 mouse:px-3",
    on ? "border-mine/45 bg-mine/12 text-foreground" : "border-edge text-dim hover:text-foreground",
  );

/**
 * Adding or changing a route, in the order one would say it: where one gets
 * on (stops near where one's phone last was, or by name), what one takes
 * there, how long the walk is (from the distance, to be corrected), and its
 * name, days and hours - the nudges come in those hours.
 */
export function RouteEditor({
  who,
  open,
  onOpenChange,
  route,
  onSave,
  onDelete,
}: {
  who: Who;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The route being changed; none, a new one. */
  route: Route | null;
  onSave: (route: Route) => Promise<void>;
  onDelete?: () => Promise<void>;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="bottom" className="inset-x-safe pb-safe max-h-[92dvh] overflow-y-auto border-edge-soft bg-background">
        <SheetHeader className="px-4 pt-4 pb-1 sm:px-6">
          <SheetTitle className="text-[15px] font-medium text-foreground">{route ? `${route.name} 경로 고치기` : "경로 추가"}</SheetTitle>
        </SheetHeader>
        {/* Keyed so a new opening starts from what is being changed. */}
        {open ? <Form key={route?.id ?? "new"} who={who} route={route} onSave={onSave} onDelete={onDelete} /> : null}
      </SheetContent>
    </Sheet>
  );
}

function Form({ who, route, onSave, onDelete }: { who: Who; route: Route | null; onSave: (r: Route) => Promise<void>; onDelete?: () => Promise<void> }) {
  const [stop, setStop] = useState<Stop | null>(route?.stop ?? null);
  const [line, setLine] = useState<Line | null>(route?.line ?? null);
  const [walk, setWalk] = useState(route?.walk ?? 5);
  const [spare, setSpare] = useState(route?.spare ?? 1);
  const [name, setName] = useState(route?.name ?? "");
  const [days, setDays] = useState<number[]>(route?.days ?? [1, 2, 3, 4, 5]);
  const [from, setFrom] = useState(route?.from ?? "");
  const [to, setTo] = useState(route?.to ?? "");
  const [push, setPush] = useState(route?.push ?? true);

  const [stops, setStops] = useState<Stop[] | null>(null);
  const [q, setQ] = useState("");
  const [lines, setLines] = useState<Line[] | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [missed, setMissed] = useState<string | null>(null);

  async function look(by: Parameters<typeof findStops>[1]) {
    setBusy("stops");
    setError(null);
    setMissed(null);
    try {
      const found = await findStops(who, by);
      setStops(found.stops);
      setMissed(found.missed ?? null);
    } catch (e) {
      const why = e instanceof Error ? e.message : "정류장을 찾지 못했어요.";
      // No fix from the phone yet: the browser's own place, then.
      if ("near" in by && by.near === "me" && navigator.geolocation) {
        navigator.geolocation.getCurrentPosition(
          (pos) => void look({ near: { lat: pos.coords.latitude, lon: pos.coords.longitude } }),
          () => {
            setError(why);
            setBusy(null);
          },
          { enableHighAccuracy: true, timeout: 12_000 },
        );
        return;
      }
      setError(why);
    }
    setBusy(null);
  }

  async function pick(s: Stop) {
    setStop(s);
    setLine(null);
    setLines(null);
    if (s.dist) setWalk(walkMinutes(s.dist));
    setBusy("lines");
    setError(null);
    try {
      setLines(await findLines(s));
    } catch (e) {
      setError(e instanceof Error ? e.message : "노선을 불러오지 못했어요.");
    }
    setBusy(null);
  }

  async function save() {
    if (!stop || !line || !name.trim()) return;
    setBusy("save");
    setError(null);
    try {
      await onSave({ id: route?.id ?? "", name: name.trim(), stop, line, walk, spare, days, from, to, push: push && from !== "" });
    } catch (e) {
      setError(e instanceof Error ? e.message : "저장하지 못했어요.");
      setBusy(null);
    }
  }

  const windowSet = from !== "" && to !== "";
  return (
    <div className="mx-auto w-full max-w-[40rem] space-y-6 px-4 pb-8 text-[13px] sm:px-6">
      <Step title="어디서 타요">
        {stop ? (
          <Picked onChange={() => { setStop(null); setLines(null); setLine(null); }}>
            <StopLabel stop={stop} />
          </Picked>
        ) : (
          <>
            <div className="flex gap-2">
              <button type="button" onClick={() => void look({ near: "me" })} disabled={busy === "stops"} className={action}>
                <LocateFixed aria-hidden className="size-4 text-mine" strokeWidth={1.8} />
                지금 위치 근처
              </button>
              <form
                className="flex min-w-0 flex-1 items-center gap-1 rounded-lg border border-edge pr-1 pl-3 focus-within:border-ring"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (q.trim()) void look({ q: q.trim() });
                }}
              >
                <input
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  placeholder="정류장·역 이름"
                  aria-label="정류장이나 역 이름"
                  enterKeyHint="search"
                  className="h-11 min-w-0 flex-1 bg-transparent text-[14px] text-foreground outline-none placeholder:text-faint mouse:h-9"
                />
                <button type="submit" aria-label="찾기" className="flex size-9 items-center justify-center rounded-md text-dim hover:text-foreground mouse:size-7">
                  <Search aria-hidden className="size-4" strokeWidth={1.8} />
                </button>
              </form>
            </div>
            {busy === "stops" ? <p className="mt-3 text-faint">찾는 중…</p> : null}
            {stops ? (
              stops.length ? (
                <ul className="mt-2 divide-y divide-edge-soft">
                  {stops.slice(0, 14).map((s) => (
                    <li key={`${s.kind}:${s.region}:${s.id}`}>
                      <button type="button" onClick={() => void pick(s)} className="flex w-full items-center gap-3 py-2.5 text-left outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50">
                        <StopLabel stop={s} />
                      </button>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="mt-3 text-dim">찾은 정류장이 없어요. 다른 이름으로 찾아 보세요.</p>
              )
            ) : null}
            {missed ? <p className="mt-2 text-[12px] leading-[1.55] break-keep text-dim">{missed}</p> : null}
          </>
        )}
      </Step>

      {stop ? (
        <Step title="무엇을 타요">
          {line ? (
            <Picked onChange={() => setLine(null)}>
              <span className="shrink-0 text-[14px] font-medium text-foreground">{line.name}</span>
              <span className="min-w-0 truncate text-dim">{line.toward}</span>
            </Picked>
          ) : busy === "lines" ? (
            <p className="text-faint">노선을 불러오는 중…</p>
          ) : lines && lines.length ? (
            <ul className="divide-y divide-edge-soft">
              {lines.map((l) => (
                <li key={l.id}>
                  <button type="button" onClick={() => setLine(l)} className="flex w-full items-baseline gap-2 py-2.5 text-left outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50">
                    <span className="shrink-0 text-[14px] font-medium text-foreground">{l.name}</span>
                    <span className="min-w-0 truncate text-dim">{l.toward}</span>
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-dim">이 정류장의 노선을 받지 못했어요.</p>
          )}
        </Step>
      ) : null}

      {stop && line ? (
        <>
          <Step title="걸어서">
            <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
              <Stepper label="정류장까지" value={walk} min={0} max={90} unit="분" onChange={setWalk} />
              <Stepper label="일찍 도착" value={spare} min={0} max={15} unit="분" onChange={setSpare} />
            </div>
            <p className="mt-2 text-[12px] text-faint">
              {stop.dist ? `지금 위치에서 ${stop.dist}m, 걸어서 ${walkMinutes(stop.dist)}분쯤이에요. ` : ""}나갈 때는 도착 시각에서 이만큼 앞이에요.
            </p>
          </Step>

          <Step title="이름과 알림">
            <div className="flex flex-wrap gap-1.5">
              {NAMES.map((n) => (
                <button key={n} type="button" aria-pressed={name === n} onClick={() => setName(n)} className={chip(name === n)}>
                  {n}
                </button>
              ))}
              <input
                value={NAMES.includes(name) ? "" : name}
                onChange={(e) => setName(e.target.value)}
                placeholder="다른 이름"
                aria-label="경로 이름"
                maxLength={20}
                className="h-9 w-28 rounded-full border border-edge bg-transparent px-3.5 text-[13px] text-foreground outline-none placeholder:text-faint focus-visible:border-ring mouse:h-7"
              />
            </div>
            <div className="mt-3 flex flex-wrap gap-1.5" role="group" aria-label="요일">
              {DAY_NAMES.map((d, i) => {
                const on = days.includes(i + 1);
                return (
                  <button
                    key={d}
                    type="button"
                    aria-pressed={on}
                    onClick={() => setDays(on ? days.filter((x) => x !== i + 1) : [...days, i + 1].sort())}
                    className={cn(chip(on), "w-10 justify-center px-0 mouse:w-8 mouse:px-0")}
                  >
                    {d}
                  </button>
                );
              })}
            </div>
            <div className="mt-3 flex items-center gap-2 text-dim">
              <input type="time" value={from} onChange={(e) => setFrom(e.target.value)} aria-label="시간대 시작" className="tnum h-11 rounded-lg border border-edge bg-transparent px-3 text-foreground outline-none focus-visible:border-ring mouse:h-9" />
              부터
              <input type="time" value={to} onChange={(e) => setTo(e.target.value)} aria-label="시간대 끝" className="tnum h-11 rounded-lg border border-edge bg-transparent px-3 text-foreground outline-none focus-visible:border-ring mouse:h-9" />
              까지
            </div>
            <label className={cn("mt-3 flex items-center gap-2.5", !windowSet && "opacity-50")}>
              <input type="checkbox" checked={push && windowSet} disabled={!windowSet} onChange={(e) => setPush(e.target.checked)} className="size-4 accent-[var(--mine)]" />
              <span className="text-foreground/90">이 시간대에 나갈 때를 알림으로 알려 주기</span>
            </label>
            <p className="mt-1.5 text-[12px] leading-[1.55] break-keep text-faint">
              나가기 15분 전에 ‘슬슬 짐 싸야 해요’, 2분 전에 ‘지금 나가요’를 보내요. 방해 금지 시간에도 울려요.
            </p>
          </Step>
        </>
      ) : null}

      {error ? (
        <p role="alert" className="text-[12.5px] text-reject">
          {error}
        </p>
      ) : null}

      <div className="flex items-center gap-2">
        <button type="button" onClick={() => void save()} disabled={!stop || !line || !name.trim() || busy === "save"} className={cn(action, "flex-1")}>
          {busy === "save" ? "저장하는 중…" : "경로 저장"}
        </button>
        {onDelete ? (
          <button
            type="button"
            onClick={() => {
              setBusy("save");
              void onDelete().catch((e) => {
                setError(e instanceof Error ? e.message : "지우지 못했어요.");
                setBusy(null);
              });
            }}
            className="h-11 rounded-lg px-3 text-[13px] text-dim outline-none hover:text-reject focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-9"
          >
            지우기
          </button>
        ) : null}
      </div>
    </div>
  );
}

/** A subway line's direction as riders say it: "외선 · 성수행 - 역삼방면". */
function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h3 className="mb-2 text-[13.5px] font-medium text-foreground">{title}</h3>
      {children}
    </section>
  );
}

function Picked({ children, onChange }: { children: ReactNode; onChange: () => void }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-3">{children}</div>
      <button type="button" onClick={onChange} className="shrink-0 text-[12.5px] text-dim underline-offset-2 outline-none hover:text-foreground hover:underline focus-visible:ring-3 focus-visible:ring-ring/50">
        바꾸기
      </button>
    </div>
  );
}

function StopLabel({ stop }: { stop: Stop }) {
  const Icon = stop.kind === "subway" ? TrainFront : BusFront;
  const sub = stop.kind === "subway" ? "지하철" : `${stop.region === "seoul" ? "서울" : "경기"} 버스${stop.no ? ` · ${stop.no}` : ""}`;
  return (
    <>
      <Icon aria-hidden className="size-[18px] shrink-0 text-dim" strokeWidth={1.7} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[14px] text-foreground">{stop.name}</span>
        <span className="block text-[12px] text-faint">{sub}</span>
      </span>
      {stop.dist ? <span className="tnum shrink-0 text-[12px] text-faint">{stop.dist >= 1000 ? `${(stop.dist / 1000).toFixed(1)}km` : `${stop.dist}m`}</span> : null}
    </>
  );
}

function Stepper({ label, value, min, max, unit, onChange }: { label: string; value: number; min: number; max: number; unit: string; onChange: (v: number) => void }) {
  const btn = "flex size-9 items-center justify-center rounded-full border border-edge text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-40 mouse:size-7";
  return (
    <div className="flex items-center gap-2.5">
      <span className="text-dim">{label}</span>
      <button type="button" aria-label={`${label} 줄이기`} disabled={value <= min} onClick={() => onChange(value - 1)} className={btn}>
        <Minus aria-hidden className="size-3.5" strokeWidth={2} />
      </button>
      <span className="tnum w-10 text-center text-[15px] text-foreground">
        {value}
        {unit}
      </span>
      <button type="button" aria-label={`${label} 늘리기`} disabled={value >= max} onClick={() => onChange(value + 1)} className={btn}>
        <Plus aria-hidden className="size-3.5" strokeWidth={2} />
      </button>
    </div>
  );
}
