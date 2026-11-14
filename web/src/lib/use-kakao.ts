"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { onResume } from "@/lib/resume";

// The collectors' read-only surfaces, behind the login proxy. The browser never
// holds their bearer token; the proxy attaches it server-side. KakaoTalk and
// iMessage serve the same shape (kakaotalk-client, imessage-client).
export type Source = "kakao" | "imessage";
const BASE: Record<Source, string> = { kakao: "/api/kakaotalk", imessage: "/api/imessage" };

export type KakaoRoom = {
  id: string; // chat_id — the stable key; names can repeat or be a peer's name
  name: string;
  messages: number;
  unread: number;
  members?: number;
  lastDate: string;
  lastTime: string;
};

export type KakaoMessage = {
  room: string;
  date: string;
  time: string;
  mine: boolean;
  sender?: string;
  text: string;
};

// ── collector wire types ──────────────────────────────────────────────────
type ChatWire = {
  chat_id: number;
  chat_name: string;
  chat_type: string;
  last_message_at: number; // epoch seconds
  message_count: number;
};
type MessageWire = {
  message_id: string;
  chat_id: number;
  chat_name: string;
  chat_type: string;
  author: string;
  is_mine: boolean;
  text: string;
  sent_at: number; // epoch seconds
};

const dateFmt = new Intl.DateTimeFormat("ko-KR", {
  year: "numeric",
  month: "long",
  day: "numeric",
});
const timeFmt = new Intl.DateTimeFormat("ko-KR", {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

function fmtDate(epoch: number): string {
  return epoch > 0 ? dateFmt.format(new Date(epoch * 1000)) : "";
}
function fmtTime(epoch: number): string {
  return epoch > 0 ? timeFmt.format(new Date(epoch * 1000)) : "";
}

/**
 * The most rooms, and the most messages of a room, read at once - the newest.
 * The collectors take no cursor to read further back, so a list this long
 * may not be all there is, and the screen says so.
 */
export const LIMIT = 200;

async function fetchRooms(api: string): Promise<KakaoRoom[]> {
  const res = await fetch(`${api}/chats?limit=${LIMIT}`, { cache: "no-store" });
  if (!res.ok) throw new Error(String(res.status));
  const { chats } = (await res.json()) as { chats: ChatWire[] | null };
  return (chats ?? []).map((c) => ({
    id: String(c.chat_id),
    name: c.chat_name,
    messages: c.message_count,
    unread: 0,
    lastDate: fmtDate(c.last_message_at),
    lastTime: fmtTime(c.last_message_at),
  }));
}

async function fetchMessages(api: string, chatId: string): Promise<KakaoMessage[]> {
  const res = await fetch(
    `${api}/messages?chat=${encodeURIComponent(chatId)}&limit=${LIMIT}`,
    { cache: "no-store" },
  );
  if (!res.ok) throw new Error(String(res.status));
  const { messages } = (await res.json()) as { messages: MessageWire[] | null };
  // 수집기는 최신순으로 준다. 화면은 시간순(아래가 최신)이라 뒤집는다.
  return (messages ?? []).slice().reverse().map((m) => ({
    room: m.chat_name,
    date: fmtDate(m.sent_at),
    time: fmtTime(m.sent_at),
    mine: m.is_mine,
    sender: m.author,
    text: m.text,
  }));
}

/** A room whose own messages have not come yet shows none. */
const NONE: KakaoMessage[] = [];

/**
 * Reads what the Mac-side collector stored. Polls rather than streams: this
 * surface is a window onto a database, not a live conversation, and the
 * collector itself only looks once a second.
 *
 * Messages are kept with the room they were read for, and only the chosen
 * room's are given out: choosing another had left the room before's
 * conversation under the new room's name until its first answer came - or
 * after it, when the room before's poll came back late.
 */
export function useKakao(source: Source = "kakao") {
  const API = BASE[source];
  const [rooms, setRooms] = useState<KakaoRoom[]>([]);
  const [roomsAt, setRoomsAt] = useState<number | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [shown, setShown] = useState<{ room: string; list: KakaoMessage[]; at: number } | null>(null);
  // What failed last: the rooms, or one room's messages - said for that room only.
  const [error, setError] = useState<{ text: string; room?: string } | null>(null);
  const [hydrated, setHydrated] = useState(false);
  // The room chosen, as an answer coming back checks it.
  const wanted = useRef<string | null>(null);

  const loadRooms = useCallback(
    () =>
      fetchRooms(API).then(
        (data) => {
          setRooms(data);
          setRoomsAt(Date.now());
          setError(null);
          // 처음 들어왔을 때는 가장 최근 방을 연다.
          setSelected((cur) => cur ?? data[0]?.id ?? null);
          setHydrated(true);
        },
        () => {
          setError({ text: "수집된 대화를 불러오지 못했어요." });
          setHydrated(true);
        },
      ),
    [API],
  );

  // An answer for a room no longer chosen is dropped, its failure too.
  const loadMessages = useCallback(
    (chatId: string) =>
      fetchMessages(API, chatId).then(
        (list) => {
          if (wanted.current !== chatId) return;
          setShown({ room: chatId, list, at: Date.now() });
          setError(null);
        },
        () => {
          if (wanted.current === chatId) setError({ text: "메시지를 불러오지 못했어요.", room: chatId });
        },
      ),
    [API],
  );

  useEffect(() => {
    void loadRooms();
    const id = setInterval(() => void loadRooms(), 10_000);
    return () => clearInterval(id);
  }, [loadRooms]);

  useEffect(() => {
    wanted.current = selected;
    if (!selected) return;
    void loadMessages(selected);
    const id = setInterval(() => void loadMessages(selected), 3_000);
    return () => clearInterval(id);
  }, [selected, loadMessages]);

  // The rooms and the room on screen, now: on coming back to the app, a pull
  // to refresh, or 다시 시도.
  const reload = useCallback(
    () => Promise.all([loadRooms(), selected ? loadMessages(selected) : null]),
    [loadRooms, loadMessages, selected],
  );
  useEffect(() => onResume(reload), [reload]);

  const own = shown?.room === selected;
  // The older of the two reads on screen: the one a failure left standing.
  const at = own ? Math.min(shown.at, roomsAt ?? shown.at) : roomsAt;
  return {
    rooms,
    selected,
    setSelected,
    messages: own ? shown.list : NONE,
    /** The chosen room's messages are on their way. */
    loading: selected !== null && !own,
    error: error && (error.room === undefined || error.room === selected) ? error.text : null,
    /** When what is shown was read. */
    readAt: at === null ? null : new Date(at),
    hydrated,
    reload,
  };
}
