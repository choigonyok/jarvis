"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { openTarget, targetOf } from "@/lib/focus";
import { SAY, failureText } from "@/lib/say";
import { MIND_KEY, NOTE } from "@/lib/kinds";
import { onInboxRequest } from "@/lib/resume";
import { Bell, Settings2 } from "lucide-react";
import { AsOf } from "@/components/shell/as-of";
import { Failed } from "@/components/shell/failed";
import { HeaderPanel } from "@/components/shell/panel";
import { useRole } from "@/components/shell/role";
import { Button } from "@/components/ui/button";
import { Pager } from "@/components/ui/pager";
import { Popover, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { usePush } from "@/lib/use-push";
import { HttpError, useResource } from "@/lib/use-resource";
import { readable } from "@/lib/brief";
import { ago } from "@/lib/when";
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

/** How many the inbox shows a page: output-svc counts them all and sends one page. */
const INBOX_PAGE = 20;

/** The inbox before its first read: one empty list, the same each time it is drawn. */
const NO_NOTES: Note[] = [];

/**
 * The bell as the last read left it. Every screen draws its own header, and
 * a new header's bell had no dot until its own first read came back: the dot
 * went out and came on again at each change of tab.
 */
let lastBell = { unread: 0, alert: false };

export function Notifications() {
  const router = useRouter();
  const role = useRole();
  const [heard, setHeard] = useState<Record<string, Heard>>(() => (typeof window === "undefined" ? {} : readHeard()));
  const [unheard, setUnheard] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<"list" | "settings">("list");
  // A page pressed, while its list is on the way.
  const [turning, setTurning] = useState<number | null>(null);
  // Marked read here, and not kept by the service.
  const [unmarked, setUnmarked] = useState(false);
  const list = useRef<HTMLUListElement>(null);
  // The page the next read asks for: the timer, a return to the app, a page pressed.
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
      if (!res.ok) throw new HttpError(res.status, SAY.couldNotLoad);
      const body = (await res.json()) as { items: Note[]; unread: number; unreadAlert?: number; total?: number };
      return {
        notes: body.items,
        unread: body.unread,
        unreadAlert: body.unreadAlert,
        total: body.total ?? body.items.length,
        page: p,
      };
    },
    { interval: 60_000 },
  );
  const notes = inbox.data?.notes ?? NO_NOTES;
  // The page whose list is on screen: its number changes with the list, not
  // before it - "2쪽, 21–40" stood over page 1's notices while page 2 was
  // read, and stayed there when it could not be (가계부's month, the same).
  const page = inbox.data?.page ?? 1;
  const unread = inbox.data ? inbox.data.unread : lastBell.unread;
  // Red only for an alert not read yet (a service that failed), as in the
  // list below; anything else unread is the grey of a waiting decision. Red
  // here is 반려 and 실패, and "you have mail" was neither. The count is
  // output-svc's, of every page; a service from before it sends none, and the
  // page read is looked through instead - which misses an alert further back.
  const alert = inbox.data
    ? inbox.data.unreadAlert !== undefined
      ? inbox.data.unreadAlert > 0
      : notes.some((n) => !n.readAt && n.level === "alert")
    : lastBell.alert;
  useEffect(() => {
    if (inbox.data) lastBell = { unread, alert };
  }, [inbox.data, unread, alert]);
  const total = inbox.data?.total ?? 0;
  const failed = inbox.error !== null;
  const load = inbox.reload;

  const turn = (p: number) => {
    shownPage.current = p;
    setTurning(p);
    void load().finally(() => setTurning((t) => (t === p ? null : t)));
  };
  // Shown while a page other than the one on screen is on its way.
  const asking = turning !== null && turning !== page ? turning : null;
  // A page that could not be read leaves the inbox on the one it shows, and
  // the timer reads that one again rather than turning to the other later.
  // Not while a page is on its way: its tries again ask for it too.
  useEffect(() => {
    if (open && turning === null && inbox.error) shownPage.current = page;
  }, [open, turning, inbox.error, page]);
  // A new page from its top.
  useEffect(() => {
    list.current?.scrollTo({ top: 0 });
  }, [page]);

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
    const before = inbox.data;
    // The bell's red goes out with the alerts read, at once as the count does.
    const alerts = notes.filter((n) => !n.readAt && n.level === "alert" && (ids.length === 0 || ids.includes(n.id))).length;
    setUnmarked(false);
    inbox.mutate((cur) =>
      cur && {
        ...cur,
        notes: cur.notes.map((n) => (ids.length === 0 || ids.includes(n.id) ? { ...n, readAt: n.readAt ?? new Date().toISOString() } : n)),
        unread: ids.length === 0 ? 0 : Math.max(0, cur.unread - ids.filter((id) => notes.find((n) => n.id === id && !n.readAt)).length),
        unreadAlert:
          cur.unreadAlert === undefined ? undefined : ids.length === 0 ? 0 : Math.max(0, cur.unreadAlert - alerts),
      },
    );
    const res = await fetch("/api/notify/notifications/read", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ ids }),
    }).catch(() => null);
    if (res?.ok) return;
    // Not kept: the dots come back now, with a word, and the service is asked
    // what it holds. They went out and came back a minute later, unsaid.
    inbox.mutate(() => before);
    setUnmarked(true);
    void load();
  };

  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        setUnmarked(false);
        // Closed, the timer reads the newest page, which the bell is about;
        // opened, the inbox starts there.
        if (!o) {
          shownPage.current = 1;
          return;
        }
        setView("list");
        turn(1);
      }}
    >
      <PopoverTrigger
        aria-label={unread ? `알림 ${unread}개 안 읽음` : "알림"}
        className="relative flex size-11 items-center justify-center rounded-lg text-faint transition-colors hover:text-dim focus-ring mouse:size-8"
      >
        <Bell aria-hidden className="size-[18px] mouse:size-4" />
        {unread ? (
          <span
            aria-hidden
            className={cn(
              "absolute top-2.5 right-2.5 size-[7px] rounded-full ring-2 ring-background sm:top-1 sm:right-1",
              alert ? "bg-reject" : "bg-foreground/80",
            )}
          />
        ) : null}
      </PopoverTrigger>

      {open ? (
        <HeaderPanel
          title={view === "list" ? "알림" : "알림 설정"}
          aside={
            <>
              {/* A page on its way, said as it comes; 모두 읽음 not kept; a
                  re-read that failed under a list still shown: the list is
                  the earlier read's, and says so. */}
              {view !== "list" ? null : asking !== null ? (
                <span aria-live="polite" className="tnum text-meta text-faint">
                  {asking}쪽 불러오는 중…
                </span>
              ) : unmarked ? (
                <span role="status" className="text-meta text-reject">
                  읽음으로 바꾸지 못했어요
                </span>
              ) : failed && notes.length > 0 ? (
                <AsOf at={inbox.readAt} failed onRetry={() => void load()} />
              ) : null}
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
            <ul
              ref={list}
              aria-busy={asking !== null || undefined}
              className="max-h-[min(28rem,70vh)] overflow-x-hidden overflow-y-auto overscroll-contain"
            >
              {failed && notes.length === 0 ? (
                <li className="px-3.5 py-4">
                  <Failed quiet onRetry={load}>
                    {failureText(inbox.error, SAY.couldNotLoad)}
                  </Failed>
                </li>
              ) : !inbox.data ? (
                <InboxPlaceholder />
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
                      // The ring every control has, inside the row: the list
                      // clips at its edges. The glass alone looked like a
                      // pointer resting there.
                      className="flex w-full gap-2.5 px-3.5 py-2.5 text-left hover:bg-glass focus-ring focus-visible:ring-inset"
                    >
                      {/* New is the dot, and an alert's is larger: not its red
                          alone. In the same 6px, so the words keep their line. */}
                      <span
                        aria-hidden
                        className={cn(
                          "shrink-0 rounded-full",
                          n.readAt
                            ? "mt-1.5 size-[6px]"
                            : n.level === "alert"
                              ? "mt-[5px] -mx-px size-2 bg-reject"
                              : "mt-1.5 size-[6px] bg-foreground/70",
                        )}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="flex items-baseline justify-between gap-2">
                          <span className={cn("truncate text-aside", n.readAt ? "text-dim" : "text-foreground")}>
                            {n.readAt ? null : <span className="sr-only">{n.level === "alert" ? "안 읽음, 급함. " : "안 읽음. "}</span>}
                            {said.title}
                          </span>
                          {/* When, as every screen says it (lib/when.ts): past twelve
                              hours the moment itself, "어제 21:04" - its own "15시간
                              전" and "2일 전" (36 hours, rounded) took counting back. */}
                          <span className="tnum shrink-0 text-meta text-faint">{ago(n.createdAt)}</span>
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

/** What the settings are drawn from: what is set, and the kinds there are to set it for. */
type Setup = { settings: Settings; categories: Category[] };

/** Both or neither: kinds without the settings have nothing to switch, and settings without the kinds showed no switch at all. */
async function readSetup(signal: AbortSignal): Promise<Setup> {
  const [s, c] = await Promise.all([
    fetch("/api/notify/settings", { cache: "no-store", signal }),
    fetch("/api/notify/categories", { cache: "no-store", signal }),
  ]);
  if (!s.ok || !c.ok) throw new HttpError(s.ok ? c.status : s.status, SAY.couldNotLoad);
  const settings = (await s.json()) as Settings;
  const body = (await c.json()) as { categories?: Category[] };
  return { settings, categories: body.categories ?? [] };
}

function NotifySettings() {
  const push = usePush();
  // Read as they open, and not again on coming back to the app: a read asked
  // while a save is on its way can answer with what was there before it, and
  // put the switch back.
  const setup = useResource("notify-settings", readSetup, { resume: false });
  const set = setup.data?.settings ?? null;
  const categories = setup.data?.categories ?? [];
  // A save that did not go, said beside what was pressed: the panel scrolls,
  // and a line at its foot was out of sight of a switch at its top.
  const [note, setNote] = useState<{ where: "muted" | "time"; text: string } | null>(null);
  // What a press under 이 기기 came to, said under its buttons: the test is
  // pressed to find out whether alerts arrive at all, and said nothing, sent
  // or not; a device that did not turn off went on reading as on.
  const [said, setSaid] = useState<{ text: string; failed?: boolean } | null>(null);
  const [testing, setTesting] = useState(false);
  const [attention, setAttention] = useState<Attention | null>(null);

  useEffect(() => {
    // The operator's only; the guest's token is refused, and nothing is offered.
    void fetch("/api/mind/attention", { cache: "no-store" })
      .then((r) => (r.ok ? (r.json() as Promise<Attention>) : null))
      .then((a) => a && setAttention(a))
      .catch(() => {});
  }, []);

  const save = async (next: Settings, where: "muted" | "time") => {
    const before = set;
    setup.mutate((d) => d && { ...d, settings: next });
    const res = await fetch("/api/notify/settings", {
      method: "PUT",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(next),
    }).catch(() => null);
    if (res?.ok) {
      setNote(null);
      return;
    }
    // Back to what the service still holds: a switch left where it was
    // flipped said the alerts had stopped while they kept coming.
    setup.mutate((d) => (d && before ? { ...d, settings: before } : d));
    setNote({ where, text: SAY.couldNotSave });
  };

  const sendTest = async () => {
    setTesting(true);
    setSaid(null);
    try {
      const res = await fetch("/api/notify/test", { method: "POST" });
      setSaid(res.ok ? { text: "보냈어요 · 곧 도착해요" } : { text: "보내지 못했어요.", failed: true });
    } catch (err) {
      setSaid({ text: failureText(err, "보내지 못했어요."), failed: true });
    } finally {
      setTesting(false);
    }
  };

  const turnOff = async () => {
    setSaid(null);
    if (!(await push.disable())) setSaid({ text: "끄지 못했어요.", failed: true });
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
              onClick={() => {
                setSaid(null);
                void push.enable();
              }}
              className="min-h-11 rounded-lg bg-glass-raised px-3 text-foreground focus-ring disabled:opacity-50 mouse:min-h-9"
            >
              {push.busy ? "켜는 중…" : "알림 받기"}
            </button>
          ) : null}
          {push.state === "on" ? (
            <>
              <button
                type="button"
                disabled={testing}
                onClick={() => void sendTest()}
                className="min-h-11 rounded-lg bg-glass-raised px-3 text-foreground focus-ring disabled:opacity-50 mouse:min-h-9"
              >
                {testing ? "보내는 중…" : "시험 알림 보내기"}
              </button>
              <button
                type="button"
                disabled={push.busy}
                onClick={() => void turnOff()}
                className="min-h-11 rounded-lg px-3 text-faint hover:text-dim focus-ring disabled:opacity-50 mouse:min-h-9"
              >
                {push.busy ? "끄는 중…" : "이 기기 끄기"}
              </button>
            </>
          ) : null}
        </div>
        {said ? (
          <p role="status" className={cn("mt-1.5 text-meta", said.failed ? "text-reject" : "text-faint")}>
            {said.text}
          </p>
        ) : null}
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
                          void save({ ...set, muted: on ? [...set.muted, id] : set.muted.filter((m) => m !== id) }, "muted")
                        }
                      />
                    </label>
                  </li>
                );
              })}
            </ul>
            {note?.where === "muted" ? (
              <p role="status" className="mt-1 text-meta text-reject">
                {note.text}
              </p>
            ) : null}
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
                <TimeField label="방해 금지 시작" value={set.quietStart} onSave={(v) => void save({ ...set, quietStart: v }, "time")} />
                <span className="text-faint">~</span>
                <TimeField label="방해 금지 끝" value={set.quietEnd} onSave={(v) => void save({ ...set, quietEnd: v }, "time")} />
              </span>
            </label>
            <label className="flex items-center justify-between gap-3">
              <span className="text-dim">하루 요약</span>
              <TimeField label="하루 요약 시각" value={set.digestAt} onSave={(v) => void save({ ...set, digestAt: v }, "time")} />
            </label>
            <DigestHint
              attention={attention}
              digestAt={set.digestAt}
              onUse={(at) => void save({ ...set, digestAt: at }, "time")}
            />
            <p className="text-meta leading-relaxed text-faint">
              방해 금지 시간에 생긴 알림은 끝나는 시각에 한 번에 와요. 큰 결제·비중 이탈 같은 건 하루 요약으로 모아서 와요.
            </p>
            {note?.where === "time" ? (
              <p role="status" className="text-meta text-reject">
                {note.text}
              </p>
            ) : null}
          </section>
        </>
      ) : setup.error ? (
        // Not left out: without them the settings read as this device's alone.
        <Failed quiet onRetry={setup.reload}>
          {failureText(setup.error, SAY.couldNotLoad)}
        </Failed>
      ) : (
        <SettingsPlaceholder />
      )}
    </div>
  );
}

/**
 * The inbox while its first read is on its way: rows of a notice's shape,
 * which the notices come into. "아직 알림이 없어요" stood there first, then
 * gave way to the list.
 */
function InboxPlaceholder() {
  return (
    <li>
      <p role="status" className="sr-only">
        {SAY.loading}
      </p>
      <div aria-hidden>
        {["w-3/5", "w-2/5", "w-1/2"].map((width) => (
          <div key={width} className="flex gap-2.5 px-3.5 py-2.5">
            <span className="mt-1.5 size-[6px] shrink-0" />
            <span className="min-w-0 flex-1">
              <span className={cn("block h-3.5 rounded bg-well", width)} />
              <span className="mt-2 block h-3 w-4/5 rounded bg-well" />
            </span>
          </div>
        ))}
      </div>
    </li>
  );
}

/** The kinds' switches and the times while they are read, as quiet blocks of their shape. */
function SettingsPlaceholder() {
  return (
    <div>
      <p role="status" className="sr-only">
        {SAY.loading}
      </p>
      <div aria-hidden className="space-y-4">
        <div>
          <span className="mb-1.5 block h-3 w-14 rounded bg-well" />
          {["w-16", "w-24", "w-20"].map((width) => (
            <div key={width} className="flex min-h-11 items-center gap-3 py-1.5">
              <span className="min-w-0 flex-1">
                <span className={cn("block h-3.5 rounded bg-well", width)} />
                <span className="mt-1.5 block h-3 w-36 rounded bg-well" />
              </span>
              <span className="h-6 w-10 shrink-0 rounded-full bg-well" />
            </div>
          ))}
        </div>
        <div className="space-y-2">
          <span className="block h-3 w-8 rounded bg-well" />
          {["w-14", "w-16"].map((width) => (
            <div key={width} className="flex items-center justify-between gap-3">
              <span className={cn("block h-3.5 rounded bg-well", width)} />
              <span className="h-11 w-24 rounded-lg bg-well mouse:h-9" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

/**
 * A time that is saved once it has been picked, not at every stop of the way:
 * a phone's wheel says change at each hour it passes, and each was a save of
 * the whole settings, any of which could land last. Left empty (a desktop
 * field cleared), it goes back to what was saved. Saved too when the panel
 * closes around it - a field taken away while focused is not blurred.
 */
function TimeField({ label, value, onSave }: { label: string; value: string; onSave: (value: string) => void }) {
  const [draft, setDraft] = useState(value);
  const [was, setWas] = useState(value);
  if (value !== was) {
    setWas(value);
    setDraft(value);
  }
  const latest = useRef({ draft, value, onSave });
  useLayoutEffect(() => {
    latest.current = { draft, value, onSave };
  });
  const commit = () => {
    const { draft: d, value: v, onSave: save } = latest.current;
    if (d && d !== v) save(d);
    else if (!d) setDraft(v);
  };
  useEffect(
    () => () => {
      const { draft: d, value: v, onSave: save } = latest.current;
      if (d && d !== v) save(d);
    },
    [],
  );
  return (
    <input
      type="time"
      aria-label={label}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      className="tnum h-11 rounded-lg border border-edge-soft bg-glass px-2 text-field text-foreground mouse:h-9 mouse:text-aside"
    />
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
