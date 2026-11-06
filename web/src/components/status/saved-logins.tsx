"use client";

import { useEffect, useMemo, useState } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Pager } from "@/components/ui/pager";
import { usePaged } from "@/lib/paging";
import { API } from "@/lib/thread";
import { SAY } from "@/lib/say";
import { dateTime } from "@/lib/when";

type Login = { domain: string; name: string; hint: string; lastUsedAt?: string; uses: number };
type Use = { at: string; domain: string; host: string; purpose: string; outcome: "released" | "none" | "refused" | "failed" };

const outcomeCopy: Record<Use["outcome"], string> = {
  released: "넣음",
  none: "저장된 정보 없음",
  refused: "거절",
  failed: "열지 못함",
};


/** "오늘 14:02", "10월 18일 14:02" - on Seoul's clock, as everywhere (lib/when.ts); it read the phone's own. */
const when = (iso: string) => dateTime(iso);

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
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setForgetting(null);
    }
  }

  if (!data) {
    return <p className="py-2 text-aside text-faint">{error ?? SAY.loading}</p>;
  }
  return (
    <div className="space-y-4 py-1">
      <p className="text-aside leading-relaxed text-dim">
        대화에서 &ldquo;쿠팡 로그인 정보 저장해줘&rdquo;라고 하면 저장 카드가 올라와요. 카드에 넣은 아이디·비밀번호는 암호화해서
        보관하고, 그 사이트의 로그인 화면에서만 자동으로 넣어요. Jarvis는 값을 볼 수 없어요.
      </p>
      {error ? (
        <p role="alert" className="text-aside text-reject">
          {error}
        </p>
      ) : null}
      {data.logins.length === 0 ? (
        <p className="text-ui text-faint">저장된 로그인 정보가 없어요.</p>
      ) : (
        <div id={loginsPaged.anchor} className="scroll-mt-4">
        <ul className="divide-y divide-edge-soft">
          {loginsPaged.items.map((l) => (
            <li key={l.domain} className="flex items-center gap-3 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="truncate text-ui text-ink">
                  {l.name} <span className="text-aside text-faint">{l.domain}</span>
                </p>
                <p className="tnum mt-0.5 text-meta text-faint">
                  아이디 {l.hint} · {l.lastUsedAt ? `마지막 사용 ${when(l.lastUsedAt)} · ${l.uses}번` : "아직 안 씀"}
                </p>
              </div>
              {/* Asked the way every irreversible 지우기 is (AlertDialog). */}
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button variant="quiet" size="touch-sm" className="shrink-0" disabled={forgetting !== null}>
                    {forgetting === l.domain ? "지우는 중" : "지우기"}
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>{l.name} 로그인 정보를 지울까요?</AlertDialogTitle>
                    <AlertDialogDescription>
                      Jarvis가 {l.domain}에 다시 로그인하려면 대화에서 로그인 정보를 다시 저장해야 해요. 되돌릴 수 없어요.
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>두기</AlertDialogCancel>
                    <AlertDialogAction onClick={() => void forget(l.domain)}>지우기</AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </li>
          ))}
        </ul>
        <Pager {...loginsPaged} unit="곳" />
        </div>
      )}
      {data.uses.length > 0 ? (
        <div id={usesPaged.anchor} className="scroll-mt-4">
          <p className="mb-1.5 text-aside text-faint">최근에 쓴 기록</p>
          <ul className="space-y-1">
            {usesPaged.items.map((u, i) => (
              <li key={`${u.at}-${i}`} className="tnum flex gap-2 text-aside text-dim">
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
