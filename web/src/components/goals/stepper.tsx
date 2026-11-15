import { Minus, Plus } from "lucide-react";

/**
 * −, the number, +: how many times a goal asks, the minutes a route's walk
 * takes. One part for both - the goal form's had no edge and the route
 * editor's was 36px, so the same thing looked like two apps. An edge, as what
 * a hand touches has, and a thumb's 44px on a phone; a pointer's only under a
 * mouse.
 */
export function Stepper({
  label,
  name = label,
  value,
  min,
  max,
  unit,
  onChange,
}: {
  /** The words before it: "정류장까지", "한 주에". */
  label: string;
  /** What its buttons change, where the words before it do not say: "하루 횟수". */
  name?: string;
  value: number;
  min: number;
  max: number;
  unit: string;
  onChange: (value: number) => void;
}) {
  const btn =
    "flex size-11 shrink-0 items-center justify-center rounded-full border border-edge text-dim hover:text-foreground focus-ring disabled:opacity-40 mouse:size-8";
  return (
    <div className="flex items-center gap-2.5">
      <span className="text-ui text-dim">{label}</span>
      <button type="button" aria-label={`${name} 줄이기`} disabled={value <= min} onClick={() => onChange(value - 1)} className={btn}>
        <Minus aria-hidden className="size-3.5" strokeWidth={2} />
      </button>
      <span aria-live="polite" className="tnum w-10 text-center text-title text-foreground">
        {value}
        {unit}
      </span>
      <button type="button" aria-label={`${name} 늘리기`} disabled={value >= max} onClick={() => onChange(value + 1)} className={btn}>
        <Plus aria-hidden className="size-3.5" strokeWidth={2} />
      </button>
    </div>
  );
}
