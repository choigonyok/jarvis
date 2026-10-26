"use client";

/**
 * Where a notification takes you, all the way in.
 *
 * A notification's url names a screen and, with ?focus=<id>, the part of it
 * the notification is about: a payment's row (tx:123), a budget line, a
 * status check (check:kakaotalk), a calendar entry (event:abc, with ?date= so
 * the day is open). Each screen marks those parts with data-focus="<id>".
 * Arriving - from the inbox, a push tapped on the phone, or a banner shown in
 * the page - the screen opens, the part is brought to the middle and lit for
 * a moment. The screen may still be loading, so it is waited for.
 *
 * Approval cards keep their own ?proposal= (chat/thread.tsx), which also
 * holds the thread still while photos above the card load.
 */

const FOCUS = "jarvis:focus";
const OPEN = "jarvis:open-params";

/** The part of a screen a url points at. */
export function focusOf(url: URL): string | null {
  return url.searchParams.get("focus");
}

/** A screen already showing is told what to show: ?focus=, ?date= and the like. */
export function onOpenParams(fn: (params: URLSearchParams) => void): () => void {
  const handle = (e: Event) => fn((e as CustomEvent<URLSearchParams>).detail);
  window.addEventListener(OPEN, handle);
  return () => window.removeEventListener(OPEN, handle);
}

export function onFocusRequest(fn: (id: string) => void): () => void {
  const handle = (e: Event) => fn((e as CustomEvent<string>).detail);
  window.addEventListener(FOCUS, handle);
  return () => window.removeEventListener(FOCUS, handle);
}

/**
 * Notifications from before their senders said which part (or a kind that
 * never does) still land on the right section, from the kind alone.
 */
const BY_KIND: [RegExp, string, string][] = [
  [/^spending\.unparsed/, "/spending", "unparsed"],
  [/^spending\.budget/, "/spending", "budget"],
  [/^spending\.daily/, "/spending", "today"],
  [/^spending\./, "/spending", "recent"],
  [/^assets\.(flow|ledger)/, "/assets", "principal"],
  [/^assets\.tax/, "/assets", "tax"],
  [/^assets\.band/, "/assets", "allocation"],
  [/^assets\./, "/assets", "summary"],
  [/^status\./, "/status", "problems"],
  [/^calendar\./, "/calendar", "day"],
  [/^location\./, "/status", "check:context.location"],
  [/^job\./, "/jobs", "waiting"],
];

/** Where an inbox entry goes: its own url, with a section filled in from its kind when it has none. */
export function targetOf(note: { url: string; kind: string }): string {
  const url = new URL(note.url || "/", "http://x");
  if (url.searchParams.has("focus") || url.searchParams.has("proposal") || url.hash) return note.url || "/";
  for (const [kind, path, focus] of BY_KIND) {
    if (!kind.test(note.kind)) continue;
    if (url.pathname !== "/" && url.pathname !== path) break;
    url.searchParams.set("focus", focus);
    return path + url.search;
  }
  return note.url || "/";
}

/**
 * Opens a notification's target. Another screen: navigate, and it reads its
 * params when it mounts. This screen: no navigation - it is told.
 */
export function openTarget(url: string, push: (path: string) => void) {
  const target = new URL(url || "/", window.location.origin);
  if (target.pathname !== window.location.pathname) {
    push(target.pathname + target.search);
    return;
  }
  window.dispatchEvent(new CustomEvent<URLSearchParams>(OPEN, { detail: target.searchParams }));
  const focus = focusOf(target);
  if (focus) window.dispatchEvent(new CustomEvent<string>(FOCUS, { detail: focus }));
}

/**
 * What is being looked for right now, while focusSection waits for it. A list
 * split into pages (lib/paging.ts) has most of its rows off screen: it reads
 * this, and hears it change, to open the page the row is on.
 */
let wanted: string | null = null;
const WANTED = "jarvis:focus-wanted";

export function wantedFocus(): string | null {
  return wanted;
}

export function onFocusWanted(fn: (id: string) => void): () => void {
  const handle = (e: Event) => fn((e as CustomEvent<string>).detail);
  window.addEventListener(WANTED, handle);
  return () => window.removeEventListener(WANTED, handle);
}

/**
 * Brings [data-focus=id] to the middle of its scroller and lights it. Waits
 * up to ten seconds for it to appear (the screen is still loading, a month
 * still being fetched, a page of rows being opened); returns a function that
 * stops waiting.
 */
export function focusSection(id: string): () => void {
  wanted = id;
  window.dispatchEvent(new CustomEvent<string>(WANTED, { detail: id }));
  let stopped = false;
  const find = () => {
    const el = document.querySelector<HTMLElement>(`[data-focus="${CSS.escape(id)}"]`);
    if (!el) return false;
    // A closed disclosure opens: the thing is inside it.
    if (el instanceof HTMLDetailsElement) el.open = true;
    el.dispatchEvent(new CustomEvent("jarvis:focused", { bubbles: true }));
    el.scrollIntoView({ block: "center" });
    el.classList.remove("focus-flash");
    void el.offsetWidth; // a second tap restarts it
    el.classList.add("focus-flash");
    return true;
  };
  if (find()) {
    wanted = null;
    return () => {};
  }
  const mo = new MutationObserver(() => {
    if (!stopped && find()) stop();
  });
  // An attribute too: a screen may point an existing section at the target
  // (the spending list becomes that day's rows).
  mo.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-focus"] });
  const timer = window.setTimeout(stop, 10_000);
  function stop() {
    stopped = true;
    if (wanted === id) wanted = null;
    mo.disconnect();
    window.clearTimeout(timer);
  }
  return stop;
}

/** Drops ?focus= from the address once used, so a reload does not do it again. */
export function forgetFocus() {
  const url = new URL(window.location.href);
  if (!url.searchParams.has("focus")) return;
  url.searchParams.delete("focus");
  window.history.replaceState(window.history.state, "", url.pathname + url.search + url.hash);
}
