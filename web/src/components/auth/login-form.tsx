"use client";

import { useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SecretInput } from "@/components/ui/secret-input";
import { Failed } from "@/components/shell/failed";

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
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ id: id.trim(), password }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        setError(body?.error ?? "로그인하지 못했어요.");
        setPassword("");
        return;
      }
      const from = params.get("from");
      // replace, not push: the login page should not be a back-button away
      // from a live session.
      router.replace(from && from.startsWith("/") ? from : "/");
      router.refresh();
    } catch {
      setError("서버에 연결하지 못했어요.");
    } finally {
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
              // No autoFocus on touch: it throws the keyboard up over the form
              // before anyone has decided to type.
              required
              className="h-11 border-edge bg-well/40 mouse:h-9 mouse:text-body"
            />
          </label>
          <label className="block">
            <span className="mb-1.5 block text-meta text-faint">비밀번호</span>
            {/* With 보기: a phone keyboard makes a typo easy, and this is the one place to see it. */}
            <SecretInput
              value={password}
              onChange={setPassword}
              autoComplete="current-password"
              required
              className="h-11 border-edge bg-well/40 mouse:h-9 mouse:text-body"
            />
          </label>
        </div>

        {error ? (
          <Failed className="mt-4">{error}</Failed>
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
