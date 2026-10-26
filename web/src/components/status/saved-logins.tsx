"use client";

import { useEffect, useMemo, useState } from "react";
import { Pager } from "@/components/ui/pager";
import { usePaged } from "@/lib/paging";
import { API } from "@/lib/thread";
import { cn } from "@/lib/utils";

type Login = { domain: string; name: string; hint: string; lastUsedAt?: string; uses: number };
type Use = { at: string; domain: string; host: string; purpose: string; outcome: "released" | "none" | "refused" | "failed" };

const outcomeCopy: Record<Use["outcome"], string> = {
  released: "넣음",
  none: "저장된 정보 없음",
  refused: "거절",
  failed: "열지 못함",
};

const quiet =
  "inline-flex h-11 items-center justify-center rounded-lg px-3 text-[13px] text-dim outline-none transition-colors hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 mouse:h-8";

function when(iso: string) {
  const d = new Date(iso);
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/**
 * The logins Jarvis keeps (vault-svc): which sites, under which username
 * hint, and every time the browser was handed one. Nothing here can show a
 * password - the console never receives one. Saving is a card in the
 * conversation ("쿠팡 로그인 정보 저장해줘"); forgetting is here, or a card.
 *
 * It is the body of one row on the 상태 screen (로그인 정보), fetched when
 * the row is opened.
 */
export function SavedLogins() {
  const [data, setData] = useState<{ logins: Login[]; uses: Use[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [forgetting, setForgetting] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<string | null>(null);
  // The sites twenty a page, what was done with them ten.
  const logins = useMemo(() => data?.logins ?? [], [data]);
  const uses = useMemo(() => data?.uses ?? [], [data]);
  const loginsPaged = usePaged(logins);
  const usesPaged = usePaged(uses, { size: 10 });

  async function load() {
    try {
      const res = await fetch(`${API}/vault`, { cache: "no-store" });
      const body = await res.json();
      if (!res.ok) throw new Error(body.error ?? "불러오지 못했어요.");
      setData(body);
      setError(null);
    } catch (e) {
      setError((e as Error).message || "불러오지 못했어요.");
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- loads once when the row opens
    void load();
  }, []);

  async function forget(domain: string) {
    setForgetting(domain);
    try {
      const res = await fetch(`${API}/vault/${encodeURIComponent(domain)}`, { method: "DELETE" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error ?? "지우지 못했어요.");
      }
      setConfirm(null);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setForgetting(null);
    }
  }

  if (!data) {
    return <p className="py-2 text-[12.5px] text-faint">{error ?? "불러오는 중이에요."}</p>;
  }
  return (
    <div className="space-y-4 py-1">
      <p className="text-[12.5px] leading-relaxed text-dim">
        대화에서 &ldquo;쿠팡 로그인 정보 저장해줘&rdquo;라고 하면 저장 카드가 올라와요. 카드에 넣은 아이디·비밀번호는 암호화해서
        보관하고, 그 사이트의 로그인 화면에서만 자동으로 넣어요. 자비스는 값을 볼 수 없어요.
      </p>
      {error ? (
        <p role="alert" className="text-[12.5px] text-reject">
          {error}
        </p>
      ) : null}
      {data.logins.length === 0 ? (
        <p className="text-[13px] text-faint">저장된 로그인 정보가 없어요.</p>
      ) : (
        <div id={loginsPaged.anchor} className="scroll-mt-4">
        <ul className="divide-y divide-edge-soft">
          {loginsPaged.items.map((l) => (
            <li key={l.domain} className="flex items-center gap-3 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13.5px] text-foreground/90">
                  {l.name} <span className="text-[12px] text-faint">{l.domain}</span>
                </p>
                <p className="tnum mt-0.5 text-[11.5px] text-faint">
                  아이디 {l.hint} · {l.lastUsedAt ? `마지막 사용 ${when(l.lastUsedAt)} · ${l.uses}번` : "아직 안 씀"}
                </p>
              </div>
              {confirm === l.domain ? (
                <div className="flex shrink-0 items-center">
                  <button type="button" className={cn(quiet, "text-reject hover:text-reject")} disabled={forgetting !== null} onClick={() => void forget(l.domain)}>
                    {forgetting === l.domain ? "지우는 중" : "정말 지우기"}
                  </button>
                  <button type="button" className={quiet} onClick={() => setConfirm(null)}>
                    두기
                  </button>
                </div>
              ) : (
                <button type="button" className={cn(quiet, "shrink-0")} onClick={() => setConfirm(l.domain)}>
                  지우기
                </button>
              )}
            </li>
          ))}
        </ul>
        <Pager {...loginsPaged} unit="곳" />
        </div>
      )}
      {data.uses.length > 0 ? (
        <div id={usesPaged.anchor} className="scroll-mt-4">
          <p className="mb-1.5 text-[12px] text-faint">최근에 쓴 기록</p>
          <ul className="space-y-1">
            {usesPaged.items.map((u, i) => (
              <li key={`${u.at}-${i}`} className="tnum flex gap-2 text-[12px] text-dim">
                <span className="shrink-0 text-faint">{when(u.at)}</span>
                <span className="min-w-0 truncate">
                  {u.host || u.domain} · {u.purpose || "?"} · {outcomeCopy[u.outcome] ?? u.outcome}
                </span>
              </li>
            ))}
          </ul>
          <Pager {...usesPaged} unit="번" />
        </div>
      ) : null}
    </div>
  );
}
