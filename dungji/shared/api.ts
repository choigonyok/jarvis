// 서버가 화면에 돌려주는 모양. 화면과 서버가 같이 읽는다.

import type { Act } from "./news";
import type { DirtKind, PlantView } from "./rules";

export interface Me {
  id: string;
  login: string;
  name: string;
  home: string; // 내 둥지의 slug
  partner: { id: string; name: string } | null;
  /** 이름을 정했나. jarvis 로 처음 들어온 사람은 false: 화면이 먼저 묻는다. */
  named: boolean;
  /** jarvis 로 들어온 사람(비밀번호 없음, 나가기는 jarvis 에서) */
  via: "jarvis" | null;
}

export interface FishItem {
  id: string;
  species: string;
  name: string;
  hunger: number;
  grown: number;
  adult: boolean;
  sick: boolean;
}

export interface ItemView {
  id: string;
  key: string;
  place: "room" | "storage";
  x: number;
  y: number;
  flip: boolean;
  wall: "left" | "right" | null;
  plant?: PlantView;
  tank?: { clarity: number; capacity: number; fish: FishItem[] };
}

export interface Member {
  id: string;
  name: string;
  online: boolean;
}

export interface Visitor {
  name: string;
  home: string;
  slug: string;
}

export interface HomeView {
  id: string;
  slug: string;
  name: string;
  intro: string;
  since: string | null;
  wallpaper: string;
  floor: string;
  members: Member[];
  cleanliness: number;
  dirt: { id: string; x: number; y: number; kind: DirtKind }[];
  items: ItemView[];
  visits: { today: number; total: number; recent: Visitor[] };
  /** 보는 사람이 이 둥지의 주인인가 */
  owner: boolean;
  /** 주인에게만 */
  coins?: number;
  storageSlots?: number;
  themes?: string[];
  invite?: string | null;
  listed?: boolean;
  /** 손님에게만: 오늘 이 방에서 더 치울 수 있는 먼지 */
  helpLeft?: number;
  /** 주인에게만: 이웃의 마지막 방명록, 상대의 마지막 일기(새 글 점) */
  news?: { guestbook: number | null; diary: number | null };
}

export interface Reward {
  delta: number;
  reason: string;
}

export interface HomeResponse {
  home: HomeView;
  rewards: Reward[];
  /** 방금 산 물건(놓인 곳): 화면이 그 물건을 고른 채 꾸미기로 들어간다 */
  added?: { id: string; place: "room" | "storage" };
}

export interface Neighbor {
  slug: string;
  name: string;
  intro: string;
  members: string[];
  cleanliness: number;
  visits: number;
  activeAt: number;
}

export interface DiaryEntry {
  authorId: string;
  author: string;
  mood: string;
  body: string;
  updatedAt: number;
}

export interface DiaryComment {
  id: string;
  authorId: string;
  author: string;
  body: string;
  at: number;
}

export interface DiaryDay {
  day: string;
  entries: DiaryEntry[];
  comments: DiaryComment[];
}

export interface DiaryMonth {
  month: string;
  days: Record<string, { authors: string[]; comments: number }>;
}

export interface GuestEntry {
  id: string;
  author: string;
  authorId: string;
  home: string;
  slug: string;
  body: string;
  secret: boolean;
  /** 비밀글이라 이 사람에게는 안 보인다 */
  hidden: boolean;
  reply: string | null;
  replyAt: number | null;
  at: number;
  canDelete: boolean;
}

export interface LedgerRow {
  delta: number;
  reason: string;
  by: string | null;
  at: number;
}

export const MOODS = ["sun", "cloud", "rain", "snow", "heart", "tired"] as const;
export type Mood = (typeof MOODS)[number];
export const MOOD_NAMES: Record<Mood, string> = {
  sun: "맑음",
  cloud: "흐림",
  rain: "비",
  snow: "눈",
  heart: "두근",
  tired: "지침",
};

/** RTCIceCandidateInit 과 같은 모양(서버 타입에는 DOM 이 없어 따로 둔다) */
export interface IceInit {
  candidate?: string;
  sdpMid?: string | null;
  sdpMLineIndex?: number | null;
  usernameFragment?: string | null;
}

/** 실시간 통로(웹소켓)로 오가는 말 */
export type LiveMessage =
  | { type: "presence"; online: string[] }
  /** 둥지가 바뀌었다. acts·name: 둘 중 한 사람(name)이 우리 둥지에서 한 일(shared/news.ts) */
  | { type: "changed"; what: "home" | "diary" | "guestbook"; by: string; acts?: Act[]; name?: string }
  | { type: "call"; from: string; fromSid: string; sdp: string; name: string }
  | { type: "answer"; from: string; fromSid: string; to: string; sdp: string }
  | { type: "ice"; from: string; fromSid: string; to: string; candidate: IceInit }
  | { type: "hangup"; from: string; fromSid: string; to?: string }
  | { type: "decline"; from: string; fromSid: string; to: string }
  | { type: "taken"; from: string; fromSid: string }
  | { type: "hello"; sid: string };
