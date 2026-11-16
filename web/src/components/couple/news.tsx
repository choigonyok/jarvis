"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DetailSheet } from "@/components/ui/detail-sheet";
import { Failed } from "@/components/shell/failed";
import type { KoreaMapData } from "@/components/couple/korea-map";
import { markNewsSeen, useNews, useNewsSeen } from "@/components/couple/use-space";
import type { News, Space } from "@/lib/couple";
import { openTarget } from "@/lib/focus";
import { SAY } from "@/lib/say";
import { ago, monthDay } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * What the two did in the space, as one sentence each - the app's activity
 * list, in the console's words. The app files each by its block and a kind:
 * no kind for the block's own thing added (an entry, a map photo), `update`
 * for one changed, `photo`, `memo`, `record` for those added. A kind not seen
 * yet reads as "바꿨어요".
 */
type Told = { noun: string; verb: string; added: boolean };

function told(n: News): Told {
  const k = n.kind.toLowerCase();
  const gone = /(delete|remove)/.test(k);
  const changed = /(update|edit|change|move)/.test(k);
  const plain = (noun: string, add = "추가했어요"): Told =>
    gone ? { noun, verb: "지웠어요", added: false } : changed || k ? { noun, verb: changed ? "변경했어요" : "바꿨어요", added: false } : { noun, verb: add, added: true };
  switch (n.blockType) {
    case "calendar":
      if (k === "photo") return { noun: "사진", verb: "추가했어요", added: true };
      if (k === "memo") return { noun: "메모", verb: "남겼어요", added: true };
      return plain("일정");
    case "map":
    case "worldmap":
      if (k === "record") return { noun: n.blockType === "map" ? "데이트 기록" : "여행 기록", verb: "남겼어요", added: true };
      if (k === "photo") return { noun: "사진", verb: "추가했어요", added: true };
      return plain("사진");
    case "bucket":
      if (/(complete|done)/.test(k)) return { noun: "버킷", verb: "이뤘어요", added: false };
      return plain("버킷");
    case "dday":
      return plain("기념일");
    case "workout":
      return { noun: "", verb: /uncheck/.test(k) || gone ? "체크를 취소했어요" : "체크했어요", added: true };
    case "diary":
      if (/comment/.test(k)) return { noun: "댓글", verb: "남겼어요", added: true };
      return plain("일기", "남겼어요");
    case "album":
      if (k === "snap") return { noun: "사진", verb: "찍어 올렸어요", added: true };
      return plain("사진", "올렸어요");
    case "log":
      return plain("기록", "남겼어요");
    case "food":
      return plain("맛집", "남겼어요");
    case "budget":
      return plain("지출", "적었어요");
    case "free":
      return plain("기록", "남겼어요");
    default:
      return plain("기록", "남겼어요");
  }
}

/** "바다가 여행 지도에 데이트 기록을 남겼어요", "바다가 우리 캘린더의 일정을 변경했어요" */
export function sentence(n: News, space: Space | undefined): string {
  const who = n.who === "me" ? space?.me.name || "내" : space?.partner?.name || "상대";
  const subject = who === "내" ? "내가" : `${who}${josa(who, "이", "가")}`;
  const t = told(n);
  if (!t.noun) return `${subject} ${n.blockTitle ? `${n.blockTitle}에서 ` : ""}${t.verb}`;
  // Something added is put on the block; something changed is the block's.
  const where = n.blockTitle ? `${n.blockTitle}${t.added ? "에" : "의"} ` : "";
  return `${subject} ${where}${t.noun}${josa(t.noun, "을", "를")} ${t.verb}`;
}

/** The particle a word takes: 이/을 after a closing consonant, 가/를 after a vowel. */
function josa(word: string, closed: string, open: string): string {
  const c = word.charCodeAt(word.length - 1) - 0xac00;
  return c >= 0 && c <= 11171 && c % 28 !== 0 ? closed : open;
}

/** The detail under a sentence: what it is about, as it stands now. */
export function detail(n: News, map: KoreaMapData | null): string {
  const it = n.item;
  if (!it) return "";
  const region = it.region && map ? map.regions.find((r) => r.code === it.region)?.name : undefined;
  return [it.title, region, it.date ? monthDay(it.date) + (it.time ? ` ${it.time}` : "") : undefined, it.note]
    .filter(Boolean)
    .join(" · ");
}

/** Where a piece of news takes you: the thing itself, on its block's page or the calendar's day. */
export function whereOf(n: News): string | null {
  const id = n.itemId && /^[0-9a-f-]{36}$/i.test(n.itemId) ? n.itemId : undefined;
  if (n.blockType === "calendar") {
    const date = n.item?.date ?? (n.itemId && /^\d{4}-\d{2}-\d{2}$/.test(n.itemId) ? n.itemId : undefined);
    if (!date) return "/calendar";
    return `/calendar?date=${date}${id && n.kind !== "photo" ? `&focus=event:${id}` : ""}`;
  }
  if (!n.block) return null;
  const page = `/couple/${encodeURIComponent(n.block)}`;
  if ((n.blockType === "map" || n.blockType === "worldmap") && n.item?.region) return `${page}?region=${encodeURIComponent(n.item.region)}`;
  if (id && ["bucket", "free", "dday", "log", "food", "diary", "budget", "album"].includes(n.blockType ?? "")) {
    return `${page}?focus=${n.blockType}:${id}`;
  }
  if (n.blockType === "workout" && n.item?.date) return `${page}?date=${n.item.date}`;
  return page;
}

/** When a piece of news was done, as a moment to compare; 0 when it says no moment. */
const when = (at: string) => {
  const t = Date.parse(at);
  return Number.isNaN(t) ? 0 : t;
};

/** The moment of the newest news in a list, whichever row it is. */
const newestOf = (list: News[]) => list.reduce((most, n) => Math.max(most, when(n.at)), 0);

/**
 * The moment of the newest news the operator has looked at: what came after
 * it is new. The space keeps it for every device of theirs (useNewsSeen) and
 * this device keeps its own as well, the later of the two counting. A device
 * that keeps nothing, or keeps something that is no moment, starts from now.
 */
const SEEN = "couple:news:seen";
function seenAt(): number {
  try {
    return when(window.localStorage.getItem(SEEN) ?? "");
  } catch {
    return 0;
  }
}
function markSeen(at: number) {
  try {
    window.localStorage.setItem(SEEN, new Date(at).toISOString());
  } catch {
    /* nowhere to keep it: the same news reads as new again */
  }
}

/** The newest thing the partner did, one line on the home; the rest in a sheet. */
export function NewsLine({ space, map }: { space: Space | undefined; map: KoreaMapData | null }) {
  const news = useNews();
  const kept = useNewsSeen();
  const [open, setOpen] = useState(false);
  const [mine, setMine] = useState(seenAt);
  const seen = Math.max(mine, when(kept.data ?? ""));
  // What was new when the sheet opened stays marked while it is open.
  const [before, setBefore] = useState(seen);
  const theirs = news.data?.find((n) => n.who === "partner");
  const fresh = seen ? (news.data ?? []).filter((n) => n.who === "partner" && when(n.at) > seen).length : 0;
  const newest = news.data ? newestOf(news.data) : 0;
  // The first time anywhere nothing is new: the mark starts from now.
  if (!seen && newest && kept.data !== undefined) {
    setMine(newest);
    markSeen(newest);
    markNewsSeen(newest);
  }
  if (!news.data && !news.error) return <div aria-hidden className="h-11" />;

  function show() {
    setBefore(seen);
    if (newest > seen) {
      setMine(newest);
      markSeen(newest);
      markNewsSeen(newest);
    }
    setOpen(true);
  }

  return (
    <>
      <button
        type="button"
        onClick={show}
        className="group -mx-2 flex min-h-11 w-[calc(100%+1rem)] items-center gap-2.5 rounded-lg px-2 py-2 text-left transition-colors hover:bg-glass focus-ring"
      >
        <span aria-hidden className="size-[7px] shrink-0 rounded-full bg-partner" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-ui text-ink">
            {news.error && !news.data ? "소식을 불러오지 못했어요" : theirs ? sentence(theirs, space) : "아직 상대 소식이 없어요"}
          </span>
          {theirs ? (
            <span className="block truncate text-aside text-faint">
              {[detail(theirs, map), ago(theirs.at)].filter(Boolean).join(" · ")}
            </span>
          ) : null}
        </span>
        {fresh ? (
          <span className="shrink-0 rounded-full bg-partner/15 px-2 py-0.5 text-meta font-medium text-ink tabular-nums">새 {fresh}</span>
        ) : null}
        <ChevronRight aria-hidden className="size-4 shrink-0 text-faint transition-colors group-hover:text-dim" />
      </button>
      <DetailSheet title="소식" open={open} onOpenChange={setOpen}>
        {open ? <NewsList space={space} map={map} since={before} onGo={() => setOpen(false)} /> : null}
      </DetailSheet>
    </>
  );
}

const PAGE = 40;

/** The news while it is read: rows of their shape - the dot, the sentence, when - as quiet blocks. */
function NewsPlaceholder() {
  return (
    <div>
      <p role="status" className="sr-only">
        {SAY.loading}
      </p>
      <div aria-hidden>
        {["w-4/5", "w-3/5", "w-2/3"].map((width) => (
          <div key={width} className="flex gap-3 py-3">
            <span className="mt-[7px] size-[7px] shrink-0 rounded-full bg-well" />
            <span className="min-w-0 flex-1">
              <span className={cn("block h-4 rounded bg-well", width)} />
              <span className="mt-2 block h-3 w-14 rounded bg-well" />
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

/** Every piece of news, forty at a time; one tapped opens what it is about. */
function NewsList({ space, map, since, onGo }: { space: Space | undefined; map: KoreaMapData | null; since: number; onGo: () => void }) {
  const router = useRouter();
  const [limit, setLimit] = useState(PAGE);
  const news = useNews(limit);
  // The next forty on their way: the resource holds the forty before under
  // their own key until these land (lib/use-resource.ts), so the list stays
  // and the button says it is reading. Its test had looked for no data.
  const pending = news.dataKey !== `couple:news:${limit}`;
  // A list the sheet opened on before its own read landed (the home line's)
  // stays the same way.
  const [kept, setKept] = useState<News[] | undefined>(undefined);
  if (news.data && news.data !== kept) setKept(news.data);
  const list = news.data ?? kept;

  if (news.error && !list) return <Failed onRetry={news.reload}>{SAY.couldNotLoad}</Failed>;
  if (!list) return <NewsPlaceholder />;
  if (list.length === 0) return <p className="text-ui text-faint">아직 소식이 없어요.</p>;
  return (
    <div>
      <ul className="divide-y divide-edge-soft">
        {list.map((n) => {
          const to = whereOf(n);
          const body = (
            <>
              <span aria-hidden className={cn("mt-[7px] size-[7px] shrink-0 rounded-full", n.who === "me" ? "bg-mine" : "bg-partner")} />
              <span className="min-w-0 flex-1">
                <span className="block text-ui leading-snug break-keep text-ink">
                  {sentence(n, space)}
                  {since && n.who === "partner" && when(n.at) > since ? (
                    <span className="ml-1.5 align-middle text-meta font-medium text-partner">새 소식</span>
                  ) : null}
                </span>
                {detail(n, map) ? <span className="mt-0.5 block text-aside leading-snug break-keep text-dim">{detail(n, map)}</span> : null}
                <span className="mt-0.5 block text-meta text-faint">{ago(n.at)}</span>
              </span>
            </>
          );
          return (
            <li key={n.id}>
              {to ? (
                <button
                  type="button"
                  onClick={() => {
                    onGo();
                    openTarget(to, router.push);
                  }}
                  className="-mx-2 flex w-[calc(100%+1rem)] gap-3 rounded-lg px-2 py-3 text-left transition-colors hover:bg-glass focus-ring"
                >
                  {body}
                </button>
              ) : (
                <div className="flex gap-3 py-3">{body}</div>
              )}
            </li>
          );
        })}
      </ul>
      {news.error && pending ? (
        <Failed quiet onRetry={news.reload} className="mt-3">
          소식을 더 불러오지 못했어요.
        </Failed>
      ) : list.length >= limit || pending ? (
        <Button variant="quiet" size="touch-sm" disabled={pending} onClick={() => setLimit((l) => l + PAGE)} className="mt-2 w-full">
          {pending ? SAY.loading : "더 보기"}
        </Button>
      ) : null}
    </div>
  );
}
