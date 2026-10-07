"use client";

import { useState } from "react";
import { X } from "lucide-react";
import {
  type FixedAsset,
  type Flow,
  type Principal,
  type PrincipalPart,
  PART_LABEL,
  VENUE_LABEL,
  krw,
  percent,
  signedKrw,
} from "@/lib/portfolio";
import { cn } from "@/lib/utils";

const signed = (v: number) => `${v > 0 ? "+" : "−"}${krw(Math.abs(v))}`;

/**
 * The money that went in and came out, which is what the headline return is
 * measured against.
 *
 * Upbit rows arrive on their own; KIS has no API for its transfers, so those
 * - and whatever was already held on the start day - are written down here.
 * Only hand-written rows can be removed: an Upbit row that looks wrong is a
 * question for Upbit, not something to delete.
 *
 * In and out are not gain and loss, so the amounts stay out of green and red.
 */
export function PrincipalLedger({
  principal,
  onChange,
}: {
  principal: Principal;
  onChange: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [all, setAll] = useState(false);
  // Newest first, and only the recent few until asked: the headline already
  // carries the sum, and a season of transfers is a long scroll on a phone.
  const RECENT = 6;
  const newest = [...principal.flows].reverse();
  const flows = all ? newest : newest.slice(0, RECENT);

  const remove = async (f: Flow) => {
    if (!window.confirm(`${f.date} ${signed(f.amountKrw)} 기록을 지울까요?`)) return;
    const res = await fetch(`/api/principal?id=${f.id.replace("manual:", "")}`, {
      method: "DELETE",
    });
    if (res.ok) onChange();
  };

  return (
    <div>
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <p className="text-[13px] text-dim">원금 기록</p>
        <p className="text-[11.5px] text-faint">
          {principal.since.slice(5).replace("-", "/")}부터 · 합계{" "}
          <span className="tnum text-dim">
            {/* The rows below, not the headline principal: that one also
                carries the fixed amounts, which have no rows here. */}
            {krw(principal.flows.reduce((sum, f) => sum + f.amountKrw, 0))}
          </span>
        </p>
      </div>

      <ul className="space-y-px">
        {flows.map((f) => (
          <li
            key={f.id}
            className="flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-glass"
          >
            <span className="tnum w-10 shrink-0 text-[12px] text-faint">
              {f.date.slice(5).replace("-", "/")}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] text-foreground/85">
                {f.memo || (f.amountKrw > 0 ? "입금" : "출금")}
              </span>
              <span className="text-[11px] text-faint">
                {VENUE_LABEL[f.venue]}
                {f.source === "auto" ? " · 자동" : ""}
              </span>
            </span>
            <span
              className={cn(
                "tnum shrink-0 text-[13px]",
                f.amountKrw > 0 ? "text-foreground/85" : "text-dim",
              )}
            >
              {signed(f.amountKrw)}
            </span>
            {f.source === "manual" ? (
              <button
                type="button"
                onClick={() => void remove(f)}
                aria-label={`${f.date} 기록 지우기`}
                className="tap -me-1 flex shrink-0 items-center justify-center rounded-md text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:size-7 sm:min-h-0 sm:min-w-0"
              >
                <X aria-hidden className="size-3.5" />
              </button>
            ) : (
              <span aria-hidden className="w-7 shrink-0 max-sm:w-11" />
            )}
          </li>
        ))}
      </ul>

      {newest.length > RECENT ? (
        <button
          type="button"
          onClick={() => setAll((v) => !v)}
          className="mt-1 min-h-10 w-full rounded-lg text-[12px] text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-8"
        >
          {all ? "최근 것만 보기" : `전체 ${newest.length}건 보기`}
        </button>
      ) : null}

      {open ? (
        <AddFlow
          since={principal.since}
          onDone={(saved) => {
            setOpen(false);
            if (saved) onChange();
          }}
        />
      ) : (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="mt-3 min-h-11 w-full rounded-lg border border-dashed border-edge-soft text-[13px] text-dim transition-colors outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-9"
        >
          입출금 기록하기
        </button>
      )}
    </div>
  );
}

const field =
  "h-11 w-full rounded-lg border border-edge-soft bg-glass px-3 text-[16px] text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-9 sm:text-[13px]";

function AddFlow({
  since,
  onDone,
}: {
  since: string;
  onDone: (saved: boolean) => void;
}) {
  const today = new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
  const [direction, setDirection] = useState<"in" | "out">("in");
  const [venue, setVenue] = useState<Flow["venue"]>("kis");
  const [date, setDate] = useState(today);
  const [amount, setAmount] = useState("");
  const [memo, setMemo] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const value = Number(amount.replace(/[^\d]/g, ""));

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!value) {
      setError("금액을 입력하세요.");
      return;
    }
    setSaving(true);
    const res = await fetch("/api/principal", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        date,
        venue,
        amountKrw: direction === "in" ? value : -value,
        memo: memo.trim(),
      }),
    }).catch(() => null);
    setSaving(false);
    if (!res?.ok) {
      const body = (await res?.json().catch(() => null)) as { error?: string } | null;
      setError(body?.error ?? "기록하지 못했습니다.");
      return;
    }
    onDone(true);
  };

  return (
    <form
      onSubmit={(e) => void save(e)}
      className="mt-3 space-y-2.5 rounded-xl border border-edge-soft p-3"
    >
      <div className="grid grid-cols-2 gap-2">
        <div
          role="radiogroup"
          aria-label="방향"
          className="inline-flex rounded-lg border border-edge-soft bg-glass p-0.5"
        >
          {(["in", "out"] as const).map((d) => (
            <button
              key={d}
              type="button"
              role="radio"
              aria-checked={direction === d}
              onClick={() => setDirection(d)}
              className={cn(
                "min-h-10 flex-1 rounded-md text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-8",
                direction === d ? "bg-glass-raised text-foreground" : "text-faint",
              )}
            >
              {d === "in" ? "넣음" : "뺌"}
            </button>
          ))}
        </div>
        <select
          aria-label="계좌"
          value={venue}
          onChange={(e) => setVenue(e.target.value as Flow["venue"])}
          className={field}
        >
          {(Object.keys(VENUE_LABEL) as Flow["venue"][]).map((v) => (
            <option key={v} value={v}>
              {VENUE_LABEL[v]}
            </option>
          ))}
        </select>
      </div>

      <div className="grid grid-cols-2 gap-2">
        <input
          type="date"
          aria-label="날짜"
          value={date}
          min={since}
          max={today}
          onChange={(e) => setDate(e.target.value)}
          className={field}
        />
        <input
          inputMode="numeric"
          aria-label="금액(원)"
          placeholder="금액(원)"
          value={value ? value.toLocaleString("ko-KR") : amount}
          onChange={(e) => {
            setAmount(e.target.value);
            setError(null);
          }}
          className={cn(field, "tnum text-right")}
        />
      </div>

      <input
        aria-label="메모"
        placeholder="메모 (선택)"
        value={memo}
        maxLength={100}
        onChange={(e) => setMemo(e.target.value)}
        className={field}
      />

      {error ? (
        <p role="status" className="text-[12px] text-reject/85">
          {error}
        </p>
      ) : null}

      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => onDone(false)}
          className="min-h-11 flex-1 rounded-lg text-[13px] text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-9"
        >
          취소
        </button>
        <button
          type="submit"
          disabled={saving}
          className="min-h-11 flex-1 rounded-lg bg-foreground text-[13px] font-medium text-background transition-opacity outline-none focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 sm:min-h-9"
        >
          {saving ? "기록하는 중" : "기록하기"}
        </button>
      </div>
    </form>
  );
}

/**
 * The headline return, split by account: shares, coins, gold. Rows rather
 * than another boxed grid - the period band right below is already one, and
 * two grids stacked would read as the same kind of fact.
 */
export function PrincipalParts({
  parts,
  fixed,
}: {
  parts: PrincipalPart[];
  fixed: FixedAsset[];
}) {
  return (
    <ul className="mt-4 divide-y divide-edge-soft border-y border-edge-soft">
      {parts.map((p) => {
        const known = p.valueKrw !== null && p.profitKrw !== null;
        const up = (p.profitKrw ?? 0) >= 0;
        return (
          <li key={p.venue} className="flex items-center gap-3 py-2.5">
            <span className="w-14 shrink-0 text-[13px] text-dim">
              {PART_LABEL[p.venue]}
            </span>
            <span className="min-w-0 flex-1">
              <span className="tnum block text-[14px] leading-tight text-foreground/90">
                {known ? krw(p.valueKrw!) : "—"}
              </span>
              <span className="tnum mt-0.5 block text-[11px] text-faint">
                {p.principalKrw !== 0 ? `원금 ${krw(p.principalKrw)}` : "원금 기록 없음"}
              </span>
            </span>
            {known ? (
              <span className="tnum shrink-0 text-right">
                <span
                  className={cn(
                    "block text-[13px] leading-tight",
                    up ? "text-approve/80" : "text-reject/80",
                  )}
                >
                  {signedKrw(p.profitKrw!)}
                </span>
                <span className="mt-0.5 block text-[11.5px] text-dim">
                  {p.rate === null ? "—" : percent(p.rate)}
                </span>
              </span>
            ) : (
              <span className="shrink-0 text-[12px] text-faint">계좌 연결 전</span>
            )}
          </li>
        );
      })}
      {/* Fixed amounts: in the total and the principal, out of the
          allocation, and never up or down - so no return column. */}
      {fixed.map((f) => (
        <li key={f.id} className="flex items-center gap-3 py-2.5">
          <span className="w-14 shrink-0 text-[13px] text-dim">{f.label}</span>
          <span className="tnum min-w-0 flex-1 text-[14px] text-foreground/90">
            {krw(f.valueKrw)}
          </span>
          <span className="shrink-0 text-[12px] text-faint">고정 · 비중 제외</span>
        </li>
      ))}
    </ul>
  );
}
