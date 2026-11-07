"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { openTarget, targetOf } from "@/lib/focus";
import { MIND_KEY, NOTE } from "@/lib/kinds";
import { onInboxRequest } from "@/lib/resume";
import { Bell, Settings2 } from "lucide-react";
import { HeaderPanel } from "@/components/shell/panel";
import { useRole } from "@/components/shell/role";
import { Button } from "@/components/ui/button";
import { Pager } from "@/components/ui/pager";
import { Popover, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { usePush } from "@/lib/use-push";
import { HttpError, useResource } from "@/lib/use-resource";
import { readable } from "@/lib/brief";
import { dayOf, monthDay } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * The notification inbox, read from output-svc. Separate from the approval
 * ledger (기록): that is what was decided, this is what happened - a card
 * waiting, a collector stopped, a budget crossed, the evening's summary.
 *
 * Every push lands here too, so a notification swiped away on the phone is
 * not lost. The same popover holds the settings: whether this device gets
 * pushes, which kinds, quiet hours and when the digest goes out.
 */
type Note = {
  id: number;
  kind: string;
  tier: "now" | "digest" | "log";
  level: "info" | "warn" | "alert";
  title: string;
  body: string;
  url: string;
  /** The sender's key: "mind:<intent>" for what mind sent. */
  key?: string | null;
  createdAt: string;
  readAt: string | null;
};

/**
 * What the operator said of one of mind's notices: it helped, or it was not
 * needed. mind keeps it and holds the next notices of the kind to it; this
 * device keeps it too, only so the inbox shows what was pressed.
 */
type Heard = "useful" | "useless";

const HEARD_KEY = "jarvis.heard";

function readHeard(): Record<string, Heard> {
  try {
    const raw = window.localStorage.getItem(HEARD_KEY);
    return raw ? (JSON.parse(raw) as Record<string, Heard>) : {};
  } catch {
    return {};
  }
}

function keepHeard(all: Record<string, Heard>) {
  try {
    // The latest few hundred: the inbox pages back further than anyone answers.
    window.localStorage.setItem(HEARD_KEY, JSON.stringify(Object.fromEntries(Object.entries(all).slice(-300))));
  } catch {
    // Not kept here; mind has it.
  }
}

/** The intent a notice of mind's was (mind.info): what the one tap answers. Cards and what was done and told are answered elsewhere. */
const noticeOf = (n: Note) => (n.kind === NOTE.mindInfo && n.key?.startsWith(MIND_KEY) ? n.key.slice(MIND_KEY.length) : null);

type Settings = { muted: string[]; quietStart: string; quietEnd: string; digestAt: string };

/** A category a sender can use (the part of `kind` before the dot). output-svc owns the list. */
type Category = { id: string; label: string; hint: string };

const ago = (iso: string) => {
  const m = Math.round((Date.now() - Date.parse(iso)) / 60_000);
  if (m < 1) return "방금";
  if (m < 60) return `${m}분 전`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}시간 전`;
  const d = Math.round(h / 24);
  // Older than a week, the day itself - as every screen names one (lib/when.ts).
  return d < 7 ? `${d}일 전` : monthDay(dayOf(new Date(iso)));
};

/** How many the inbox shows a page: output-svc counts them all and sends one page. */
const INBOX_PAGE = 20;

/** The inbox before its first read: one empty list, the same each time it is drawn. */
const NO_NOTES: Note[] = [];

export function Notifications() {
  const router = useRouter();
  const role = useRole();
  const [heard, setHeard] = useState<Record<string, Heard>>(() => (typeof window === "undefined" ? {} : readHeard()));
  const [unheard, setUnheard] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<"list" | "settings">("list");
  const [page, setPage] = useState(1);
  const list = useRef<HTMLUListElement>(null);
  // The page the timer and a return to the app read again.
  const shownPage = useRef(1);

  // The badge stays current while the console is open - read every minute
  // while it is on screen, and again on coming back to it; a push arriving
  // means the next look already has it.
  const inbox = useResource(
    "inbox",
    async (signal) => {
      const p = shownPage.current;
      const res = await fetch(`/api/notify/notifications?limit=${INBOX_PAGE}&offset=${(p - 1) * INBOX_PAGE}`, {
        cache: "no-store",
        signal,
      });
      if (!res.ok) throw new HttpError(res.status);
      const body = (await res.json()) as { items: Note[]; unread: number; total?: number };
      return { notes: body.items, unread: body.unread, total: body.total ?? body.items.length };
    },
    { interval: 60_000 },
  );
  const notes = inbox.data?.notes ?? NO_NOTES;
  const unread = inbox.data?.unread ?? 0;
  const total = inbox.data?.total ?? 0;
  const failed = inbox.error !== null;
  const load = inbox.reload;

  const turn = (p: number) => {
    shownPage.current = p;
    setPage(p);
    void load();
    list.current?.scrollTo({ top: 0 });
  };

  // A card decided on this screen: its "결재 대기" was just marked read.
  useEffect(() => {
    const decided = () => void load();
    window.addEventListener("jarvis:decided", decided);
    return () => window.removeEventListener("jarvis:decided", decided);
  }, [load]);

  // A grouped push ("밤사이 알림 3건") opens the console with ?inbox=1.
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("inbox") === "1") {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- opened once, from the URL a tap arrived with
      setOpen(true);
    }
    // ...or, with the console already open, tells it so (lib/resume.ts).
    return onInboxRequest(() => setOpen(true));
  }, []);

  // One tap, kept at once; taken back when mind did not take it.
  const say = async (id: string, v: Heard) => {
    const before = heard;
    const next = { ...heard, [id]: v };
    setHeard(next);
    setUnheard(null);
    keepHeard(next);
    const res = await fetch(`/api/mind/intents/${encodeURIComponent(id)}/${v}`, { method: "POST", keepalive: true }).catch(() => null);
    if (!res?.ok) {
      setHeard(before);
      keepHeard(before);
      setUnheard(id);
    }
  };

  const markRead = async (ids: number[]) => {
    inbox.mutate((cur) =>
      cur && {
        ...cur,
        notes: cur.notes.map((n) => (ids.length === 0 || ids.includes(n.id) ? { ...n, readAt: n.readAt ?? new Date().toISOString() } : n)),
        unread: ids.length === 0 ? 0 : Math.max(0, cur.unread - ids.filter((id) => notes.find((n) => n.id === id && !n.readAt)).length),
      },
    );
    await fetch("/api/notify/notifications/read", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ ids }),
    }).catch(() => {});
  };

  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) return;
        setView("list");
        // Opened, the inbox starts at its newest page.
        shownPage.current = 1;
        setPage(1);
        void load();
      }}
    >
      <PopoverTrigger
        aria-label={unread ? `알림 ${unread}개 안 읽음` : "알림"}
        className="relative flex size-11 items-center justify-center rounded-lg text-faint transition-colors hover:text-dim focus-ring mouse:size-8"
      >
        <Bell aria-hidden className="size-[18px] mouse:size-4" />
        {unread ? (
          <span aria-hidden className="absolute top-2.5 right-2.5 size-[7px] rounded-full bg-reject ring-2 ring-background sm:top-1 sm:right-1" />
        ) : null}
      </PopoverTrigger>

      {open ? (
        <HeaderPanel
          title={view === "list" ? "알림" : "알림 설정"}
          aside={
            <>
              {view === "list" && unread ? (
                <button
                  type="button"
                  onClick={() => void markRead([])}
                  className="tap rounded px-1.5 text-meta text-faint hover:text-dim focus-ring mouse:min-h-8 mouse:min-w-0"
                >
                  모두 읽음
                </button>
              ) : null}
              <button
                type="button"
                onClick={() => setView((v) => (v === "list" ? "settings" : "list"))}
                aria-label={view === "list" ? "알림 설정" : "알림 목록"}
                aria-pressed={view === "settings"}
                className="flex size-11 items-center justify-center rounded text-faint hover:text-dim focus-ring mouse:size-8"
              >
                {view === "list" ? <Settings2 aria-hidden className="size-4" /> : <Bell aria-hidden className="size-4" />}
              </button>
            </>
          }
        >

          {/* Up and down only. A card's alert once carried an 86-character
              checkout URL, and the whole list slid sideways under the thumb;
              long words wrap now (globals.css), and nothing here pans. */}
          {view === "list" ? (
            <>
            <ul ref={list} className="max-h-[min(28rem,70vh)] overflow-x-hidden overflow-y-auto overscroll-contain">
              {failed && notes.length === 0 ? (
                <li className="px-3.5 py-6 text-center text-aside text-dim">알림 서비스에 연결하지 못했어요.</li>
              ) : notes.length === 0 ? (
                <li className="px-3.5 py-6 text-center text-aside text-faint">아직 알림이 없어요.</li>
              ) : (
                notes.map((n) => {
                  const about = role === "owner" ? noticeOf(n) : null;
                  // One line of title, two of body: a job's report ran to
                  // 300 characters of document here.
                  const said = readable(n.title, n.body);
                  return (
                  <li key={n.id}>
                    <button
                      type="button"
                      onClick={() => {
                        if (!n.readAt) void markRead([n.id]);
                        setOpen(false);
                        // Into the screen and onto the part it is about.
                        openTarget(targetOf(n), (path) => router.push(path));
                      }}
                      className="flex w-full gap-2.5 px-3.5 py-2.5 text-left outline-none hover:bg-glass focus-visible:bg-glass"
                    >
                      <span
                        aria-hidden
                        className={cn(
                          "mt-1.5 size-[6px] shrink-0 rounded-full",
                          n.readAt ? "bg-transparent" : n.level === "alert" ? "bg-reject" : "bg-foreground/70",
                        )}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="flex items-baseline justify-between gap-2">
                          <span className={cn("truncate text-aside", n.readAt ? "text-dim" : "text-foreground")}>{said.title}</span>
                          <span className="shrink-0 text-meta text-faint">{ago(n.createdAt)}</span>
                        </span>
                        {said.body ? <span className="mt-0.5 line-clamp-2 text-aside leading-relaxed text-dim">{said.body}</span> : null}
                      </span>
                    </button>
                    {about ? <HeardRow said={heard[about]} failed={unheard === about} onSay={(v) => void say(about, v)} /> : null}
                  </li>
                  );
                })
              )}
            </ul>
            <Pager
              page={page}
              pageCount={Math.max(1, Math.ceil(total / INBOX_PAGE))}
              total={total}
              from={(page - 1) * INBOX_PAGE + 1}
              to={(page - 1) * INBOX_PAGE + notes.length}
              go={turn}
              className="mt-0 border-t border-edge-soft px-3.5 py-1.5"
            />
            </>
          ) : (
            <NotifySettings />
          )}
        </HeaderPanel>
      ) : null}
    </Popover>
  );
}

/**
 * The one tap on a notice of mind's, under its words: aligned with them, past
 * the unread dot (each button's px-1 inside the row's inset), as close under
 * them as the 44px press allows. Pressed, it stays pressed; pressing the other
 * changes it.
 */
function HeardRow({ said, failed, onSay }: { said?: Heard; failed: boolean; onSay: (v: Heard) => void }) {
  return (
    <div role="group" aria-label="이 알림이 쓸모 있었는지" className="flex items-center gap-2 pr-3.5 pb-2 pl-[1.625rem] text-meta">
      {(["useful", "useless"] as const).map((v) => (
        <button
          key={v}
          type="button"
          aria-pressed={said === v}
          onClick={() => onSay(v)}
          className={cn(
            "tap -my-3 rounded px-1 transition-colors focus-ring mouse:-my-1 mouse:min-h-7 mouse:min-w-0",
            said === v ? "text-foreground" : "text-faint hover:text-dim",
          )}
        >
          {v === "useful" ? "도움됐어요" : "필요 없어요"}
        </button>
      ))}
      {failed ? <span role="status" className="text-reject">보내지 못했어요</span> : null}
    </div>
  );
}

/**
 * When the operator answers mind's cards, as mind counts it (GET /api/attention):
 * the evening hour they answer most at, with how often - offered for the digest,
 * whose time stays theirs to set.
 */
type Attention = { known: boolean; cards: number; evening?: { hour: number; rate: number; rang: number } };

function NotifySettings() {
  const push = usePush();
  const [set, setSet] = useState<Settings | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [categories, setCategories] = useState<Category[]>([]);
  const [attention, setAttention] = useState<Attention | null>(null);

  useEffect(() => {
    // The operator's only; the guest's token is refused, and nothing is offered.
    void fetch("/api/mind/attention", { cache: "no-store" })
      .then((r) => (r.ok ? (r.json() as Promise<Attention>) : null))
      .then((a) => a && setAttention(a))
      .catch(() => {});
    void fetch("/api/notify/categories", { cache: "no-store" })
      .then((r) => (r.ok ? (r.json() as Promise<{ categories: Category[] }>) : null))
      .then((b) => b && setCategories(b.categories ?? []))
      .catch(() => {});
    void fetch("/api/notify/settings", { cache: "no-store" })
      .then((r) => (r.ok ? (r.json() as Promise<Settings>) : null))
      .then((s) => s && setSet(s))
      .catch(() => {});
  }, []);

  const save = async (next: Settings) => {
    setSet(next);
    const res = await fetch("/api/notify/settings", {
      method: "PUT",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(next),
    }).catch(() => null);
    if (!res?.ok) {
      const msg = (await res?.json().catch(() => null)) as { error?: string } | null;
      setNote(msg?.error ?? "저장하지 못했어요.");
    } else setNote(null);
  };

  const device: Record<string, string> = {
    loading: "확인하는 중…",
    unsupported: "이 브라우저는 푸시 알림을 받을 수 없어요.",
    "needs-home-screen": "아이폰은 홈 화면에 추가한 앱에서만 받을 수 있어요. 공유 → 홈 화면에 추가 후 거기서 켜 주세요.",
    off: "이 기기는 알림을 받지 않아요.",
    on: "이 기기로 알림이 와요.",
    denied: "브라우저에서 알림이 차단돼 있어요. 기기 설정에서 허용해 주세요.",
    unavailable: "알림 서비스에 연결하지 못했어요.",
  };

  return (
    <div className="max-h-[min(30rem,72vh)] space-y-4 overflow-x-hidden overflow-y-auto overscroll-contain px-3.5 py-3 text-aside">
      <section>
        <p className="mb-1.5 text-meta text-faint">이 기기</p>
        <p className="leading-relaxed text-dim">{device[push.state]}</p>
        <div className="mt-2 flex flex-wrap gap-2">
          {push.state === "off" || push.state === "unavailable" ? (
            <button
              type="button"
              disabled={push.busy}
              onClick={() => void push.enable()}
              className="min-h-11 rounded-lg bg-glass-raised px-3 text-foreground focus-ring disabled:opacity-50 mouse:min-h-9"
            >
              알림 받기
            </button>
          ) : null}
          {push.state === "on" ? (
            <>
              <button
                type="button"
                onClick={() => void fetch("/api/notify/test", { method: "POST" })}
                className="min-h-11 rounded-lg bg-glass-raised px-3 text-foreground focus-ring mouse:min-h-9"
              >
                시험 알림 보내기
              </button>
              <button
                type="button"
                disabled={push.busy}
                onClick={() => void push.disable()}
                className="min-h-11 rounded-lg px-3 text-faint hover:text-dim focus-ring disabled:opacity-50 mouse:min-h-9"
              >
                이 기기 끄기
              </button>
            </>
          ) : null}
        </div>
      </section>

      {set ? (
        <>
          <section>
            <p className="mb-1 text-meta text-faint">받을 알림</p>
            <ul className="divide-y divide-edge-soft">
              {categories.map(({ id, label, hint }) => {
                const on = !set.muted.includes(id);
                return (
                  <li key={id}>
                    <label className="flex min-h-11 cursor-pointer items-center gap-3 py-1.5">
                      <span className="min-w-0 flex-1">
                        <span className="block text-ink">{label}</span>
                        <span className="block text-meta text-faint">{hint}</span>
                      </span>
                      <Switch
                        checked={on}
                        onCheckedChange={() =>
                          void save({ ...set, muted: on ? [...set.muted, id] : set.muted.filter((m) => m !== id) })
                        }
                      />
                    </label>
                  </li>
                );
              })}
            </ul>
            <p className="mt-1 text-meta leading-relaxed text-faint">끈 알림도 알림함에는 남아요.</p>
          </section>

          <section className="space-y-2">
            <p className="text-meta text-faint">시간</p>
            {/* Two time fields are wide where a browser draws a clock in
                each (Chrome, 136px at 16px): on a narrow phone they go under
                the words, to the right, rather than past the edge. */}
            <label className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5">
              <span className="text-dim">방해 금지</span>
              <span className="ms-auto flex items-center gap-1.5">
                <input
                  type="time"
                  value={set.quietStart}
                  onChange={(e) => void save({ ...set, quietStart: e.target.value })}
                  className="tnum h-11 rounded-lg border border-edge-soft bg-glass px-2 text-field text-foreground mouse:h-9 mouse:text-aside"
                />
                <span className="text-faint">~</span>
                <input
                  type="time"
                  value={set.quietEnd}
                  onChange={(e) => void save({ ...set, quietEnd: e.target.value })}
                  className="tnum h-11 rounded-lg border border-edge-soft bg-glass px-2 text-field text-foreground mouse:h-9 mouse:text-aside"
                />
              </span>
            </label>
            <label className="flex items-center justify-between gap-3">
              <span className="text-dim">하루 요약</span>
              <input
                type="time"
                value={set.digestAt}
                onChange={(e) => void save({ ...set, digestAt: e.target.value })}
                className="tnum h-11 rounded-lg border border-edge-soft bg-glass px-2 text-field text-foreground mouse:h-9 mouse:text-aside"
              />
            </label>
            <DigestHint attention={attention} digestAt={set.digestAt} onUse={(at) => void save({ ...set, digestAt: at })} />
            <p className="text-meta leading-relaxed text-faint">
              방해 금지 시간에 생긴 알림은 끝나는 시각에 한 번에 와요. 큰 결제·비중 이탈 같은 건 하루 요약으로 모아서 와요.
            </p>
            {note ? <p className="text-meta text-reject">{note}</p> : null}
          </section>
        </>
      ) : null}
    </div>
  );
}

/**
 * The evening hour the operator answers cards at most, under the digest's time:
 * a line when it is already there, and a way to move it when it is not. Nothing
 * until there have been cards enough to say.
 */
function DigestHint({
  attention,
  digestAt,
  onUse,
}: {
  attention: Attention | null;
  digestAt: string;
  onUse: (at: string) => void;
}) {
  const best = attention?.known ? attention.evening : undefined;
  if (!best) return null;
  const at = `${String(best.hour).padStart(2, "0")}:00`;
  const pct = Math.round(best.rate * 100);
  const here = Number(digestAt.slice(0, 2)) === best.hour;
  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-meta leading-relaxed text-faint">
      <span>
        {here
          ? `요즘 ${best.hour}시 전후에 카드에 가장 잘 답해요(${pct}%). 요약도 그때 와요.`
          : `요즘 ${best.hour}시 전후에 카드에 가장 잘 답해요(${pct}%).`}
      </span>
      {here ? null : (
        // A word to press inside the sentence: no pill, no edge, a thumb's height.
        <Button variant="underline" size="inline" onClick={() => onUse(at)} className="tap ms-1 text-meta">
          요약을 {best.hour}시로
        </Button>
      )}
    </p>
  );
}
