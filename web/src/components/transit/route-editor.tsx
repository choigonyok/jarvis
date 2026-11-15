"use client";

import { type ReactNode, useEffect, useRef, useState } from "react";
import { BusFront, LocateFixed, Search, TrainFront } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Stepper } from "@/components/goals/stepper";
import { Failed } from "@/components/shell/failed";
import { BottomSheetContent, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Command, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Input } from "@/components/ui/input";
import { InputGroupAddon, InputGroupButton } from "@/components/ui/input-group";
import { Sheet } from "@/components/ui/sheet";
import { SwitchRow } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { failureText, SAY } from "@/lib/say";
import type { Who } from "@/lib/track";
import { DAY_NAMES, type Line, type Route, type Stop, findLines, findStops, walkMinutes } from "@/lib/transit";
import { HttpError } from "@/lib/use-resource";

const NAMES = ["출근", "퇴근", "학원"];

/**
 * Stops of one name close together - the two sides of a road, or one stop as
 * both bus feeds list it - chosen as one place, and told apart by what one
 * takes there: "27 · 수원여자대학교정문 방면" is one side, "27 · 일성아파트
 * 방면" the other.
 */
type Place = { key: string; name: string; stops: Stop[]; dist?: number };

/** A line at one of a place's stops. */
type Choice = { stop: Stop; line: Line };

function apart(a: Stop, b: Stop): number {
  if (a.no && a.no === b.no) return 0;
  if (!a.lat || !a.lon || !b.lat || !b.lon) return Infinity;
  const rad = Math.PI / 180;
  const x = (b.lon - a.lon) * rad * Math.cos(((a.lat + b.lat) / 2) * rad);
  const y = (b.lat - a.lat) * rad;
  return Math.hypot(x, y) * 6_371_000;
}

function places(stops: Stop[]): Place[] {
  const out: Place[] = [];
  for (const s of stops) {
    // The feeds write one stop differently (강남역.홍우빌딩앞, 강남역홍우빌딩앞):
    // the number on its sign says it is one.
    const same = out.find(
      (p) =>
        p.stops[0].kind === s.kind &&
        (p.stops.some((o) => o.no && o.no === s.no) || (p.name === s.name && p.stops.every((o) => apart(o, s) < 400))),
    );
    if (!same) {
      out.push({ key: `${s.kind}:${s.region ?? ""}:${s.id}`, name: s.name, stops: [s], dist: s.dist });
      continue;
    }
    same.stops.push(s);
    if (s.region === "gyeonggi") same.name = s.name;
    if (s.dist != null && (same.dist == null || s.dist < same.dist)) same.dist = s.dist;
  }
  return out;
}

/** How many stops a place really is: one stop listed by both feeds is one. */
function sides(p: Place): number {
  return new Set(p.stops.map((s) => s.no || `${s.region}:${s.id}`)).size;
}

/**
 * A place's lines, one row each: a route listed for one stop by both feeds
 * once - 경기's, whose arrivals come to the second and count the seats - and by
 * number. A route that passes a stop both ways is two rows: they are two ways
 * to go.
 */
function choicesOf(rows: Choice[]): Choice[] {
  const gyeonggi = rows.filter((c) => c.stop.region === "gyeonggi");
  const told = (c: Choice) => gyeonggi.some((g) => g.stop.no === c.stop.no && (g.line.id === c.line.id || g.line.name === c.line.name));
  const out = rows.filter((c) => c.stop.region === "gyeonggi" || !c.stop.no || !told(c));
  if (out[0]?.stop.kind === "bus") {
    out.sort((a, b) => a.line.name.localeCompare(b.line.name, "ko", { numeric: true }) || (a.line.toward ?? "").localeCompare(b.line.toward ?? "", "ko"));
  }
  return out;
}

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
  // Four steps and two lookups in, closing by hand - a thumb slipping to the
  // edge, the title pulled down - asks before it throws them away (ui/sheet.tsx).
  const [entered, setEntered] = useState(false);
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <BottomSheetContent width="full" height="tall" keepFocus={false} aria-describedby={undefined} className="gap-4 overflow-y-auto" guard={entered}>
        <BottomSheetHeader className="pb-1 sm:px-6">
          <BottomSheetTitle className="pr-0">{route ? `${route.name} 경로 고치기` : "경로 추가"}</BottomSheetTitle>
        </BottomSheetHeader>
        {/* Keyed so a new opening starts from what is being changed. */}
        {open ? <Form key={route?.id ?? "new"} who={who} route={route} onSave={onSave} onDelete={onDelete} onEntered={setEntered} /> : null}
      </BottomSheetContent>
    </Sheet>
  );
}

function Form({
  who,
  route,
  onSave,
  onDelete,
  onEntered,
}: {
  who: Who;
  route: Route | null;
  onSave: (r: Route) => Promise<void>;
  onDelete?: () => Promise<void>;
  /** Whether anything differs from what it opened with. */
  onEntered: (entered: boolean) => void;
}) {
  const [place, setPlace] = useState<Place | null>(route ? places([route.stop])[0] : null);
  const [stop, setStop] = useState<Stop | null>(route?.stop ?? null);
  const [line, setLine] = useState<Line | null>(route?.line ?? null);
  const [walk, setWalk] = useState(route?.walk ?? 5);
  const [spare, setSpare] = useState(route?.spare ?? 1);
  const [name, setName] = useState(route?.name ?? "");
  // A route kept with no days is every day: it opens so, not as 월-금.
  const [days, setDays] = useState<number[]>(route ? (route.days ?? []) : [1, 2, 3, 4, 5]);
  const [from, setFrom] = useState(route?.from ?? "");
  const [to, setTo] = useState(route?.to ?? "");
  const [push, setPush] = useState(route?.push ?? true);

  const [stops, setStops] = useState<Stop[] | null>(null);
  const [q, setQ] = useState("");
  const [choices, setChoices] = useState<Choice[] | null>(null);
  // A place's lines that could not be read: all of them - not "no lines
  // here" - or some stops' (a side of the road missing from the list).
  const [unread, setUnread] = useState<{ why: string; all: boolean } | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [missed, setMissed] = useState<string | null>(null);
  // 경로 저장 pressed with no name: asked for where names are chosen. A greyed
  // button said nothing, and from the foot of a long sheet the names were
  // out of sight.
  const [unnamed, setUnnamed] = useState(false);
  const names = useRef<HTMLDivElement>(null);
  // Why no stops came, said under the search that was pressed - and when the
  // place could not be had, the way left: the name.
  const [unfound, setUnfound] = useState<string | null>(null);
  const field = useRef<HTMLInputElement>(null);
  // The stop the arrow keys light in what was found; Enter takes it.
  const [lit, setLit] = useState("");

  // What saving would keep, against how it stood on opening - or a name typed
  // to search by.
  const kept = JSON.stringify([place?.key, stop?.region, stop?.id, line?.id, line?.order, walk, spare, name.trim(), days, from, to, push]);
  const [opened] = useState(kept);
  const entered = kept !== opened || q.trim() !== "";
  useEffect(() => {
    onEntered(entered);
    return () => onEntered(false);
  }, [entered, onEntered]);

  async function look(by: Parameters<typeof findStops>[1]) {
    setBusy("stops");
    setError(null);
    setUnfound(null);
    setMissed(null);
    try {
      const found = await findStops(who, by);
      setStops(found.stops);
      setMissed(found.missed ?? null);
    } catch (e) {
      // No fix from the phone yet (404): the browser's own place, then - and
      // only then. Asked for on a dropped line too, the place was refused
      // for nothing, and a phone keeps a refusal.
      if ("near" in by && by.near === "me" && e instanceof HttpError && e.status === 404 && navigator.geolocation) {
        navigator.geolocation.getCurrentPosition(
          (pos) => void look({ near: { lat: pos.coords.latitude, lon: pos.coords.longitude } }),
          (err) => {
            setUnfound(
              err.code === err.PERMISSION_DENIED
                ? "위치 권한이 꺼져 있어요. 정류장이나 역 이름으로 찾아 보세요."
                : "지금 위치를 알아내지 못했어요. 정류장이나 역 이름으로 찾아 보세요.",
            );
            setBusy(null);
            field.current?.focus();
          },
          { enableHighAccuracy: true, timeout: 12_000 },
        );
        return;
      }
      setUnfound(failureText(e, "정류장을 찾지 못했어요."));
    }
    setBusy(null);
  }

  async function pick(p: Place) {
    setUnfound(null);
    setPlace(p);
    setStop(p.stops.length === 1 ? p.stops[0] : null);
    setLine(null);
    if (p.dist) setWalk(walkMinutes(p.dist));
    await loadChoices(p);
  }

  async function loadChoices(p: Place) {
    setChoices(null);
    setUnread(null);
    setBusy("lines");
    setError(null);
    const got = await Promise.allSettled(p.stops.map((s) => findLines(s).then((ls) => ls.map((l) => ({ stop: s, line: l })))));
    const rows = got.flatMap((g) => (g.status === "fulfilled" ? g.value : []));
    const failed = got.find((g): g is PromiseRejectedResult => g.status === "rejected");
    if (failed) setUnread({ why: failureText(failed.reason, "노선을 불러오지 못했어요."), all: !rows.length });
    // Nothing read leaves nothing to choose from, so the line's 바꾸기 reads
    // again; an empty list stood for it, and the editor had no way on.
    setChoices(failed && !rows.length ? null : choicesOf(rows));
    setBusy(null);
  }

  function choose(c: Choice) {
    setStop(c.stop);
    setLine(c.line);
    if (c.stop.dist) setWalk(walkMinutes(c.stop.dist));
  }

  const windowSet = from !== "" && to !== "";

  async function save() {
    if (!stop || !line) return;
    if (!name.trim()) {
      setUnnamed(true);
      names.current?.scrollIntoView({ block: "center" });
      return;
    }
    // One end of the hours is no window: the service refuses it, in words
    // about HH:MM. Said here in the form's own, before it goes.
    if ((from === "") !== (to === "")) {
      setError("시간대는 시작과 끝을 둘 다 넣거나 둘 다 비워 주세요.");
      return;
    }
    setBusy("save");
    setError(null);
    try {
      // The alert as the switch shows it: on only with the hours it comes in.
      await onSave({ id: route?.id ?? "", name: name.trim(), stop, line, walk, spare, days, from, to, push: push && windowSet });
    } catch (e) {
      setError(failureText(e, SAY.couldNotSave));
      setBusy(null);
    }
  }

  return (
    <div className="mx-auto w-full max-w-[40rem] space-y-6 px-4 pb-8 text-ui sm:px-6">
      <Step title="어디서 타요">
        {place ? (
          <Picked onChange={() => { setPlace(null); setStop(null); setChoices(null); setLine(null); }}>
            {stop ? <StopLabel stop={stop} /> : <PlaceLabel place={place} />}
          </Picked>
        ) : (
          // The name is searched on the server (shouldFilter off). Enter with
          // nothing found searches; with stops found it takes the lit one, so
          // changing the name drops what the last search found. The form is
          // the 찾기 button's.
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (q.trim()) void look({ q: q.trim() });
            }}
          >
            <Command shouldFilter={false} value={lit} onValueChange={setLit} label="정류장 찾기">
              <div className="flex gap-2">
                <Button type="button" variant="raised" size="touch" onClick={() => void look({ near: "me" })} disabled={busy === "stops"}>
                  <LocateFixed aria-hidden className="text-mine" strokeWidth={1.8} />
                  지금 위치 근처
                </Button>
                <CommandInput
                  ref={field}
                  icon={false}
                  value={q}
                  onValueChange={(v) => {
                    setQ(v);
                    setStops(null);
                  }}
                  // cmdk keeps Enter for the lit row (and stops the form's own
                  // submit), so with nothing found Enter searches from here.
                  onKeyDown={(e) => {
                    if (e.key !== "Enter" || e.nativeEvent.isComposing || e.keyCode === 229) return;
                    if (!stops?.length && q.trim()) void look({ q: q.trim() });
                  }}
                  placeholder="정류장·역 이름"
                  aria-label="정류장이나 역 이름"
                  enterKeyHint="search"
                  wrapperClassName="min-w-0 flex-1"
                >
                  <InputGroupAddon align="inline-end">
                    <InputGroupButton type="submit" size="icon-sm" aria-label="찾기" className="size-11 text-dim hover:text-foreground mouse:size-7">
                      <Search aria-hidden strokeWidth={1.8} />
                    </InputGroupButton>
                  </InputGroupAddon>
                </CommandInput>
              </div>
              {/* What a search came to is said as it comes, to one who cannot see the list arrive. */}
              {busy === "stops" ? (
                <p role="status" className="mt-3 text-faint">
                  찾는 중…
                </p>
              ) : null}
              {unfound ? (
                <p role="alert" className="mt-3 text-aside leading-[1.55] break-keep text-reject">
                  {unfound}
                </p>
              ) : null}
              {stops ? (
                stops.length ? (
                  <>
                    {stops.every((s) => s.like) ? (
                      <p role="status" className="mt-3 text-aside leading-[1.55] break-keep text-dim">
                        그 이름의 정류장은 없어서 이름의 일부로 찾았어요.
                      </p>
                    ) : null}
                    <CommandList className="mt-2 *:divide-y *:divide-edge-soft">
                      {places(stops).slice(0, 40).map((p) => (
                        <CommandItem key={p.key} value={p.key} onSelect={() => void pick(p)} className="rounded-none px-0 py-2.5">
                          {p.stops.length === 1 ? <StopLabel stop={p.stops[0]} /> : <PlaceLabel place={p} />}
                        </CommandItem>
                      ))}
                    </CommandList>
                  </>
                ) : (
                  <p role="status" className="mt-3 text-dim">
                    찾은 정류장이 없어요. 다른 이름으로 찾아 보세요.
                  </p>
                )
              ) : null}
              {missed ? <p className="mt-2 text-aside leading-[1.55] break-keep text-dim">{missed}</p> : null}
            </Command>
          </form>
        )}
      </Step>

      {place ? (
        <Step title="무엇을 타요">
          {stop && line ? (
            <Picked
              onChange={() => {
                setLine(null);
                if (!choices) void loadChoices(place);
              }}
            >
              <span className="shrink-0 text-body font-medium text-foreground">{line.name}</span>
              <span className="min-w-0 truncate text-dim">{line.toward}</span>
            </Picked>
          ) : (
            <>
              {busy === "lines" ? (
                <p role="status" className="text-faint">
                  노선을 불러오는 중…
                </p>
              ) : unread?.all ? (
                <Failed quiet onRetry={() => loadChoices(place)}>
                  {unread.why}
                </Failed>
              ) : choices && choices.length ? (
                <>
                  {sides(place) > 1 ? (
                    <p role="status" className="mb-1 text-aside leading-[1.55] break-keep text-faint">
                      이름이 같은 정류장 {sides(place)}곳의 노선이에요. 타는 방향을 고르면 그쪽 정류장으로 정해져요 - 끝의 번호는 정류장 표지판의 번호예요.
                    </p>
                  ) : null}
                  <ul className="divide-y divide-edge-soft">
                    {choices.map((c) => (
                      <li key={`${c.stop.region ?? ""}:${c.stop.id}:${c.line.id}:${c.line.order ?? ""}`}>
                        <button type="button" onClick={() => choose(c)} className="flex w-full items-baseline gap-2 py-2.5 text-left hover:bg-glass focus-ring">
                          <span className="shrink-0 text-body font-medium text-foreground">{c.line.name}</span>
                          <span className="min-w-0 flex-1 truncate text-dim">{c.line.toward}</span>
                          {sides(place) > 1 && c.stop.no ? <span className="tnum shrink-0 text-aside text-faint">{c.stop.no}</span> : null}
                        </button>
                      </li>
                    ))}
                  </ul>
                  {unread ? (
                    <Failed quiet onRetry={() => loadChoices(place)} className="mt-2">
                      일부 정류장의 노선은 불러오지 못했어요.
                    </Failed>
                  ) : null}
                </>
              ) : choices ? (
                <p role="status" className="text-dim">
                  이 정류장을 지나는 노선이 없어요. 다른 정류장을 골라 보세요.
                </p>
              ) : null}
              {/* Changing a route's line, the one it had stays a press away: the
                  lines may not come, and 경로 저장 waits on one. */}
              {route && place.stops.some((s) => s.kind === route.stop.kind && s.region === route.stop.region && s.id === route.stop.id) ? (
                <button
                  type="button"
                  onClick={() => {
                    setStop(route.stop);
                    setLine(route.line);
                  }}
                  className="tap-y mt-3 text-aside text-dim underline-offset-2 hover:text-foreground hover:underline focus-ring"
                >
                  원래 노선으로
                </button>
              ) : null}
            </>
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
            <p className="mt-2 text-aside text-faint">
              {stop.dist ? `지금 위치에서 ${stop.dist}m, 걸어서 ${walkMinutes(stop.dist)}분쯤이에요. ` : ""}나갈 때는 도착 시각에서 이만큼 앞이에요.
            </p>
          </Step>

          <Step title="이름과 알림">
            <div ref={names} className="flex flex-wrap gap-1.5">
              {/* The chip lit is the name written; the field takes any other. */}
              <ToggleGroup
                type="single"
                aria-label="이름"
                variant="chip"
                tone="mine"
                size="chip"
                spacing={1.5}
                value={NAMES.includes(name) ? name : ""}
                onValueChange={(v) => {
                  if (v) setName(v);
                }}
              >
                {NAMES.map((n) => (
                  <ToggleGroupItem key={n} value={n} className="h-11 px-3.5 text-ui mouse:h-7 mouse:px-3">
                    {n}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
              <Input
                surface="glass"
                value={NAMES.includes(name) ? "" : name}
                onChange={(e) => setName(e.target.value)}
                placeholder="다른 이름"
                aria-label="경로 이름"
                maxLength={20}
                className="h-11 w-28 rounded-full px-3.5 mouse:h-7"
              />
            </div>
            {unnamed && !name.trim() ? (
              <p role="alert" className="mt-1.5 text-aside text-reject">
                이름을 고르거나 적어 주세요.
              </p>
            ) : null}
            <ToggleGroup
              type="multiple"
              aria-label="요일"
              variant="chip"
              tone="mine"
              size="chip"
              spacing={1}
              value={days.map(String)}
              onValueChange={(v) => setDays(v.map(Number).sort())}
              // Seven of a thumb's 44px fit a 375px phone only this close together.
              className="mt-3 flex-wrap mouse:gap-1.5"
            >
              {DAY_NAMES.map((d, i) => (
                <ToggleGroupItem key={d} value={String(i + 1)} className="size-11 px-0 text-ui mouse:h-7 mouse:w-8">
                  {d}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            {/* No day lit reads as no day, and is every day: one who turned
                them all off to quiet the alert had it at the weekend too. */}
            {days.length === 0 ? (
              <p className="mt-1.5 text-aside leading-[1.55] break-keep text-faint">
                요일을 고르지 않으면 매일이에요.{push && windowSet ? " 알림을 끄려면 아래 스위치를 꺼 주세요." : ""}
              </p>
            ) : null}
            <div className="mt-3 flex items-center gap-2 text-dim">
              <Input surface="glass" type="time" value={from} onChange={(e) => setFrom(e.target.value)} aria-label="시간대 시작" className="tnum w-auto" />
              부터
              <Input surface="glass" type="time" value={to} onChange={(e) => setTo(e.target.value)} aria-label="시간대 끝" className="tnum w-auto" />
              까지
            </div>
            <SwitchRow
              checked={push && windowSet}
              disabled={!windowSet}
              onCheckedChange={setPush}
              label="이 시간대에 나갈 때를 알림으로 알려 주기"
              className="mt-2"
            />
            <p className="mt-1.5 text-aside leading-[1.55] break-keep text-faint">
              {windowSet
                ? "나가기 15분 전에 ‘슬슬 짐 싸야 해요’, 2분 전에 ‘지금 나가요’를 보내요. 방해 금지 시간에도 울려요."
                : "시간대를 정하면 알림을 켤 수 있어요."}
            </p>
          </Step>
        </>
      ) : null}

      {error ? (
        <p role="alert" className="text-aside text-reject">
          {error}
        </p>
      ) : null}

      <div className="flex items-center gap-2">
        <Button variant="raised" size="touch" onClick={() => void save()} disabled={!stop || !line || busy === "save" || busy === "delete"} className="flex-1">
          {busy === "save" ? "저장하는 중…" : "경로 저장"}
        </Button>
        {onDelete && route ? (
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="reject" size="touch-sm" disabled={busy === "save" || busy === "delete"}>
                {busy === "delete" ? "지우는 중…" : "지우기"}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{route.name} 경로를 지울까요?</AlertDialogTitle>
                <AlertDialogDescription>이 경로의 출발 시각과 알림이 함께 사라져요. 되돌릴 수 없어요.</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>두기</AlertDialogCancel>
                <AlertDialogAction
                  onClick={() => {
                    setBusy("delete");
                    void onDelete().catch((e) => {
                      setError(failureText(e, SAY.couldNotDelete));
                      setBusy(null);
                    });
                  }}
                >
                  지우기
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        ) : null}
      </div>
    </div>
  );
}

function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h3 className="mb-2 text-ui font-medium text-foreground">{title}</h3>
      {children}
    </section>
  );
}

function Picked({ children, onChange }: { children: ReactNode; onChange: () => void }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-3">{children}</div>
      <button
        type="button"
        onClick={onChange}
        className="tap-y -mx-2 shrink-0 px-2 text-aside text-dim underline-offset-2 hover:text-foreground hover:underline focus-ring mouse:mx-0 mouse:px-0 mouse:before:hidden"
      >
        바꾸기
      </button>
    </div>
  );
}

/** A place of several stops: its name, and that the way one goes chooses among them. */
function PlaceLabel({ place }: { place: Place }) {
  const n = sides(place);
  const region = place.stops.every((s) => s.region === "seoul") ? "서울" : place.stops.every((s) => s.region === "gyeonggi") ? "경기" : "서울·경기";
  return (
    <>
      <BusFront aria-hidden className="size-[18px] shrink-0 text-dim" strokeWidth={1.7} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-body text-foreground">{place.name}</span>
        <span className="block text-aside text-faint">
          {n > 1 ? `${region} 버스 · 정류장 ${n}곳, 타는 방향으로 골라요` : `${region} 버스${place.stops[0].no ? ` · ${place.stops[0].no}` : ""}`}
        </span>
        <Renamed was={place.stops.find((s) => s.was)?.was} />
      </span>
      {place.dist ? <span className="tnum shrink-0 text-aside text-faint">{place.dist >= 1000 ? `${(place.dist / 1000).toFixed(1)}km` : `${place.dist}m`}</span> : null}
    </>
  );
}

/** A stop found by what it used to be called: why a name one did not type is here. */
function Renamed({ was }: { was?: string }) {
  return was ? <span className="block text-aside text-dim">‘{was}’의 지금 이름이에요</span> : null;
}

function StopLabel({ stop }: { stop: Stop }) {
  const Icon = stop.kind === "subway" ? TrainFront : BusFront;
  const sub = stop.kind === "subway" ? "지하철" : `${stop.region === "seoul" ? "서울" : "경기"} 버스${stop.no ? ` · ${stop.no}` : ""}`;
  return (
    <>
      <Icon aria-hidden className="size-[18px] shrink-0 text-dim" strokeWidth={1.7} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-body text-foreground">{stop.name}</span>
        <span className="block text-aside text-faint">{sub}</span>
        <Renamed was={stop.was} />
      </span>
      {stop.dist ? <span className="tnum shrink-0 text-aside text-faint">{stop.dist >= 1000 ? `${(stop.dist / 1000).toFixed(1)}km` : `${stop.dist}m`}</span> : null}
    </>
  );
}
