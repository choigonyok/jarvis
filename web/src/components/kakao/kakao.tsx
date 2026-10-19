"use client";

import { useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { ChevronDown, Search } from "lucide-react";
import { Header, TabBar } from "@/components/shell/header";
import { Retry } from "@/components/shell/retry";
import { StandingBar } from "@/components/shell/standing-bar";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { useKakao, type KakaoMessage, type KakaoRoom, type Source } from "@/lib/use-kakao";
import { useThread } from "@/lib/use-thread";
import { isPending } from "@/lib/thread";
import { cn } from "@/lib/utils";

/**
 * What the collectors have read out of KakaoTalk.
 *
 * This is a reading surface, not a chat: nothing here sends, and the operator
 * cannot change anything. So it borrows the transcript's grammar rather than
 * inventing one - the agent's own words are borderless text on the left, and
 * the glass capsule is reserved for the operator's, on the right. A KakaoTalk
 * conversation splits the same way, which is why `mine` is worth the trouble
 * the collector went to in order to determine it.
 */
const SOURCE_KEY = "jarvis.messages-source";

export function Kakao() {
  const { proposals, connection } = useThread();
  const waitingList = proposals.filter(isPending);
  const pending = waitingList.length;
  // The last messenger looked at, remembered per browser. Read on the client
  // only, so the server render (always 카카오톡) never disagrees with it.
  const source = useSyncExternalStore<Source>(
    (fn) => {
      addEventListener("jarvis:messages-source", fn);
      return () => removeEventListener("jarvis:messages-source", fn);
    },
    () => {
      try {
        return localStorage.getItem(SOURCE_KEY) === "imessage" ? "imessage" : "kakao";
      } catch {
        return "kakao";
      }
    },
    () => "kakao",
  );
  const pick = (s: Source) => {
    try {
      localStorage.setItem(SOURCE_KEY, s);
    } catch {}
    dispatchEvent(new Event("jarvis:messages-source"));
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={pending} />
      <StandingBar pending={waitingList} href="/record" />
      <div className="px-gutter mx-auto w-full max-w-[46rem] shrink-0 pt-2 sm:pt-3">
        <div role="tablist" aria-label="메신저" className="inline-flex gap-0.5 rounded-lg border border-edge-soft bg-glass p-0.5">
          {(
            [
              ["kakao", "카카오톡"],
              ["imessage", "iMessage"],
            ] as const
          ).map(([key, label]) => (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={source === key}
              onClick={() => pick(key)}
              className={cn(
                "tap-y min-h-8 rounded-md px-3 text-[12.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                source === key ? "bg-glass-raised text-foreground" : "text-faint hover:text-dim",
              )}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      {/* Each source keeps its own rooms and scroll; switching starts fresh. */}
      <Feed key={source} source={source} />
      <TabBar pending={pending} />
    </div>
  );
}

function Feed({ source }: { source: Source }) {
  const { rooms, selected, setSelected, messages, error, hydrated, reload } = useKakao(source);

  // 방을 열면 맨 아래(최근 대화)로. 그 뒤 수집이 새로 고칠 때는, 이미 맨
  // 아래를 보고 있을 때만 따라 내려간다 - 위로 올려 읽던 사람을 몇 초마다
  // 끌어내리면 지난 대화를 읽을 수가 없다.
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const shownRoom = useRef<string | null>(null);
  const lastKey = useRef("");
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const last = messages[messages.length - 1];
    const key = `${messages.length}:${last?.date ?? ""}:${last?.time ?? ""}:${last?.text ?? ""}`;
    const roomChanged = shownRoom.current !== selected;
    if (roomChanged || (pinned.current && key !== lastKey.current)) {
      el.scrollTop = el.scrollHeight;
      pinned.current = true;
    }
    shownRoom.current = selected;
    lastKey.current = key;
  }, [messages, selected]);

  return (
    <>
      <main className="px-gutter pb-tabbar mx-auto flex min-h-0 w-full max-w-[46rem] flex-1 flex-col overflow-x-hidden">
        {/* 방 고르기. 수집된 방만 나온다 - 목록 상태만 있고 본문이 없는 방은
            읽을 것이 없으므로 자리를 차지할 이유가 없다. */}
        {rooms.length > 0 ? <RoomPicker rooms={rooms} selected={selected} onSelect={setSelected} /> : null}

        <div
          ref={scroller}
          onScroll={(e) => {
            const el = e.currentTarget;
            pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          }}
          className="scrollbar-hairline min-h-0 flex-1 touch-pan-y overflow-x-hidden overflow-y-auto overscroll-contain pb-4 sm:pb-6"
        >
          {!hydrated ? null : error && source !== "imessage" ? (
            <div className="pt-10 text-center">
              <p className="text-[13px] text-faint">{error}</p>
              <Retry onRetry={reload} />
            </div>
          ) : rooms.length === 0 ? (
            <div className="grid place-items-center px-8 pt-16">
              {source === "imessage" ? (
                <p className="max-w-[26rem] text-center text-[13px] leading-relaxed text-faint">
                  아직 수집된 문자가 없습니다.
                  <br />
                  맥미니의 메시지 앱에 Apple ID로 로그인하고(iCloud에 메시지 켜기), Docker에 전체 디스크 접근 권한을 주면
                  여기에 쌓입니다.
                </p>
              ) : (
                <p className="max-w-[26rem] text-center text-[13px] leading-relaxed text-faint">
                  아직 수집된 대화가 없습니다.
                  <br />
                  맥에서 수집기를 띄우면 여기에 쌓입니다.
                  <br />
                  <code className="mt-2 inline-block font-mono text-[12px] text-dim">
                    docker compose up -d kakaotalk
                  </code>
                </p>
              )}
            </div>
          ) : (
            <div className="space-y-2.5">
              {messages.map((m, i) => (
                <Line key={i} message={m} previous={messages[i - 1]} />
              ))}
            </div>
          )}
        </div>
      </main>
    </>
  );
}

/**
 * Which room. Up to two hundred are collected, and a wall of chips - or a
 * small box of them scrolling up and down - put the room you wanted below
 * everything else. Sideways is out too: a strip that scrolls sideways drags
 * the whole tab with it on a phone (2026-10-08).
 *
 * So one line: the room being read, which opens all of them with a search,
 * and then as many of the other recent rooms as fit - a room that does not
 * fit wraps onto a line that is never shown, so there is no half a chip.
 */
function RoomPicker({
  rooms,
  selected,
  onSelect,
}: {
  rooms: KakaoRoom[];
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const current = rooms.find((r) => r.id === selected) ?? rooms[0];
  const others = rooms.filter((r) => r.id !== current.id);
  const found = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q ? rooms.filter((r) => r.name.toLowerCase().includes(q)) : rooms;
  }, [rooms, query]);

  return (
    <div className="flex shrink-0 items-start gap-1 py-1 sm:py-2">
      <Sheet
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) setQuery("");
        }}
      >
        <SheetTrigger
          aria-label={`대화방: ${current.name}. 누르면 전체 ${rooms.length}개 방에서 고릅니다`}
          className="group flex h-11 max-w-[60%] shrink-0 items-center outline-none"
        >
          <span className="flex min-w-0 items-center gap-1.5 rounded-lg bg-glass-raised px-3 py-2 text-[13px] text-foreground group-focus-visible:ring-3 group-focus-visible:ring-ring/50">
            <span className="truncate">{current.name}</span>
            <span className="shrink-0 tabular-nums text-faint">{current.messages}</span>
            <ChevronDown aria-hidden className="size-3.5 shrink-0 text-faint" />
          </span>
        </SheetTrigger>
        <SheetContent
          side="bottom"
          // A finger came to scroll the recent rooms; a keyboard popping up
          // over them would hide the list it opened. A mouse types at once.
          onOpenAutoFocus={(e) => {
            if (matchMedia("(pointer: coarse)").matches) e.preventDefault();
          }}
          className="inset-x-safe pb-safe max-h-[85dvh] gap-0 border-edge-soft bg-background sm:inset-x-auto sm:left-1/2 sm:w-[30rem] sm:-translate-x-1/2 sm:rounded-t-2xl"
        >
          <SheetHeader className="px-4 pt-4 pb-2">
            <SheetTitle className="text-[14px] font-medium text-foreground">
              대화방 <span className="tabular-nums text-faint">{rooms.length}</span>
            </SheetTitle>
          </SheetHeader>
          <label className="relative mx-4 mb-2 block">
            <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
            <input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="방 이름으로 찾기"
              aria-label="방 이름으로 찾기"
              enterKeyHint="search"
              className="h-11 w-full rounded-lg bg-glass ps-9 pe-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-9 mouse:text-[13px]"
            />
          </label>
          <ul className="scrollbar-hairline min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pb-4">
            {found.map((room) => {
              const active = room.id === current.id;
              return (
                <li key={room.id}>
                  <button
                    type="button"
                    onClick={() => {
                      onSelect(room.id);
                      setOpen(false);
                      setQuery("");
                    }}
                    aria-current={active ? "true" : undefined}
                    className={cn(
                      "flex min-h-12 w-full items-center gap-3 rounded-xl px-3 text-left outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50",
                      active ? "liquid-glass-flat text-foreground" : "text-dim hover:bg-glass",
                    )}
                  >
                    <span className="min-w-0 flex-1 truncate text-[14.5px]">{room.name}</span>
                    <span className="shrink-0 text-[11.5px] tabular-nums text-faint">{when(room)}</span>
                    <span className="w-9 shrink-0 text-end text-[11.5px] tabular-nums text-faint">{room.messages}</span>
                  </button>
                </li>
              );
            })}
            {found.length === 0 ? (
              <li className="px-3 py-6 text-center text-[13px] text-faint">&lsquo;{query.trim()}&rsquo;에 맞는 방이 없어요.</li>
            ) : null}
          </ul>
        </SheetContent>
      </Sheet>

      {/* The rest, newest first: one line's worth, and nothing sideways. */}
      <div className="flex h-11 min-w-0 flex-1 flex-wrap content-start gap-x-0.5 gap-y-8 overflow-hidden">
        {others.map((room) => (
          <button
            key={room.id}
            type="button"
            onClick={() => onSelect(room.id)}
            className="group flex h-11 max-w-full shrink-0 items-center outline-none"
          >
            <span className="flex min-w-0 items-center rounded-md px-2.5 py-2 text-[12.5px] whitespace-nowrap text-faint transition-colors group-hover:text-dim group-focus-visible:ring-3 group-focus-visible:ring-ring/50">
              <span className="truncate">{room.name}</span>
              <span className="ms-1.5 shrink-0 tabular-nums text-faint">{room.messages}</span>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}

/** "23:59" for a room heard from today, "10월 18일" before that. */
function when(room: KakaoRoom): string {
  const today = new Intl.DateTimeFormat("ko-KR", { year: "numeric", month: "long", day: "numeric" }).format(new Date());
  if (room.lastDate === today) return room.lastTime;
  return /(\d+월 \d+일)/.exec(room.lastDate)?.[1] ?? room.lastDate;
}

/**
 * One message. The date only appears when it changes, so a long day of
 * conversation reads as one block rather than a repeated stamp.
 */
function Line({
  message,
  previous,
}: {
  message: KakaoMessage;
  previous?: KakaoMessage;
}) {
  const newDay = message.date !== previous?.date;
  // 같은 사람이 연달아 말하면 이름을 다시 쓰지 않는다. 카카오톡이 그렇게
  // 보여주고, 반복된 이름은 읽는 데 방해가 된다.
  const repeated =
    previous && previous.mine === message.mine && previous.sender === message.sender;

  return (
    <>
      {newDay && (
        <p className="pt-4 pb-1 text-center text-[11px] text-faint tabular-nums">
          {message.date}
        </p>
      )}
      <div
        className={cn(
          "flex items-baseline gap-2.5",
          message.mine ? "justify-end" : "justify-start",
        )}
      >
        {message.mine ? (
          <>
            <span className="shrink-0 text-[11px] text-faint tabular-nums">
              {message.time}
            </span>
            <p className="max-w-[78%] rounded-xl bg-glass-raised px-3 py-1.5 text-[14px] leading-[1.7] [word-break:break-all] whitespace-pre-wrap text-foreground sm:max-w-[26rem]">
              {message.text}
            </p>
          </>
        ) : (
          <>
            <span className="w-[2.6rem] shrink-0 text-end text-[11px] text-faint tabular-nums sm:w-[3.2rem]">
              {repeated ? "" : message.time}
            </span>
            <div className="min-w-0">
              {!repeated && message.sender && (
                <p className="text-[11.5px] text-dim">{message.sender}</p>
              )}
              <p className="text-[14px] leading-[1.7] [word-break:break-all] whitespace-pre-wrap text-foreground/90 sm:max-w-[30rem]">
                {message.text}
              </p>
            </div>
          </>
        )}
      </div>
    </>
  );
}
