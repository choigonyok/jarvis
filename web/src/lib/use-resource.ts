"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { onResume } from "@/lib/resume";

/**
 * What a screen reads from the server and shows: read on opening and when its
 * key changes, again on coming back to the console (lib/resume.ts - the page
 * shown again, a pull to refresh, a tapped notification) and, if asked, every
 * so often while the page is on screen.
 *
 * One read at a time, each with its own AbortController: leaving the screen,
 * a new key or a newer read calls off the one under way, and what it brings
 * is never shown. A poll leaves a read under way to finish rather than cut
 * it off - a slow answer would otherwise never land - unless it has hung for
 * three rounds and half a minute at least. A read that fails is tried again
 * after a wait that doubles; until one succeeds, what was shown stays, and
 * the error is beside it. A key that changes keeps the data shown until the
 * new key's read lands (dataKey says whose it is).
 */

/** How a resource is read, given the signal that calls the read off. */
export type Fetcher<T> = (signal: AbortSignal) => Promise<T>;

/** A response that said no. A refusal (4xx) is not tried again: asking again gets the same answer. */
export class HttpError extends Error {
  constructor(
    readonly status: number,
    message?: string,
  ) {
    super(message ?? String(status));
  }
}

export type ResourceOptions<T> = {
  /** Read again this often (ms) while the page is on screen - or as often as what was read says (a job running). */
  interval?: number | ((data: T | undefined) => number | undefined);
  /** How many times a failed read is tried again before its failure shows (default 2). */
  retries?: number;
  /** The wait before the first try again (default 1000 ms); each one after waits twice as long. */
  retryDelay?: number;
  /** Read again on coming back to the console (default true). */
  resume?: boolean;
};

export type Resource<T> = {
  /** What the last read brought: kept while the next is under way, and through a failure. */
  data: T | undefined;
  /** The key `data` was read for. */
  dataKey: string | null;
  /** Why the last read failed, until one succeeds. */
  error: Error | null;
  /** When `data` was read. */
  readAt: Date | null;
  /** Reads now, calling off a read under way, and resolves once it has landed. */
  reload: () => Promise<void>;
  /**
   * Reads now this once another way (a POST that makes the service look
   * again). It is an action, so its failure shows at once, untried again.
   */
  reloadWith: (once: Fetcher<T>) => Promise<void>;
  /** Changes what is shown until the next read lands: what an action here just did. */
  mutate: (next: (prev: T | undefined) => T | undefined) => void;
};

type Shown<T> = Pick<Resource<T>, "data" | "dataKey" | "error" | "readAt">;

const retryable = (err: unknown) => !(err instanceof HttpError && err.status >= 400 && err.status < 500);

/** `key` null reads nothing (not yet known what to read). */
export function useResource<T>(key: string | null, fetcher: Fetcher<T>, options: ResourceOptions<T> = {}): Resource<T> {
  const { retries = 2, retryDelay = 1000, resume = true } = options;
  const [shown, setShown] = useState<Shown<T>>({ data: undefined, dataKey: null, error: null, readAt: null });
  const interval = typeof options.interval === "function" ? options.interval(shown.data) : options.interval;
  // The fetcher as last drawn: a read uses the newest, and a new one is no reason to read.
  const latest = useRef(fetcher);
  useLayoutEffect(() => {
    latest.current = fetcher;
  });
  const keyRef = useRef(key);
  // The read under way, and when it started.
  const flight = useRef<{ ctrl: AbortController; at: number } | null>(null);
  const again = useRef<number | undefined>(undefined);

  const stop = useCallback(() => {
    flight.current?.ctrl.abort();
    flight.current = null;
    window.clearTimeout(again.current);
  }, []);

  const read = useCallback(
    (k: string | null, once?: Fetcher<T>): Promise<void> => {
      stop();
      if (k === null) return Promise.resolve();
      const ctrl = new AbortController();
      const mine = { ctrl, at: Date.now() };
      flight.current = mine;
      const tries = once ? 0 : retries;
      return new Promise<void>((resolve) => {
        // Called off: whoever waits on it (a pull to refresh) waits no more.
        ctrl.signal.addEventListener("abort", () => resolve(), { once: true });
        const attempt = (n: number) => {
          (once ?? latest.current)(ctrl.signal).then(
            (data) => {
              if (flight.current !== mine) return;
              flight.current = null;
              setShown({ data, dataKey: k, error: null, readAt: new Date() });
              resolve();
            },
            (err: unknown) => {
              if (flight.current !== mine) return;
              if (n < tries && retryable(err)) {
                again.current = window.setTimeout(() => attempt(n + 1), retryDelay * 2 ** n);
                return;
              }
              flight.current = null;
              const error = err instanceof Error ? err : new Error(String(err));
              setShown((s) => ({ ...s, error }));
              resolve();
            },
          );
        };
        attempt(0);
      });
    },
    [stop, retries, retryDelay],
  );

  // The key's own read: on opening, and when the key changes.
  useEffect(() => {
    keyRef.current = key;
    void read(key);
    return stop;
  }, [key, read, stop]);

  // Every so often, only while the page is on screen.
  useEffect(() => {
    if (!interval || key === null) return;
    const id = window.setInterval(() => {
      if (document.visibilityState !== "visible") return;
      const f = flight.current;
      if (f && Date.now() - f.at < Math.max(interval * 3, 30_000)) return;
      void read(keyRef.current);
    }, interval);
    return () => window.clearInterval(id);
  }, [interval, key, read]);

  // Back to the console: a read under way may have died with the connection
  // the phone dropped in the background, so it is called off and made again.
  useEffect(() => {
    if (!resume || key === null) return;
    return onResume(() => read(keyRef.current));
  }, [resume, key, read]);

  const reload = useCallback(() => read(keyRef.current), [read]);
  const reloadWith = useCallback((once: Fetcher<T>) => read(keyRef.current, once), [read]);
  const mutate = useCallback((next: (prev: T | undefined) => T | undefined) => {
    setShown((s) => ({ ...s, data: next(s.data) }));
  }, []);

  return { ...shown, reload, reloadWith, mutate };
}
