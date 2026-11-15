"use client";

import { useEffect, useId, useState } from "react";
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
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Field, FieldContent, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Pager } from "@/components/ui/pager";
import { SwitchRow } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { keepValue, keptValue } from "@/lib/keep";
import { usePaged } from "@/lib/paging";
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
  foreign,
  send,
} from "@/lib/spending";


const digits = (s: string) => Number(s.replace(/[^\d]/g, "")) || 0;
const grouped = (s: string) => {
  const n = digits(s);
  return n ? n.toLocaleString("ko-KR") : "";
};

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
  // "" is 자동, and a group's "" means nothing is picked: the empty name goes
  // by a word of its own while it is in the group.
  const AUTO = "__auto";
  return (
    <ToggleGroup
      type="single"
      aria-label="분류"
      variant="tile"
      size="touch"
      spacing={1.5}
      value={value === "" ? (allowAuto ? AUTO : "") : value}
      // One is always picked: pressing the picked one again keeps it.
      onValueChange={(v) => {
        if (v) onChange(v === AUTO ? "" : v);
      }}
      className="grid w-full grid-cols-3 sm:grid-cols-4"
    >
      {options.map((name) => (
        <ToggleGroupItem key={name || AUTO} value={name || AUTO} className="px-2">
          {name || "자동"}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

// ── 내역 하나 ────────────────────────────────────────────────────────────

/** A payment's form changed and not saved, kept while the app is open (lib/keep.ts). */
type TxValues = { category: string; remember: boolean; memo: string; excluded: boolean; merchant: string; amount: string };
const txKey = (id: number) => `spending:tx:${id}`;

export function TxSheet({
  tx,
  names,
  onClose,
  onSaved,
  onChanged,
}: {
  tx: Tx | null;
  names: string[];
  onClose: () => void;
  onSaved: (note: string) => void;
  /** Something in it was saved as it was picked, the sheet staying open: the book is read again. */
  onChanged: () => void;
}) {
  // Changed and not saved: closing it by hand asks first (ui/sheet.tsx).
  const [held, setHeld] = useState(false);
  return (
    <Panel
      open={tx !== null}
      onOpenChange={(o) => {
        if (o) return;
        // Closed by hand - asked first, if anything was changed: it goes.
        if (tx) keepValue(txKey(tx.id), undefined);
        onClose();
      }}
      guard={held}
      title={tx?.merchant ?? ""}
      description={
        tx
          ? // Money taken out of an investment account has a day, not an hour.
            `${dayLabel(dayKey(tx.approvedAt))}${tx.source === "asset" ? "" : ` ${clock(tx.approvedAt)}`} · ${cardLabel(tx)}`
          : undefined
      }
    >
      {/* Keyed by id so the form starts from this charge, not the last one opened. */}
      {tx ? (
        <TxForm key={tx.id} tx={tx} names={names} onSaved={onSaved} onClose={onClose} onChanged={onChanged} onHeld={setHeld} />
      ) : null}
    </Panel>
  );
}

function TxForm({
  tx,
  names,
  onSaved,
  onClose,
  onChanged,
  onHeld,
}: {
  tx: Tx;
  names: string[];
  onSaved: (note: string) => void;
  onClose: () => void;
  onChanged: () => void;
  onHeld: (held: boolean) => void;
}) {
  // What was changed is kept while the app is open, as an order half typed
  // is (lib/keep.ts): the sheet came back after a tab switch, its memo gone.
  const [kept] = useState(() => keptValue<TxValues>(txKey(tx.id)));
  const [category, setCategory] = useState(kept?.category ?? tx.category);
  const [remember, setRemember] = useState(kept?.remember ?? true);
  const [memo, setMemo] = useState(kept?.memo ?? tx.memo);
  const [excluded, setExcluded] = useState(kept?.excluded ?? tx.excluded);
  const [merchant, setMerchant] = useState(kept?.merchant ?? tx.merchant);
  const [amount, setAmount] = useState(kept?.amount ?? tx.amountKrw.toLocaleString("ko-KR"));
  const [error, setError] = useState<string | null>(null);
  // What is on its way: both buttons wait for it. A 저장 pressed while the
  // line was being deleted had sent a change to it.
  const [busy, setBusy] = useState<"save" | "remove" | null>(null);
  const id = useId();

  // A won figure can be corrected where it was a guess (a dollar charge) or
  // where a person wrote it. One read off an alert is what the card company
  // said; changing it would only make the book disagree with the statement.
  const amountEditable = tx.source === "manual" || tx.estimated;
  const recategorised = category !== tx.category;

  // What saving would send: nothing, and nothing here is unsaved.
  const patch: Record<string, unknown> = {};
  if (recategorised) {
    patch.category = category;
    patch.remember = remember;
  }
  if (memo.trim() !== tx.memo) patch.memo = memo.trim();
  if (excluded !== tx.excluded) patch.excluded = excluded;
  if (tx.source === "manual" && merchant.trim() !== tx.merchant) patch.merchant = merchant.trim();
  if (amountEditable && digits(amount) !== tx.amountKrw) patch.amountKrw = digits(amount);
  const changed = Object.keys(patch).length > 0;
  useEffect(() => {
    keepValue(txKey(tx.id), changed ? { category, remember, memo, excluded, merchant, amount } : undefined);
  }, [tx.id, changed, category, remember, memo, excluded, merchant, amount]);
  useEffect(() => {
    onHeld(changed);
    return () => onHeld(false);
  }, [changed, onHeld]);

  const save = async () => {
    if (!changed) {
      onClose();
      return;
    }
    // Checked as 직접 적기 checks them: an emptied amount had saved as ₩0.
    if (tx.source === "manual" && !merchant.trim()) return setError("사용처를 적어 주세요.");
    if (amountEditable && !digits(amount)) return setError("금액을 적어 주세요.");
    setBusy("save");
    setError(null);
    const err = await send(`transactions/${tx.id}`, "PATCH", patch);
    setBusy(null);
    if (err) {
      setError(err);
      return;
    }
    keepValue(txKey(tx.id), undefined);
    onSaved(
      recategorised && remember
        ? `${tx.merchant}의 다른 내역도 ${category}로 바꿨어요`
        : "저장했어요",
    );
  };

  // Asked first (the 지우기 below opens the question): a deleted line is gone.
  const remove = async () => {
    setBusy("remove");
    setError(null);
    const err = await send(`transactions/${tx.id}`, "DELETE");
    if (err) {
      setBusy(null);
      return setError(err);
    }
    keepValue(txKey(tx.id), undefined);
    onSaved("지웠어요");
  };

  return (
    <div className="space-y-5">
      <p className="tnum text-display leading-none font-semibold tracking-tight text-foreground">
        {tx.estimated ? "약 " : ""}
        {krw(tx.amountKrw)}
        {tx.foreignAmount !== null ? (
          <span className="ms-2 text-ui font-normal text-faint">{foreign(tx)}</span>
        ) : null}
        {tx.installment > 0 ? (
          <span className="ms-2 text-ui font-normal text-faint">{tx.installment}개월 할부</span>
        ) : null}
      </p>
      {tx.status === "cancelled" ? (
        <p className="text-aside text-dim">취소 알림이 와서 합계에서 빠졌어요.</p>
      ) : tx.status === "cancel" ? (
        <p className="text-aside text-dim">위 결제를 취소한 알림이에요. 짝이 되는 결제와 함께 합계에서 빠졌어요.</p>
      ) : tx.status === "refund" ? (
        <p className="text-aside text-dim">짝이 되는 결제를 못 찾은 취소라서, 돌려받은 돈으로 합계에서 뺐어요.</p>
      ) : null}
      {tx.source === "asset" ? (
        <p className="text-aside leading-relaxed text-dim">
          {tx.issuer}에서 뺀 돈을 지출로 답한 내역이에요. 금액이나 용도를 바꾸려면 자산 탭 원금에서 그 답을 지우고 다시 답해 주세요.
        </p>
      ) : null}

      {tx.enrichSource ? <OrderSection tx={tx} names={names} onSaved={onSaved} onChanged={onChanged} /> : null}

      <section>
        <h3 className="mb-2 text-aside text-dim">
          {tx.items?.length ? "결제 전체 분류 (상품이 없을 때 쓰여요)" : "분류"}
        </h3>
        <CategoryPicker names={names} value={category} onChange={setCategory} />
        {recategorised ? (
          <SwitchRow
            checked={remember}
            onCheckedChange={setRemember}
            label="이 가맹점은 앞으로도 이 분류로"
            hint="지난 내역 중 같은 가맹점도 함께 바뀌어요. 하나씩 고친 건 그대로 둬요."
          />
        ) : null}
      </section>

      {amountEditable ? (
        <section className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {tx.source === "manual" ? (
            <Field>
              <FieldLabel htmlFor={`${id}-merchant`}>사용처</FieldLabel>
              <Input id={`${id}-merchant`} surface="glass" value={merchant} onChange={(e) => setMerchant(e.target.value)} />
            </Field>
          ) : null}
          <Field>
            <FieldLabel htmlFor={`${id}-amount`}>{tx.estimated ? "원화 금액 (명세서에 찍힌 값으로)" : "금액"}</FieldLabel>
            <Input
              id={`${id}-amount`}
              surface="glass"
              className="tnum"
              inputMode="numeric"
              value={amount}
              onChange={(e) => setAmount(grouped(e.target.value))}
            />
          </Field>
        </section>
      ) : null}

      <Field>
        <FieldLabel htmlFor={`${id}-memo`}>메모</FieldLabel>
        <Input
          id={`${id}-memo`}
          surface="glass"
          value={memo}
          maxLength={200}
          placeholder="누구와, 무엇을"
          onChange={(e) => setMemo(e.target.value)}
        />
      </Field>

      {tx.status !== "cancel" ? (
        <SwitchRow
          checked={excluded}
          onCheckedChange={setExcluded}
          label="합계에서 빼기"
          hint="돌려받을 경비나 내 계좌끼리 옮긴 돈처럼 지출이 아닌 것"
        />
      ) : null}

      {error ? <p className="text-aside text-reject">{error}</p> : null}

      <div className="flex items-center gap-2 pt-1">
        {tx.source === "manual" ? (
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="quiet" size="touch-sm" disabled={busy !== null}>
                {busy === "remove" ? "지우는 중" : "지우기"}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>이 내역을 지울까요?</AlertDialogTitle>
                <AlertDialogDescription>
                  {tx.merchant} {krw(tx.amountKrw)}이 가계부와 이번 달 합계에서 빠져요. 되돌릴 수 없어요.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>두기</AlertDialogCancel>
                <AlertDialogAction onClick={() => void remove()}>지우기</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        ) : null}
        <Button variant="raised" size="touch" disabled={busy !== null} onClick={() => void save()} className="flex-1">
          {busy === "save" ? "저장하는 중" : "저장"}
        </Button>
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
  onChanged,
}: {
  tx: Tx;
  names: string[];
  onSaved: (note: string) => void;
  onChanged: () => void;
}) {
  const site = SITE_LABEL[tx.enrichSource ?? ""] ?? "주문";
  const [error, setError] = useState<string | null>(null);
  // Said and locked while it goes: on a slow line nothing had changed until
  // the answer, and a second press asked twice.
  const [busy, setBusy] = useState(false);
  const items = tx.items ?? [];

  const retry = async () => {
    setBusy(true);
    const err = await send("enrich/retry", "POST", { id: tx.id });
    if (err) {
      setBusy(false);
      setError(err);
    } else onSaved(`${site} 주문을 다시 확인할게요`);
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
      <h3 className="mb-2 text-aside text-dim">{site}에서 산 것</h3>
      {status ? <p className="mb-2 text-aside text-dim">{status}</p> : null}
      {items.length > 0 ? (
        <ul className="space-y-1">
          {items.map((it) => (
            <ItemRow key={it.id} item={it} names={names} onError={setError} onChanged={onChanged} />
          ))}
        </ul>
      ) : null}
      {items.length > 0 && tx.enrichNote ? (
        <p className="mt-1.5 text-meta text-faint">{tx.enrichNote}</p>
      ) : null}
      {tx.enrichStatus !== "pending" ? (
        <Button variant="quiet" size="touch-sm" disabled={busy} onClick={() => void retry()} className="-ms-3 mt-1">
          {busy ? "확인하는 중…" : "주문 다시 확인하기"}
        </Button>
      ) : null}
      {error ? <p className="mt-1 text-aside text-reject">{error}</p> : null}
    </section>
  );
}

function ItemRow({
  item,
  names,
  onError,
  onChanged,
}: {
  item: Item;
  names: string[];
  onError: (e: string | null) => void;
  onChanged: () => void;
}) {
  const [category, setCategory] = useState(item.category);
  // Saved as it is picked. The list's item line and the category totals are
  // the book's, read again for it - they had kept the old category until the
  // screen was left, and so had this picker when the payment was opened again.
  const change = async (next: string) => {
    const before = category;
    setCategory(next);
    const err = await send(`items/${item.id}`, "PATCH", { category: next });
    if (err) {
      setCategory(before);
      onError(err);
    } else {
      onError(null);
      onChanged();
    }
  };
  return (
    <li className="flex items-center gap-2 rounded-lg bg-glass px-3 py-2">
      <span className="min-w-0 flex-1">
        <span className="block text-ui leading-snug text-ink">
          {item.name}
          {item.quantity > 1 ? <span className="text-faint"> ×{item.quantity}</span> : null}
        </span>
        <span className="tnum block text-meta text-faint">
          {krw(item.amountKrw)}
          {item.listedKrw !== item.amountKrw ? ` (주문서 ${krw(item.listedKrw)})` : ""}
        </span>
      </span>
      <NativeSelect aria-label={`${item.name} 분류`} value={category} onChange={(e) => void change(e.target.value)}>
        {names.map((n) => (
          <NativeSelectOption key={n} value={n}>
            {n}
          </NativeSelectOption>
        ))}
      </NativeSelect>
    </li>
  );
}

// ── 직접 적기 ────────────────────────────────────────────────────────────

export type Draft = { date: string; time: string; from?: Unparsed };

/** What is typed into one entry and not written down yet, kept while the app is open (lib/keep.ts). */
type AddValues = { date: string; time: string; merchant: string; amount: string; category: string; memo: string };
const addKey = (draft: Draft) => `spending:add:${draft.from?.messageId ?? "new"}`;

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
  // Typed and not written down: closing it by hand asks first (ui/sheet.tsx).
  const [held, setHeld] = useState(false);
  return (
    <Panel
      open={draft !== null}
      onOpenChange={(o) => {
        if (o) return;
        // Closed by hand - asked first, if anything was typed: it goes.
        if (draft) keepValue(addKey(draft), undefined);
        onClose();
      }}
      guard={held}
      title={draft?.from ? "알림 내용 적기" : "직접 적기"}
      description={
        draft?.from
          ? "읽지 못한 알림이에요. 아래 원문을 보고 적어 주세요."
          : "현금이나 알림이 오지 않는 카드로 쓴 돈"
      }
    >
      {draft ? (
        <AddForm key={draft.from?.messageId ?? "new"} draft={draft} names={names} onSaved={onSaved} onHeld={setHeld} />
      ) : null}
    </Panel>
  );
}

function AddForm({
  draft,
  names,
  onSaved,
  onHeld,
}: {
  draft: Draft;
  names: string[];
  onSaved: (note: string) => void;
  onHeld: (held: boolean) => void;
}) {
  // What was typed is kept while the app is open, as an order half typed is
  // (lib/keep.ts): a look at another tab had closed the sheet and lost all
  // six fields - and with an unread alert, the way back to its text.
  const [kept] = useState(() => keptValue<AddValues>(addKey(draft)));
  const [date, setDate] = useState(kept?.date ?? draft.date);
  const [time, setTime] = useState(kept?.time ?? draft.time);
  const [merchant, setMerchant] = useState(kept?.merchant ?? "");
  const [amount, setAmount] = useState(kept?.amount ?? "");
  const [category, setCategory] = useState(kept?.category ?? "");
  const [memo, setMemo] = useState(kept?.memo ?? "");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const id = useId();

  const typed =
    Boolean(merchant.trim() || amount || category || memo.trim()) || date !== draft.date || time !== draft.time;
  const key = addKey(draft);
  useEffect(() => {
    keepValue(key, typed ? { date, time, merchant, amount, category, memo } : undefined);
  }, [key, typed, date, time, merchant, amount, category, memo]);
  useEffect(() => {
    onHeld(typed);
    return () => onHeld(false);
  }, [typed, onHeld]);

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
    keepValue(key, undefined);
    onSaved(`${merchant.trim()} ${krw(digits(amount))}을 적었어요`);
  };

  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      {draft.from ? (
        <pre className="max-h-40 overflow-y-auto rounded-lg bg-well p-3 font-sans text-aside leading-relaxed whitespace-pre-wrap text-dim">
          {draft.from.body}
        </pre>
      ) : null}
      <div className="grid grid-cols-2 gap-2">
        <Field>
          <FieldLabel htmlFor={`${id}-date`}>날짜</FieldLabel>
          <Input id={`${id}-date`} surface="glass" type="date" required value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-time`}>시각</FieldLabel>
          <Input id={`${id}-time`} surface="glass" type="time" value={time} onChange={(e) => setTime(e.target.value)} />
        </Field>
      </div>
      <Field>
        <FieldLabel htmlFor={`${id}-merchant`}>사용처</FieldLabel>
        <Input id={`${id}-merchant`} surface="glass" value={merchant} maxLength={80} onChange={(e) => setMerchant(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-amount`}>금액</FieldLabel>
        <Input
          id={`${id}-amount`}
          surface="glass"
          className="tnum"
          inputMode="numeric"
          placeholder="0"
          value={amount}
          onChange={(e) => setAmount(grouped(e.target.value))}
        />
      </Field>
      <section>
        <h3 className="mb-2 text-aside text-dim">분류</h3>
        <CategoryPicker names={names} value={category} onChange={setCategory} allowAuto />
      </section>
      <Field>
        <FieldLabel htmlFor={`${id}-memo`}>메모</FieldLabel>
        <Input id={`${id}-memo`} surface="glass" value={memo} maxLength={200} onChange={(e) => setMemo(e.target.value)} />
      </Field>
      {error ? <p className="text-aside text-reject">{error}</p> : null}
      <div className="flex pt-1">
        <Button type="submit" variant="raised" size="touch" disabled={saving} className="flex-1">
          {saving ? "적는 중" : "적기"}
        </Button>
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
  const id = useId();

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
    <Field key={key || "total"} orientation="horizontal" className="min-h-0">
      <FieldContent>
        <FieldLabel htmlFor={`${id}-${key || "total"}`}>{label}</FieldLabel>
        <FieldDescription>{hint}</FieldDescription>
      </FieldContent>
      <Input
        id={`${id}-${key || "total"}`}
        surface="glass"
        className="tnum w-36 text-right sm:w-32"
        inputMode="numeric"
        placeholder="없음"
        value={values[key] ?? ""}
        onChange={(e) => setValues((v) => ({ ...v, [key]: grouped(e.target.value) }))}
      />
    </Field>
  );

  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      {row("", "전체", book.prev.totalKrw ? `지난달 ${krw(book.prev.totalKrw)}` : "지난달 기록 없음")}
      <div className="space-y-3 border-t border-edge-soft pt-4">
        {book.categoryNames.map((name) =>
          row(name, name, prevOf(name) ? `지난달 ${krw(prevOf(name))}` : "지난달 없음"),
        )}
      </div>
      {error ? <p className="text-aside text-reject">{error}</p> : null}
      <div className="flex pt-1">
        <Button type="submit" variant="raised" size="touch" disabled={saving} className="flex-1">
          {saving ? "저장하는 중" : "저장"}
        </Button>
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
  /** One set aside; last: none are left, and the sheet can go. */
  onDismissed: (note: string, last: boolean) => void;
}) {
  const [error, setError] = useState<string | null>(null);
  // Set aside: off the list as soon as the server has it, not when the book
  // is next read. The sheet stays for the rest - it had closed after each.
  const [gone, setGone] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const left = items.filter((u) => !gone.includes(u.messageId));
  // Each is the whole alert: ten a page.
  const paged = usePaged(left, { size: 10 });
  const dismiss = async (u: Unparsed) => {
    setBusy(true);
    setError(null);
    const err = await send(`unparsed/${encodeURIComponent(u.messageId)}/dismiss`, "POST");
    setBusy(false);
    if (err) return setError(err);
    setGone((g) => [...g, u.messageId]);
    onDismissed("결제가 아닌 알림으로 넘겼어요", left.length <= 1);
  };
  return (
    <Panel
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title="읽지 못한 카드 알림"
      description="결제처럼 보이지만 사용처나 금액을 찾지 못했거나, 기록하다 거듭 실패해 따로 둔 알림이에요. 합계에는 아직 없어요."
    >
      <div id={paged.anchor} className="scroll-mt-4" />
      <ul className="space-y-3">
        {paged.items.map((u) => (
          <li key={u.messageId} className="rounded-xl bg-glass p-3">
            <p className="mb-2 text-meta text-faint">
              {u.chatName} · {dayLabel(dayKey(u.sentAt))} {clock(u.sentAt)}
            </p>
            {u.reason ? <p className="mb-2 text-meta break-words text-faint">{u.reason}</p> : null}
            <pre className="font-sans text-aside leading-relaxed whitespace-pre-wrap text-dim">{u.body}</pre>
            <div className="mt-3 flex gap-2">
              {/* Asked first: the server cannot put an alert back, and with it
                  goes the only copy of what it said - a payment set aside here
                  by a slip of the thumb stays out of the totals for good. */}
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button variant="quiet" size="touch-sm" disabled={busy}>
                    결제 아님
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>결제가 아닌 알림으로 넘길까요?</AlertDialogTitle>
                    <AlertDialogDescription>
                      {u.chatName} {dayLabel(dayKey(u.sentAt))} {clock(u.sentAt)} 알림이 목록에서 빠지고, 결제였더라도 합계에
                      들어가지 않아요. 되돌릴 수 없어요.
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>두기</AlertDialogCancel>
                    <AlertDialogAction onClick={() => void dismiss(u)}>넘기기</AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
              <Button variant="raised" size="touch" disabled={busy} onClick={() => onWrite(u)} className="flex-1">
                내용 적기
              </Button>
            </div>
          </li>
        ))}
      </ul>
      <Pager {...paged} />
      {error ? <p className="mt-3 text-aside text-reject">{error}</p> : null}
    </Panel>
  );
}
