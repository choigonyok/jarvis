"use client";

import { useSyncExternalStore } from "react";

/**
 * Whether the phone has a network at all (navigator.onLine): false in a
 * tunnel, on a plane, with no signal. A weak signal still reads true - that
 * shows as the stream's own "연결 끊김".
 */
function subscribe(cb: () => void) {
  window.addEventListener("online", cb);
  window.addEventListener("offline", cb);
  return () => {
    window.removeEventListener("online", cb);
    window.removeEventListener("offline", cb);
  };
}

export function useOnline(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => navigator.onLine,
    () => true,
  );
}
