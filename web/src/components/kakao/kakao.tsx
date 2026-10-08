"use client";

import { useLayoutEffect, useRef, useSyncExternalStore } from "react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { useKakao, type KakaoMessage, type Source } from "@/lib/use-kakao";
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
      <div className="inset-x-safe mx-auto w-full max-w-[46rem] shrink-0 px-4 pt-2 sm:px-8 sm:pt-3">
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
                "min-h-8 rounded-md px-3 text-[12.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
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
  const { rooms, selected, setSelected, messages, error, hydrated } = useKakao(source);

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
      <main className="inset-x-safe pb-tabbar mx-auto flex min-h-0 w-full max-w-[46rem] flex-1 flex-col overflow-x-hidden px-4 sm:px-8">
        {/* 방 고르기. 수집된 방만 나온다 - 목록 상태만 있고 본문이 없는 방은
            읽을 것이 없으므로 자리를 차지할 이유가 없다. */}
        {/* Wrapped, not a sideways strip: on a phone a row that scrolls
            sideways drags the whole tab with it. Two rows show, the rest
            scroll up and down. */}
        <div className="scrollbar-hairline max-h-[5.25rem] shrink-0 overflow-x-hidden overflow-y-auto overscroll-contain py-2 sm:max-h-none sm:py-3">
          <div className="flex flex-wrap items-center gap-1.5">
            {rooms.map((room) => {
              const active = room.id === selected;
              return (
                <button
                  key={room.id}
                  type="button"
                  onClick={() => setSelected(room.id)}
                  aria-current={active ? "true" : undefined}
                  className={cn(
                    "flex min-h-9 max-w-full shrink-0 items-center rounded-md px-3 text-[12.5px] whitespace-nowrap transition-colors sm:min-h-0 sm:px-2.5 sm:py-1",
                    "outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                    active
                      ? "bg-glass-raised text-foreground"
                      : "text-faint hover:text-dim",
                  )}
                >
                  {room.name}
                  <span className="ms-1.5 tabular-nums text-faint">
                    {room.messages}
                  </span>
                </button>
              );
            })}
          </div>
        </div>

        <div
          ref={scroller}
          onScroll={(e) => {
            const el = e.currentTarget;
            pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          }}
          className="scrollbar-hairline min-h-0 flex-1 touch-pan-y overflow-x-hidden overflow-y-auto overscroll-contain pb-4 sm:pb-6"
        >
          {!hydrated ? null : error && source !== "imessage" ? (
            <p className="pt-10 text-center text-[13px] text-faint">{error}</p>
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
