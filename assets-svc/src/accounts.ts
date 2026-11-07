/**
 * The accounts a read of the portfolio touches, by the name a problem with
 * each is said under ("업비트: 허용되지 않은 IP"). Written here and read here:
 * the status rows, the guard against a half-read portfolio and the trade plan
 * found a problem's account by its first words, each with its own copy of the
 * names.
 */
export const ACCOUNTS = {
  upbit: "업비트",
  kis: "한국투자증권",
  gold: "금현물",
  upbitFlows: "업비트 입출금 내역",
  flows: "원금 기록",
} as const;

export type Account = keyof typeof ACCOUNTS;

/** A problem with an account, in the words the screen shows. */
export function problem(account: Account, message: string): string {
  return `${ACCOUNTS[account]}: ${message}`;
}

/** The account a problem is about, the longest name first (업비트 입출금 내역 is not 업비트). */
export function accountOf(message: string): Account | undefined {
  const names = (Object.entries(ACCOUNTS) as [Account, string][]).sort((a, b) => b[1].length - a[1].length);
  return names.find(([, name]) => message.startsWith(name + ":"))?.[0];
}

/** What a problem says beyond its account. */
export function detailOf(message: string): string {
  const a = accountOf(message);
  return a ? message.slice(ACCOUNTS[a].length + 1).trim() : message;
}

/** The balances without which a portfolio is not the whole of it. */
const BALANCES: readonly Account[] = ["upbit", "kis", "gold"];

/** Whether a read missed a balance: a plan from it is wrong by that account. */
export function halfRead(problems: readonly string[] | undefined): boolean {
  return (problems ?? []).some((m) => BALANCES.includes(accountOf(m) as Account));
}
