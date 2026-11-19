import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { itemDef, plantDef } from "../../shared/catalog";
import type { HomeResponse, HomeView, ItemView, Me, Reward } from "../../shared/api";
import { api, ApiError, get } from "../lib/api";
import { useCall } from "../lib/call";
import { useLive } from "../lib/live";
import { go, linkTo, useLocation } from "../lib/router";
import { daysSince, phaseOf, todayKey } from "../lib/time";
import { useToast } from "../lib/toast";
import { josa } from "../lib/josa";
import { Room, SKY, type MoveTo } from "../room/Room";
import { Feathers, Icon } from "../ui/icons";
import { DiaryPanel } from "./Diary";
import { GuestbookPanel } from "./Guestbook";
import { NeighborsPanel } from "./Neighbors";
import { CareSheet, LedgerSheet, NameSheet, SettingsSheet, ShopSheet, StorageSheet, type ShopTab } from "./sheets";

type Tab = "room" | "diary" | "guestbook" | "neighbors";

/** 다른 화면(jarvis) 안에 띄워져 있나 */
const EMBEDDED = typeof window !== "undefined" && window.self !== window.top;
type SheetState = null | { kind: "shop"; tab?: ShopTab; tankId?: string } | { kind: "storage" } | { kind: "care"; id: string } | { kind: "settings" } | { kind: "ledger" };

export interface HomeActions {
  /** 서버에 쓰고 돌아온 둥지로 화면을 바꾼다. 실패하면 이유를 알린다. */
  act: (path: string, body?: unknown, method?: "POST" | "PATCH" | "DELETE") => Promise<HomeView | null>;
  openSheet: (s: SheetState) => void;
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
  const busy = useRef(false);

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
        clearTimeout(timer);
        timer = setTimeout(() => !busy.current && void load(), 250);
      } else setStamps((s) => ({ ...s, [m.what]: Date.now() }));
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

  const act = useCallback<HomeActions["act"]>(
    async (path, body, method = "POST") => {
      if (busy.current) return null;
      busy.current = true;
      try {
        const r = await api<HomeResponse>(method, path, method === "DELETE" ? undefined : (body ?? {}));
        setHome(r.home);
        showRewards(r.rewards);
        return r.home;
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

  const actions: HomeActions = useMemo(() => ({ act, openSheet: setSheet }), [act]);

  const setTab = (t: Tab) => go(t === "room" ? `/h/${slug}` : `/h/${slug}?tab=${t}`, { replace: true });

  if (error)
    return (
      <div className="grid min-h-dvh place-items-center p-6 text-center">
        <div>
          <p className="hand text-[28px] font-bold">{error}</p>
          <a className="btn btn-primary mt-4" {...linkTo(`/h/${me.home}`)}>
            우리 둥지로
          </a>
        </div>
      </div>
    );
  if (!home) return <div className="min-h-dvh" aria-busy="true" />;

  const phase = phaseOf(now);
  const onlineIds = owner ? live.online : home.members.filter((m) => m.online).map((m) => m.id);
  const partner = owner ? home.members.find((m) => m.id !== me.id) : undefined;
  const partnerOnline = Boolean(partner && onlineIds.includes(partner.id));
  const storageUsed = home.items.filter((i) => i.place === "storage").length;
  const selectedItem = home.items.find((i) => i.id === selected);

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
    if (owner && (def.kind === "plant" || def.kind === "tank")) {
      setSheet({ kind: "care", id: item.id });
      return;
    }
    if (item.plant) {
      const p = plantDef(item.key)!;
      toast.show(item.plant.ready ? `${def.name}에서 ${josa(p.harvest, "을", "를")} 거둘 때예요` : `${def.name}: ${stageName(item.key, item.plant.stage)}`);
    } else if (item.tank) toast.show(`${def.name}: 물고기 ${item.tank.fish.length}마리`);
    else toast.show(def.name);
  }

  async function move(item: ItemView, to: MoveTo) {
    await act(`/items/${item.id}/move`, to);
  }

  const visitorsLine =
    home.visits.today > 0 ? `오늘 이웃 ${home.visits.today}명이 다녀갔어요` : "오늘은 아직 다녀간 이웃이 없어요";

  return (
    <div className="min-h-dvh pb-[calc(env(safe-area-inset-bottom)+76px)] lg:pb-10">
      {/* 머리 */}
      <header className="sticky top-0 z-30 border-b border-line/70 bg-paper/90 pt-[env(safe-area-inset-top)] backdrop-blur">
        <div className="mx-auto flex h-14 max-w-[1180px] items-center gap-2 px-3 lg:px-6">
          {owner ? (
            // jarvis 안에서는 jarvis 머리글이 이미 '둥지'라고 말한다.
            !EMBEDDED && <span className="hand px-1 text-[26px] leading-none font-bold text-twig">둥지</span>
          ) : (
            <a className="btn btn-quiet h-10 min-h-0 px-3 text-[14px]" {...linkTo(`/h/${me.home}`)}>
              <Icon name="back" className="size-4" />
              우리 둥지로
            </a>
          )}
          <span className="flex-1" />
          {owner && home.coins !== undefined && (
            <button type="button" onClick={() => setSheet({ kind: "ledger" })} className="flex h-10 items-center rounded-full bg-card px-3.5 text-[15px] font-bold shadow-[0_1px_0_var(--line)]" aria-label={`깃털 ${home.coins}개, 내역 보기`}>
              <Feathers n={home.coins} />
            </button>
          )}
          {owner && (
            <button type="button" className="icon-btn" onClick={() => setSheet({ kind: "settings" })} aria-label="둥지 설정">
              <Icon name="gear" />
            </button>
          )}
        </div>
        {/* 패드·컴퓨터에서는 탭이 머리에 */}
        <nav className="mx-auto hidden max-w-[1180px] gap-1 px-6 pb-2 lg:flex" aria-label="둥지 메뉴">
          {tabs.map((t) => (
            <button key={t} type="button" onClick={() => setTab(t)} aria-current={tab === t ? "page" : undefined} className={`h-10 rounded-full px-4 text-[15px] font-bold ${tab === t ? "bg-ink text-paper" : "text-muted hover:bg-well"}`}>
              {TAB_NAMES[t](owner)}
            </button>
          ))}
        </nav>
      </header>

      <main className="mx-auto max-w-[1180px] px-4 pt-4 lg:grid lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)] lg:items-start lg:gap-8 lg:px-6">
        {/* 방 */}
        <section className={`${tab === "room" ? "" : "hidden lg:block"} lg:sticky lg:top-[120px]`} aria-label="방">
          <Nameplate home={home} onlineIds={onlineIds} meId={me.id} />
          <div className="sky relative mt-3 overflow-hidden rounded-[28px] px-2 pt-5 pb-3" style={{ background: SKY[phase] }}>
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
              poofs={poofs}
              showNeeds={owner}
              label={`${home.name}의 방. 청결 ${home.cleanliness}퍼센트, 먼지 ${home.dirt.length}개`}
            />
          </div>

          {/* 방 아래 한 줄 */}
          {editing ? (
            <EditBar
              item={selectedItem}
              onFlip={() => selectedItem && act(`/items/${selectedItem.id}/move`, { x: selectedItem.x, y: selectedItem.y, flip: !selectedItem.flip, wall: selectedItem.wall })}
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
          ) : (
            <div className="mt-3 space-y-3">
              <div className="flex items-center gap-3 rounded-2xl bg-card px-4 py-3">
                <CleanMeter value={home.cleanliness} spots={home.dirt.length} />
                {owner && partner && (
                  <button
                    type="button"
                    onClick={call.start}
                    disabled={!partnerOnline || call.state.s !== "idle"}
                    className="btn btn-primary h-11 min-h-0 shrink-0 px-3.5 text-[15px]"
                    aria-label={partnerOnline ? `${partner.name}님과 통화하기` : `${partner.name}님이 들어오면 통화할 수 있어요`}
                  >
                    <Icon name="phone" className="size-[18px]" />
                    통화
                  </button>
                )}
              </div>
              {owner ? (
                <div className="grid grid-cols-3 gap-2">
                  <button type="button" className="btn btn-quiet h-12 flex-col gap-0 text-[14px]" onClick={() => setEditing(true)}>
                    <Icon name="pencil" className="size-[18px]" />
                    꾸미기
                  </button>
                  <button type="button" className="btn btn-quiet h-12 flex-col gap-0 text-[14px]" onClick={() => setSheet({ kind: "storage" })}>
                    <Icon name="box" className="size-[18px]" />
                    창고 {storageUsed}/{home.storageSlots}
                  </button>
                  <button type="button" className="btn btn-quiet h-12 flex-col gap-0 text-[14px]" onClick={() => setSheet({ kind: "shop" })}>
                    <Icon name="bag" className="size-[18px]" />
                    상점
                  </button>
                </div>
              ) : (
                <p className="rounded-2xl bg-card px-4 py-3 text-[15px] text-muted">
                  {(home.helpLeft ?? 0) > 0 && home.dirt.length > 0
                    ? `먼지를 누르면 청소를 도와줄 수 있어요. 오늘 ${home.helpLeft}개 더 치워 줄 수 있어요.`
                    : home.dirt.length === 0
                      ? "깨끗한 방이에요. 방명록에 한마디 남겨 보세요."
                      : "오늘은 충분히 도와줬어요. 방명록에 한마디 남겨 보세요."}
                </p>
              )}
            </div>
          )}
        </section>

        {/* 탭 내용 */}
        <section className={`${tab === "room" ? "mt-6 lg:mt-0" : ""} space-y-6`}>
          {tab === "room" && (
            <>
              {owner && <CarePanel home={home} actions={actions} onDiary={() => setTab("diary")} meId={me.id} />}
              <VisitorsToday home={home} line={visitorsLine} />
              {!owner && <GuestbookPanel home={home} me={me} stamp={stamps.guestbook} compact />}
            </>
          )}
          {tab === "diary" && owner && <DiaryPanel me={me} home={home} stamp={stamps.diary} onRewards={showRewards} />}
          {tab === "guestbook" && <GuestbookPanel home={home} me={me} stamp={stamps.guestbook} />}
          {tab === "neighbors" && owner && <NeighborsPanel />}
        </section>
      </main>

      {/* 폰의 아래 탭 */}
      <nav className="tabbar fixed inset-x-0 bottom-0 z-30 border-t border-line bg-card/95 backdrop-blur lg:hidden" aria-label="둥지 메뉴">
        <div className="mx-auto grid max-w-[560px]" style={{ gridTemplateColumns: `repeat(${tabs.length}, 1fr)` }}>
          {tabs.map((t) => (
            <button key={t} type="button" onClick={() => setTab(t)} aria-current={tab === t ? "page" : undefined} className={`flex h-[60px] flex-col items-center justify-center gap-0.5 text-[12px] font-bold ${tab === t ? "text-egg-deep" : "text-muted"}`}>
              <Icon name={TAB_ICONS[t]} className="size-[22px]" />
              {TAB_NAMES[t](owner)}
            </button>
          ))}
        </div>
      </nav>

      {sheet?.kind === "shop" && <ShopSheet home={home} actions={actions} initialTab={sheet.tab} tankId={sheet.tankId} onClose={() => setSheet(null)} />}
      {sheet?.kind === "storage" && <StorageSheet home={home} actions={actions} onClose={() => setSheet(null)} />}
      {sheet?.kind === "care" && <CareSheet home={home} itemId={sheet.id} actions={actions} onClose={() => setSheet(null)} />}
      {sheet?.kind === "settings" && <SettingsSheet home={home} me={me} actions={actions} onHome={setHome} onMe={onMe} onClose={() => setSheet(null)} />}
      {sheet?.kind === "ledger" && <LedgerSheet onClose={() => setSheet(null)} />}
      {owner && !me.named && <NameSheet home={home} onSaved={() => void load()} onMe={onMe} />}
    </div>
  );
}

const TAB_NAMES: Record<Tab, (owner: boolean) => string> = {
  room: (owner) => (owner ? "우리 방" : "방"),
  diary: () => "일기",
  guestbook: () => "방명록",
  neighbors: () => "이웃",
};
const TAB_ICONS: Record<Tab, string> = { room: "home", diary: "book", guestbook: "chat", neighbors: "people" };

export function stageName(key: string, stage: number) {
  const bloom = plantDef(key)?.harvest === "열매" ? "열매가 익었어요" : key === "plant_monstera" ? "새 잎이 났어요" : "꽃이 폈어요";
  return ["씨앗에서 싹이 났어요", "새싹이 자라요", "잎이 무성해요", key === "plant_monstera" ? "잎이 커지고 있어요" : "봉오리가 맺혔어요", bloom][stage] ?? "";
}

function Nameplate({ home, onlineIds, meId }: { home: HomeView; onlineIds: string[]; meId: string }) {
  return (
    <div>
      <div className="nameplate inline-block max-w-full rounded-lg px-4 py-1.5">
        <h1 className="hand truncate text-[28px] leading-tight font-bold text-[#3B2B1E] lg:text-[32px]">{home.name}</h1>
      </div>
      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        {home.members.map((m) => {
          const on = onlineIds.includes(m.id);
          return (
            <span key={m.id} className="inline-flex h-8 items-center gap-1.5 rounded-full bg-card px-3 text-[14px] font-bold" title={on ? "들어와 있어요" : "나가 있어요"}>
              <span className={`size-2.5 rounded-full ${on ? "bg-straw shadow-[0_0_0_3px_color-mix(in_srgb,var(--straw)_35%,transparent)]" : "bg-line"}`} />
              {m.name}
              {m.id === meId && <span className="font-normal text-muted">나</span>}
              <span className="sr-only">{on ? "들어와 있음" : "나가 있음"}</span>
            </span>
          );
        })}
        {home.since && <span className="px-1.5 text-[14px] font-bold text-twig">함께한 지 {daysSince(home.since).toLocaleString("ko-KR")}일</span>}
      </div>
      {home.intro && <p className="hand mt-1.5 text-[21px] leading-snug">{home.intro}</p>}
    </div>
  );
}

function CleanMeter({ value, spots }: { value: number; spots: number }) {
  const tone = value >= 70 ? "bg-egg-deep" : value >= 40 ? "bg-straw" : "bg-blush";
  const word = value >= 85 ? "반짝반짝" : value >= 60 ? "괜찮아요" : value >= 35 ? "먼지가 보여요" : "치울 때가 됐어요";
  return (
    <div className="min-w-0 flex-1">
      <div className="flex items-baseline justify-between gap-2 text-[14px]">
        <span className="font-bold">청결 {value}%</span>
        <span className="truncate text-muted">{spots > 0 ? `${word}, 먼지 ${spots}개` : word}</span>
      </div>
      <div className="mt-1.5 h-2 overflow-hidden rounded-full bg-well" role="meter" aria-valuenow={value} aria-valuemin={0} aria-valuemax={100} aria-label="청결도">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.max(4, value)}%` }} />
      </div>
    </div>
  );
}

function EditBar({ item, onFlip, onStore, onDone }: { item?: ItemView; onFlip: () => void; onStore: () => void; onDone: () => void }) {
  const def = item ? itemDef(item.key) : undefined;
  return (
    <div className="mt-3 rounded-2xl bg-card p-3">
      <p className="px-1 text-[14px] text-muted">
        {def ? (
          <>
            <b className="text-ink">{josa(def.name, "을", "를")}</b> {def.kind === "wall" ? "벽을 따라 끌거나, 놓을 벽을 눌러 옮겨요." : "끌거나, 놓을 바닥 칸을 눌러 옮겨요."}
          </>
        ) : (
          "옮길 물건을 눌러 골라 주세요."
        )}
      </p>
      <div className="mt-2 flex gap-2">
        {def && def.kind !== "wall" && (
          <button type="button" className="btn btn-quiet h-11 min-h-0 flex-1 px-2 text-[14px]" onClick={onFlip}>
            <Icon name="flip" className="size-[18px]" />
            돌리기
          </button>
        )}
        {def && (
          <button type="button" className="btn btn-quiet h-11 min-h-0 flex-1 px-2 text-[14px]" onClick={onStore}>
            <Icon name="box" className="size-[18px]" />
            창고에 넣기
          </button>
        )}
        <button type="button" className="btn btn-primary h-11 min-h-0 flex-1 px-2 text-[14px]" onClick={onDone}>
          <Icon name="check" className="size-[18px]" />
          다 꾸몄어요
        </button>
      </div>
    </div>
  );
}

function CarePanel({ home, actions, onDiary, meId }: { home: HomeView; actions: HomeActions; onDiary: () => void; meId: string }) {
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
  if (home.dirt.length) tasks.push({ key: "dirt", icon: "broom", text: `방에 먼지 ${home.dirt.length}개, 눌러서 치워요` });
  if (wroteToday === false) tasks.push({ key: "diary", icon: "book", text: "오늘 일기를 아직 안 썼어요", action: { label: "쓰러 가기", run: onDiary } });

  return (
    <div>
      <h2 className="mb-2 text-[17px] font-bold">오늘 돌볼 것</h2>
      {tasks.length === 0 ? (
        <p className="rounded-2xl bg-card px-4 py-4 text-[15px] text-muted">다 돌봤어요. 이웃 둥지에 놀러 가 볼까요?</p>
      ) : (
        <ul className="divide-y divide-line overflow-hidden rounded-2xl bg-card">
          {tasks.map((t) => (
            <li key={t.key} className="flex min-h-[60px] items-center gap-3 px-4 py-2.5">
              <Icon name={t.icon} className="size-5 shrink-0 text-egg-deep" />
              <span className="min-w-0 flex-1 text-[15px]">{t.text}</span>
              {t.action && (
                <button type="button" onClick={t.action.run} className="btn btn-line h-10 min-h-0 shrink-0 px-3 text-[14px]">
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
      <h2 className="mb-2 text-[17px] font-bold">오늘 놀러 온 이웃</h2>
      <div className="rounded-2xl bg-card px-4 py-3">
        <p className="text-[15px] text-muted">
          {line}. 지금까지 {home.visits.total.toLocaleString("ko-KR")}명이 놀러 왔어요.
        </p>
        {home.visits.recent.length > 0 && (
          <ul className="mt-2 flex flex-wrap gap-2">
            {home.visits.recent.map((v, i) => (
              <li key={`${v.slug}-${i}`}>
                <a className="inline-flex h-9 items-center gap-1 rounded-full bg-well px-3 text-[14px]" {...linkTo(`/h/${v.slug}`)}>
                  <b>{v.name}</b>
                  <span className="text-muted">{v.home}</span>
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
