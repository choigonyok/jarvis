// 커플 일기: 하루에 한 사람이 한 편, 같은 날의 두 편을 나란히. 둘만 본다.

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { MOOD_NAMES, MOODS, type DiaryDay, type DiaryMonth, type HomeView, type Me, type Mood, type Reward } from "../../shared/api";
import { BatteryLow, Cloud, CloudRain, Heart, type LucideIcon, Snowflake, Sun } from "lucide-react";
import { ApiError, del, get, post, put } from "../lib/api";
import { stamp, todayKey } from "../lib/time";
import { useToast } from "../lib/toast";
import { Icon } from "../ui/icons";

const WEEK = ["일", "월", "화", "수", "목", "금", "토"];

const MOOD_ICON: Record<Mood, LucideIcon> = {
  sun: Sun,
  cloud: Cloud,
  rain: CloudRain,
  snow: Snowflake,
  heart: Heart,
  tired: BatteryLow,
};

/** 그날의 날씨·기분: 색 없는 선 아이콘(콘솔의 아이콘과 같은 굵기) */
function MoodMark({ mood, className = "size-5" }: { mood: string; className?: string }) {
  const m = (MOODS as readonly string[]).includes(mood) ? (mood as Mood) : "sun";
  const C = MOOD_ICON[m];
  return <C className={className} strokeWidth={1.8} role="img" aria-label={MOOD_NAMES[m]} />;
}

export function DiaryPanel({ me, home, stamp: changed, onRewards }: { me: Me; home: HomeView; stamp: number; onRewards: (r: Reward[]) => void }) {
  const today = todayKey();
  const [month, setMonth] = useState(today.slice(0, 7));
  const [data, setData] = useState<DiaryMonth | null>(null);
  const [day, setDay] = useState(today);

  const loadMonth = useCallback(() => {
    get<DiaryMonth>(`/diary?month=${month}`)
      .then(setData)
      .catch(() => setData({ month, days: {} }));
  }, [month]);

  useEffect(() => {
    loadMonth();
  }, [loadMonth, changed]);

  const [y, m] = month.split("-").map(Number);
  const first = new Date(Date.UTC(y, m - 1, 1)).getUTCDay();
  const days = new Date(Date.UTC(y, m, 0)).getUTCDate();
  const shift = (n: number) => {
    const d = new Date(Date.UTC(y, m - 1 + n, 1));
    setMonth(`${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}`);
  };
  const partner = home.members.find((x) => x.id !== me.id);

  return (
    <div className="space-y-4">
      <div className="rounded-xl bg-glass p-4">
        <div className="mb-3 flex items-center justify-between">
          <button type="button" className="icon-btn" onClick={() => shift(-1)} aria-label="지난달">
            <Icon name="back" />
          </button>
          <h2 className="text-title font-semibold">
            {y}년 {m}월 일기
          </h2>
          <button type="button" className="icon-btn" onClick={() => shift(1)} disabled={month >= today.slice(0, 7)} aria-label="다음 달">
            <Icon name="forward" />
          </button>
        </div>
        <div className="grid grid-cols-7 gap-1 text-center text-meta text-dim" aria-hidden>
          {WEEK.map((w) => (
            <span key={w}>{w}</span>
          ))}
        </div>
        <div className="mt-1 grid grid-cols-7 gap-1" role="grid" aria-label="날짜 고르기">
          {Array.from({ length: first }, (_, i) => (
            <span key={`b${i}`} />
          ))}
          {Array.from({ length: days }, (_, i) => {
            const key = `${month}-${String(i + 1).padStart(2, "0")}`;
            const info = data?.days[key];
            const mine = info?.authors.includes(me.id);
            const theirs = partner && info?.authors.includes(partner.id);
            const future = key > today;
            return (
              <button
                key={key}
                type="button"
                disabled={future}
                onClick={() => setDay(key)}
                aria-pressed={day === key}
                aria-label={`${m}월 ${i + 1}일${mine ? ", 내 일기 있음" : ""}${theirs ? `, ${partner?.name} 일기 있음` : ""}`}
                className={`focus-ring tnum flex aspect-square max-h-14 w-full flex-col items-center justify-center justify-self-center rounded-lg text-ui sm:aspect-auto sm:h-14 ${future ? "text-faint" : day === key ? "bg-glass-raised text-foreground" : "text-ink hover:bg-glass"}`}
              >
                {/* 오늘은 숫자의 가는 링, 고른 날은 유리 한 칸(콘솔의 달력과 같다) */}
                <span className={`grid size-7 place-items-center rounded-full ${key === today ? "font-semibold ring-1 ring-foreground/60" : ""}`}>{i + 1}</span>
                <span className="mt-0.5 flex h-1.5 gap-0.5">
                  {mine && <span className="size-1.5 rounded-full bg-mine" />}
                  {theirs && <span className="size-1.5 rounded-full bg-partner" />}
                </span>
              </button>
            );
          })}
        </div>
        <p className="mt-3 flex items-center gap-3 text-meta text-dim">
          <span className="inline-flex items-center gap-1">
            <span className="size-2 rounded-full bg-mine" /> 나
          </span>
          {partner && (
            <span className="inline-flex items-center gap-1">
              <span className="size-2 rounded-full bg-partner" /> {partner.name}
            </span>
          )}
        </p>
      </div>
      <DayView key={day} day={day} me={me} home={home} changed={changed} onSaved={loadMonth} onRewards={onRewards} />
    </div>
  );
}

function DayView({ day, me, home, changed, onSaved, onRewards }: { day: string; me: Me; home: HomeView; changed: number; onSaved: () => void; onRewards: (r: Reward[]) => void }) {
  const toast = useToast();
  const [data, setData] = useState<DiaryDay | null>(null);
  const [editing, setEditing] = useState(false);
  const [mood, setMood] = useState<Mood>("sun");
  const [body, setBody] = useState("");
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    get<DiaryDay>(`/diary/${day}`)
      .then((d) => {
        setData(d);
        const mine = d.entries.find((e) => e.authorId === me.id);
        if (!mine) setEditing(true);
      })
      .catch(() => setData({ day, entries: [], comments: [] }));
  }, [day, me.id, changed]);

  if (!data) return <div className="h-40 rounded-xl bg-glass" aria-busy="true" />;
  const mine = data.entries.find((e) => e.authorId === me.id);
  const partner = home.members.find((x) => x.id !== me.id);
  const theirs = partner ? data.entries.find((e) => e.authorId === partner.id) : undefined;
  const label = new Date(`${day}T00:00:00+09:00`).toLocaleDateString("ko-KR", { month: "long", day: "numeric", weekday: "long", timeZone: "Asia/Seoul" });

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await put<DiaryDay & { rewards: Reward[] }>(`/diary/${day}`, { mood, body });
      setData(r);
      setEditing(false);
      onRewards(r.rewards);
      onSaved();
    } catch (err) {
      toast.show(err instanceof ApiError ? err.message : "저장하지 못했어요.");
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!confirm("내 일기를 지울까요?")) return;
    const r = await del<DiaryDay>(`/diary/${day}`).catch(() => null);
    if (r) {
      setData(r);
      setBody("");
      setEditing(true);
      onSaved();
    }
  }

  async function addComment(e: FormEvent) {
    e.preventDefault();
    if (!comment.trim()) return;
    try {
      setData(await post<DiaryDay>(`/diary/${day}/comments`, { body: comment }));
      setComment("");
      onSaved();
    } catch (err) {
      toast.show(err instanceof ApiError ? err.message : "댓글을 달지 못했어요.");
    }
  }

  return (
    <article className="space-y-3" aria-label={`${label}의 일기`}>
      <h3 className="text-title font-semibold">{label}</h3>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2">
        {/* 내 쪽 */}
        <section className="rounded-xl bg-glass p-4" aria-label="내 일기">
          <div className="mb-2 flex items-center gap-2">
            <span className="size-2 rounded-full bg-mine" />
            <b className="flex-1 text-body">{me.name}</b>
            {mine && !editing && (
              <>
                <button type="button" className="h-9 rounded-lg px-2 text-ui text-dim" onClick={() => (setMood(mine.mood as Mood), setBody(mine.body), setEditing(true))}>
                  고치기
                </button>
                <button type="button" className="h-9 rounded-lg px-2 text-ui text-reject" onClick={remove}>
                  지우기
                </button>
              </>
            )}
          </div>
          {editing ? (
            <form onSubmit={save} className="space-y-3">
              <fieldset>
                <legend className="sr-only">오늘의 날씨</legend>
                <div className="flex flex-wrap gap-1.5">
                  {MOODS.map((md) => (
                    <label key={md} className={`flex h-10 cursor-pointer items-center gap-1 rounded-full border px-3 text-aside has-[:focus-visible]:ring-3 has-[:focus-visible]:ring-ring/50 ${mood === md ? "border-foreground/30 bg-glass-raised font-medium text-foreground" : "border-edge text-dim"}`}>
                      <input type="radio" name="mood" value={md} checked={mood === md} onChange={() => setMood(md)} className="sr-only" />
                      <MoodMark mood={md} />
                      {MOOD_NAMES[md]}
                    </label>
                  ))}
                </div>
              </fieldset>
              <textarea className="field  text-read leading-snug" value={body} onChange={(e) => setBody(e.target.value)} maxLength={2000} placeholder="오늘 둘이 있었던 일" aria-label="일기" required />
              <div className="flex gap-2">
                {mine && (
                  <button type="button" className="btn btn-raised flex-1" onClick={() => setEditing(false)}>
                    그만두기
                  </button>
                )}
                <button className="btn btn-approve flex-1" disabled={busy || !body.trim()}>
                  {busy ? "저장하는 중" : "일기 저장"}
                </button>
              </div>
            </form>
          ) : mine ? (
            <>
              <MoodMark mood={mine.mood} className="size-7" />
              <p className=" mt-1 text-read leading-snug whitespace-pre-wrap">{mine.body}</p>
              <p className="mt-2 text-meta text-dim">{stamp(mine.updatedAt)}</p>
            </>
          ) : null}
        </section>

        {/* 상대 쪽 */}
        <section className="rounded-xl bg-glass p-4" aria-label={partner ? `${partner.name}의 일기` : "상대의 일기"}>
          <div className="mb-2 flex items-center gap-2">
            <span className="size-2 rounded-full bg-partner" />
            <b className="text-body">{partner?.name ?? "아직 혼자예요"}</b>
          </div>
          {theirs ? (
            <>
              <MoodMark mood={theirs.mood} className="size-7" />
              <p className=" mt-1 text-read leading-snug whitespace-pre-wrap">{theirs.body}</p>
              <p className="mt-2 text-meta text-dim">{stamp(theirs.updatedAt)}</p>
            </>
          ) : (
            <p className="text-body text-dim">{partner ? `${partner.name}님은 이날 일기를 아직 안 썼어요.` : "설정에서 초대 코드를 보내 상대를 불러 주세요."}</p>
          )}
        </section>
      </div>

      {(data.entries.length > 0 || data.comments.length > 0) && (
        <section className="rounded-xl bg-glass p-4" aria-label="댓글">
          <h4 className="mb-2 text-body font-semibold">댓글 {data.comments.length}</h4>
          <ul className="space-y-2">
            {data.comments.map((c) => (
              <li key={c.id} className="flex items-start gap-2 text-body">
                <b className="shrink-0">{c.author}</b>
                <span className="min-w-0 flex-1 break-words">{c.body}</span>
                {c.authorId === me.id && (
                  <button
                    type="button"
                    className="shrink-0 text-dim"
                    aria-label="댓글 지우기"
                    onClick={async () => {
                      const r = await del<DiaryDay>(`/diary/comments/${c.id}`).catch(() => null);
                      if (r) setData(r);
                    }}
                  >
                    <Icon name="trash" className="size-4" />
                  </button>
                )}
              </li>
            ))}
          </ul>
          <form onSubmit={addComment} className="mt-3 flex gap-2">
            <input className="field h-11 min-h-0" value={comment} onChange={(e) => setComment(e.target.value)} maxLength={300} placeholder="한마디 남기기" aria-label="댓글" />
            <button className="btn btn-approve h-11 min-h-0 shrink-0 px-4" disabled={!comment.trim()}>
              달기
            </button>
          </form>
        </section>
      )}
    </article>
  );
}
