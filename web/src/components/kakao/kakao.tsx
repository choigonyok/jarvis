"use client";

import { useEffect, useRef } from "react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { useKakao, type KakaoMessage } from "@/lib/use-kakao";
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
export function Kakao() {
  const { proposals, connection } = useThread();
  const waitingList = proposals.filter(isPending);
  const pending = waitingList.length;
  const { rooms, selected, setSelected, messages, error, hydrated } = useKakao();

  // 새로 고칠 때마다 맨 아래로. 이 화면에서 찾는 것은 늘 최근 대화다.
  const bottom = useRef<HTMLDivElement>(null);
  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [messages, selected]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={pending} />
      <StandingBar pending={waitingList} href="/record" />

      <main className="inset-x-safe pb-tabbar mx-auto flex min-h-0 w-full max-w-[46rem] flex-1 flex-col px-4 sm:px-8">
        {/* 방 고르기. 수집된 방만 나온다 - 목록 상태만 있고 본문이 없는 방은
            읽을 것이 없으므로 자리를 차지할 이유가 없다. */}
        <div className="scrollbar-hairline shrink-0 overflow-x-auto overscroll-x-contain py-2 sm:py-3">
          <div className="flex items-center gap-1.5">
            {rooms.map((room) => {
              const active = room.id === selected;
              return (
                <button
                  key={room.id}
                  type="button"
                  onClick={() => setSelected(room.id)}
                  aria-current={active ? "true" : undefined}
                  className={cn(
                    "flex min-h-9 shrink-0 items-center rounded-md px-3 text-[12.5px] whitespace-nowrap transition-colors sm:min-h-0 sm:px-2.5 sm:py-1",
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

        <div className="scrollbar-hairline min-h-0 flex-1 overflow-y-auto overscroll-contain pb-4 sm:pb-6">
          {!hydrated ? null : error ? (
            <p className="pt-10 text-center text-[13px] text-faint">{error}</p>
          ) : rooms.length === 0 ? (
            <div className="grid place-items-center px-8 pt-16">
              <p className="max-w-[26rem] text-center text-[13px] leading-relaxed text-faint">
                아직 수집된 대화가 없습니다.
                <br />
                맥에서 수집기를 띄우면 여기에 쌓입니다.
                <br />
                <code className="mt-2 inline-block font-mono text-[12px] text-dim">
                  docker compose up -d kakaotalk
                </code>
              </p>
            </div>
          ) : (
            <div className="space-y-2.5">
              {messages.map((m, i) => (
                <Line key={i} message={m} previous={messages[i - 1]} />
              ))}
              <div ref={bottom} />
            </div>
          )}
        </div>
      </main>

      <TabBar pending={pending} />
    </div>
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
            <p className="max-w-[78%] rounded-xl bg-glass-raised px-3 py-1.5 text-[14px] leading-[1.7] text-pretty text-foreground sm:max-w-[26rem]">
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
              <p className="text-[14px] leading-[1.7] text-pretty text-foreground/90 sm:max-w-[30rem]">
                {message.text}
              </p>
            </div>
          </>
        )}
      </div>
    </>
  );
}
