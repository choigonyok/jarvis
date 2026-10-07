"use client";

import { useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

/**
 * The first thing anyone sees. It stays as quiet as the rest of the console -
 * a single card on the same dark ground - because it is a door, not a product
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
        setError(body?.error ?? "로그인하지 못했습니다.");
        setPassword("");
        return;
      }
      const from = params.get("from");
      // replace, not push: the login page should not be a back-button away
      // from a live session.
      router.replace(from && from.startsWith("/") ? from : "/");
      router.refresh();
    } catch {
      setError("서버에 연결하지 못했습니다.");
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
        className="w-full max-w-[20rem] rounded-2xl border border-edge bg-glass p-5 backdrop-blur-xl sm:p-6"
      >
        <h1 className="text-[15px] font-semibold tracking-tight text-foreground">
          Jarvis
        </h1>
        <p className="mt-1.5 text-[13px] leading-relaxed text-faint">
          계속하려면 로그인하세요.
        </p>

        <div className="mt-6 space-y-3">
          <label className="block">
            <span className="mb-1.5 block text-[11.5px] text-faint">아이디</span>
            <Input
              value={id}
              onChange={(e) => setId(e.target.value)}
              autoComplete="username"
              // No autoFocus on touch: it throws the keyboard up over the form
              // before anyone has decided to type.
              required
              className="h-11 border-edge bg-well/40 sm:h-9 sm:text-[14px]"
            />
          </label>
          <label className="block">
            <span className="mb-1.5 block text-[11.5px] text-faint">비밀번호</span>
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              className="h-11 border-edge bg-well/40 sm:h-9 sm:text-[14px]"
            />
          </label>
        </div>

        {error ? (
          <p
            role="status"
            className="mt-4 border-l-2 border-reject pl-3 text-[12.5px] leading-relaxed text-dim"
          >
            {error}
          </p>
        ) : null}

        <Button
          type="submit"
          disabled={busy || !id.trim() || !password}
          className="mt-6 h-12 w-full rounded-lg text-[14px] sm:h-9 sm:text-[13.5px]"
        >
          {busy ? "확인 중" : "로그인"}
        </Button>
      </form>
    </main>
  );
}
