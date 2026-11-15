"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { onResume } from "@/lib/resume";
import { HttpError } from "@/lib/use-resource";

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
  /** The collector's message_id: what a line is known by from one read to the next. */
  id: string;
  room: string;
  date: string;
  time: string;
  mine: boolean;
  sender?: string;
  text: string;
  /** Its place in the collector's order (see `order`): the second it was sent, then the row it was read from. */
  sentAt: number;
  rowid: number;
};

/**
 * The top of a room's conversation: more before it to read (`more`), that
 * read on its way or failed, the first message collected reached by reading
 * back (`start`) - or nothing to say, over a room that came whole in one read.
 */
export type Older = "more" | "loading" | "failed" | "start" | null;

// ── collector wire types ──────────────────────────────────────────────────
type ChatWire = {
  chat_id: number;
  chat_name: string;
  chat_type: string;
  last_message_at: number; // epoch seconds
  message_count: number;
};
type MessageWire = {
  rowid: number;
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
 * A room reads further back a page this long at a time (`before`); the rooms
 * do not, so a list of them this long may not be all there is, and the
 * screen says so.
 */
export const LIMIT = 200;

async function fetchRooms(api: string): Promise<KakaoRoom[]> {
  const res = await fetch(`${api}/chats?limit=${LIMIT}`, { cache: "no-store" });
  if (!res.ok) throw new HttpError(res.status);
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

/**
 * A room's newest page - or, given before (a message's id), the page of the
 * ones older than that message: the collectors' cursor.
 */
async function fetchMessages(api: string, chatId: string, before?: string): Promise<KakaoMessage[]> {
  const cursor = before ? `&before=${encodeURIComponent(before)}` : "";
  const res = await fetch(
    `${api}/messages?chat=${encodeURIComponent(chatId)}&limit=${LIMIT}${cursor}`,
    { cache: "no-store" },
  );
  if (!res.ok) throw new Error(String(res.status));
  const { messages } = (await res.json()) as { messages: MessageWire[] | null };
  // 수집기는 최신순으로 준다. 화면은 시간순(아래가 최신)이라 뒤집는다.
  return (messages ?? []).slice().reverse().map((m) => ({
    id: m.message_id,
    room: m.chat_name,
    date: fmtDate(m.sent_at),
    time: fmtTime(m.sent_at),
    mine: m.is_mine,
    sender: m.author,
    text: m.text,
    sentAt: m.sent_at,
    rowid: m.rowid,
  }));
}

/**
 * The collectors' order, oldest first: the second it was sent, then the row
 * it was read from, then its id - a burst shares a second, so the time alone
 * does not say which of two came first.
 */
function order(a: KakaoMessage, b: KakaoMessage): number {
  return a.sentAt - b.sentAt || a.rowid - b.rowid || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

/** A room whose own messages have not come yet shows none. */
const NONE: KakaoMessage[] = [];

/**
 * A room's messages read again, with the very objects on screen kept for
 * the lines that are the same: a poll that brought nothing new hands back
 * the list shown, and one that brought a line redraws that line - not the
 * two hundred, every three seconds.
 */
function settle(prev: KakaoMessage[] | undefined, next: KakaoMessage[]): KakaoMessage[] {
  if (!prev) return next;
  const before = new Map(prev.map((m) => [m.id, m]));
  let same = prev.length === next.length;
  const out = next.map((m, i) => {
    const old = before.get(m.id);
    const kept =
      old &&
      old.text === m.text &&
      old.sender === m.sender &&
      old.mine === m.mine &&
      old.date === m.date &&
      old.time === m.time &&
      old.sentAt === m.sentAt &&
      old.rowid === m.rowid
        ? old
        : m;
    if (kept !== prev[i]) same = false;
    return kept;
  });
  return same ? prev : out;
}

/** One room's conversation as it is on screen. */
type Shown = {
  room: string;
  /** Oldest first, as drawn. */
  list: KakaoMessage[];
  /** When the newest page was read. */
  at: number;
  /** Nothing older than the first of list was collected. */
  start: boolean;
  /** A page before the newest was read: the start, reached so, is said. */
  paged: boolean;
  /** The page before the first of list, while it is asked for or after it failed. */
  older: "idle" | "loading" | "failed";
};

/**
 * The newest page read again, laid over what is on screen. Lines older than
 * the page - pages read further back, and the ones newer lines pushed out of
 * it - stay: a three-second poll that dropped them would take away what the
 * reader went back for, from under them. Only a page that does not reach
 * back to the newest line shown (more than a page came while the screen was
 * away) starts the list again from itself: between the two there would be a
 * hole that read as nothing said.
 */
function laid(prev: Shown | undefined, room: string, page: KakaoMessage[], at: number): Shown {
  const full = page.length >= LIMIT;
  const newest = prev?.list.at(-1);
  if (!prev || !newest || (full && order(page[0], newest) > 0)) {
    return { room, list: settle(prev?.list, page), at, start: !full, paged: false, older: "idle" };
  }
  // Short of a page, it is the whole room.
  if (!full) return { ...prev, list: settle(prev.list, page), at, start: true };
  const fresh = new Set(page.map((m) => m.id));
  const kept = prev.list.filter((m) => order(m, page[0]) < 0 && !fresh.has(m.id));
  return { ...prev, list: settle(prev.list, [...kept, ...page]), at };
}

/**
 * The page before the first line, put in front of it. A page short of the
 * limit reached the first message collected. Only what is older than the
 * first line is taken: a collector from before the cursor answers every
 * before= with the newest page.
 */
function withOlder(prev: Shown, page: KakaoMessage[]): Shown {
  const first = prev.list[0];
  const have = new Set(prev.list.map((m) => m.id));
  const older = page.filter((m) => !have.has(m.id) && order(m, first) < 0);
  return {
    ...prev,
    list: older.length > 0 ? [...older, ...prev.list] : prev.list,
    start: page.length < LIMIT,
    paged: true,
    older: "idle",
  };
}

/** The rooms read again are the ones shown: the list on screen stays as it is. */
function sameRooms(a: KakaoRoom[], b: KakaoRoom[]): boolean {
  return (
    a.length === b.length &&
    a.every((r, i) => {
      const o = b[i];
      return r.id === o.id && r.name === o.name && r.messages === o.messages && r.lastDate === o.lastDate && r.lastTime === o.lastTime;
    })
  );
}

/**
 * Reads what the Mac-side collector stored. Polls rather than streams: this
 * surface is a window onto a database, not a live conversation, and the
 * collector itself only looks once a second. The poll reads a room's newest
 * page and lays it over what is shown (laid); the pages before it are read on
 * a press (loadOlder) and stay through the polls.
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
  const [shown, setShown] = useState<Shown | null>(null);
  // What failed last: the rooms (with the status they were answered with, if
  // any), or one room's messages - said for that room only.
  const [error, setError] = useState<{ text: string; room?: string; status?: number } | null>(null);
  const [hydrated, setHydrated] = useState(false);
  // The room chosen, as an answer coming back checks it.
  const wanted = useRef<string | null>(null);

  const loadRooms = useCallback(
    () =>
      fetchRooms(API).then(
        (data) => {
          setRooms((prev) => (sameRooms(prev, data) ? prev : data));
          setRoomsAt(Date.now());
          setError(null);
          // 처음 들어왔을 때는 가장 최근 방을 연다.
          setSelected((cur) => cur ?? data[0]?.id ?? null);
          setHydrated(true);
        },
        (err: unknown) => {
          setError({ text: "수집된 대화를 불러오지 못했어요.", status: err instanceof HttpError ? err.status : undefined });
          setHydrated(true);
        },
      ),
    [API],
  );

  // An answer for a room no longer chosen is dropped, its failure too.
  const loadMessages = useCallback(
    (chatId: string) =>
      fetchMessages(API, chatId).then(
        (page) => {
          if (wanted.current !== chatId) return;
          const at = Date.now();
          setShown((prev) => laid(prev?.room === chatId ? prev : undefined, chatId, page, at));
          setError(null);
        },
        () => {
          if (wanted.current === chatId) setError({ text: "메시지를 불러오지 못했어요.", room: chatId });
        },
      ),
    [API],
  );

  // The page before the first line shown, on a press. The polls go on
  // meanwhile; what comes back for a list that has since started again - a
  // room left, a gap - would not join it, and is dropped.
  const loadOlder = useCallback(async () => {
    const first = shown?.list[0];
    if (!shown || !first || shown.room !== wanted.current || shown.start || shown.older === "loading") return;
    const room = shown.room;
    const same = (prev: Shown | null): prev is Shown => prev?.room === room && prev.list[0]?.id === first.id;
    setShown((prev) => (same(prev) ? { ...prev, older: "loading" } : prev));
    try {
      const page = await fetchMessages(API, room, first.id);
      setShown((prev) => (same(prev) ? withOlder(prev, page) : prev));
    } catch {
      setShown((prev) => (same(prev) ? { ...prev, older: "failed" } : prev));
    }
  }, [API, shown]);

  // Only while the page is on screen, as the 상태 tab reads: a phone left on
  // this tab in a pocket, or a desktop tab under other windows all day, kept
  // fetching two hundred messages every three seconds. Coming back reads at
  // once (onResume, below).
  useEffect(() => {
    void loadRooms();
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void loadRooms();
    }, 10_000);
    return () => clearInterval(id);
  }, [loadRooms]);

  useEffect(() => {
    wanted.current = selected;
    if (!selected) return;
    void loadMessages(selected);
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void loadMessages(selected);
    }, 3_000);
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
  const older: Older = !own
    ? null
    : shown.start
      ? shown.paged
        ? "start"
        : null
      : shown.older === "idle"
        ? "more"
        : shown.older;
  return {
    rooms,
    selected,
    setSelected,
    messages: own ? shown.list : NONE,
    older,
    loadOlder,
    /** The chosen room's messages are on their way. */
    loading: selected !== null && !own,
    error: error && (error.room === undefined || error.room === selected) ? error.text : null,
    /**
     * The rooms were answered that there is no such collector (404, 501): one
     * not set up, rather than one that failed - a Mac mini restarting answers
     * 502.
     */
    missing: error?.room === undefined && (error?.status === 404 || error?.status === 501),
    /** When what is shown was read. */
    readAt: at === null ? null : new Date(at),
    hydrated,
    reload,
  };
}
