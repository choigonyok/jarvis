import { useEffect, useState } from "react";
import { FISH, ITEMS, itemDef, plantDef, THEMES, type ItemDef } from "../../shared/catalog";
import type { HomeResponse, HomeView, LedgerRow, Me } from "../../shared/api";
import { expandPrice, sellPrice, STORAGE_MAX } from "../../shared/rules";
import { ApiError, get, patch, post } from "../lib/api";
import { josa } from "../lib/josa";
import { stamp } from "../lib/time";
import { useToast } from "../lib/toast";
import { FishThumb, ItemThumb, ThemeThumb } from "../room/Thumb";
import { Feathers, Icon } from "../ui/icons";
import { Sheet } from "../ui/sheet";
import { stageName, type HomeActions } from "./Home";

export type ShopTab = "floor" | "plant" | "tank" | "fish" | "wall" | "theme";
const SHOP_TABS: [ShopTab, string][] = [
  ["floor", "가구"],
  ["plant", "화분"],
  ["tank", "어항"],
  ["fish", "물고기"],
  ["wall", "벽 장식"],
  ["theme", "벽지·바닥"],
];

function Price({ n, coins }: { n: number; coins: number }) {
  return <Feathers n={n} className={`text-ui font-semibold ${coins < n ? "text-dim" : ""}`} />;
}

// ── 상점 ─────────────────────────────────────────────────────────

export function ShopSheet({ home, actions, initialTab, tankId, onClose }: { home: HomeView; actions: HomeActions; initialTab?: ShopTab; tankId?: string; onClose: () => void }) {
  const [tab, setTab] = useState<ShopTab>(initialTab ?? "floor");
  const coins = home.coins ?? 0;
  const tanks = home.items.filter((i) => i.place === "room" && i.tank);
  const [tank, setTank] = useState<string | undefined>(tankId ?? tanks[0]?.id);
  const stored = home.items.filter((i) => i.place === "storage").length;
  const full = stored >= (home.storageSlots ?? 4);

  const list: ItemDef[] = ITEMS.filter((i) => (tab === "floor" ? i.kind === "floor" : i.kind === tab));

  return (
    <Sheet
      open
      onClose={onClose}
      title={
        <span className="flex items-center gap-3">
          상점 <Feathers n={coins} className="text-field font-semibold" />
        </span>
      }
    >
      <div className="-mx-5 mb-3 flex gap-1.5 overflow-x-auto px-5 pb-1" role="tablist" aria-label="상점 칸">
        {SHOP_TABS.map(([t, name]) => (
          <button key={t} type="button" role="tab" aria-selected={tab === t} onClick={() => setTab(t)} className={`focus-ring h-9 shrink-0 rounded-full border px-3.5 text-ui ${tab === t ? "border-edge bg-glass-raised font-medium text-foreground" : "border-transparent text-dim hover:text-foreground"}`}>
            {name}
          </button>
        ))}
      </div>

      {tab !== "fish" && tab !== "theme" && (
        <p className="mb-3 text-aside text-dim">{full ? "창고가 꽉 차서 산 물건은 방의 빈자리에 바로 놓여요." : "산 물건은 창고에 들어가요. 창고에서 꺼내 방에 두세요."}</p>
      )}

      {tab === "fish" ? (
        tanks.length === 0 ? (
          <div className="rounded-xl bg-glass p-4 text-body">
            물고기는 방에 놓인 어항에서 살아요.
            <button type="button" className="btn btn-approve mt-3 w-full" onClick={() => setTab("tank")}>
              어항 보러 가기
            </button>
          </div>
        ) : (
          <>
            {tanks.length > 1 && (
              <div className="mb-3 flex gap-2">
                {tanks.map((t) => (
                  <button key={t.id} type="button" onClick={() => setTank(t.id)} className={`h-10 rounded-full px-3 text-ui ${tank === t.id ? "bg-glass-raised font-semibold" : "bg-glass"}`}>
                    {itemDef(t.key)?.name} {t.tank!.fish.length}/{t.tank!.capacity}
                  </button>
                ))}
              </div>
            )}
            <ul className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
              {FISH.map((f) => {
                const target = tanks.find((t) => t.id === tank) ?? tanks[0];
                const roomy = target.tank!.fish.length < target.tank!.capacity;
                return (
                  <li key={f.key} className="flex flex-col items-center rounded-xl bg-glass p-3 text-center">
                    <FishThumb species={f.key} className="h-12 w-20" />
                    <p className="mt-1 text-body font-semibold">{f.name}</p>
                    <Price n={f.price} coins={coins} />
                    <button type="button" disabled={coins < f.price || !roomy} onClick={() => void actions.act("/shop/fish", { species: f.key, tankId: target.id })} className="btn btn-approve mt-2 h-10 min-h-0 w-full text-ui">
                      {!roomy ? "어항이 꽉 찼어요" : coins < f.price ? "깃털이 모자라요" : "들이기"}
                    </button>
                  </li>
                );
              })}
            </ul>
          </>
        )
      ) : tab === "theme" ? (
        <ul className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
          {THEMES.map((t) => {
            const owned = home.themes?.includes(t.key);
            const using = home.wallpaper === t.key || home.floor === t.key;
            return (
              <li key={t.key} className="flex flex-col items-center rounded-xl bg-glass p-3 text-center">
                <ThemeThumb themeKey={t.key} className="size-16 rounded-lg" />
                <p className="mt-1 text-body font-semibold">{t.name}</p>
                <p className="text-aside text-dim">{t.kind === "wallpaper" ? "벽지" : "바닥"}</p>
                {owned ? <p className="text-aside text-dim">가진 것</p> : <Price n={t.price} coins={coins} />}
                <button type="button" disabled={using || (!owned && coins < t.price)} onClick={() => void actions.act("/shop/theme", { key: t.key })} className="btn btn-approve mt-2 h-10 min-h-0 w-full text-ui">
                  {using ? "쓰는 중" : owned ? "바르기" : coins < t.price ? "깃털이 모자라요" : "사서 바르기"}
                </button>
              </li>
            );
          })}
        </ul>
      ) : (
        <ul className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
          {list.map((d) => (
            <li key={d.key} className="flex flex-col items-center rounded-xl bg-glass p-3 text-center">
              <ItemThumb itemKey={d.key} className="h-20 w-full" />
              <p className="mt-1 text-body leading-tight font-semibold">{d.name}</p>
              <p className="mt-0.5 text-aside leading-snug text-dim">{d.blurb}</p>
              <Price n={d.price} coins={coins} />
              <button type="button" disabled={coins < d.price} onClick={() => void actions.act("/shop/item", { key: d.key })} className="btn btn-approve mt-2 h-10 min-h-0 w-full text-ui">
                {coins < d.price ? "깃털이 모자라요" : "사기"}
              </button>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}

// ── 창고 ─────────────────────────────────────────────────────────

export function StorageSheet({ home, actions, onClose }: { home: HomeView; actions: HomeActions; onClose: () => void }) {
  const slots = home.storageSlots ?? 4;
  const stored = home.items.filter((i) => i.place === "storage");
  const coins = home.coins ?? 0;
  const [selling, setSelling] = useState<string | null>(null);
  const price = expandPrice(slots);

  return (
    <Sheet open onClose={onClose} title={`창고 ${stored.length}/${slots}칸`}>
      <p className="mb-3 text-ui text-dim">안 쓰는 물건을 넣어 두는 곳이에요. 창고에 든 화분과 어항은 꺼낼 때까지 그대로 멈춰 있어요.</p>
      <ul className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
        {Array.from({ length: STORAGE_MAX }, (_, i) => {
          const item = stored[i];
          if (i < slots && item) {
            const def = itemDef(item.key)!;
            const fish = item.tank?.fish.length ?? 0;
            return (
              <li key={item.id} className="flex flex-col items-center rounded-xl bg-glass p-3 text-center">
                <ItemThumb itemKey={item.key} view={item} className="h-16 w-full" />
                <p className="mt-1 text-body leading-tight font-semibold">{def.name}</p>
                {fish > 0 && <p className="text-meta text-dim">물고기 {fish}마리와 함께</p>}
                <button type="button" onClick={() => void actions.act(`/items/${item.id}/place`)} className="btn btn-approve mt-2 h-10 min-h-0 w-full text-ui">
                  방에 두기
                </button>
                <button
                  type="button"
                  onClick={() => {
                    if (selling !== item.id) return setSelling(item.id);
                    setSelling(null);
                    void actions.act(`/items/${item.id}/sell`);
                  }}
                  className={`btn mt-1.5 h-10 min-h-0 w-full text-ui ${selling === item.id ? "btn-refuse" : "btn-pill"}`}
                >
                  {selling === item.id ? (fish > 0 ? "물고기도 보내요. 팔까요?" : "정말 팔까요?") : <>팔기 <Feathers n={sellPrice(def.price)} /></>}
                </button>
              </li>
            );
          }
          if (i < slots)
            return (
              <li key={`empty-${i}`} className="grid min-h-[150px] place-items-center rounded-xl border-2 border-dashed border-edge text-ui text-dim">
                빈칸
              </li>
            );
          if (i === slots)
            return (
              <li key="expand" className="flex min-h-[150px] flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed border-foreground/40 p-3 text-center">
                <Icon name="plus" className="size-6 text-foreground" />
                <p className="text-ui font-semibold">한 칸 넓히기</p>
                <button type="button" disabled={coins < price} onClick={() => void actions.act("/storage/expand")} className="btn btn-approve h-10 min-h-0 w-full text-ui">
                  <Feathers n={price} />
                </button>
              </li>
            );
          return (
            <li key={`lock-${i}`} className="grid min-h-[150px] place-items-center rounded-xl bg-glass text-dim" aria-label="잠긴 칸">
              <Icon name="lock" className="size-5 opacity-50" />
            </li>
          );
        })}
      </ul>
    </Sheet>
  );
}

// ── 화분·어항 돌보기 ──────────────────────────────────────────────

function Bar({ value, label, tone }: { value: number; label: string; tone: string }) {
  return (
    <div>
      <div className="flex justify-between text-ui">
        <span className="font-semibold">{label}</span>
        <span className="text-dim">{value}%</span>
      </div>
      <div className="mt-1 h-2.5 overflow-hidden rounded-full bg-glass" role="meter" aria-valuenow={value} aria-valuemin={0} aria-valuemax={100} aria-label={label}>
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.max(3, value)}%` }} />
      </div>
    </div>
  );
}

export function CareSheet({ home, itemId, actions, onClose }: { home: HomeView; itemId: string; actions: HomeActions; onClose: () => void }) {
  const item = home.items.find((i) => i.id === itemId);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [releasing, setReleasing] = useState<string | null>(null);
  if (!item) return null;
  const def = itemDef(item.key)!;

  if (item.plant) {
    const p = item.plant;
    const pdef = plantDef(item.key)!;
    return (
      <Sheet open onClose={onClose} title={def.name}>
        <div className="flex items-center gap-4">
          <ItemThumb itemKey={item.key} view={item} className="h-28 w-24 shrink-0" />
          <div className="min-w-0 flex-1">
            <p className=" text-heading leading-snug">{p.wilted ? "시들었어요. 물을 주면 다시 살아나요." : stageName(item.key, p.stage)}</p>
            <p className="mt-1 text-ui text-dim">
              {p.ready
                ? `${josa(pdef.harvest, "을", "를")} 거두면 깃털 ${pdef.reward}개를 받아요.`
                : p.nextHours !== null
                  ? `물이 있는 동안 ${Math.ceil(p.nextHours)}시간쯤 더 자라면 다음 단계예요.`
                  : ""}
            </p>
          </div>
        </div>
        <div className="mt-4">
          <Bar value={p.water} label="흙의 물기" tone={p.water < 30 ? "bg-caution" : "bg-foreground/70"} />
          <p className="mt-2 text-aside text-dim">물은 {pdef.waterHours}시간이면 다 말라요. 방이 지저분하면 절반 속도로 자라요.</p>
        </div>
        <div className="mt-4 grid grid-cols-2 gap-2">
          <button type="button" disabled={!p.thirsty} onClick={() => void actions.act(`/items/${item.id}/water`)} className="btn btn-approve h-12">
            <Icon name="drop" className="size-[18px]" />
            {p.thirsty ? "물 주기" : "아직 촉촉해요"}
          </button>
          <button type="button" disabled={!p.ready} onClick={() => void actions.act(`/items/${item.id}/harvest`)} className="btn btn-raised h-12">
            <Icon name="sparkle" className="size-[18px]" />
            {pdef.harvest} 거두기
          </button>
        </div>
      </Sheet>
    );
  }

  const t = item.tank!;
  return (
    <Sheet open onClose={onClose} title={def.name}>
      <div className="flex items-center gap-4">
        <ItemThumb itemKey={item.key} view={item} className="h-24 w-28 shrink-0" />
        <div className="min-w-0 flex-1 space-y-3">
          <Bar value={t.clarity} label="물의 맑기" tone={t.clarity < 40 ? "bg-caution" : "bg-foreground/70"} />
          <p className="text-aside text-dim">
            물고기 {t.fish.length}/{t.capacity}마리. 물은 나흘이면 탁해지고, 물고기는 하루면 배고파해요.
          </p>
        </div>
      </div>
      <div className="mt-4 grid grid-cols-3 gap-2">
        <button type="button" disabled={!t.fish.length || t.fish.every((f) => f.hunger >= 70)} onClick={() => void actions.act(`/items/${item.id}/feed`)} className="btn btn-approve h-12 px-2 text-body">
          먹이 주기
        </button>
        <button type="button" disabled={t.clarity >= 60} onClick={() => void actions.act(`/items/${item.id}/scrub`)} className="btn btn-raised h-12 px-2 text-body">
          물 갈기
        </button>
        <button type="button" disabled={t.fish.length >= t.capacity} onClick={() => actions.openSheet({ kind: "shop", tab: "fish", tankId: item.id })} className="btn btn-raised h-12 px-2 text-body">
          물고기 들이기
        </button>
      </div>
      {t.fish.length > 0 && (
        <ul className="mt-4 divide-y divide-edge-soft rounded-xl bg-glass">
          {t.fish.map((f) => (
            <li key={f.id} className="flex items-center gap-3 px-3 py-3">
              <FishThumb species={f.species} className="h-8 w-12 shrink-0" />
              <div className="min-w-0 flex-1">
                {renaming === f.id ? (
                  <form
                    className="flex gap-2"
                    onSubmit={async (e) => {
                      e.preventDefault();
                      const r = await actions.act(`/fish/${f.id}`, { name }, "PATCH");
                      if (r) setRenaming(null);
                    }}
                  >
                    <input className="field h-10 min-h-0 py-1" value={name} onChange={(e) => setName(e.target.value)} maxLength={8} autoFocus aria-label="물고기 이름" />
                    <button className="btn btn-approve h-10 min-h-0 px-3 text-ui">저장</button>
                  </form>
                ) : (
                  <button type="button" className="text-left text-body font-semibold" onClick={() => (setRenaming(f.id), setName(f.name))}>
                    {f.name} <Icon name="pencil" className="inline size-3.5 text-dim" />
                  </button>
                )}
                <p className="text-aside text-dim">
                  {f.sick ? "기운이 없어요. " : ""}
                  배부름 {f.hunger}%, {f.adult ? "다 자랐어요" : `${Math.round(f.grown * 100)}% 자랐어요`}
                </p>
              </div>
              <button
                type="button"
                onClick={() => {
                  if (releasing !== f.id) return setReleasing(f.id);
                  setReleasing(null);
                  void actions.act(`/fish/${f.id}`, undefined, "DELETE");
                }}
                className={`focus-ring h-10 shrink-0 rounded-lg px-2.5 text-aside ${releasing === f.id ? "bg-reject-wash text-reject" : "text-dim hover:text-reject"}`}
              >
                {releasing === f.id ? "보낼까요?" : "보내 주기"}
              </button>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}

// ── 설정 ─────────────────────────────────────────────────────────

export function SettingsSheet({
  home,
  me,
  actions,
  onHome,
  onMe,
  onClose,
}: {
  home: HomeView;
  me: Me;
  actions: HomeActions;
  onHome: (h: HomeView) => void;
  onMe: (m: Me | null) => void;
  onClose: () => void;
}) {
  const toast = useToast();
  const [name, setName] = useState(home.name);
  const [intro, setIntro] = useState(home.intro);
  const [since, setSince] = useState(home.since ?? "");
  const [listed, setListed] = useState(home.listed ?? true);
  const [myName, setMyName] = useState(me.name);
  const [saving, setSaving] = useState(false);
  const link = home.invite ? `${location.origin}/signup?invite=${home.invite}` : "";

  async function save() {
    setSaving(true);
    try {
      const r = await patch<HomeResponse>("/home", { name, intro, since: since || null, listed });
      onHome(r.home);
      if (myName.trim() !== me.name) {
        const m = await patch<{ me: Me }>("/me", { name: myName });
        onMe(m.me);
      }
      toast.show("저장했어요");
      onClose();
    } catch (e) {
      toast.show(e instanceof ApiError ? e.message : "저장하지 못했어요.");
    } finally {
      setSaving(false);
    }
  }

  async function share() {
    const text = `둥지에서 같이 살아요. 초대 코드 ${home.invite}`;
    try {
      if (navigator.share) await navigator.share({ title: "둥지 초대", text, url: link });
      else {
        await navigator.clipboard.writeText(`${text}\n${link}`);
        toast.show("초대 링크를 복사했어요");
      }
    } catch {
      // 공유 창을 닫았다
    }
  }

  return (
    <Sheet
      open
      onClose={onClose}
      title="둥지 설정"
      footer={
        <button type="button" className="btn btn-approve h-12 w-full" disabled={saving} onClick={save}>
          {saving ? "저장하는 중" : "저장"}
        </button>
      }
    >
      <div className="space-y-4">
        {home.invite && (
          <div className="rounded-xl bg-glass-raised p-4">
            <p className="text-body font-semibold">함께 살 사람을 불러요</p>
            <p className="mt-1 text-ui text-dim">
              {me.via === "jarvis"
                ? "상대가 jarvis 의 둥지 화면을 열면 이 둥지로 바로 들어와요. jarvis 밖의 사람은 가입할 때 이 코드를 적으면 돼요."
                : "상대가 가입할 때 \u2018초대받았어요\u2019를 고르고 이 코드를 적으면 같은 둥지에 들어와요."}
            </p>
            <p className="mt-3 text-center text-display font-semibold tracking-[0.35em]" aria-label={`초대 코드 ${home.invite.split("").join(" ")}`}>
              {home.invite}
            </p>
            <div className="mt-3 grid grid-cols-2 gap-2">
              <button type="button" className="btn btn-approve h-11" onClick={share}>
                초대 보내기
              </button>
              <button type="button" className="btn btn-raised h-11" onClick={() => void actions.act("/home/invite")}>
                코드 새로 받기
              </button>
            </div>
          </div>
        )}
        <label className="block">
          <span className="mb-1.5 block text-ui font-semibold">둥지 이름</span>
          <input className="field" value={name} onChange={(e) => setName(e.target.value)} maxLength={16} />
        </label>
        <label className="block">
          <span className="mb-1.5 block text-ui font-semibold">한 줄 소개</span>
          <input className="field" value={intro} onChange={(e) => setIntro(e.target.value)} maxLength={60} placeholder="이웃이 놀러 오면 보이는 말" />
        </label>
        <label className="block">
          <span className="mb-1.5 block text-ui font-semibold">처음 만난 날</span>
          <input className="field" type="date" value={since} max={new Date().toISOString().slice(0, 10)} onChange={(e) => setSince(e.target.value)} />
        </label>
        <label className="flex min-h-[52px] items-center gap-3 rounded-xl bg-glass px-4">
          <span className="flex-1">
            <span className="block text-body font-semibold">이웃 목록에 보이기</span>
            <span className="block text-aside text-dim">끄면 주소를 아는 사람만 놀러 올 수 있어요</span>
          </span>
          <input type="checkbox" className="size-6 accent-[var(--foreground)]" checked={listed} onChange={(e) => setListed(e.target.checked)} />
        </label>
        <label className="block">
          <span className="mb-1.5 block text-ui font-semibold">내 이름</span>
          <input className="field" value={myName} onChange={(e) => setMyName(e.target.value)} maxLength={10} />
        </label>
        {me.via === "jarvis" ? (
          <p className="border-t border-edge pt-4 text-ui text-dim">jarvis 로 들어왔어요. 로그인과 나가기는 jarvis 가 맡아요.</p>
        ) : (
        <div className="flex items-center justify-between gap-3 border-t border-edge pt-4">
          <p className="text-ui text-dim">아이디 {me.login}</p>
          <button
            type="button"
            className="btn btn-pill h-10 min-h-0 text-ui"
            onClick={async () => {
              await post("/auth/logout");
              onMe(null);
              location.href = "/";
            }}
          >
            <Icon name="logout" className="size-4" />
            나가기
          </button>
        </div>
        )}
      </div>
    </Sheet>
  );
}

// ── 깃털 내역 ────────────────────────────────────────────────────

export function LedgerSheet({ onClose }: { onClose: () => void }) {
  const [rows, setRows] = useState<LedgerRow[] | null>(null);
  useEffect(() => {
    get<{ rows: LedgerRow[] }>("/ledger")
      .then((r) => setRows(r.rows))
      .catch(() => setRows([]));
  }, []);
  return (
    <Sheet open onClose={onClose} title="깃털 내역">
      <p className="mb-3 text-ui text-dim">깃털은 둘이 함께 써요. 매일 들어오기, 일기, 청소, 물 주기, 이웃 방명록으로 모여요.</p>
      {rows === null ? (
        <p className="py-6 text-center text-dim">불러오는 중</p>
      ) : rows.length === 0 ? (
        <p className="py-6 text-center text-dim">아직 내역이 없어요</p>
      ) : (
        <ul className="divide-y divide-edge-soft">
          {rows.map((r, i) => (
            <li key={i} className="flex items-center gap-3 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="truncate text-body">{r.reason}</p>
                <p className="text-meta text-dim">
                  {r.by ? `${r.by}, ` : ""}
                  {stamp(r.at)}
                </p>
              </div>
              <span className={`text-body font-semibold tabular-nums ${r.delta >= 0 ? "text-foreground" : "text-dim"}`}>{r.delta >= 0 ? `+${r.delta}` : r.delta}</span>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}

// ── 처음 들어온 사람의 이름 ──────────────────────────────────────

export function NameSheet({ home, onSaved, onMe }: { home: HomeView; onSaved: () => void; onMe: (m: Me | null) => void }) {
  const toast = useToast();
  // 둥지를 처음 지은 사람(혼자 사는 동안)만 둥지 이름도 정한다.
  const first = home.members.length === 1;
  const [name, setName] = useState("");
  const [homeName, setHomeName] = useState(home.name === "우리 둥지" ? "" : home.name);
  const [saving, setSaving] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true);
    try {
      const m = await patch<{ me: Me }>("/me", { name });
      if (first && homeName.trim() && homeName.trim() !== home.name) await patch<HomeResponse>("/home", { name: homeName });
      // 이름표(둥지의 사람들)와 둥지 이름을 다시 읽는다.
      onSaved();
      onMe(m.me);
    } catch (err) {
      toast.show(err instanceof ApiError ? err.message : "저장하지 못했어요.");
      setSaving(false);
    }
  }

  return (
    <Sheet open onClose={() => undefined} title="둥지에 처음 왔어요">
      <form onSubmit={save} className="space-y-4">
        <p className="text-body text-dim">
          {first ? "둘이 함께 쓸 둥지를 지었어요. 이웃에게 보일 이름을 정해 주세요." : `${home.name}에 들어왔어요. 둥지에서 불릴 이름을 정해 주세요.`}
        </p>
        <label className="block">
          <span className="mb-1.5 block text-ui font-semibold">내 이름</span>
          <input className="field" value={name} onChange={(e) => setName(e.target.value)} maxLength={10} required autoFocus />
        </label>
        {first && (
          <label className="block">
            <span className="mb-1.5 block text-ui font-semibold">둥지 이름</span>
            <input className="field" value={homeName} onChange={(e) => setHomeName(e.target.value)} maxLength={16} placeholder="우리 둥지" />
          </label>
        )}
        <button className="btn btn-approve h-12 w-full" disabled={saving || !name.trim()}>
          {saving ? "저장하는 중" : "들어가기"}
        </button>
      </form>
    </Sheet>
  );
}
