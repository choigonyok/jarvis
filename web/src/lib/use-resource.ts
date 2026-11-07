"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { onResume } from "@/lib/resume";

/**
 * What a screen reads from the server and shows: read on opening and when its
 * key changes, again on coming back to the console (lib/resume.ts - the page
 * shown again, a pull to refresh, a tapped notification) and, if asked, every
 * so often while the page is on screen.
 *
 * The newest read is the one whose answer is shown. A newer read does not
 * call off one under way, only stops showing what it brings: cutting a
 * request short cuts short what it set going on the service, which may keep
 * that as a failure. A read made before the page was last shown again - before
 * it was hidden, or while it was - is the exception: a phone drops a hidden
 * page's connections without an error, so it may never answer, and coming
 * back or the next poll calls it off and reads again. One made since is
 * waited for instead: coming back makes no other, and a pull to refresh waits
 * for it. A poll leaves a read under way to finish unless it has hung for
 * three rounds and half a minute at least. Leaving the screen or a new key
 * calls off every read under way.
 *
 * A read that fails is tried again after a wait that doubles; until one
 * succeeds, what was shown stays, and the error is beside it. A key that
 * changes keeps the data shown until the new key's read lands (dataKey says
 * whose it is).
 */

/**
 * How a resource is read, given the signal that calls the read off and which
 * try this is: 0, then 1, 2, ... for the tries again after a failure. Work a
 * read does beside its request is done on the first.
 */
export type Fetcher<T> = (signal: AbortSignal, attempt: number) => Promise<T>;

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
  /** Reads now, and resolves once its answer, or a newer read's, has landed or failed. */
  reload: () => Promise<void>;
  /**
   * Reads now this once another way: an action, such as a POST that makes
   * the service look again. Nothing calls it off - not leaving the screen,
   * coming back or another read - as that would cut it short where it runs;
   * its answer is shown unless a newer read has been made since. It is an
   * action, so its failure shows at once, untried again. It resolves as
   * reload does.
   */
  reloadWith: (action: () => Promise<T>) => Promise<void>;
  /** Changes what is shown until the next read lands: what an action here just did. */
  mutate: (next: (prev: T | undefined) => T | undefined) => void;
};

type Shown<T> = Pick<Resource<T>, "data" | "dataKey" | "error" | "readAt">;

/** A read under way. */
type Flight = {
  /** How many times the page had been shown again when it started (shows). */
  shows: number;
  /** When it started: a read waited on for rounds is taken for hung. */
  at: number;
  /** What calls it off; an action has none. */
  ctrl?: AbortController;
  /** Set while it waits to be tried again. */
  retry?: number;
};

/**
 * How many times the page has been shown again after it was hidden. A read
 * made before the last of them may have died with a connection the phone
 * dropped meanwhile, and is not waited on.
 */
let shows = 0;
if (typeof document !== "undefined") {
  let hidden = document.visibilityState === "hidden";
  const hide = () => {
    hidden = true;
  };
  // Counted once each time, though a page back from the back-forward cache
  // says so twice (pageshow, visibilitychange): the second must not take the
  // read the first one started for a stale one.
  const show = () => {
    if (!hidden) return;
    hidden = false;
    shows += 1;
  };
  // Added when this module loads, before any screen's onResume: listeners
  // run in the order they were added, so a screen coming back sees the count
  // that includes this time.
  document.addEventListener("visibilitychange", () => (document.visibilityState === "visible" ? show() : hide()));
  window.addEventListener("pagehide", hide);
  window.addEventListener("pageshow", (e) => {
    if (e.persisted) show();
  });
}

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
  // The newest read, whose answer is the one shown.
  const flight = useRef<Flight | null>(null);
  // Every read under way that can be called off: the newest, and the older
  // ones it left to finish.
  const out = useRef(new Set<Flight>());
  // Whoever waits for the screen to catch up (a reload, a pull to refresh):
  // let go once the newest read lands or fails, or everything is called off.
  const waiting = useRef<(() => void)[]>([]);

  const letGo = useCallback(() => {
    const done = waiting.current;
    waiting.current = [];
    for (const resolve of done) resolve();
  }, []);
  const caughtUp = useCallback(() => new Promise<void>((resolve) => waiting.current.push(resolve)), []);

  // Leaving the screen, or a new key: every read under way is called off. An
  // action is not, but what it brings is no longer shown.
  const stop = useCallback(() => {
    for (const f of out.current) f.ctrl?.abort();
    out.current.clear();
    window.clearTimeout(flight.current?.retry);
    flight.current = null;
    letGo();
  }, [letGo]);

  const read = useCallback(
    (k: string | null, action?: () => Promise<T>): Promise<void> => {
      if (k === null) return Promise.resolve();
      const prev = flight.current;
      if (prev?.retry !== undefined) {
        // Waiting to be tried again, it has nothing out.
        window.clearTimeout(prev.retry);
        out.current.delete(prev);
      }
      // One made before the page was last shown again may never answer.
      for (const f of out.current) {
        if (f.shows === shows) continue;
        f.ctrl?.abort();
        out.current.delete(f);
      }
      const ctrl = new AbortController();
      const mine: Flight = { shows, at: Date.now(), ctrl: action ? undefined : ctrl };
      flight.current = mine;
      if (!action) out.current.add(mine);
      const done = caughtUp();
      const tries = action ? 0 : retries;
      const attempt = (n: number) => {
        (action ? action() : latest.current(ctrl.signal, n)).then(
          (data) => {
            out.current.delete(mine);
            if (flight.current !== mine) return;
            flight.current = null;
            setShown({ data, dataKey: k, error: null, readAt: new Date() });
            letGo();
          },
          (err: unknown) => {
            if (flight.current === mine && n < tries && retryable(err)) {
              mine.retry = window.setTimeout(() => {
                mine.retry = undefined;
                attempt(n + 1);
              }, retryDelay * 2 ** n);
              return;
            }
            out.current.delete(mine);
            if (flight.current !== mine) return;
            flight.current = null;
            const error = err instanceof Error ? err : new Error(String(err));
            setShown((s) => ({ ...s, error }));
            letGo();
          },
        );
      };
      attempt(0);
      return done;
    },
    [caughtUp, letGo, retries, retryDelay],
  );

  // The key's own read: on opening, and when the key changes.
  useEffect(() => {
    keyRef.current = key;
    void read(key);
    return stop;
  }, [key, read, stop]);

  // Every so often, only while the page is on screen. A read under way is
  // left to finish - a slow answer would otherwise never land - unless it was
  // made before the page was last shown again, or has hung for three rounds
  // and half a minute at least.
  useEffect(() => {
    if (!interval || key === null) return;
    const id = window.setInterval(() => {
      if (document.visibilityState !== "visible") return;
      const f = flight.current;
      if (f && f.shows === shows && Date.now() - f.at < Math.max(interval * 3, 30_000)) return;
      void read(keyRef.current);
    }, interval);
    return () => window.clearInterval(id);
  }, [interval, key, read]);

  // Back to the console: a read made since the page was shown again is the
  // one coming back wants, and whoever asked (a pull to refresh) waits for it.
  // One made before may have died with the connection the phone dropped in
  // the background, so it is called off and made again.
  useEffect(() => {
    if (!resume || key === null) return;
    return onResume(() => {
      const f = flight.current;
      if (f && f.shows === shows) return caughtUp();
      return read(keyRef.current);
    });
  }, [resume, key, read, caughtUp]);

  const reload = useCallback(() => read(keyRef.current), [read]);
  const reloadWith = useCallback((action: () => Promise<T>) => read(keyRef.current, action), [read]);
  const mutate = useCallback((next: (prev: T | undefined) => T | undefined) => {
    setShown((s) => ({ ...s, data: next(s.data) }));
  }, []);

  return { ...shown, reload, reloadWith, mutate };
}
