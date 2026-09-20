"use client";

import { useEffect, useRef, useState } from "react";
import { API, type AgentEvent, type Connection } from "@/lib/thread";

/**
 * One SSE connection per page, feeding whatever that page renders. Every
 * frame - turns, proposals, calendar changes - arrives on the same stream,
 * so a page only listens for the ones it cares about.
 *
 * Frames can be dropped when a client falls behind, so every caller also
 * hydrates over HTTP on connect and on reconnect.
 */
export function useAgentStream(
  onEvent: (event: AgentEvent) => void,
  onReconnect: () => void,
) {
  const [connection, setConnection] = useState<Connection>("connecting");
  const handler = useRef(onEvent);
  const reconnect = useRef(onReconnect);

  // Latest-callback refs, updated in an effect rather than during render, so
  // the connection below can stay mounted for the life of the page while
  // still calling back into fresh state.
  useEffect(() => {
    handler.current = onEvent;
    reconnect.current = onReconnect;
  });

  useEffect(() => {
    const source = new EventSource(`${API}/events`);
    let dropped = false;

    source.onopen = () => {
      setConnection("open");
      // A reopened stream means frames were missed while it was down.
      if (dropped) reconnect.current();
      dropped = false;
    };
    source.onerror = () => {
      // EventSource retries on its own; say so rather than looking dead.
      dropped = true;
      setConnection("closed");
    };
    source.onmessage = (raw) => {
      try {
        handler.current(JSON.parse(raw.data) as AgentEvent);
      } catch {
        /* 해석할 수 없는 프레임은 버린다 */
      }
    };

    return () => source.close();
  }, []);

  return connection;
}
