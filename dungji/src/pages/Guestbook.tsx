// 방명록: 놀러 온 이웃이 한마디 남기고, 주인은 답글을 단다.

import { useCallback, useEffect, useState, type FormEvent } from "react";
import type { GuestEntry, HomeView, Me, Reward } from "../../shared/api";
import { ApiError, del, get, post } from "../lib/api";
import { linkTo } from "../lib/router";
import { ago } from "../lib/time";
import { useToast } from "../lib/toast";
import { Icon } from "../ui/icons";

export function GuestbookPanel({ home, me, stamp, compact }: { home: HomeView; me: Me; stamp: number; compact?: boolean }) {
  const toast = useToast();
  const [entries, setEntries] = useState<GuestEntry[] | null>(null);
  const [more, setMore] = useState(false);
  const [body, setBody] = useState("");
  const [secret, setSecret] = useState(false);
  const [busy, setBusy] = useState(false);
  const base = `/homes/${encodeURIComponent(home.slug)}/guestbook`;

  const load = useCallback(() => {
    get<{ entries: GuestEntry[] }>(base)
      .then((r) => {
        setEntries(r.entries);
        setMore(r.entries.length === 20);
      })
      .catch(() => setEntries([]));
  }, [base]);

  useEffect(() => {
    load();
  }, [load, stamp]);

  async function write(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await post<{ entries: GuestEntry[]; rewards: Reward[] }>(base, { body, secret });
      setEntries(r.entries);
      setBody("");
      setSecret(false);
      r.rewards.forEach((x) => toast.feathers(x.delta, x.reason));
      if (!r.rewards.length) toast.show("방명록을 남겼어요");
    } catch (err) {
      toast.show(err instanceof ApiError ? err.message : "남기지 못했어요.");
    } finally {
      setBusy(false);
    }
  }

  async function older() {
    if (!entries?.length) return;
    const r = await get<{ entries: GuestEntry[] }>(`${base}?before=${entries[entries.length - 1].at}`).catch(() => null);
    if (r) {
      setEntries([...entries, ...r.entries]);
      setMore(r.entries.length === 20);
    }
  }

  const shown = compact ? entries?.slice(0, 3) : entries;

  return (
    <div>
      <h2 className="mb-2 text-title font-semibold">{home.owner ? "우리 방명록" : `${home.name} 방명록`}</h2>
      {!home.owner && (
        <form onSubmit={write} className="mb-3 rounded-xl bg-glass p-3">
          <textarea
            className="field  min-h-[96px] text-read leading-snug"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            maxLength={300}
            placeholder={`${home.name}에 한마디`}
            aria-label="방명록에 남길 말"
          />
          <div className="mt-2 flex items-center gap-3">
            <label className="flex h-11 cursor-pointer items-center gap-2 text-ui">
              <input type="checkbox" checked={secret} onChange={(e) => setSecret(e.target.checked)} className="size-5 accent-[var(--foreground)]" />
              <Icon name="lock" className="size-4 text-dim" />
              둥지 주인만 보기
            </label>
            <span className="flex-1" />
            <button className="btn btn-approve h-11 min-h-0" disabled={busy || !body.trim()}>
              남기기
            </button>
          </div>
        </form>
      )}
      {entries === null ? (
        <div className="h-24 rounded-xl bg-glass" aria-busy="true" />
      ) : entries.length === 0 ? (
        <p className="rounded-xl bg-glass px-4 py-4 text-body text-dim">{home.owner ? "아직 방명록이 비어 있어요. 이웃에 먼저 놀러 가 보세요." : "첫 한마디를 남겨 주세요."}</p>
      ) : (
        <ul className="space-y-2.5">
          {shown!.map((g) => (
            <Entry key={g.id} g={g} owner={home.owner} meId={me.id} onEntries={setEntries} />
          ))}
        </ul>
      )}
      {!compact && more && (
        <button type="button" className="btn btn-raised mt-3 w-full" onClick={older}>
          지난 방명록 더 보기
        </button>
      )}
      {compact && (entries?.length ?? 0) > 3 && (
        <a className="btn btn-raised mt-3 w-full" {...linkTo(`/h/${home.slug}?tab=guestbook`)}>
          방명록 모두 보기
        </a>
      )}
    </div>
  );
}

function Entry({ g, owner, meId, onEntries }: { g: GuestEntry; owner: boolean; meId: string; onEntries: (e: GuestEntry[]) => void }) {
  const toast = useToast();
  const [replying, setReplying] = useState(false);
  const [reply, setReply] = useState(g.reply ?? "");

  async function sendReply(e: FormEvent) {
    e.preventDefault();
    try {
      const r = await post<{ entries: GuestEntry[] }>(`/guestbook/${g.id}/reply`, { body: reply });
      onEntries(r.entries);
      setReplying(false);
    } catch (err) {
      toast.show(err instanceof ApiError ? err.message : "답글을 달지 못했어요.");
    }
  }

  async function remove() {
    if (!confirm("이 방명록을 지울까요?")) return;
    try {
      const r = await del<{ entries: GuestEntry[] }>(`/guestbook/${g.id}`);
      onEntries(r.entries);
    } catch (err) {
      toast.show(err instanceof ApiError ? err.message : "지우지 못했어요.");
    }
  }

  return (
    <li className="rounded-xl bg-glass p-4">
      <div className="flex items-center gap-2 text-ui">
        <a className="min-w-0 truncate font-semibold underline-offset-2 hover:underline" {...linkTo(`/h/${g.slug}`)}>
          {g.author}
          <span className="font-normal text-dim"> {g.home}</span>
        </a>
        {g.secret && <Icon name="lock" className="size-3.5 shrink-0 text-dim" title="비밀글" />}
        <span className="ml-auto shrink-0 text-meta text-dim">{ago(g.at)}</span>
      </div>
      {g.hidden ? (
        <p className="mt-1.5 text-body text-dim">둥지 주인만 볼 수 있는 글이에요.</p>
      ) : (
        <p className=" mt-1.5 text-read leading-snug break-words whitespace-pre-wrap">{g.body}</p>
      )}
      {g.reply && !replying && (
        <div className="mt-2 flex gap-2 rounded-xl bg-glass px-3 py-2">
          <Icon name="reply" className="mt-0.5 size-4 shrink-0 text-foreground" />
          <p className=" text-read leading-snug break-words">{g.reply}</p>
        </div>
      )}
      {replying && (
        <form onSubmit={sendReply} className="mt-2 flex gap-2">
          <input className="field h-11 min-h-0" value={reply} onChange={(e) => setReply(e.target.value)} maxLength={300} autoFocus aria-label="답글" placeholder="답글 (비우면 지워요)" />
          <button className="btn btn-approve h-11 min-h-0 shrink-0 px-4">달기</button>
        </form>
      )}
      {(owner || g.canDelete) && !replying && (
        <div className="mt-2 flex justify-end gap-1">
          {owner && !g.hidden && (
            <button type="button" className="h-9 rounded-lg px-2.5 text-ui text-dim" onClick={() => setReplying(true)}>
              {g.reply ? "답글 고치기" : "답글"}
            </button>
          )}
          {g.canDelete && (
            <button type="button" className="h-9 rounded-lg px-2.5 text-ui text-reject" onClick={remove}>
              {g.authorId === meId ? "내 글 지우기" : "지우기"}
            </button>
          )}
        </div>
      )}
    </li>
  );
}
