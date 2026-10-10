"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Bell, Settings2 } from "lucide-react";
import { usePush } from "@/lib/use-push";
import { cn } from "@/lib/utils";

/**
 * The notification inbox, read from notify-svc. Separate from the approval
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
  createdAt: string;
  readAt: string | null;
};

type Settings = { muted: string[]; quietStart: string; quietEnd: string; digestAt: string };

/** The categories a sender can use (the part of `kind` before the dot). */
const CATEGORIES: [string, string, string][] = [
  ["approval", "결재 대기", "에이전트가 승인을 기다릴 때"],
  ["suggestion", "AI 제안", "약속을 일정에, 비중 이탈을 리밸런싱으로 먼저 권할 때"],
  ["job", "작업 요청", "로그인·확인이 필요할 때"],
  ["status", "수집·연결 이상", "카톡·카드 알림 수집이 멈췄을 때"],
  ["calendar", "공유 일정", "상대가 일정을 추가했을 때"],
  ["spending", "가계부", "큰 결제, 예산, 하루 지출"],
  ["assets", "자산", "비중 이탈, 입출금 감지, 하루 자산"],
];

const ago = (iso: string) => {
  const m = Math.round((Date.now() - Date.parse(iso)) / 60_000);
  if (m < 1) return "방금";
  if (m < 60) return `${m}분 전`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}시간 전`;
  const d = Math.round(h / 24);
  return d < 7 ? `${d}일 전` : new Date(iso).toLocaleDateString("ko-KR", { month: "numeric", day: "numeric" });
};

export function Notifications() {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<"list" | "settings">("list");
  const [notes, setNotes] = useState<Note[]>([]);
  const [unread, setUnread] = useState(0);
  const [failed, setFailed] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    try {
      const res = await fetch("/api/notify/notifications?limit=50", { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const body = (await res.json()) as { items: Note[]; unread: number };
      setNotes(body.items);
      setUnread(body.unread);
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }, []);

  // The badge stays current while the console is open; a push arriving means
  // the next look already has it.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the fetch sets state when it resolves
    void load();
    const id = window.setInterval(() => void load(), 60_000);
    const back = () => document.visibilityState === "visible" && void load();
    document.addEventListener("visibilitychange", back);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", back);
    };
  }, [load]);

  // A grouped push ("밤사이 알림 3건") opens the console with ?inbox=1.
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("inbox") === "1") {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- opened once, from the URL a tap arrived with
      setOpen(true);
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open]);

  const markRead = async (ids: number[]) => {
    setNotes((ns) => ns.map((n) => (ids.length === 0 || ids.includes(n.id) ? { ...n, readAt: n.readAt ?? new Date().toISOString() } : n)));
    setUnread((u) => (ids.length === 0 ? 0 : Math.max(0, u - ids.filter((id) => notes.find((n) => n.id === id && !n.readAt)).length)));
    await fetch("/api/notify/notifications/read", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ ids }),
    }).catch(() => {});
  };

  return (
    <div ref={wrap} className="relative">
      <button
        type="button"
        onClick={() => {
          setOpen((v) => !v);
          setView("list");
          void load();
        }}
        aria-expanded={open}
        aria-label={unread ? `알림 ${unread}개 안 읽음` : "알림"}
        className="relative flex size-11 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:size-8"
      >
        <Bell aria-hidden className="size-[18px] sm:size-4" />
        {unread ? (
          <span aria-hidden className="absolute top-2.5 right-2.5 size-[7px] rounded-full bg-reject ring-2 ring-background sm:top-1 sm:right-1" />
        ) : null}
      </button>

      {open ? (
        <div className="absolute top-full right-0 z-50 mt-1 w-[21rem] overflow-hidden rounded-xl border border-edge bg-background/95 shadow-lg backdrop-blur-md max-sm:fixed max-sm:inset-x-3 max-sm:top-[calc(env(safe-area-inset-top)+3.5rem)] max-sm:w-auto">
          <div className="flex items-center justify-between gap-2 border-b border-edge-soft px-3.5 py-2">
            <p className="text-[13px] text-foreground">{view === "list" ? "알림" : "알림 설정"}</p>
            <div className="flex items-center gap-1">
              {view === "list" && unread ? (
                <button
                  type="button"
                  onClick={() => void markRead([])}
                  className="min-h-8 rounded px-1.5 text-[11.5px] text-faint outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  모두 읽음
                </button>
              ) : null}
              <button
                type="button"
                onClick={() => setView((v) => (v === "list" ? "settings" : "list"))}
                aria-label={view === "list" ? "알림 설정" : "알림 목록"}
                aria-pressed={view === "settings"}
                className="flex size-8 items-center justify-center rounded text-faint outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                {view === "list" ? <Settings2 aria-hidden className="size-4" /> : <Bell aria-hidden className="size-4" />}
              </button>
            </div>
          </div>

          {view === "list" ? (
            <ul className="max-h-[min(28rem,70vh)] overflow-y-auto overscroll-contain">
              {failed && notes.length === 0 ? (
                <li className="px-3.5 py-6 text-center text-[12.5px] text-dim">알림 서비스에 연결하지 못했어요.</li>
              ) : notes.length === 0 ? (
                <li className="px-3.5 py-6 text-center text-[12.5px] text-faint">아직 알림이 없어요.</li>
              ) : (
                notes.map((n) => (
                  <li key={n.id}>
                    <button
                      type="button"
                      onClick={() => {
                        if (!n.readAt) void markRead([n.id]);
                        setOpen(false);
                        router.push(n.url || "/");
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
                          <span className={cn("truncate text-[12.5px]", n.readAt ? "text-dim" : "text-foreground")}>{n.title}</span>
                          <span className="shrink-0 text-[11px] text-faint">{ago(n.createdAt)}</span>
                        </span>
                        {n.body ? <span className="mt-0.5 block text-[12px] leading-relaxed text-dim">{n.body}</span> : null}
                      </span>
                    </button>
                  </li>
                ))
              )}
            </ul>
          ) : (
            <NotifySettings />
          )}
        </div>
      ) : null}
    </div>
  );
}

function NotifySettings() {
  const push = usePush();
  const [set, setSet] = useState<Settings | null>(null);
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
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
    <div className="max-h-[min(30rem,72vh)] space-y-4 overflow-y-auto overscroll-contain px-3.5 py-3 text-[12.5px]">
      <section>
        <p className="mb-1.5 text-[11.5px] text-faint">이 기기</p>
        <p className="leading-relaxed text-dim">{device[push.state]}</p>
        <div className="mt-2 flex flex-wrap gap-2">
          {push.state === "off" || push.state === "unavailable" ? (
            <button
              type="button"
              disabled={push.busy}
              onClick={() => void push.enable()}
              className="min-h-9 rounded-lg bg-glass-raised px-3 text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
            >
              알림 받기
            </button>
          ) : null}
          {push.state === "on" ? (
            <>
              <button
                type="button"
                onClick={() => void fetch("/api/notify/test", { method: "POST" })}
                className="min-h-9 rounded-lg bg-glass-raised px-3 text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                시험 알림 보내기
              </button>
              <button
                type="button"
                disabled={push.busy}
                onClick={() => void push.disable()}
                className="min-h-9 rounded-lg px-3 text-faint outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
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
            <p className="mb-1 text-[11.5px] text-faint">받을 알림</p>
            <ul className="divide-y divide-edge-soft">
              {CATEGORIES.map(([id, label, hint]) => {
                const on = !set.muted.includes(id);
                return (
                  <li key={id}>
                    <label className="flex min-h-11 cursor-pointer items-center gap-3 py-1.5">
                      <span className="min-w-0 flex-1">
                        <span className="block text-foreground/90">{label}</span>
                        <span className="block text-[11px] text-faint">{hint}</span>
                      </span>
                      <input
                        type="checkbox"
                        checked={on}
                        onChange={() =>
                          void save({ ...set, muted: on ? [...set.muted, id] : set.muted.filter((m) => m !== id) })
                        }
                        className="size-4 accent-foreground"
                      />
                    </label>
                  </li>
                );
              })}
            </ul>
            <p className="mt-1 text-[11px] leading-relaxed text-faint">끈 알림도 알림함에는 남아요.</p>
          </section>

          <section className="space-y-2">
            <p className="text-[11.5px] text-faint">시간</p>
            <label className="flex items-center justify-between gap-3">
              <span className="text-dim">방해 금지</span>
              <span className="flex items-center gap-1.5">
                <input
                  type="time"
                  value={set.quietStart}
                  onChange={(e) => void save({ ...set, quietStart: e.target.value })}
                  className="tnum h-9 rounded-lg border border-edge-soft bg-glass px-2 text-[16px] text-foreground sm:text-[12.5px]"
                />
                <span className="text-faint">~</span>
                <input
                  type="time"
                  value={set.quietEnd}
                  onChange={(e) => void save({ ...set, quietEnd: e.target.value })}
                  className="tnum h-9 rounded-lg border border-edge-soft bg-glass px-2 text-[16px] text-foreground sm:text-[12.5px]"
                />
              </span>
            </label>
            <label className="flex items-center justify-between gap-3">
              <span className="text-dim">하루 요약</span>
              <input
                type="time"
                value={set.digestAt}
                onChange={(e) => void save({ ...set, digestAt: e.target.value })}
                className="tnum h-9 rounded-lg border border-edge-soft bg-glass px-2 text-[16px] text-foreground sm:text-[12.5px]"
              />
            </label>
            <p className="text-[11px] leading-relaxed text-faint">
              방해 금지 시간에 생긴 알림은 끝나는 시각에 한 번에 와요. 큰 결제·비중 이탈 같은 건 하루 요약으로 모아서 와요.
            </p>
            {note ? <p className="text-[11.5px] text-reject/90">{note}</p> : null}
          </section>
        </>
      ) : null}
    </div>
  );
}
