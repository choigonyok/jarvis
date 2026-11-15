"use client";

import { useId, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SecretInput } from "@/components/ui/secret-input";
import { Failed } from "@/components/shell/failed";

/**
 * Where to go once in: the screen the session ran out on (?from=), when it is
 * one of this console's own. A leading "/" alone let "//elsewhere.example" -
 * or "/\elsewhere.example", which a browser reads the same - through, and a
 * crafted login link sent the person off-site the moment they signed in.
 */
function backTo(from: string | null): string {
  if (!from) return "/";
  try {
    const url = new URL(from, window.location.origin);
    return url.origin === window.location.origin ? url.pathname + url.search + url.hash : "/";
  } catch {
    return "/";
  }
}

/**
 * The first thing anyone sees. It stays as quiet as the rest of the console -
 * a single card on the page's own ground - because it is a door, not a product
 * page: there is exactly one person who gets through it.
 */
export function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const [id, setId] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  // The id and password did not match: both fields are what was wrong.
  const [wrong, setWrong] = useState(false);
  const [busy, setBusy] = useState(false);
  const errorId = useId();
  const secret = useRef<HTMLInputElement>(null);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    setWrong(false);
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ id: id.trim(), password }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        setError(body?.error ?? "로그인하지 못했어요.");
        setWrong(res.status === 401);
        setPassword("");
        setBusy(false);
        // Back in the field to type again: submitted from the keyboard, the
        // keyboard had gone, and the emptied field had to be found and tapped.
        secret.current?.focus();
        return;
      }
      // replace, not push: the login page should not be a back-button away
      // from a live session.
      router.replace(backTo(params.get("from")));
      router.refresh();
      // Still 확인 중 while the console loads: the form goes with this page.
      // Back to 로그인 meanwhile, it looked refused, and was pressed again.
    } catch {
      setError("서버에 연결하지 못했어요.");
      setBusy(false);
    }
  }

  return (
    // Centred on a laptop; on a phone it sits a third of the way down, where
    // the thumb is and clear of the keyboard when it comes up.
    <main className="inset-x-safe flex h-full items-start justify-center px-5 pt-[18vh] sm:items-center sm:pt-0">
      <form
        onSubmit={submit}
        className="w-full max-w-[20rem] rounded-2xl bg-glass p-5 sm:p-6"
      >
        <h1 className="text-title font-semibold tracking-tight text-foreground">
          Jarvis
        </h1>
        <p className="mt-1.5 text-ui leading-relaxed text-faint">
          계속하려면 로그인하세요.
        </p>

        <div className="mt-6 space-y-3">
          <label className="block">
            <span className="mb-1.5 block text-meta text-faint">아이디</span>
            <Input
              value={id}
              onChange={(e) => setId(e.target.value)}
              autoComplete="username"
              // Compared as typed: a phone's capital first letter, or its
              // correction, made a right id a wrong one.
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              // No autoFocus on touch: it throws the keyboard up over the form
              // before anyone has decided to type.
              required
              aria-invalid={wrong || undefined}
              aria-describedby={error ? errorId : undefined}
              className="h-11 border-edge bg-well/40 mouse:h-9 mouse:text-body"
            />
          </label>
          <label className="block">
            <span className="mb-1.5 block text-meta text-faint">비밀번호</span>
            {/* With 보기: a phone keyboard makes a typo easy, and this is the one place to see it. */}
            <SecretInput
              ref={secret}
              value={password}
              onChange={setPassword}
              autoComplete="current-password"
              required
              aria-invalid={wrong || undefined}
              aria-describedby={error ? errorId : undefined}
              className="h-11 border-edge bg-well/40 mouse:h-9 mouse:text-body"
            />
          </label>
        </div>

        {error ? (
          <div id={errorId} className="mt-4">
            <Failed>{error}</Failed>
          </div>
        ) : null}

        <Button
          type="submit"
          disabled={busy || !id.trim() || !password}
          className="mt-6 h-12 w-full rounded-lg text-body mouse:h-9 mouse:text-ui"
        >
          {busy ? "확인 중" : "로그인"}
        </Button>
      </form>
    </main>
  );
}
