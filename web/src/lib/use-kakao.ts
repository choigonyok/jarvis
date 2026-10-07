"use client";

import { useCallback, useEffect, useState } from "react";

// The collector's read-only surface, behind the login proxy. The browser never
// holds its bearer token; the proxy attaches it server-side.
const API = "/api/kakaotalk";

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
 * Reads what the Mac-side collector stored. Polls rather than streams: this
 * surface is a window onto a database, not a live conversation, and the
 * collector itself only looks once a second.
 */
export function useKakao() {
  const [rooms, setRooms] = useState<KakaoRoom[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [messages, setMessages] = useState<KakaoMessage[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(false);

  const loadRooms = useCallback(async () => {
    try {
      const res = await fetch(`${API}/chats?limit=200`, { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const { chats } = (await res.json()) as { chats: ChatWire[] | null };
      const data: KakaoRoom[] = (chats ?? []).map((c) => ({
        id: String(c.chat_id),
        name: c.chat_name,
        messages: c.message_count,
        unread: 0,
        lastDate: fmtDate(c.last_message_at),
        lastTime: fmtTime(c.last_message_at),
      }));
      setRooms(data);
      setError(null);
      // 처음 들어왔을 때는 가장 최근 방을 연다.
      setSelected((cur) => cur ?? data[0]?.id ?? null);
    } catch {
      setError("수집된 대화를 불러오지 못했습니다.");
    } finally {
      setHydrated(true);
    }
  }, []);

  const loadMessages = useCallback(async (chatId: string) => {
    try {
      const res = await fetch(
        `${API}/messages?chat=${encodeURIComponent(chatId)}&limit=200`,
        { cache: "no-store" },
      );
      if (!res.ok) throw new Error(String(res.status));
      const { messages } = (await res.json()) as { messages: MessageWire[] | null };
      // 수집기는 최신순으로 준다. 화면은 시간순(아래가 최신)이라 뒤집는다.
      const ordered = (messages ?? []).slice().reverse();
      setMessages(
        ordered.map((m) => ({
          room: m.chat_name,
          date: fmtDate(m.sent_at),
          time: fmtTime(m.sent_at),
          mine: m.is_mine,
          sender: m.author,
          text: m.text,
        })),
      );
      setError(null);
    } catch {
      setError("메시지를 불러오지 못했습니다.");
    }
  }, []);

  useEffect(() => {
    void loadRooms();
    const id = setInterval(() => void loadRooms(), 10_000);
    return () => clearInterval(id);
  }, [loadRooms]);

  useEffect(() => {
    if (!selected) return;
    void loadMessages(selected);
    const id = setInterval(() => void loadMessages(selected), 3_000);
    return () => clearInterval(id);
  }, [selected, loadMessages]);

  return { rooms, selected, setSelected, messages, error, hydrated };
}
