"use client";

import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import Link from "next/link";
import { ChevronDown } from "lucide-react";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { StandingBar } from "@/components/shell/standing-bar";
import { BottomSheetContent, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Pager } from "@/components/ui/pager";
import { Sheet, SheetTrigger } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { AsOf } from "@/components/shell/as-of";
import { Failed } from "@/components/shell/failed";
import { usePaged } from "@/lib/paging";
import { type CheckState, type StatusReport, held, readStatus } from "@/lib/status";
import { LIMIT, useKakao, type KakaoMessage, type KakaoRoom, type Older, type Source } from "@/lib/use-kakao";
import { useResource } from "@/lib/use-resource";
import { useThread } from "@/lib/use-thread";
import { isPending } from "@/lib/thread";
import { SAY } from "@/lib/say";
import { cn } from "@/lib/utils";
import { clock } from "@/lib/when";

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
  // Whether the collectors are collecting, as the 상태 tab knows it: read
  // once for both messengers, and every minute while the page is on screen.
  const status = useResource("status", readStatus, { interval: 60_000 });

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop fade={false}>
        <Header connection={connection} pending={pending} />
        <StandingBar pending={waitingList} />
      </PageTop>
      {/* The root draws no box: the list and the panel stay in this column. */}
      <Tabs value={source} onValueChange={(v) => pick(v as Source)} className="contents">
        <div className="px-gutter mx-auto w-full max-w-page shrink-0 pt-2 sm:pt-3">
          <TabsList aria-label="메신저">
            <TabsTrigger value="kakao">카카오톡</TabsTrigger>
            <TabsTrigger value="imessage">iMessage</TabsTrigger>
          </TabsList>
        </div>
        {/* Each source keeps its own rooms and scroll; switching starts fresh. */}
        <TabsContent value={source} className="flex min-h-0 flex-1 flex-col">
          <Feed key={source} source={source} report={status.data} />
        </TabsContent>
      </Tabs>
      <TabBar pending={pending} />
    </div>
  );
}

function Feed({ source, report }: { source: Source; report?: StatusReport }) {
  const { rooms, selected, setSelected, messages, older, loadOlder, loading, error, missing, readAt, hydrated, reload } =
    useKakao(source);
  // A read again that fails leaves the conversation on screen, said to be
  // from when it was read: one poll lost in a tunnel had put "불러오지
  // 못했어요" in its place until the next, and it came back at its top. Only
  // what never came is the failure in its place - except an iMessage
  // collector that is not there, which is not set up yet and says how. Every
  // other iMessage failure had said that too, and a Mac mini restarting sent
  // the operator off to set up again what was working.
  const stale = error !== null && rooms.length > 0 && !loading;
  const failed = error !== null && (rooms.length > 0 ? loading : !(source === "imessage" && missing));

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
  // Older lines put in front of the first one: that line stays where it was
  // on screen, and the reader on the message they were reading. Its place in
  // the list is taken after every drawing, so the next is measured against
  // what was on screen; the browser's own scroll anchoring is off on the list
  // (overflow-anchor) - Safari has none, and where there is one the view
  // would be moved twice.
  const top = useRef<{ id: string; at: number } | null>(null);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const was = top.current;
    const first = el.querySelector<HTMLElement>("[data-message-id]");
    if (was && first?.dataset.messageId !== was.id) {
      const row = el.querySelector<HTMLElement>(`[data-message-id="${CSS.escape(was.id)}"]`);
      if (row) el.scrollTop += placeIn(el, row) - was.at;
    }
    top.current = first?.dataset.messageId ? { id: first.dataset.messageId, at: placeIn(el, first) } : null;
  });
  // The lines over the conversation come and go - a read that failed, the
  // collector's - and one that comes while the newest is in view must not
  // push it out of view.
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const keep = new ResizeObserver(() => {
      if (pinned.current) el.scrollTop = el.scrollHeight;
    });
    keep.observe(el);
    return () => keep.disconnect();
  }, []);

  return (
    <>
      <main className="px-gutter pb-tabbar mx-auto flex min-h-0 w-full max-w-page flex-1 flex-col overflow-x-hidden">
        {/* 방 고르기. 수집된 방만 나온다 - 목록 상태만 있고 본문이 없는 방은
            읽을 것이 없으므로 자리를 차지할 이유가 없다. */}
        {rooms.length > 0 ? (
          <RoomPicker rooms={rooms} selected={selected} onSelect={setSelected} />
        ) : !hydrated ? (
          // The picker's place on opening, so the conversation is not pushed
          // down by it when the rooms come.
          <div aria-hidden className="shrink-0 py-1 sm:py-2">
            <div className="flex h-11 items-center">
              <div className="h-9 w-36 rounded-lg bg-well" />
            </div>
          </div>
        ) : null}
        {stale ? <AsOf at={readAt} failed onRetry={() => void reload()} className="shrink-0 pb-1" /> : null}
        <Collecting source={source} report={report} />

        <div
          ref={scroller}
          onScroll={(e) => {
            const el = e.currentTarget;
            pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          }}
          className="scrollbar-hairline min-h-0 flex-1 touch-pan-y overflow-x-hidden overflow-y-auto overscroll-contain pb-4 [overflow-anchor:none] sm:pb-6"
        >
          {!hydrated ? (
            <Placeholder />
          ) : failed ? (
            <Failed onRetry={reload} className="pt-10">
              {error}
            </Failed>
          ) : rooms.length === 0 ? (
            <Empty className="px-8 pt-16">
              {source === "imessage" ? (
                <EmptyHeader>
                  <EmptyTitle>아직 수집된 문자가 없어요</EmptyTitle>
                  <EmptyDescription>
                    맥미니의 메시지 앱에 Apple ID로 로그인하고(iCloud에 메시지 켜기), Docker에 전체 디스크 접근 권한을 주면
                    여기에 쌓여요.
                  </EmptyDescription>
                </EmptyHeader>
              ) : (
                <>
                  <EmptyHeader>
                    <EmptyTitle>아직 수집된 대화가 없어요</EmptyTitle>
                    <EmptyDescription>맥에서 수집기를 띄우면 여기에 쌓여요.</EmptyDescription>
                  </EmptyHeader>
                  <EmptyContent>
                    <code className="font-mono text-aside text-dim">docker compose up -d kakaotalk</code>
                  </EmptyContent>
                </>
              )}
            </Empty>
          ) : loading ? (
            <Placeholder />
          ) : (
            <div className="space-y-2.5">
              <Earlier state={older} onMore={loadOlder} />
              {messages.map((m, i) => (
                <Line key={m.id} message={m} previous={messages[i - 1]} />
              ))}
            </div>
          )}
        </div>
      </main>
    </>
  );
}

/** Where a row stands in the list's content, however far the list is scrolled. */
function placeIn(list: HTMLElement, row: HTMLElement): number {
  return row.getBoundingClientRect().top - list.getBoundingClientRect().top + list.scrollTop;
}

/**
 * The top of a room's conversation. A room cut at its newest two hundred - a
 * busy room's are a day or two, and their top had read as the start of the
 * conversation - has the page before them a press away. Read back to the
 * first message collected, that is said: the collector began at some point
 * and keeps two years unless told otherwise, so that may not be where the
 * conversation began. Over a room that came whole in one read, nothing.
 */
function Earlier({ state, onMore }: { state: Older; onMore: () => Promise<void> }) {
  if (state === null) return null;
  if (state === "start") {
    return (
      <p role="status" className="pt-2 text-center text-meta text-faint">
        수집된 메시지는 여기까지예요
      </p>
    );
  }
  if (state === "failed") {
    // Said as a failure where the conversation starts: in the control's quiet
    // words it would read as the start.
    return (
      <Failed quiet onRetry={onMore} className="pt-2">
        이전 메시지를 불러오지 못했어요.
      </Failed>
    );
  }
  return (
    <Button variant="quiet" size="touch-sm" disabled={state === "loading"} onClick={() => void onMore()} className="w-full">
      {state === "loading" ? SAY.loading : "이전 메시지 더 보기"}
    </Button>
  );
}

/**
 * Whether what is here is still coming, from the 상태 tab's checks. A
 * KakaoTalk stuck on the Mac's update window for two days and a quiet two
 * days drew the same screen: nothing new, the last messages as if current.
 * So one line under the rooms says the collector was reading as of when -
 * or that it is not, or that nothing has come in for long, with the way to
 * the check that says why.
 */
function Collecting({ source, report }: { source: Source; report?: StatusReport }) {
  const said = collecting(source, report);
  if (!said) return null;
  const fault = said.state === "fail";
  return (
    <p className={cn("tnum shrink-0 pb-1 text-meta", fault ? "text-reject" : said.state === "warn" ? "text-dim" : "text-faint")}>
      {said.text}
      {said.check ? (
        <>
          {" · "}
          <Link
            href={`/status?focus=check:${said.check}`}
            className={cn(
              "tap-y underline underline-offset-[3px] focus-ring",
              fault ? "decoration-reject-fill hover:text-reject" : "decoration-edge hover:text-foreground",
            )}
          >
            상태 보기
          </Link>
        </>
      ) : null}
    </p>
  );
}

/** The collector's line for a messenger, from the report: nothing when the checks have not said. */
function collecting(source: Source, report?: StatusReport): { state: CheckState; text: string; check?: string } | null {
  if (!report) return null;
  const check = (id: string) => report.results.find((r) => r.id === id);
  const reader = check(source === "kakao" ? "kakao.collector" : "imessage.collector");
  if (reader && (reader.state === "fail" || reader.state === "warn")) {
    return { state: reader.state, text: `수집기가 ${held(reader.since)} 읽지 못하고 있어요`, check: reader.id };
  }
  // A KakaoTalk that is off leaves the collector reading a database that no
  // longer changes: what tells is that nothing has come in.
  const heard = source === "kakao" ? check("kakao.messages") : undefined;
  if (heard?.observedAt && (heard.state === "fail" || heard.state === "warn")) {
    return { state: heard.state, text: `${held(heard.observedAt)} 새 카톡이 없어요`, check: heard.id };
  }
  if (reader?.state === "ok") return { state: "ok", text: `수집 중 · ${clock(reader.checkedAt)} 확인` };
  return null;
}

/**
 * Which room. Up to two hundred are collected, and a wall of chips - or a
 * small box of them scrolling up and down - put the room you wanted below
 * everything else. Sideways is out too: a strip that scrolls sideways drags
 * the whole tab with it on a phone.
 *
 * So one line: the room being read, which opens all of them with a search,
 * and then as many of the other recent rooms as fit - a room that does not
 * fit wraps onto a line that is never shown, so there is no half a chip.
 * Drawn again only when the rooms change, not on every poll of them.
 */
const RoomPicker = memo(function RoomPicker({
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
  // Twenty rooms a page. The list scrolls on its own inside the sheet, so a
  // page turned takes it back to its top.
  const paged = usePaged(found, { reset: query.trim() });
  const list = useRef<HTMLDivElement>(null);
  const turn = (page: number) => {
    paged.go(page);
    list.current?.scrollTo({ top: 0 });
  };

  // A room that wrapped onto the line never shown is out of reach of a key
  // and a screen reader as well. Up to 199 unseen chips stood in the tab
  // order, and the browser scrolled the clipped strip to show the one
  // focused, taking the recent rooms out of sight with no way back.
  const strip = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = strip.current;
    if (!el) return;
    const mark = () => {
      const chips = [...el.children] as HTMLElement[];
      const line = chips[0]?.offsetTop ?? 0;
      for (const chip of chips) chip.inert = chip.offsetTop > line;
    };
    mark();
    const resized = new ResizeObserver(mark);
    resized.observe(el);
    return () => resized.disconnect();
  });

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
          aria-label={`대화방: ${current.name}. 누르면 전체 ${rooms.length}개 방에서 골라요`}
          className="group flex h-11 max-w-[60%] shrink-0 items-center outline-none"
        >
          <span className="flex min-w-0 items-center gap-1.5 rounded-lg bg-glass-raised px-3 py-2 text-ui text-foreground group-focus-visible:ring-3 group-focus-visible:ring-ring/50">
            <span className="truncate">{current.name}</span>
            <span className="shrink-0 tabular-nums text-faint">{current.messages}</span>
            <ChevronDown aria-hidden className="size-3.5 shrink-0 text-faint" />
          </span>
        </SheetTrigger>
        <BottomSheetContent
          keepFocus={false}
          // A finger came to scroll the recent rooms; a keyboard popping up
          // over them would hide the list it opened. A mouse types at once.
          onOpenAutoFocus={(e) => {
            if (matchMedia("(pointer: coarse)").matches) e.preventDefault();
          }}
          aria-describedby={undefined}
          className="max-h-[85dvh] sm:max-w-[30rem]"
        >
          <BottomSheetHeader className="sm:px-4">
            <BottomSheetTitle className="text-body">
              대화방 <span className="tabular-nums text-faint">{rooms.length >= LIMIT ? `${LIMIT}+` : rooms.length}</span>
            </BottomSheetTitle>
          </BottomSheetHeader>
          {/* Found by name, gone through with the arrow keys; Enter opens the
              lit room. The list is filtered and paged here (shouldFilter off). */}
          <Command shouldFilter={false} label="대화방 찾기" className="min-h-0 flex-1">
            <CommandInput
              value={query}
              onValueChange={setQuery}
              placeholder="방 이름으로 찾기"
              aria-label="방 이름으로 찾기"
              enterKeyHint="search"
              wrapperClassName="mx-4 mb-2 w-auto border-transparent"
            />
            <CommandList ref={list} className="scrollbar-hairline min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pb-4">
              {paged.items.map((room) => {
                const active = room.id === current.id;
                return (
                  <CommandItem
                    key={room.id}
                    value={room.id}
                    onSelect={() => {
                      onSelect(room.id);
                      setOpen(false);
                      setQuery("");
                    }}
                    aria-current={active ? "true" : undefined}
                    className={cn("min-h-12 rounded-xl px-3", active ? "bg-glass-raised text-foreground" : "text-dim")}
                  >
                    <span className="min-w-0 flex-1 truncate text-body">{room.name}</span>
                    <span className="shrink-0 text-meta tabular-nums text-faint">{when(room)}</span>
                    <span className="w-9 shrink-0 text-end text-meta tabular-nums text-faint">{room.messages}</span>
                  </CommandItem>
                );
              })}
              <CommandEmpty className="px-3 py-6 text-center">&lsquo;{query.trim()}&rsquo;에 맞는 방이 없어요.</CommandEmpty>
            </CommandList>
          </Command>
          <Pager {...paged} go={turn} unit="개" className="mx-4 mt-0 mb-3" />
        </BottomSheetContent>
      </Sheet>

      {/* The rest, newest first: one line's worth, and nothing sideways. */}
      <div ref={strip} className="flex h-11 min-w-0 flex-1 flex-wrap content-start gap-x-0.5 gap-y-8 overflow-hidden">
        {others.map((room) => (
          <button
            key={room.id}
            type="button"
            onClick={() => onSelect(room.id)}
            aria-label={`${room.name}, 메시지 ${room.messages}개`}
            className="group flex h-11 max-w-full shrink-0 items-center outline-none"
          >
            <span className="flex min-w-0 items-center rounded-md px-2.5 py-2 text-aside whitespace-nowrap text-faint transition-colors group-hover:text-dim group-focus-visible:ring-3 group-focus-visible:ring-ring/50">
              <span className="truncate">{room.name}</span>
              <span className="ms-1.5 shrink-0 tabular-nums text-faint">{room.messages}</span>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
});

/**
 * A room's place while its messages are read - and on opening, while the
 * rooms are: still blocks where they will be - theirs on the left past the
 * times, mine on the right - and nothing of the room chosen before.
 */
function Placeholder() {
  return (
    <div className="pt-4">
      <p role="status" className="sr-only">
        {SAY.loading}
      </p>
      <div aria-hidden className="space-y-2.5">
        {SHAPE.map((place, i) => (
          <div key={i} className={cn("h-7 rounded-lg bg-well", place)} />
        ))}
      </div>
    </div>
  );
}

const SHAPE = ["ms-[3.25rem] w-[48%]", "ms-[3.25rem] w-[62%]", "ms-auto w-[38%]", "ms-[3.25rem] w-[54%]", "ms-auto w-[46%]"];

/** "23:59" for a room heard from today, "10월 18일" before that. */
function when(room: KakaoRoom): string {
  const today = new Intl.DateTimeFormat("ko-KR", { year: "numeric", month: "long", day: "numeric" }).format(new Date());
  if (room.lastDate === today) return room.lastTime;
  return /(\d+월 \d+일)/.exec(room.lastDate)?.[1] ?? room.lastDate;
}

/**
 * A message's words with its web addresses as links, drawn as the
 * conversation's are. Reading is all this screen does, and what is read here
 * - a booking link, a map, an address, an account number - is taken
 * elsewhere: the words are selectable on a touch screen too, as the
 * conversation's are, and a link opens. Neither could be done on a phone.
 */
function linked(text: string): ReactNode {
  const parts = text.split(LINK);
  if (parts.length === 1) return text;
  return parts.map((part, i) =>
    i % 2 === 1 ? (
      <a
        key={i}
        href={part}
        target="_blank"
        rel="noopener noreferrer"
        className="underline decoration-dim underline-offset-[3px] transition-colors hover:decoration-foreground focus-ring"
      >
        {part}
      </a>
    ) : (
      part
    ),
  );
}

/** An address up to the space after it, less the punctuation a sentence puts after it. */
const LINK = /(https?:\/\/[^\s<>"']*[^\s<>"'.,!?)\]}」』…])/;

/**
 * One message. The date only appears when it changes, so a long day of
 * conversation reads as one block rather than a repeated stamp. Drawn again
 * only when it or the one before it changed (lib/use-kakao.ts keeps both).
 */
const Line = memo(function Line({
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
        <p className="pt-4 pb-1 text-center text-meta text-faint tabular-nums">
          {message.date}
        </p>
      )}
      <div
        data-message-id={message.id}
        className={cn(
          "flex items-baseline gap-2.5",
          message.mine ? "justify-end" : "justify-start",
        )}
      >
        {message.mine ? (
          <>
            <span className="shrink-0 text-meta text-faint tabular-nums">
              {message.time}
            </span>
            <p className="selectable max-w-[78%] rounded-xl bg-glass-raised px-3 py-1.5 text-body leading-[1.7] [overflow-wrap:anywhere] whitespace-pre-wrap text-foreground sm:max-w-[26rem]">
              {linked(message.text)}
            </p>
          </>
        ) : (
          <>
            <span className="w-[2.6rem] shrink-0 text-end text-meta text-faint tabular-nums sm:w-[3.2rem]">
              {repeated ? "" : message.time}
            </span>
            <div className="min-w-0">
              {!repeated && message.sender && (
                <p className="text-meta text-dim">{message.sender}</p>
              )}
              <p className="selectable text-body leading-[1.7] [overflow-wrap:anywhere] whitespace-pre-wrap text-ink sm:max-w-[30rem]">
                {linked(message.text)}
              </p>
            </div>
          </>
        )}
      </div>
    </>
  );
});
