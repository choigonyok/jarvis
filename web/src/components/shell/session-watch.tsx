"use client";

import { useEffect } from "react";
import { SIGNED_OUT_HEADER } from "@/lib/signed-out";

/** How often a request that got no answer may ask whether Access let the session go. */
const PROBE_EVERY = 60_000;

/**
 * Every request the page makes, watched for a session that ran out
 * (lib/signed-out.ts): the middleware's 401 goes to the login, and back to
 * this screen after it. Watched at fetch itself rather than in each screen's
 * read - the screens read in a dozen ways, and any one of them is enough to
 * notice.
 *
 * Access's month running out looks different: Access sends the request to
 * its login on another host, which a fetch cannot follow, so it fails as if
 * offline. A request that fails that way while the phone says it is online
 * asks once in a while without following redirects; a redirect is Access,
 * and loading the page anew is what lets it show its login.
 */
export function SessionWatch() {
  useEffect(() => {
    if (window.location.pathname.startsWith("/login")) return;
    const native = window.fetch;
    let leaving = false;
    let probed = 0;

    const toLogin = () => {
      if (leaving) return;
      leaving = true;
      const from = window.location.pathname + window.location.search;
      window.location.assign(from === "/" ? "/login" : `/login?from=${encodeURIComponent(from)}`);
    };
    const reload = () => {
      if (leaving) return;
      leaving = true;
      window.location.reload();
    };
    const probe = async () => {
      const now = Date.now();
      if (now - probed < PROBE_EVERY) return;
      probed = now;
      try {
        const res = await native.call(window, "/api/version", { cache: "no-store", redirect: "manual" });
        if (res.type === "opaqueredirect") reload();
        else if (res.status === 401 && res.headers.get(SIGNED_OUT_HEADER)) toLogin();
      } catch {
        // Really offline: nothing to tell.
      }
    };

    const watched: typeof window.fetch = async (input, init) => {
      try {
        const res = await native.call(window, input, init);
        if (res.status === 401 && res.headers.get(SIGNED_OUT_HEADER)) toLogin();
        return res;
      } catch (err) {
        // An abort is a DOMException, not this.
        if (err instanceof TypeError && navigator.onLine && ownApi(input)) void probe();
        throw err;
      }
    };
    window.fetch = watched;
    return () => {
      if (window.fetch === watched) window.fetch = native;
    };
  }, []);
  return null;
}

/** A request for this console's own API. */
function ownApi(input: RequestInfo | URL): boolean {
  try {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, window.location.href);
    return url.origin === window.location.origin && url.pathname.startsWith("/api/");
  } catch {
    return false;
  }
}
