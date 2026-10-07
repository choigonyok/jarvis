"use client";

import { useState } from "react";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { krw } from "@/lib/portfolio";
import {
  type Book,
  SITE_LABEL,
  type Item,
  type Tx,
  type Unparsed,
  cardLabel,
  clock,
  dayKey,
  dayLabel,
  send,
} from "@/lib/spending";
import { cn } from "@/lib/utils";

const field =
  "h-11 w-full rounded-lg border border-edge-soft bg-glass px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-9 sm:text-[13px]";

const quietButton =
  "tap rounded-lg px-3 text-[13px] text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50";

/** The one bright control in a sheet is the one that saves - same as the approval card. */
const saveButton =
  "tap min-h-11 flex-1 rounded-lg bg-glass-raised px-4 text-[14px] font-medium text-foreground transition-colors outline-none hover:bg-foreground/15 focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 sm:min-h-9 sm:text-[13px]";

const digits = (s: string) => Number(s.replace(/[^\d]/g, "")) || 0;
const grouped = (s: string) => {
  const n = digits(s);
  return n ? n.toLocaleString("ko-KR") : "";
};

/** A bottom sheet on a phone; a centred one on a wide screen rather than a 1400px-wide strip. */
function Panel({
  open,
  onOpenChange,
  title,
  description,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        // Opening a sheet should not throw up the keyboard or a date picker
        // before anything has been read.
        onOpenAutoFocus={(e) => e.preventDefault()}
        className="inset-x-safe pb-safe max-h-[88dvh] gap-0 border-edge-soft bg-background sm:mx-auto sm:max-w-lg sm:rounded-t-2xl sm:border-x"
      >
        <SheetHeader className="px-4 pt-4 pb-2 sm:px-5">
          <SheetTitle className="pr-8 text-[15px] font-medium">{title}</SheetTitle>
          {description ? (
            <SheetDescription className="text-[12px] text-faint">{description}</SheetDescription>
          ) : null}
        </SheetHeader>
        <div className="scrollbar-hairline min-h-0 overflow-y-auto px-4 pb-4 sm:px-5">{children}</div>
      </SheetContent>
    </Sheet>
  );
}

function CategoryPicker({
  names,
  value,
  onChange,
  allowAuto,
}: {
  names: string[];
  value: string;
  onChange: (v: string) => void;
  allowAuto?: boolean;
}) {
  const options = allowAuto ? ["", ...names] : names;
  return (
    <div role="radiogroup" aria-label="분류" className="grid grid-cols-3 gap-1.5 sm:grid-cols-4">
      {options.map((name) => {
        const on = value === name;
        return (
          <button
            key={name || "auto"}
            type="button"
            role="radio"
            aria-checked={on}
            onClick={() => onChange(name)}
            className={cn(
              "min-h-11 rounded-lg border px-2 text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-9 sm:text-[12.5px]",
              on
                ? "border-edge bg-glass-raised text-foreground"
                : "border-edge-soft text-dim hover:bg-glass",
            )}
          >
            {name || "자동"}
          </button>
        );
      })}
    </div>
  );
}

function Toggle({
  checked,
  onChange,
  label,
  hint,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint?: string;
}) {
  return (
    <label className="flex min-h-11 cursor-pointer items-center justify-between gap-3 py-1">
      <span>
        <span className="block text-[13.5px] text-foreground/90">{label}</span>
        {hint ? <span className="block text-[11.5px] text-faint">{hint}</span> : null}
      </span>
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="peer sr-only"
      />
      <span
        aria-hidden
        className={cn(
          "relative h-6 w-10 shrink-0 rounded-full transition-colors peer-focus-visible:ring-3 peer-focus-visible:ring-ring/50",
          checked ? "bg-foreground/80" : "bg-glass-raised",
        )}
      >
        <span
          className={cn(
            "absolute top-0.5 size-5 rounded-full bg-background transition-transform motion-reduce:transition-none",
            checked ? "translate-x-[18px]" : "translate-x-0.5",
          )}
        />
      </span>
    </label>
  );
}

// ── 내역 하나 ────────────────────────────────────────────────────────────

export function TxSheet({
  tx,
  names,
  onClose,
  onSaved,
}: {
  tx: Tx | null;
  names: string[];
  onClose: () => void;
  onSaved: (note: string) => void;
}) {
  return (
    <Panel
      open={tx !== null}
      onOpenChange={(o) => !o && onClose()}
      title={tx?.merchant ?? ""}
      description={
        tx
          ? `${dayLabel(dayKey(tx.approvedAt))} ${clock(tx.approvedAt)} · ${cardLabel(tx)}`
          : undefined
      }
    >
      {/* Keyed by id so the form starts from this charge, not the last one opened. */}
      {tx ? <TxForm key={tx.id} tx={tx} names={names} onSaved={onSaved} onClose={onClose} /> : null}
    </Panel>
  );
}

function TxForm({
  tx,
  names,
  onSaved,
  onClose,
}: {
  tx: Tx;
  names: string[];
  onSaved: (note: string) => void;
  onClose: () => void;
}) {
  const [category, setCategory] = useState(tx.category);
  const [remember, setRemember] = useState(true);
  const [memo, setMemo] = useState(tx.memo);
  const [excluded, setExcluded] = useState(tx.excluded);
  const [merchant, setMerchant] = useState(tx.merchant);
  const [amount, setAmount] = useState(tx.amountKrw.toLocaleString("ko-KR"));
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // A won figure can be corrected where it was a guess (a dollar charge) or
  // where a person wrote it. One read off an alert is what the card company
  // said; changing it would only make the book disagree with the statement.
  const amountEditable = tx.source === "manual" || tx.estimated;
  const recategorised = category !== tx.category;

  const save = async () => {
    const patch: Record<string, unknown> = {};
    if (recategorised) {
      patch.category = category;
      patch.remember = remember;
    }
    if (memo.trim() !== tx.memo) patch.memo = memo.trim();
    if (excluded !== tx.excluded) patch.excluded = excluded;
    if (tx.source === "manual" && merchant.trim() !== tx.merchant) patch.merchant = merchant.trim();
    if (amountEditable && digits(amount) !== tx.amountKrw) patch.amountKrw = digits(amount);
    if (Object.keys(patch).length === 0) {
      onClose();
      return;
    }
    setSaving(true);
    const err = await send(`transactions/${tx.id}`, "PATCH", patch);
    setSaving(false);
    if (err) {
      setError(err);
      return;
    }
    onSaved(
      recategorised && remember
        ? `${tx.merchant}의 다른 내역도 ${category}로 바꿨어요`
        : "저장했어요",
    );
  };

  const remove = async () => {
    if (!window.confirm(`${tx.merchant} ${krw(tx.amountKrw)}을 지울까요?`)) return;
    const err = await send(`transactions/${tx.id}`, "DELETE");
    if (err) setError(err);
    else onSaved("지웠어요");
  };

  return (
    <div className="space-y-5">
      <p className="tnum text-[24px] leading-none font-semibold tracking-tight text-foreground">
        {tx.estimated ? "약 " : ""}
        {krw(tx.amountKrw)}
        {tx.foreignAmount !== null ? (
          <span className="ms-2 text-[13px] font-normal text-faint">${tx.foreignAmount.toFixed(2)}</span>
        ) : null}
        {tx.installment > 0 ? (
          <span className="ms-2 text-[13px] font-normal text-faint">{tx.installment}개월 할부</span>
        ) : null}
      </p>
      {tx.status === "cancelled" ? (
        <p className="text-[12.5px] text-dim">취소 알림이 와서 합계에서 빠졌어요.</p>
      ) : tx.status === "cancel" ? (
        <p className="text-[12.5px] text-dim">위 결제를 취소한 알림이에요. 짝이 되는 결제와 함께 합계에서 빠졌어요.</p>
      ) : tx.status === "refund" ? (
        <p className="text-[12.5px] text-dim">짝이 되는 결제를 못 찾은 취소라서, 돌려받은 돈으로 합계에서 뺐어요.</p>
      ) : null}

      {tx.enrichSource ? <OrderSection tx={tx} names={names} onSaved={onSaved} /> : null}

      <section>
        <h3 className="mb-2 text-[12px] text-dim">
          {tx.items?.length ? "결제 전체 분류 (상품이 없을 때 쓰여요)" : "분류"}
        </h3>
        <CategoryPicker names={names} value={category} onChange={setCategory} />
        {recategorised ? (
          <Toggle
            checked={remember}
            onChange={setRemember}
            label="이 가맹점은 앞으로도 이 분류로"
            hint="지난 내역 중 같은 가맹점도 함께 바뀌어요. 하나씩 고친 건 그대로 둬요."
          />
        ) : null}
      </section>

      {amountEditable ? (
        <section className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {tx.source === "manual" ? (
            <label className="block">
              <span className="mb-1 block text-[12px] text-dim">사용처</span>
              <input className={field} value={merchant} onChange={(e) => setMerchant(e.target.value)} />
            </label>
          ) : null}
          <label className="block">
            <span className="mb-1 block text-[12px] text-dim">
              {tx.estimated ? "원화 금액 (명세서에 찍힌 값으로)" : "금액"}
            </span>
            <input
              className={cn(field, "tnum")}
              inputMode="numeric"
              value={amount}
              onChange={(e) => setAmount(grouped(e.target.value))}
            />
          </label>
        </section>
      ) : null}

      <label className="block">
        <span className="mb-1 block text-[12px] text-dim">메모</span>
        <input
          className={field}
          value={memo}
          maxLength={200}
          placeholder="누구와, 무엇을"
          onChange={(e) => setMemo(e.target.value)}
        />
      </label>

      {tx.status !== "cancel" ? (
        <Toggle
          checked={excluded}
          onChange={setExcluded}
          label="합계에서 빼기"
          hint="돌려받을 경비나 내 계좌끼리 옮긴 돈처럼 지출이 아닌 것"
        />
      ) : null}

      {error ? <p className="text-[12.5px] text-reject">{error}</p> : null}

      <div className="flex items-center gap-2 pt-1">
        {tx.source === "manual" ? (
          <button type="button" onClick={() => void remove()} className={quietButton}>
            지우기
          </button>
        ) : null}
        <button type="button" disabled={saving} onClick={() => void save()} className={saveButton}>
          {saving ? "저장하는 중" : "저장"}
        </button>
      </div>
    </div>
  );
}

// ── 주문 상품 ────────────────────────────────────────────────────────────

/**
 * What a marketplace charge bought. Jarvis reads the order page by itself
 * when the charge arrives; this is where its reading is shown and corrected.
 */
function OrderSection({
  tx,
  names,
  onSaved,
}: {
  tx: Tx;
  names: string[];
  onSaved: (note: string) => void;
}) {
  const site = SITE_LABEL[tx.enrichSource ?? ""] ?? "주문";
  const [error, setError] = useState<string | null>(null);
  const items = tx.items ?? [];

  const retry = async () => {
    const err = await send("enrich/retry", "POST", { id: tx.id });
    if (err) setError(err);
    else onSaved(`${site} 주문을 다시 확인할게요`);
  };

  const status =
    tx.enrichStatus === "pending"
      ? `${site} 주문을 확인하는 중이에요. 몇 분 안에 상품이 채워져요.`
      : tx.enrichStatus === "login_required"
        ? `${site} 로그인이 풀려서 멈췄어요. 화면 탭에서 다시 로그인하세요.`
        : tx.enrichStatus === "not_found"
          ? `${site}에서 맞는 주문을 찾지 못했어요.${tx.enrichNote ? ` (${tx.enrichNote})` : ""}`
          : null;

  return (
    <section>
      <h3 className="mb-2 text-[12px] text-dim">{site}에서 산 것</h3>
      {status ? <p className="mb-2 text-[12.5px] text-dim">{status}</p> : null}
      {items.length > 0 ? (
        <ul className="space-y-1">
          {items.map((it) => (
            <ItemRow key={it.id} item={it} names={names} onError={setError} />
          ))}
        </ul>
      ) : null}
      {items.length > 0 && tx.enrichNote ? (
        <p className="mt-1.5 text-[11.5px] text-faint">{tx.enrichNote}</p>
      ) : null}
      {tx.enrichStatus !== "pending" ? (
        <button type="button" onClick={() => void retry()} className={cn(quietButton, "-ms-3 mt-1")}>
          주문 다시 확인하기
        </button>
      ) : null}
      {error ? <p className="mt-1 text-[12.5px] text-reject">{error}</p> : null}
    </section>
  );
}

function ItemRow({
  item,
  names,
  onError,
}: {
  item: Item;
  names: string[];
  onError: (e: string | null) => void;
}) {
  const [category, setCategory] = useState(item.category);
  const change = async (next: string) => {
    const before = category;
    setCategory(next);
    const err = await send(`items/${item.id}`, "PATCH", { category: next });
    if (err) {
      setCategory(before);
      onError(err);
    } else onError(null);
  };
  return (
    <li className="flex items-center gap-2 rounded-lg bg-glass px-3 py-2">
      <span className="min-w-0 flex-1">
        <span className="block text-[13px] leading-snug text-foreground/90">
          {item.name}
          {item.quantity > 1 ? <span className="text-faint"> ×{item.quantity}</span> : null}
        </span>
        <span className="tnum block text-[11.5px] text-faint">
          {krw(item.amountKrw)}
          {item.listedKrw !== item.amountKrw ? ` (주문서 ${krw(item.listedKrw)})` : ""}
        </span>
      </span>
      <label className="shrink-0">
        <span className="sr-only">{item.name} 분류</span>
        <select
          value={category}
          onChange={(e) => void change(e.target.value)}
          className="h-11 rounded-lg border border-edge-soft bg-background px-2 text-[16px] text-dim outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-8 sm:text-[12.5px]"
        >
          {names.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </label>
    </li>
  );
}

// ── 직접 적기 ────────────────────────────────────────────────────────────

export type Draft = { date: string; time: string; from?: Unparsed };

export function AddSheet({
  draft,
  names,
  onClose,
  onSaved,
}: {
  draft: Draft | null;
  names: string[];
  onClose: () => void;
  onSaved: (note: string) => void;
}) {
  return (
    <Panel
      open={draft !== null}
      onOpenChange={(o) => !o && onClose()}
      title={draft?.from ? "알림 내용 적기" : "직접 적기"}
      description={
        draft?.from
          ? "읽지 못한 알림이에요. 아래 원문을 보고 적어 주세요."
          : "현금이나 알림이 오지 않는 카드로 쓴 돈"
      }
    >
      {draft ? <AddForm key={draft.from?.messageId ?? "new"} draft={draft} names={names} onSaved={onSaved} /> : null}
    </Panel>
  );
}

function AddForm({
  draft,
  names,
  onSaved,
}: {
  draft: Draft;
  names: string[];
  onSaved: (note: string) => void;
}) {
  const [date, setDate] = useState(draft.date);
  const [time, setTime] = useState(draft.time);
  const [merchant, setMerchant] = useState("");
  const [amount, setAmount] = useState("");
  const [category, setCategory] = useState("");
  const [memo, setMemo] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!merchant.trim()) return setError("사용처를 적어 주세요.");
    if (!digits(amount)) return setError("금액을 적어 주세요.");
    setSaving(true);
    const body = { date, time, merchant: merchant.trim(), amountKrw: digits(amount), category, memo: memo.trim() };
    const err = draft.from
      ? await send(`unparsed/${encodeURIComponent(draft.from.messageId)}/resolve`, "POST", body)
      : await send("transactions", "POST", body);
    setSaving(false);
    if (err) return setError(err);
    onSaved(`${merchant.trim()} ${krw(digits(amount))}을 적었어요`);
  };

  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      {draft.from ? (
        <pre className="max-h-40 overflow-y-auto rounded-lg bg-well p-3 font-sans text-[12.5px] leading-relaxed whitespace-pre-wrap text-dim">
          {draft.from.body}
        </pre>
      ) : null}
      <div className="grid grid-cols-2 gap-2">
        <label className="block">
          <span className="mb-1 block text-[12px] text-dim">날짜</span>
          <input type="date" required className={field} value={date} onChange={(e) => setDate(e.target.value)} />
        </label>
        <label className="block">
          <span className="mb-1 block text-[12px] text-dim">시각</span>
          <input type="time" className={field} value={time} onChange={(e) => setTime(e.target.value)} />
        </label>
      </div>
      <label className="block">
        <span className="mb-1 block text-[12px] text-dim">사용처</span>
        <input className={field} value={merchant} maxLength={80} onChange={(e) => setMerchant(e.target.value)} />
      </label>
      <label className="block">
        <span className="mb-1 block text-[12px] text-dim">금액</span>
        <input
          className={cn(field, "tnum")}
          inputMode="numeric"
          placeholder="0"
          value={amount}
          onChange={(e) => setAmount(grouped(e.target.value))}
        />
      </label>
      <section>
        <h3 className="mb-2 text-[12px] text-dim">분류</h3>
        <CategoryPicker names={names} value={category} onChange={setCategory} allowAuto />
      </section>
      <label className="block">
        <span className="mb-1 block text-[12px] text-dim">메모</span>
        <input className={field} value={memo} maxLength={200} onChange={(e) => setMemo(e.target.value)} />
      </label>
      {error ? <p className="text-[12.5px] text-reject">{error}</p> : null}
      <div className="flex pt-1">
        <button type="submit" disabled={saving} className={saveButton}>
          {saving ? "적는 중" : "적기"}
        </button>
      </div>
    </form>
  );
}

// ── 예산 ──────────────────────────────────────────────────────────────────

export function BudgetSheet({
  book,
  open,
  onClose,
  onSaved,
}: {
  book: Book;
  open: boolean;
  onClose: () => void;
  onSaved: (note: string) => void;
}) {
  return (
    <Panel
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title="한 달 예산"
      description="매달 같은 금액이 적용돼요. 비워 두면 예산 없이 봐요."
    >
      {open ? <BudgetForm book={book} onSaved={onSaved} /> : null}
    </Panel>
  );
}

function BudgetForm({ book, onSaved }: { book: Book; onSaved: (note: string) => void }) {
  const initial = (key: string) =>
    book.budgets[key] ? book.budgets[key].toLocaleString("ko-KR") : "";
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(["", ...book.categoryNames].map((k) => [k, initial(k)])),
  );
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const prevOf = (name: string) => book.categories.find((c) => c.name === name)?.prevKrw ?? 0;

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    for (const [category, v] of Object.entries(values)) {
      const amount = digits(v);
      if (amount === (book.budgets[category] ?? 0)) continue;
      const err = await send("budgets", "PUT", { category, amountKrw: amount });
      if (err) {
        setSaving(false);
        return setError(err);
      }
    }
    setSaving(false);
    onSaved("예산을 저장했어요");
  };

  const row = (key: string, label: string, hint: string) => (
    <label key={key || "total"} className="flex items-center gap-3">
      <span className="min-w-0 flex-1">
        <span className="block text-[13.5px] text-foreground/90">{label}</span>
        <span className="block text-[11.5px] text-faint">{hint}</span>
      </span>
      <input
        className={cn(field, "tnum w-36 text-right sm:w-32")}
        inputMode="numeric"
        placeholder="없음"
        value={values[key] ?? ""}
        onChange={(e) => setValues((v) => ({ ...v, [key]: grouped(e.target.value) }))}
      />
    </label>
  );

  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      {row("", "전체", book.prev.totalKrw ? `지난달 ${krw(book.prev.totalKrw)}` : "지난달 기록 없음")}
      <div className="space-y-3 border-t border-edge-soft pt-4">
        {book.categoryNames.map((name) =>
          row(name, name, prevOf(name) ? `지난달 ${krw(prevOf(name))}` : "지난달 없음"),
        )}
      </div>
      {error ? <p className="text-[12.5px] text-reject">{error}</p> : null}
      <div className="flex pt-1">
        <button type="submit" disabled={saving} className={saveButton}>
          {saving ? "저장하는 중" : "저장"}
        </button>
      </div>
    </form>
  );
}

// ── 읽지 못한 알림 ────────────────────────────────────────────────────────

export function UnparsedSheet({
  items,
  open,
  onClose,
  onWrite,
  onDismissed,
}: {
  items: Unparsed[];
  open: boolean;
  onClose: () => void;
  onWrite: (u: Unparsed) => void;
  onDismissed: (note: string) => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const dismiss = async (u: Unparsed) => {
    const err = await send(`unparsed/${encodeURIComponent(u.messageId)}/dismiss`, "POST");
    if (err) setError(err);
    else onDismissed("결제가 아닌 알림으로 넘겼어요");
  };
  return (
    <Panel
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title="읽지 못한 카드 알림"
      description="결제처럼 보이지만 사용처나 금액을 찾지 못했어요. 합계에는 아직 없어요."
    >
      <ul className="space-y-3">
        {items.map((u) => (
          <li key={u.messageId} className="rounded-xl bg-glass p-3">
            <p className="mb-2 text-[11.5px] text-faint">
              {u.chatName} · {dayLabel(dayKey(u.sentAt))} {clock(u.sentAt)}
            </p>
            <pre className="font-sans text-[12.5px] leading-relaxed whitespace-pre-wrap text-dim">{u.body}</pre>
            <div className="mt-3 flex gap-2">
              <button type="button" onClick={() => void dismiss(u)} className={quietButton}>
                결제 아님
              </button>
              <button type="button" onClick={() => onWrite(u)} className={saveButton}>
                내용 적기
              </button>
            </div>
          </li>
        ))}
      </ul>
      {error ? <p className="mt-3 text-[12.5px] text-reject">{error}</p> : null}
    </Panel>
  );
}
