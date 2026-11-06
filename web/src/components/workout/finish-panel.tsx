"use client";

import { Flame } from "lucide-react";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import {
  clock,
  doneSets,
  kg,
  load,
  sessionRecords,
  sessionVolume,
  signedKg,
  type Session,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * Finishing, as a look back rather than a button.
 *
 * The summary is what you would have opened the history to check, and it
 * comes with the decision so a mis-tap on 끝내기 costs a second, not a
 * session. Unticked sets are named before they are dropped.
 */
export function FinishPanel({
  open,
  onOpenChange,
  session,
  history,
  elapsed,
  onEnd,
  onDiscard,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  session: Session;
  history: Session[];
  elapsed: number;
  onEnd: () => void;
  onDiscard: () => void;
}) {
  const sets = doneSets(session);
  const volume = sessionVolume(session);
  const open_ = session.exercises.reduce(
    (n, e) => n + e.sets.filter((s) => !s.done).length,
    0,
  );
  const anything = session.exercises.some((e) => e.sets.some((s) => s.done));
  const records = sessionRecords(session, history);
  const lastSame = session.routineName
    ? history
        .filter(
          (s) =>
            s.id !== session.id &&
            s.endedAt &&
            (s.routineId === session.routineId ||
              s.routineName === session.routineName),
        )
        .sort((a, b) => b.startedAt - a.startedAt)[0]
    : undefined;
  const delta = lastSame ? volume - sessionVolume(lastSame) : null;

  return (
    <Panel open={open} onOpenChange={onOpenChange} title="운동 끝내기">
      <dl className="mb-4 grid grid-cols-3 gap-3">
        {[
          ["시간", clock(elapsed)],
          ["볼륨", kg(volume)],
          ["세트", `${sets}`],
        ].map(([label, value]) => (
          <div key={label}>
            <dt className="text-meta text-faint">{label}</dt>
            <dd className="font-score tnum text-[30px] leading-tight font-semibold text-foreground">
              {value}
            </dd>
          </div>
        ))}
      </dl>

      {delta !== null && anything ? (
        <p className="mb-3 text-ui text-dim">
          지난 {session.routineName}보다{" "}
          <span
            className={cn(
              "tnum",
              delta > 0 ? "text-approve" : "text-ink",
            )}
          >
            {delta === 0 ? "같은 볼륨" : signedKg(delta)}
          </span>
        </p>
      ) : null}

      {records.length > 0 ? (
        <ul className="mb-4 space-y-1.5">
          {records.map((r) => (
            <li key={r.name} className="flex items-center gap-2 text-ui">
              <Flame aria-hidden className="size-3.5 shrink-0 text-approve" />
              <span className="min-w-0 flex-1 truncate text-ink">
                {r.name}
              </span>
              <span className="tnum shrink-0 text-faint">
                {r.kind === "무게" ? "최고 무게" : "추정 1RM"}{" "}
                {load(Math.round(r.before * 10) / 10)} →{" "}
                <span className="text-approve">
                  {load(Math.round(r.value * 10) / 10)}kg
                </span>
              </span>
            </li>
          ))}
        </ul>
      ) : null}

      {!anything ? (
        <p className="mb-4 text-ui leading-relaxed text-dim">
          체크한 세트가 없어서 남길 기록이 없어요.
        </p>
      ) : open_ > 0 ? (
        <p className="mb-4 text-ui leading-relaxed text-dim">
          체크하지 않은 세트 {open_}개는 기록에서 빠져요.
        </p>
      ) : null}

      <div className="flex gap-2">
        <Button variant="soft" size="touch-sm" onClick={() => onOpenChange(false)} className="flex-1">
          계속하기
        </Button>
        {anything ? (
          <Button variant="raised" size="touch" onClick={onEnd} className="flex-1">
            기록하고 끝내기
          </Button>
        ) : (
          <Button variant="raised" size="touch" onClick={onDiscard} className="flex-1">
            운동 취소
          </Button>
        )}
      </div>
      {anything ? (
        <div className="mt-2 flex justify-center">
          <Button variant="discard" size="touch-sm" onClick={onDiscard}>
            기록 버리기
          </Button>
        </div>
      ) : null}
    </Panel>
  );
}
