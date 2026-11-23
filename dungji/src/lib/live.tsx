// 우리 둥지의 실시간 통로: 누가 들어와 있나, 상대가 무엇을 바꿨나, 통화 신호.
// 이 창이 사람 눈에 보이는지도 알린다(seen): 상대가 한 일을 둥지 안에 띄울지, 상대의 jarvis 알림으로
// 보낼지를 서버가 그것으로 가른다(worker/live.ts).

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { LiveMessage } from "../../shared/api";
import { inSight, onSight } from "./embed";

interface Live {
  online: string[];
  sid: string | null;
  connected: boolean;
  send: (msg: Record<string, unknown>) => boolean;
  on: (fn: (m: LiveMessage) => void) => () => void;
}

const LiveCtx = createContext<Live>({
  online: [],
  sid: null,
  connected: false,
  send: () => false,
  on: () => () => undefined,
});

export const useLive = () => useContext(LiveCtx);

export function LiveProvider({ enabled, children }: { enabled: boolean; children: ReactNode }) {
  const [online, setOnline] = useState<string[]>([]);
  const [sid, setSid] = useState<string | null>(null);
  const [connected, setConnected] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const listeners = useRef(new Set<(m: LiveMessage) => void>());

  useEffect(() => {
    if (!enabled) return;
    let stopped = false;
    let retry = 0;
    let ping: ReturnType<typeof setInterval> | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    // 이 연결로 마지막에 알린 '보이나'(새 연결마다 처음부터 다시 알린다)
    let told: boolean | null = null;
    const tellSeen = () => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) return;
      const on = inSight();
      if (on === told) return;
      told = on;
      ws.send(JSON.stringify({ type: "seen", on }));
    };
    const offSight = onSight(tellSeen);

    const connect = () => {
      const proto = location.protocol === "https:" ? "wss:" : "ws:";
      const ws = new WebSocket(`${proto}//${location.host}/api/live`);
      wsRef.current = ws;
      ws.onopen = () => {
        retry = 0;
        setConnected(true);
        told = null;
        tellSeen();
        ping = setInterval(() => ws.readyState === WebSocket.OPEN && ws.send("ping"), 25_000);
      };
      ws.onmessage = (e) => {
        if (e.data === "pong") return;
        let msg: LiveMessage;
        try {
          msg = JSON.parse(e.data as string) as LiveMessage;
        } catch {
          return;
        }
        if (msg.type === "hello") setSid(msg.sid);
        if (msg.type === "presence") setOnline(msg.online);
        listeners.current.forEach((fn) => fn(msg));
      };
      ws.onclose = () => {
        clearInterval(ping);
        setConnected(false);
        setSid(null);
        if (wsRef.current === ws) wsRef.current = null;
        if (stopped) return;
        // 잠깐 끊긴 것: 조금씩 늦춰 가며 다시 잇는다.
        retry = Math.min(retry + 1, 6);
        timer = setTimeout(connect, 600 * 2 ** retry);
      };
    };
    connect();

    // 화면으로 돌아오면 바로 다시 잇는다(폰이 잠들었다 깬 경우).
    const wake = () => {
      if (document.visibilityState === "visible" && !wsRef.current) {
        clearTimeout(timer);
        retry = 0;
        connect();
      }
    };
    document.addEventListener("visibilitychange", wake);
    return () => {
      stopped = true;
      clearTimeout(timer);
      clearInterval(ping);
      offSight();
      document.removeEventListener("visibilitychange", wake);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [enabled]);

  const send = useCallback((msg: Record<string, unknown>) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return false;
    ws.send(JSON.stringify(msg));
    return true;
  }, []);

  const on = useCallback((fn: (m: LiveMessage) => void) => {
    listeners.current.add(fn);
    return () => {
      listeners.current.delete(fn);
    };
  }, []);

  const value = useMemo(() => ({ online, sid, connected, send, on }), [online, sid, connected, send, on]);
  return <LiveCtx.Provider value={value}>{children}</LiveCtx.Provider>;
}
