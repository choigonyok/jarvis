"use client";

import { useId, useState } from "react";
import { ChevronDown, Plus } from "lucide-react";
import { BlockTitle, useViewOnly } from "@/components/couple/screen";
import { Segment } from "@/components/couple/segment";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { SwitchRow } from "@/components/ui/switch";
import { dMark, milestones, nextOn, today, together, todayIs, writeCouple, type Block, type DDay, type Space } from "@/lib/couple";
import { dateLabel } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * Since when, and what is coming: the day count, every hundredth day and
 * year ahead, and the anniversaries the two keep (a birthday comes round
 * every year). The day they met is changed here too, and whether the home
 * shows the days coming.
 */
export function DDayPage({ block, space, onChanged }: { block: Block; space: Space; onChanged: () => Promise<void> }) {
  const viewOnly = useViewOnly();
  const now = today();
  const start = space.anniversary;
  const [more, setMore] = useState(false);
  const [editing, setEditing] = useState<DDay | "new" | null>(null);
  const [setting, setSetting] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const upcoming = block.config?.showUpcoming === true;
  const [showing, setShowing] = useState(upcoming);
  const [seenUpcoming, setSeenUpcoming] = useState(upcoming);
  if (seenUpcoming !== upcoming) {
    setSeenUpcoming(upcoming);
    setShowing(upcoming);
  }
  async function toggleUpcoming(next: boolean) {
    setShowing(next);
    setNote(null);
    const sent = await writeCouple("PATCH", `/blocks/${block.id}`, { showUpcoming: next });
    if (!sent.ok) {
      setShowing(!next);
      setNote(sent.error);
      return;
    }
    await onChanged();
  }
  const ahead = start ? milestones(start, more ? 12 : 3, now) : [];
  const days = (block.dday ?? [])
    .filter((d) => !d.start)
    .map((d) => ({ ...d, next: nextOn(d.date, d.repeat, now) }))
    .sort((a, b) => (a.next < now) === (b.next < now) ? a.next.localeCompare(b.next) : a.next < now ? 1 : -1);

  return (
    <div>
      <BlockTitle title={block.title} block={block} onRenamed={onChanged} />
      {start ? (
        <div className="mt-4">
          <p className="text-display-lg leading-none font-semibold tracking-tight text-foreground tabular-nums">D+{together(start, now)}</p>
          <p className="mt-2 flex flex-wrap items-center gap-x-1 text-ui text-dim">
            {dateLabel(start)}부터{todayIs(start, now) ? <span className="text-ink"> · 오늘 {todayIs(start, now)}</span> : null}
            {viewOnly ? null : (
              <Button variant="underline" size="inline" onClick={() => setSetting(true)} className="tap ml-1 text-ui">
                바꾸기
              </Button>
            )}
          </p>
        </div>
      ) : (
        <div className="mt-4">
          <p className="text-ui text-dim">만난 날이 아직 없어요.</p>
          {viewOnly ? null : (
            <Button variant="raised" size="touch" onClick={() => setSetting(true)} className="mt-2">
              만난 날 정하기
            </Button>
          )}
        </div>
      )}
      {viewOnly ? null : (
        <SwitchRow
          label="홈에 다가오는 날 보이기"
          hint="우리 화면의 D+ 아래에 가까운 기념일 셋을 둬요"
          checked={showing}
          onCheckedChange={(v) => void toggleUpcoming(v)}
          className="mt-4"
        />
      )}
      {note ? (
        <p role="alert" className="text-aside text-reject">
          {note}
        </p>
      ) : null}

      {ahead.length ? (
        <section className="mt-7" aria-label="다가오는 날">
          <h2 className="text-ui text-dim">다가오는 날</h2>
          <ul className="mt-1 divide-y divide-edge-soft">
            {ahead.map((m) => (
              <Row key={m.key} title={m.label} line={dateLabel(m.date)} mark={dMark(m.date, now)} />
            ))}
          </ul>
          <Button variant="quiet" size="touch-sm" onClick={() => setMore((v) => !v)} className="-ml-2 gap-1" aria-expanded={more}>
            {more ? "접기" : "더 보기"}
            <ChevronDown aria-hidden className={cn("size-4 transition-transform", more && "rotate-180")} />
          </Button>
        </section>
      ) : null}

      <section className="mt-6" aria-label="기념일">
        <div className="flex items-center justify-between">
          <h2 className="text-ui text-dim">기념일</h2>
          {viewOnly ? null : (
            <Button variant="quiet" size="touch-sm" onClick={() => setEditing("new")} className="-mr-2 gap-1">
              <Plus aria-hidden className="size-4" />
              기념일 추가
            </Button>
          )}
        </div>
        {days.length === 0 ? (
          <p className="mt-1 text-ui text-faint">생일이나 처음 여행 간 날을 적어 두면 날을 세 줘요.</p>
        ) : (
          <ul className="mt-1 divide-y divide-edge-soft">
            {days.map((d) => (
              <Row
                key={d.id}
                focus={`dday:${d.id}`}
                title={d.title}
                line={`${dateLabel(d.next)}${d.repeat === "yearly" ? " · 매년" : ""}`}
                mark={dMark(d.next, now)}
                onOpen={viewOnly ? undefined : () => setEditing(d)}
              />
            ))}
          </ul>
        )}
      </section>

      <Panel open={setting} onOpenChange={setSetting} title="만난 날" description="바꾸면 상대의 uniple 앱의 디데이와 캘린더 기념일도 함께 바뀌어요.">
        {setting ? (
          <StartForm
            block={block}
            start={start}
            title={(block.dday ?? []).find((d) => d.start)?.title ?? ""}
            onClose={() => setSetting(false)}
            onSaved={async () => {
              setSetting(false);
              await onChanged();
            }}
          />
        ) : null}
      </Panel>

      <AnniversarySheet
        block={block}
        editing={editing}
        onClose={() => setEditing(null)}
        onSaved={async () => {
          setEditing(null);
          await onChanged();
        }}
      />
    </div>
  );
}

function Row({ title, line, mark, onOpen, focus }: { title: string; line: string; mark: string; onOpen?: () => void; focus?: string }) {
  const body = (
    <>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-body text-foreground">{title}</span>
        <span className="block text-aside text-faint">{line}</span>
      </span>
      <span className={cn("shrink-0 text-body font-semibold tabular-nums", mark === "D-DAY" ? "text-foreground" : "text-ink")}>{mark}</span>
    </>
  );
  return (
    <li data-focus={focus}>
      {onOpen ? (
        <button type="button" onClick={onOpen} className="-mx-2 flex w-[calc(100%+1rem)] items-center gap-3 rounded-lg px-2 py-3 text-left transition-colors hover:bg-glass focus-ring">
          {body}
        </button>
      ) : (
        <div className="flex items-center gap-3 py-3">{body}</div>
      )}
    </li>
  );
}

const DAY_REMINDERS = [
  { value: -1, label: "없음" },
  { value: 0, label: "당일" },
  { value: 1440, label: "하루 전" },
  { value: 4320, label: "3일 전" },
  { value: 10080, label: "1주 전" },
];

/** An anniversary, added or changed: its name, its day, whether it comes every year, a reminder. */
function AnniversarySheet({
  block,
  editing,
  onClose,
  onSaved,
}: {
  block: Block;
  editing: DDay | "new" | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  return (
    <Panel open={editing !== null} onOpenChange={(open) => !open && onClose()} title={editing === "new" ? "기념일 추가" : "기념일 고치기"}>
      {editing ? <AnniversaryForm key={editing === "new" ? "new" : editing.id} block={block} item={editing === "new" ? null : editing} onClose={onClose} onSaved={onSaved} /> : null}
    </Panel>
  );
}

function AnniversaryForm({ block, item, onClose, onSaved }: { block: Block; item: DDay | null; onClose: () => void; onSaved: () => Promise<void> }) {
  const id = useId();
  const [title, setTitle] = useState(item?.title ?? "");
  const [date, setDate] = useState(item?.date ?? today());
  const [repeat, setRepeat] = useState<"none" | "yearly">(item?.repeat === "yearly" ? "yearly" : "none");
  const [reminder, setReminder] = useState(item?.reminder ?? -1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ready = title.trim() !== "" && /^\d{4}-\d{2}-\d{2}$/.test(date);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!ready || busy) return;
    setBusy(true);
    setError(null);
    const body = { title: title.trim(), date, repeat, reminder };
    const sent = item
      ? await writeCouple("PATCH", `/blocks/${block.id}/dday/${item.id}`, body)
      : await writeCouple("POST", `/blocks/${block.id}/dday`, body);
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    await onSaved();
  }
  async function remove() {
    if (!item || busy) return;
    setBusy(true);
    const sent = await writeCouple("DELETE", `/blocks/${block.id}/dday/${item.id}`);
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    await onSaved();
  }

  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      <Field>
        <FieldLabel htmlFor={`${id}-title`}>이름</FieldLabel>
        <Input id={`${id}-title`} surface="glass" value={title} maxLength={40} placeholder="예: 🎂 생일" onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-date`}>날짜</FieldLabel>
        <Input id={`${id}-date`} surface="glass" type="date" required value={date} onChange={(e) => setDate(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel>반복</FieldLabel>
        <Segment
          label="반복"
          value={repeat}
          onChange={setRepeat}
          options={[
            { value: "none", label: "안 함" },
            { value: "yearly", label: "매년" },
          ]}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-reminder`}>알림</FieldLabel>
        <NativeSelect id={`${id}-reminder`} value={String(reminder)} onChange={(e) => setReminder(Number(e.target.value))}>
          {DAY_REMINDERS.map((r) => (
            <NativeSelectOption key={r.value} value={String(r.value)}>
              {r.label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </Field>
      {error ? (
        <p role="alert" className="text-aside text-reject">
          {error}
        </p>
      ) : null}
      <div className="flex gap-2 pt-1">
        {item ? (
          <Button type="button" variant="discard" size="touch-sm" onClick={() => void remove()} disabled={busy}>
            지우기
          </Button>
        ) : (
          <Button type="button" variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
            두기
          </Button>
        )}
        <Button type="submit" variant="raised" size="touch" disabled={!ready || busy} className="flex-1">
          {busy ? "저장하는 중…" : "저장"}
        </Button>
      </div>
    </form>
  );
}

/** The day they met, and what it is called. */
function StartForm({
  block,
  start,
  title,
  onClose,
  onSaved,
}: {
  block: Block;
  start?: string;
  title: string;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const id = useId();
  const [name, setName] = useState(title);
  const [date, setDate] = useState(start ?? today());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ready = /^\d{4}-\d{2}-\d{2}$/.test(date) && date <= today() && (date !== start || name.trim() !== title);
  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!ready || busy) return;
    setBusy(true);
    setError(null);
    const sent = await writeCouple("POST", `/blocks/${block.id}/start`, { title: name.trim(), date });
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    await onSaved();
  }
  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      <Field>
        <FieldLabel htmlFor={`${id}-name`}>이름</FieldLabel>
        <Input id={`${id}-name`} surface="glass" value={name} maxLength={40} placeholder="예: 우리가 만난 날" onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-date`}>날짜 · 그날이 D+1</FieldLabel>
        <Input id={`${id}-date`} surface="glass" type="date" max={today()} required value={date} onChange={(e) => setDate(e.target.value)} />
      </Field>
      {error ? (
        <p role="alert" className="text-aside text-reject">
          {error}
        </p>
      ) : null}
      <div className="flex gap-2 pt-1">
        <Button type="button" variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
          두기
        </Button>
        <Button type="submit" variant="raised" size="touch" disabled={!ready || busy} className="flex-1">
          {busy ? "저장하는 중…" : "저장"}
        </Button>
      </div>
    </form>
  );
}
