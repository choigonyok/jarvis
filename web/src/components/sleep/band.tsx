import { STAGES, type Night, type Stage, clockAt, duration, stageMinutes, staged } from "@/lib/sleep";
import { cn } from "@/lib/utils";

/**
 * How deep a stretch was, as ink: the deeper the sleep, the more of the
 * page's own ink, and in the night's band the further it hangs from the top
 * edge - the surface it sinks from. Awake and in bed are the bare well. One
 * grey ramp, both themes (the ink is the foreground, dark by day), no new hue:
 * the meals' 배달·집에서·밖에서 are told apart the same way.
 */
const INK: Partial<Record<Stage, string>> = {
  deep: "bg-foreground/85",
  core: "bg-foreground/50",
  rem: "bg-foreground/30",
  asleep: "bg-foreground/45",
};
const HANG: Partial<Record<Stage, string>> = {
  deep: "h-full",
  core: "h-[62%]",
  rem: "h-[36%]",
  asleep: "h-[62%]",
};
/** A night that said only when the bed was: the time in bed, faint - it is not sleep. */
const BED = "bg-foreground/15";

const pct = (v: number) => `${Math.min(100, Math.max(0, v * 100))}%`;

/** A night's length in minutes, from bed to waking. */
const lengthOf = (n: Night) => Math.max(1, (Date.parse(n.wakeAt) - Date.parse(n.bedAt)) / 60_000);

/** What the band shows, read out: the window, then each stage's minutes. */
export function bandLabel(n: Night): string {
  const said = [`${clockAt(n.bedAt)}에 누워 ${clockAt(n.wakeAt)}에 일어났어요`];
  if (n.bedOnly) said.push(`침대에 ${duration(n.asleepMinutes)}`);
  else if (staged(n)) {
    for (const { stage, label } of STAGES) said.push(`${label} ${duration(stageMinutes(n, stage) ?? 0)}`);
    said.push(`깸 ${duration(n.awakeMinutes)}`);
  } else said.push(`${duration(n.asleepMinutes)} 잠`);
  return said.join(", ");
}

/**
 * The night from bed to waking, between the two times: each stretch where it
 * was, hanging as deep as it was. A night that came summed has no stretches -
 * its sleep fills the window evenly, as a sleep whose stage is not known.
 */
export function NightBand({ night }: { night: Night }) {
  const total = lengthOf(night);
  return (
    <div className="flex items-center gap-3">
      <span className="tnum shrink-0 text-ui text-ink">{clockAt(night.bedAt)}</span>
      <div role="img" aria-label={bandLabel(night)} className="relative h-8 min-w-0 flex-1 overflow-hidden rounded-md bg-well">
        {night.bedOnly ? (
          <span aria-hidden className={cn("absolute inset-0", BED)} />
        ) : night.timeline?.length ? (
          night.timeline.map((s) =>
            INK[s.stage] ? (
              <span
                key={s.from}
                aria-hidden
                className={cn("absolute top-0", INK[s.stage], HANG[s.stage])}
                style={{ left: pct(s.from / total), width: pct((s.to - s.from) / total) }}
              />
            ) : null,
          )
        ) : (
          <span aria-hidden className={cn("absolute inset-x-0 top-0", INK.asleep, HANG.asleep)} />
        )}
      </div>
      <span className="tnum shrink-0 text-ui text-ink">{clockAt(night.wakeAt)}</span>
    </div>
  );
}

/**
 * A night in a row of the diary, on the shared clock: the same ink, the full
 * height of the row - too thin for depth to hang.
 */
export function RowBand({ night }: { night: Night }) {
  const total = lengthOf(night);
  if (night.bedOnly) return <span aria-hidden className={cn("absolute inset-0", BED)} />;
  if (!night.timeline?.length) return <span aria-hidden className={cn("absolute inset-0", INK.asleep)} />;
  return (
    <>
      {night.timeline.map((s) =>
        INK[s.stage] ? (
          <span
            key={s.from}
            aria-hidden
            className={cn("absolute inset-y-0", INK[s.stage])}
            style={{ left: pct(s.from / total), width: pct((s.to - s.from) / total) }}
          />
        ) : null,
      )}
    </>
  );
}

/**
 * The stages' minutes under the band, each beside its ink - the key to the
 * band and the night's figures at once. Awake is the bare well's square. A
 * night without stages says how long it was in bed and awake instead.
 */
export function StageLegend({ night, className }: { night: Night; className?: string }) {
  if (night.bedOnly) {
    return (
      <p className={cn("text-aside text-dim", className)}>
        <span aria-hidden className={cn("me-1.5 inline-block size-2.5 rounded-[3px] align-[-1px]", BED)} />
        침대 · 언제 잠들었는지와 단계는 오지 않았어요
      </p>
    );
  }
  const items: { key: string; ink: string; label: string; minutes: number }[] = staged(night)
    ? STAGES.map(({ stage, label }) => ({ key: stage, ink: INK[stage]!, label, minutes: stageMinutes(night, stage) ?? 0 }))
    : [{ key: "asleep", ink: INK.asleep!, label: "잠", minutes: night.asleepMinutes }];
  items.push({ key: "awake", ink: "bg-well ring-1 ring-inset ring-edge", label: "깸", minutes: night.awakeMinutes });
  return (
    <ul className={cn("flex flex-wrap gap-x-4 gap-y-1.5 text-aside text-dim", className)} aria-label="단계">
      {items.map((it) => (
        <li key={it.key} className="flex items-center gap-1.5 whitespace-nowrap">
          <span aria-hidden className={cn("size-2.5 shrink-0 rounded-[3px]", it.ink)} />
          {it.label}
          <span className="tnum text-ink">{duration(it.minutes)}</span>
        </li>
      ))}
    </ul>
  );
}
