// Shapes of market-svc's JSON (Joongna listings). The service is the source of truth.

export type ListingStatus = "queued" | "active" | "reserved" | "sold" | "deleted" | "failed";
export type TaskKind = "post" | "price" | "status" | "bump" | "delete";

export type MarketTask = {
  id: number;
  listingId: number;
  kind: TaskKind;
  priceKrw?: number;
  toStatus?: "active" | "reserved" | "sold";
  state: "pending" | "failed";
  attempts: number;
  note?: string;
  createdAt: string;
};

export type Listing = {
  id: number;
  status: ListingStatus;
  title: string;
  priceKrw: number;
  description: string;
  category: string;
  condition: string;
  shipping: "included" | "separate";
  photos: string[];
  joongnaId?: string;
  url?: string;
  views: number;
  likes: number;
  chats: number;
  note?: string;
  postedAt?: string;
  soldAt?: string;
  syncedAt?: string;
  photosGone?: boolean;
  createdAt: string;
  tasks: MarketTask[];
};

export type MarketState = {
  loginRequired: boolean;
  loginNote?: string;
  syncRequested: boolean;
  lastSyncAt?: string;
  lastSyncNote?: string;
};

export type Market = {
  listings: Listing[];
  state: MarketState;
  syncEveryMinutes: number;
};

export const STATUS_LABEL: Record<ListingStatus, string> = {
  queued: "등록 대기",
  active: "판매중",
  reserved: "예약중",
  sold: "판매완료",
  deleted: "삭제됨",
  failed: "등록 실패",
};

/** The status word's colour, from the system's own tokens. */
export const STATUS_TONE: Record<ListingStatus, string> = {
  queued: "text-dim",
  active: "text-approve",
  reserved: "text-mine",
  sold: "text-faint",
  deleted: "text-faint",
  failed: "text-reject",
};

/** What a waiting change is called while it waits. */
export function taskLabel(t: MarketTask): string {
  switch (t.kind) {
    case "post":
      return "중고나라에 올리는 중";
    case "price":
      return `가격을 ${(t.priceKrw ?? 0).toLocaleString("ko-KR")}원으로 바꾸는 중`;
    case "status":
      return `${STATUS_LABEL[t.toStatus ?? "active"]}으로 바꾸는 중`;
    case "bump":
      return "끌어올리는 중";
    case "delete":
      return "삭제하는 중";
  }
}

export async function send(
  path: string,
  method: "POST" | "DELETE",
  body?: unknown,
): Promise<string | null> {
  const res = await fetch(`/api/market/${path}`, {
    method,
    headers: body ? { "content-type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  }).catch(() => null);
  if (res?.ok) return null;
  const err = (await res?.json().catch(() => null)) as { error?: string } | null;
  return err?.error ?? "요청하지 못했습니다.";
}
