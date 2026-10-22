"use client";

import { useEffect, useRef, useState } from "react";
import { onResume } from "@/lib/resume";
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
    let source: EventSource;
    let dropped = false;
    // When the stream in use was opened, and the reopen of our own that is
    // waiting (see onerror) with how long the next one waits.
    let openedAt = 0;
    let again: ReturnType<typeof setTimeout> | undefined;
    let wait = 0;

    const open = () => {
      clearTimeout(again);
      openedAt = Date.now();
      source = new EventSource(`${API}/events`);
      source.onopen = () => {
        setConnection("open");
        wait = 0;
        // A reopened stream means frames were missed while it was down.
        if (dropped) reconnect.current();
        dropped = false;
      };
      source.onerror = () => {
        dropped = true;
        setConnection("closed");
        // A dropped connection the browser retries on its own (CONNECTING).
        // An answer that is not a stream - the gateway's 502 when the
        // request reached the guest's agent through the tunnel they share,
        // or a service restarting - closes it for good (CLOSED), and nothing
        // opened it again until the app came back from the background: what
        // was sent meanwhile showed only after a restart (2026-10-22). So a
        // closed stream is opened again here, sooner the first time.
        if (source.readyState !== EventSource.CLOSED) return;
        wait = Math.min(wait ? wait * 2 : 1000, 15_000);
        clearTimeout(again);
        // In the background a phone would only kill it again; coming back
        // opens it (onResume, below).
        again = setTimeout(() => document.visibilityState === "visible" && open(), wait);
      };
      source.onmessage = (raw) => {
        try {
          handler.current(JSON.parse(raw.data) as AgentEvent);
        } catch {
          /* 해석할 수 없는 프레임은 버린다 */
        }
      };
    };
    open();

    // Back from the background the stream may be dead without having said
    // so: start a fresh one, and re-read what it would have carried. One
    // notification asks for this twice (shown in the page, and relayed by the
    // service worker), and every reopen is one more chance to be refused: a
    // stream opened a moment ago and not closed is already the fresh one.
    const stop = onResume(() => {
      if (source.readyState !== EventSource.CLOSED && Date.now() - openedAt < 1500) return;
      source.close();
      dropped = true;
      open();
    });

    return () => {
      stop();
      clearTimeout(again);
      source.close();
    };
  }, []);

  return connection;
}
