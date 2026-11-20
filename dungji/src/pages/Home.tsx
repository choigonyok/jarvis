import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { itemDef, plantDef } from "../../shared/catalog";
import type { HomeResponse, HomeView, ItemView, Me, Reward } from "../../shared/api";
import { fitsFloor, fitsWall, type Placed } from "../../shared/rules";
import { api, ApiError, get } from "../lib/api";
import { useCall } from "../lib/call";
import { EMBEDDED, tellParent } from "../lib/embed";
import { useLive } from "../lib/live";
import { go, goBackTo, linkTo, useLocation } from "../lib/router";
import { daysSince, phaseOf, todayKey } from "../lib/time";
import { useToast } from "../lib/toast";
import { josa } from "../lib/josa";
import { Room, SKY, type MoveTo } from "../room/Room";
import { Feathers, Icon } from "../ui/icons";
import { afterSheets } from "../ui/sheet";
import { DiaryPanel } from "./Diary";
import { GuestbookPanel } from "./Guestbook";
import { NeighborsPanel } from "./Neighbors";
import { CareSheet, ItemSheet, LedgerSheet, NameSheet, SettingsSheet, ShopSheet, StorageSheet, type ShopTab } from "./sheets";

type Tab = "room" | "diary" | "guestbook" | "neighbors";

type SheetState =
  | null
  | { kind: "shop"; tab?: ShopTab; tankId?: string }
  | { kind: "storage" }
  | { kind: "care"; id: string }
  | { kind: "item"; id: string }
  | { kind: "settings" }
  | { kind: "ledger" };

export interface HomeActions {
  /** 서버에 쓰고 돌아온 둥지로 화면을 바꾼다. 실패하면 이유를 알리고 null. */
  act: (path: string, body?: unknown, method?: "POST" | "PATCH" | "DELETE") => Promise<HomeResponse | null>;
  openSheet: (s: SheetState) => void;
  /** 방에 놓인 물건 하나를 고른 채 꾸미기로 들어간다(산 물건, 창고에서 꺼낸 물건, '옮기기'). */
  arrange: (itemId: string) => void;
}

/** 방에 놓인 물건들(자리 검사용) */
function placedIn(home: HomeView): Placed[] {
  return home.items.filter((i) => i.place === "room").map((i) => ({ id: i.id, key: i.key, x: i.x, y: i.y, flip: i.flip, wall: i.wall }));
}

/** 이 기기에서 그 칸(방명록·일기)을 마지막으로 본 때 */
function seenAt(homeId: string, what: string) {
  try {
    return Number(localStorage.getItem(`dungji:seen:${homeId}:${what}`) ?? 0);
  } catch {
    return 0;
  }
}
function markSeen(homeId: string, what: string) {
  try {
    localStorage.setItem(`dungji:seen:${homeId}:${what}`, String(Date.now()));
  } catch {
    // 저장소를 못 쓰면 점이 남을 뿐
  }
}

export function HomePage({ slug, me, onMe }: { slug: string; me: Me; onMe: (m: Me | null) => void }) {
  const loc = useLocation();
  const live = useLive();
  const call = useCall();
  const toast = useToast();
  const [home, setHome] = useState<HomeView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [sheet, setSheet] = useState<SheetState>(null);
  const [poofs, setPoofs] = useState<{ key: string; x: number; y: number }[]>([]);
  const [now, setNow] = useState(() => new Date());
  const [stamps, setStamps] = useState({ diary: 0, guestbook: 0 });
  const [, setSeenTick] = useState(0);
  const busy = useRef(false);
  // 옮기기는 화면에서 먼저 옮기고 서버에는 차례로 보낸다: 그 줄, 보낸 수, 마지막 것의 번호, 실패했는지
  const moves = useRef<Promise<void>>(Promise.resolve());
  const moving = useRef(0);
  const moveSeq = useRef(0);
  const moveFailed = useRef(false);
  const told = useRef(false);
  const roomRef = useRef<HTMLDivElement>(null);
  const navRef = useRef<HTMLElement>(null);
  const barRef = useRef<HTMLDivElement>(null);
  const [framing, setFraming] = useState(0);

  // 꾸미기를 시작하면 방과 그 아래 옮기기 줄이 한 화면에 들어오게 옮긴다(이미 보이면 그대로).
  // 둘 다 들어가지 않는 작은 화면이면 방 위쪽을 메뉴 줄 바로 아래에 맞추고, 줄은 내려서 본다.
  useEffect(() => {
    if (!framing) return;
    afterSheets(() => {
      const room = roomRef.current?.getBoundingClientRect();
      if (!room) return;
      const top = (navRef.current?.getBoundingClientRect().bottom ?? 0) + 8;
      const bottom = (barRef.current?.getBoundingClientRect().bottom ?? room.bottom) + 12;
      const need = bottom - window.innerHeight;
      const most = room.top - top;
      const dy = need > most ? most : Math.min(Math.max(0, need), most);
      if (Math.abs(dy) > 1) window.scrollBy({ top: dy, behavior: "smooth" });
    });
  }, [framing]);

  const tabParam = loc.query.get("tab") as Tab | null;
  const owner = home?.owner ?? slug === me.home;
  const tabs: Tab[] = owner ? ["room", "diary", "guestbook", "neighbors"] : ["room", "guestbook"];
  const tab: Tab = tabParam && tabs.includes(tabParam) ? tabParam : "room";

  const showRewards = useCallback(
    (rewards: Reward[]) => rewards.forEach((r) => toast.feathers(r.delta, r.reason)),
    [toast],
  );

  const load = useCallback(async () => {
    try {
      const r = await get<HomeResponse>(`/homes/${encodeURIComponent(slug)}`);
      setHome(r.home);
      setError(null);
      showRewards(r.rewards);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "둥지를 불러오지 못했어요.");
    }
  }, [slug, showRewards]);

  useEffect(() => {
    void load();
  }, [load]);

  // 콘솔이 '여는 중'을 이때 걷는다(빈 화면이 보이지 않게).
  useEffect(() => {
    if ((home || error) && !told.current) {
      told.current = true;
      tellParent({ type: "dungji:ready" });
    }
  }, [home, error]);

  // 시계·하늘은 1분마다
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(t);
  }, []);

  // 상대가 바꾼 것은 바로 다시 읽는다(우리 둥지만 실시간 통로가 있다).
  useEffect(() => {
    if (!owner) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const off = live.on((m) => {
      if (m.type !== "changed" || m.by === me.id) return;
      if (m.what === "home") {
        // 내가 쓰는 중이면 끝난 뒤에 읽는다(그사이 화면에 먼저 옮긴 물건이 되돌아가 보이지 않게)
        const reload = () => (busy.current || moving.current ? (timer = setTimeout(reload, 400)) : void load());
        clearTimeout(timer);
        timer = setTimeout(reload, 250);
      } else {
        setStamps((s) => ({ ...s, [m.what]: Date.now() }));
        void load();
      }
    });
    return () => {
      off();
      clearTimeout(timer);
    };
  }, [owner, live, me.id, load]);

  // 다시 화면으로 돌아오면 그사이 쌓인 먼지·마른 화분을 본다.
  useEffect(() => {
    const on = () => document.visibilityState === "visible" && void load();
    document.addEventListener("visibilitychange", on);
    return () => document.removeEventListener("visibilitychange", on);
  }, [load]);

  // 상대가 새로 들어왔으면 통화 상대를 알기 위해 내 정보를 다시 읽는다.
  useEffect(() => {
    if (home?.owner && home.members.length === 2 && !me.partner)
      void get<{ me: Me | null }>("/me").then((r) => r.me && onMe(r.me));
  }, [home, me.partner, onMe]);

  // 방명록·일기 칸을 열면 그 칸의 새 글 점이 꺼진다.
  useEffect(() => {
    if (!home?.owner) return;
    if (tab === "guestbook" || tab === "diary") {
      markSeen(home.id, tab);
      setSeenTick((n) => n + 1);
    }
  }, [tab, home?.owner, home?.id, home?.news]);

  const act = useCallback<HomeActions["act"]>(
    async (path, body, method = "POST") => {
      if (busy.current) return null;
      busy.current = true;
      try {
        await moves.current; // 먼저 놓은 물건이 서버에 닿은 뒤에
        const r = await api<HomeResponse>(method, path, method === "DELETE" ? undefined : (body ?? {}));
        setHome(r.home);
        showRewards(r.rewards);
        return r;
      } catch (e) {
        toast.show(e instanceof ApiError ? e.message : "잠깐 문제가 생겼어요.");
        if (e instanceof ApiError && e.code === "busy") void load();
        return null;
      } finally {
        busy.current = false;
      }
    },
    [load, showRewards, toast],
  );

  /** 물건을 옮긴다: 화면은 바로 바뀌고(놓자마자 제자리로 튀지 않게), 서버에는 놓은 차례대로 간다. */
  const move = useCallback(
    (item: ItemView, to: MoveTo) => {
      setHome((h) => h && { ...h, items: h.items.map((i) => (i.id === item.id ? { ...i, x: to.x, y: to.y, flip: to.flip, wall: to.wall } : i)) });
      const seq = ++moveSeq.current;
      moving.current++;
      moves.current = moves.current.then(async () => {
        try {
          const r = await api<HomeResponse>("POST", `/items/${item.id}/move`, to);
          // 그사이 또 옮겼으면 마지막 답만 쓴다(중간 답으로 잠깐 되돌아가 보이지 않게)
          if (seq === moveSeq.current) setHome(r.home);
        } catch (e) {
          moveFailed.current = true;
          toast.show(e instanceof ApiError ? e.message : "옮기지 못했어요.");
        } finally {
          moving.current--;
          if (!moving.current && moveFailed.current) {
            moveFailed.current = false;
            void load();
          }
        }
      });
    },
    [load, toast],
  );

  const arrange = useCallback((itemId: string) => {
    setSheet(null);
    setEditing(true);
    setSelected(itemId);
    setFraming((n) => n + 1);
  }, []);

  const actions: HomeActions = useMemo(() => ({ act, openSheet: setSheet, arrange }), [act, arrange]);

  const setTab = (t: Tab) => {
    setEditing(false);
    setSelected(null);
    go(t === "room" ? `/h/${slug}` : `/h/${slug}?tab=${t}`, { replace: true });
    window.scrollTo({ top: 0 });
  };

  if (error)
    return (
      <div className="grid min-h-dvh place-items-center p-6 text-center">
        <div>
          <p className="text-heading font-semibold">{error}</p>
          <a className="btn btn-approve mt-4" {...linkTo(`/h/${me.home}`)}>
            우리 둥지로
          </a>
        </div>
      </div>
    );
  if (!home) return <HomeSkeleton />;

  const phase = phaseOf(now);
  const onlineIds = owner ? live.online : home.members.filter((m) => m.online).map((m) => m.id);
  const partner = owner ? home.members.find((m) => m.id !== me.id) : undefined;
  const partnerOnline = Boolean(partner && onlineIds.includes(partner.id));
  const storageUsed = home.items.filter((i) => i.place === "storage").length;
  const selectedItem = home.items.find((i) => i.id === selected);
  const fresh = (what: "guestbook" | "diary") => Boolean(home.news?.[what] && home.news[what]! > seenAt(home.id, what));

  async function clean(id: string) {
    if (!home) return;
    if (!owner && (home.helpLeft ?? 0) <= 0) {
      toast.show("오늘은 이 방을 충분히 도와줬어요. 내일 또 와 주세요.");
      return;
    }
    const spot = home.dirt.find((d) => d.id === id);
    if (spot) {
      const key = `${id}-${Date.now()}`;
      setPoofs((p) => [...p, { key, x: spot.x, y: spot.y }]);
      setTimeout(() => setPoofs((p) => p.filter((x) => x.key !== key)), 700);
      setHome({ ...home, dirt: home.dirt.filter((d) => d.id !== id) });
    }
    await act(`/homes/${encodeURIComponent(home.slug)}/dirt/${id}/clean`);
  }

  function tapItem(item: ItemView) {
    const def = itemDef(item.key);
    if (!def) return;
    if (owner) {
      setSheet(def.kind === "plant" || def.kind === "tank" ? { kind: "care", id: item.id } : { kind: "item", id: item.id });
      return;
    }
    if (item.plant) {
      const p = plantDef(item.key)!;
      toast.show(item.plant.ready ? `${def.name}에서 ${josa(p.harvest, "을", "를")} 거둘 때예요` : `${def.name}: ${stageName(item.key, item.plant.stage)}`);
    } else if (item.tank) toast.show(`${def.name}: 물고기 ${item.tank.fish.length}마리`);
    else toast.show(def.name);
  }

  /** 꾸미기의 화살표: 바닥은 칸 하나, 벽 장식은 벽을 따라 하나 */
  function nudge(dx: number, dy: number) {
    if (!home || !selectedItem) return;
    const def = itemDef(selectedItem.key)!;
    const room = placedIn(home);
    if (def.kind === "wall") {
      const pos = selectedItem.x + dx;
      if (!fitsWall(room, def, selectedItem.wall ?? "right", pos, selectedItem.id)) return toast.show("그쪽은 막혀 있어요");
      move(selectedItem, { x: pos, y: 0, flip: false, wall: selectedItem.wall });
      return;
    }
    const x = selectedItem.x + dx;
    const y = selectedItem.y + dy;
    if (!fitsFloor(room, def, x, y, selectedItem.flip, selectedItem.id)) return toast.show("그쪽은 막혀 있어요");
    move(selectedItem, { x, y, flip: selectedItem.flip, wall: null });
  }

  /** 꾸미기의 돌리기: 그 자리에서 앞뒤를 바꾼다(넓이가 바뀌어 다른 물건과 겹치면 못 돌린다). */
  function flip() {
    if (!home || !selectedItem) return;
    const def = itemDef(selectedItem.key)!;
    if (!fitsFloor(placedIn(home), def, selectedItem.x, selectedItem.y, !selectedItem.flip, selectedItem.id))
      return toast.show("돌리면 다른 물건과 겹쳐요. 조금 옮긴 뒤 돌려 주세요");
    move(selectedItem, { x: selectedItem.x, y: selectedItem.y, flip: !selectedItem.flip, wall: null });
  }

  function switchWall() {
    if (!home || !selectedItem) return;
    const def = itemDef(selectedItem.key)!;
    const wall = selectedItem.wall === "left" ? "right" : "left";
    const room = placedIn(home);
    for (let pos = 0; pos + def.w <= 7; pos++)
      if (fitsWall(room, def, wall, pos, selectedItem.id)) return move(selectedItem, { x: pos, y: 0, flip: false, wall });
    toast.show("저쪽 벽에는 자리가 없어요");
  }

  const startEditing = () => {
    setEditing(true);
    setFraming((n) => n + 1);
  };

  const visitorsLine =
    home.visits.today > 0 ? `오늘 이웃 ${home.visits.today}명이 다녀갔어요` : "오늘은 아직 다녀간 이웃이 없어요";

  const tools = owner ? (
    <div className="flex shrink-0 items-center gap-1">
      {home.coins !== undefined && (
        <button
          type="button"
          onClick={() => setSheet({ kind: "ledger" })}
          className="focus-ring flex h-11 items-center rounded-full bg-glass px-3.5 text-ui font-medium"
          aria-label={`깃털 ${home.coins}개, 내역 보기`}
        >
          <Feathers n={home.coins} />
        </button>
      )}
      <button type="button" className="icon-btn" onClick={() => setSheet({ kind: "settings" })} aria-label="둥지 설정">
        <Icon name="gear" />
      </button>
    </div>
  ) : null;

  return (
    <div className="min-h-dvh pb-10">
      {/* 맨 위 낱말 줄: 이 둥지의 칸들(콘솔 머리글처럼 지금 칸은 굵게). 손님은 앞에 '우리 둥지'로 돌아가는 길. */}
      <nav
        ref={navRef}
        className="sticky top-0 z-30 border-b border-edge-soft bg-background pt-[env(safe-area-inset-top)]"
        aria-label="둥지 메뉴"
      >
        <div className="mx-auto flex h-12 max-w-page-wide items-center gap-1 px-2 lg:px-6">
          {!EMBEDDED && owner && <span className="px-2 text-title font-semibold">둥지</span>}
          {!owner && (
            <a
              className="focus-ring inline-flex h-11 items-center gap-0.5 rounded-lg pr-2 pl-1 text-title text-dim"
              href={`/h/${me.home}`}
              onClick={(e) => {
                if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
                e.preventDefault();
                goBackTo(`/h/${me.home}`);
              }}
            >
              <Icon name="back" className="size-4" />
              우리 둥지
            </a>
          )}
          {tabs.map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => setTab(t)}
              aria-current={tab === t ? "page" : undefined}
              className={`focus-ring relative h-11 min-w-11 rounded-lg px-2.5 text-title ${tab === t ? "font-semibold text-foreground" : "text-dim"}`}
            >
              {TAB_NAMES[t](owner)}
              {(t === "guestbook" || t === "diary") && owner && tab !== t && fresh(t) && (
                <span className="absolute top-2.5 right-0.5 size-[6px] rounded-full bg-partner" aria-label="새 글" />
              )}
            </button>
          ))}
        </div>
      </nav>

      <main className="mx-auto max-w-page-wide px-4 pt-4 lg:grid lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)] lg:items-start lg:gap-10 lg:px-8">
        {/* 방 */}
        <section className={`${tab === "room" ? "" : "hidden lg:block"} lg:sticky lg:top-16`} aria-label="방">
          <Nameplate home={home} onlineIds={onlineIds} meId={me.id} tools={tools} />
          <div ref={roomRef} className="sky relative mt-4 overflow-hidden rounded-2xl px-2 pt-5 pb-3" style={{ background: SKY[phase] }}>
            {phase === "night" && <Stars />}
            <Room
              home={home}
              phase={phase}
              now={now}
              editing={editing}
              selected={selected}
              onSelect={setSelected}
              onMove={move}
              onDirt={clean}
              onItem={tapItem}
              onBlocked={() => toast.show("다른 물건이 있는 자리라 놓지 못했어요")}
              onLift={
                owner
                  ? (id) => {
                      setEditing(true);
                      setSelected(id);
                    }
                  : undefined
              }
              poofs={poofs}
              showNeeds={owner}
              label={`${home.name}의 방. 청결 ${home.cleanliness}퍼센트, 먼지 ${home.dirt.length}개`}
            />
          </div>

          {/* 방 아래 */}
          {editing ? (
            <div ref={barRef}>
              <EditBar
                item={selectedItem}
                onNudge={nudge}
                onSwitchWall={switchWall}
                onFlip={flip}
                onStore={async () => {
                  if (!selectedItem) return;
                  const r = await act(`/items/${selectedItem.id}/store`);
                  if (r) setSelected(null);
                }}
                onDone={() => {
                  setEditing(false);
                  setSelected(null);
                }}
              />
            </div>
          ) : (
            <div className="mt-3 space-y-3">
              <div className="flex items-center gap-3 rounded-xl bg-glass px-4 py-3">
                <CleanMeter value={home.cleanliness} spots={home.dirt.length} />
                {owner && partner && (
                  <button
                    type="button"
                    onClick={() => (partnerOnline ? call.start() : toast.show(`${partner.name}님이 들어오면 통화할 수 있어요`))}
                    aria-disabled={!partnerOnline || call.state.s !== "idle"}
                    className={`btn h-11 shrink-0 px-3.5 ${partnerOnline ? "btn-approve" : "btn-soft"}`}
                    aria-label={partnerOnline ? `${partner.name}님과 통화하기` : `통화: ${partner.name}님이 나가 있어요`}
                  >
                    <Icon name="phone" className="size-[18px]" />
                    통화
                  </button>
                )}
              </div>
              {owner ? (
                <div className="grid grid-cols-3 gap-2">
                  <button type="button" className="btn btn-raised h-14 flex-col gap-0.5 px-1 text-ui" onClick={startEditing}>
                    <Icon name="decorate" className="size-[18px]" />
                    꾸미기
                  </button>
                  <button type="button" className="btn btn-raised h-14 flex-col gap-0.5 px-1 text-ui" onClick={() => setSheet({ kind: "storage" })}>
                    <Icon name="box" className="size-[18px]" />
                    <span className="tnum">
                      창고 {storageUsed}/{home.storageSlots}
                    </span>
                  </button>
                  <button type="button" className="btn btn-raised h-14 flex-col gap-0.5 px-1 text-ui" onClick={() => setSheet({ kind: "shop" })}>
                    <Icon name="bag" className="size-[18px]" />
                    상점
                  </button>
                </div>
              ) : (
                <p className="rounded-xl bg-glass px-4 py-3 text-body text-dim">
                  {(home.helpLeft ?? 0) > 0 && home.dirt.length > 0
                    ? `먼지를 누르면 청소를 도와줄 수 있어요. 오늘 ${home.helpLeft}개 더 치워 줄 수 있어요.`
                    : home.dirt.length === 0
                      ? "깨끗한 방이에요. 아래 방명록에 한마디 남겨 보세요."
                      : "오늘은 충분히 도와줬어요. 아래 방명록에 한마디 남겨 보세요."}
                </p>
              )}
            </div>
          )}
        </section>

        {/* 칸의 내용 */}
        <section className={`${tab === "room" ? "mt-6 lg:mt-0" : ""} space-y-6`}>
          {tab === "room" && (
            <>
              {owner && (
                <CarePanel
                  home={home}
                  actions={actions}
                  onDiary={() => setTab("diary")}
                  onRoom={() => roomRef.current?.scrollIntoView({ block: "center", behavior: "smooth" })}
                  meId={me.id}
                />
              )}
              {!owner && <GuestbookPanel home={home} me={me} stamp={stamps.guestbook} compact />}
              <VisitorsToday home={home} line={visitorsLine} />
            </>
          )}
          {tab === "diary" && owner && <DiaryPanel me={me} home={home} stamp={stamps.diary} onRewards={showRewards} />}
          {tab === "guestbook" && <GuestbookPanel home={home} me={me} stamp={stamps.guestbook} />}
          {tab === "neighbors" && owner && <NeighborsPanel />}
        </section>
      </main>

      {sheet?.kind === "shop" && <ShopSheet home={home} actions={actions} initialTab={sheet.tab} tankId={sheet.tankId} onClose={() => setSheet(null)} />}
      {sheet?.kind === "storage" && <StorageSheet home={home} actions={actions} onClose={() => setSheet(null)} />}
      {sheet?.kind === "care" && <CareSheet home={home} itemId={sheet.id} actions={actions} onClose={() => setSheet(null)} />}
      {sheet?.kind === "item" && <ItemSheet home={home} itemId={sheet.id} actions={actions} onClose={() => setSheet(null)} />}
      {sheet?.kind === "settings" && <SettingsSheet home={home} me={me} actions={actions} onHome={setHome} onMe={onMe} onClose={() => setSheet(null)} />}
      {sheet?.kind === "ledger" && <LedgerSheet onClose={() => setSheet(null)} />}
      {owner && !me.named && <NameSheet home={home} onSaved={() => void load()} onMe={onMe} />}
    </div>
  );
}

/** 처음 열 때 올 것의 자리(움직이지 않는다): 낱말 줄, 이름, 방, 줄 둘 */
function HomeSkeleton() {
  return (
    <div className="min-h-dvh" aria-busy="true" aria-label="둥지를 여는 중">
      <div className="h-12 border-b border-edge-soft" />
      <div className="mx-auto max-w-page-wide px-4 pt-5">
        <div className="h-8 w-44 rounded-lg bg-glass-raised" />
        <div className="mt-3 flex gap-2">
          <div className="h-8 w-20 rounded-full bg-glass" />
          <div className="h-8 w-20 rounded-full bg-glass" />
        </div>
        <div className="mt-5 aspect-[420/340] rounded-2xl bg-glass" />
        <div className="mt-3 h-16 rounded-xl bg-glass" />
        <div className="mt-3 grid grid-cols-3 gap-2">
          <div className="h-14 rounded-lg bg-glass" />
          <div className="h-14 rounded-lg bg-glass" />
          <div className="h-14 rounded-lg bg-glass" />
        </div>
      </div>
    </div>
  );
}

const TAB_NAMES: Record<Tab, (owner: boolean) => string> = {
  room: (owner) => (owner ? "우리 방" : "방"),
  diary: () => "일기",
  guestbook: () => "방명록",
  neighbors: () => "이웃",
};

export function stageName(key: string, stage: number) {
  const bloom = plantDef(key)?.harvest === "열매" ? "열매가 익었어요" : key === "plant_monstera" ? "새 잎이 났어요" : "꽃이 폈어요";
  return ["씨앗에서 싹이 났어요", "새싹이 자라요", "잎이 무성해요", key === "plant_monstera" ? "잎이 커지고 있어요" : "봉오리가 맺혔어요", bloom][stage] ?? "";
}

function Nameplate({ home, onlineIds, meId, tools }: { home: HomeView; onlineIds: string[]; meId: string; tools: ReactNode }) {
  return (
    <div>
      <div className="flex items-start gap-2">
        <h1 className="min-w-0 flex-1 truncate text-display leading-tight font-semibold tracking-tight">{home.name}</h1>
        {tools}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        {home.members.map((m) => {
          const on = onlineIds.includes(m.id);
          return (
            <span key={m.id} className="inline-flex h-8 items-center gap-1.5 rounded-full bg-glass px-3 text-ui" title={on ? "들어와 있어요" : "나가 있어요"}>
              <span className={`size-[7px] rounded-full ${on ? "bg-online" : "bg-faint/50"}`} />
              <span className="font-medium">{m.name}</span>
              {m.id === meId && <span className="text-dim">나</span>}
              <span className="sr-only">{on ? "들어와 있음" : "나가 있음"}</span>
            </span>
          );
        })}
        {home.since && <span className="tnum px-1.5 text-ui text-dim">함께한 지 {daysSince(home.since).toLocaleString("ko-KR")}일</span>}
      </div>
      {home.intro && <p className="mt-2 text-body text-ink">{home.intro}</p>}
    </div>
  );
}

function CleanMeter({ value, spots }: { value: number; spots: number }) {
  // 막대의 채움은 무채색, 치울 때가 가까우면 '곧'의 색(caution). 빨강은 반려·실패의 색이라 쓰지 않는다.
  const tone = value >= 60 ? "bg-foreground/70" : "bg-caution";
  const word = value >= 85 ? "반짝반짝" : value >= 60 ? "괜찮아요" : value >= 35 ? "먼지가 보여요" : "치울 때가 됐어요";
  return (
    <div className="min-w-0 flex-1">
      <div className="flex items-baseline justify-between gap-2 text-ui">
        <span className="tnum font-medium">청결 {value}%</span>
        <span className="truncate text-dim">{spots > 0 ? `${word}, 먼지 ${spots}개` : word}</span>
      </div>
      <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-glass-raised" role="meter" aria-valuenow={value} aria-valuemin={0} aria-valuemax={100} aria-label="청결도">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.max(4, value)}%` }} />
      </div>
    </div>
  );
}

function EditBar({
  item,
  onNudge,
  onSwitchWall,
  onFlip,
  onStore,
  onDone,
}: {
  item?: ItemView;
  onNudge: (dx: number, dy: number) => void;
  onSwitchWall: () => void;
  onFlip: () => void;
  onStore: () => void;
  onDone: () => void;
}) {
  const def = item ? itemDef(item.key) : undefined;
  const wall = def?.kind === "wall";
  // 화면에서 칸이 늘어선 방향: ↖ 은 x 를 하나 줄이고, ↗ 은 y 를 하나 줄인다(방이 비스듬해서).
  const arrows: [string, string, number, number][] = wall
    ? [
        // 두 벽 모두 벽 좌표가 화면 오른쪽으로 늘어난다(src/room/iso.tsx 의 벽 틀)
        ["back", "벽을 따라 왼쪽으로", -1, 0],
        ["forward", "벽을 따라 오른쪽으로", 1, 0],
      ]
    : [
        ["upLeft", "왼쪽 위로", -1, 0],
        ["upRight", "오른쪽 위로", 0, -1],
        ["downLeft", "왼쪽 아래로", 0, 1],
        ["downRight", "오른쪽 아래로", 1, 0],
      ];
  return (
    <div className="mt-3 rounded-xl bg-glass p-3">
      <p className="px-1 text-ui text-dim">
        {def ? (
          <>
            <b className="font-medium text-ink">{josa(def.name, "을", "를")}</b> 끌거나 화살표로 옮겨요.
          </>
        ) : (
          "물건을 끌어서 옮기거나, 눌러 고른 뒤 화살표로 옮겨요."
        )}
      </p>
      {def && (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <div className="flex gap-1.5" role="group" aria-label="한 칸씩 옮기기">
            {arrows.map(([icon, label, dx, dy]) => (
              <button key={label} type="button" className="btn btn-raised size-11 min-h-0 px-0" onClick={() => onNudge(dx, dy)} aria-label={label}>
                <Icon name={icon} className="size-[18px]" />
              </button>
            ))}
          </div>
          {wall ? (
            <button type="button" className="btn btn-raised h-11 flex-1 px-3 text-ui" onClick={onSwitchWall}>
              다른 벽으로
            </button>
          ) : (
            <button type="button" className="btn btn-raised h-11 flex-1 px-3 text-ui" onClick={onFlip}>
              <Icon name="flip" className="size-[18px]" />
              돌리기
            </button>
          )}
        </div>
      )}
      <div className="mt-2 grid grid-cols-2 gap-2">
        {def ? (
          <button type="button" className="btn btn-raised h-11 px-2 text-ui" onClick={onStore}>
            <Icon name="box" className="size-[18px]" />
            창고에 넣기
          </button>
        ) : (
          <span />
        )}
        <button type="button" className="btn btn-approve h-11 px-2 text-ui" onClick={onDone}>
          <Icon name="check" className="size-[18px]" />
          다 꾸몄어요
        </button>
      </div>
    </div>
  );
}

function CarePanel({ home, actions, onDiary, onRoom, meId }: { home: HomeView; actions: HomeActions; onDiary: () => void; onRoom: () => void; meId: string }) {
  const [wroteToday, setWroteToday] = useState<boolean | null>(null);
  useEffect(() => {
    get<{ entries: { authorId: string }[] }>(`/diary/${todayKey()}`)
      .then((r) => setWroteToday(r.entries.some((e) => e.authorId === meId)))
      .catch(() => setWroteToday(null));
  }, [meId, home.id]);

  const tasks: { key: string; icon: string; text: string; action?: { label: string; run: () => void } }[] = [];
  for (const i of home.items) {
    if (i.place !== "room") continue;
    const def = itemDef(i.key);
    if (!def) continue;
    if (i.plant?.ready) {
      const p = plantDef(i.key)!;
      tasks.push({ key: `h-${i.id}`, icon: "sparkle", text: `${def.name}에서 ${josa(p.harvest, "을", "를")} 거둘 수 있어요`, action: { label: "거두기", run: () => void actions.act(`/items/${i.id}/harvest`) } });
    }
    if (i.plant && i.plant.thirsty)
      tasks.push({
        key: `w-${i.id}`,
        icon: "drop",
        text: i.plant.wilted ? `${josa(def.name, "이", "가")} 시들었어요` : i.plant.water < 30 ? `${josa(def.name, "이", "가")} 목말라요` : `${def.name} 흙이 말라 가요`,
        action: { label: "물 주기", run: () => void actions.act(`/items/${i.id}/water`) },
      });
    if (i.tank) {
      if (i.tank.fish.some((f) => f.hunger < 70))
        tasks.push({ key: `f-${i.id}`, icon: "fish", text: `${def.name}의 물고기들이 배고파해요`, action: { label: "먹이 주기", run: () => void actions.act(`/items/${i.id}/feed`) } });
      if (i.tank.clarity < 60)
        tasks.push({ key: `s-${i.id}`, icon: "drop", text: `${def.name} 물이 탁해졌어요`, action: { label: "물 갈기", run: () => void actions.act(`/items/${i.id}/scrub`) } });
      if (!i.tank.fish.length)
        tasks.push({ key: `e-${i.id}`, icon: "fish", text: `${josa(def.name, "이", "가")} 비어 있어요`, action: { label: "물고기 들이기", run: () => actions.openSheet({ kind: "shop", tab: "fish", tankId: i.id }) } });
    }
  }
  if (home.dirt.length) tasks.push({ key: "dirt", icon: "broom", text: `방에 먼지 ${home.dirt.length}개`, action: { label: "치우러 가기", run: onRoom } });
  if (wroteToday === false) tasks.push({ key: "diary", icon: "book", text: "오늘 일기를 아직 안 썼어요", action: { label: "쓰러 가기", run: onDiary } });

  return (
    <div>
      <h2 className="mb-2 text-title font-semibold">오늘 돌볼 것</h2>
      {tasks.length === 0 ? (
        <p className="rounded-xl bg-glass px-4 py-4 text-body text-dim">다 돌봤어요. 이웃 둥지에 놀러 가 볼까요?</p>
      ) : (
        <ul className="divide-y divide-edge-soft overflow-hidden rounded-xl bg-glass">
          {tasks.map((t) => (
            <li key={t.key} className="flex min-h-[60px] items-center gap-3 px-4 py-2.5">
              <Icon name={t.icon} className="size-5 shrink-0 text-dim" />
              <span className="min-w-0 flex-1 text-body">{t.text}</span>
              {t.action && (
                <button type="button" onClick={t.action.run} className="btn btn-pill h-11 shrink-0 px-3.5 text-ui">
                  {t.action.label}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function VisitorsToday({ home, line }: { home: HomeView; line: string }) {
  return (
    <div>
      <h2 className="mb-2 text-title font-semibold">오늘 놀러 온 이웃</h2>
      <div className="rounded-xl bg-glass px-4 py-3">
        <p className="text-body text-dim">
          {line}. 지금까지 {home.visits.total.toLocaleString("ko-KR")}명이 놀러 왔어요.
        </p>
        {home.visits.recent.length > 0 && (
          <ul className="mt-2 flex flex-wrap gap-2">
            {home.visits.recent.map((v, i) => (
              <li key={`${v.slug}-${i}`}>
                <a className="focus-ring inline-flex h-11 items-center gap-1 rounded-full bg-glass px-3.5 text-ui" {...linkTo(`/h/${v.slug}`)}>
                  <b>{v.name}</b>
                  <span className="text-dim">{v.home}</span>
                </a>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function Stars() {
  const stars = useMemo(
    () => Array.from({ length: 18 }, (_, i) => ({ x: (i * 53) % 100, y: (i * 29) % 38, r: i % 3 === 0 ? 1.6 : 1 })),
    [],
  );
  return (
    <svg className="pointer-events-none absolute inset-0 h-full w-full" aria-hidden>
      {stars.map((s, i) => (
        <circle key={i} cx={`${s.x}%`} cy={`${s.y}%`} r={s.r} fill="#F5EBC4" opacity={0.8} />
      ))}
    </svg>
  );
}
