import type { Role } from "@/lib/role";

/**
 * The console's places and the screens in each, as plain data. The shell
 * draws them (components/shell: the header, the tab bar, the screen menu,
 * each icon looked up by the place's id) and the middleware reads the
 * guest's from here (lib/role.ts), so this imports nothing that runs.
 */

/** One screen: where it is, and the word it goes by. */
export type Tab = { href: string; label: string };

export type HubId = "talk" | "today" | "plan" | "money" | "life" | "more" | "workout" | "location" | "edit";

/** One place on the phone's bar, and the screens that live there. */
export type Hub = {
  id: HubId;
  label: string;
  screens: Tab[];
  /** Opens as a list of its screens: none of them is where you would land. */
  list?: boolean;
};

/**
 * Seventeen screens are more than a thumb bar can hold - six is the limit before
 * a slot is too narrow for its word (two-syllable words, 320px). So the bar
 * names six places, by what you came for, and a place's screens sit in the
 * header where the name was: going between them costs no row, and each stays
 * one short page. 일정 is the two of us - the couple's space and its calendar;
 * 더보기 is the screens you reach for only when something is up.
 */
export const OWNER_HUBS: Hub[] = [
  { id: "talk", label: "대화", screens: [{ href: "/", label: "대화" }] },
  {
    id: "today",
    label: "오늘",
    screens: [
      { href: "/jobs", label: "작업" },
      { href: "/goals", label: "목표" },
      { href: "/workout", label: "운동" },
      { href: "/transit", label: "출발" },
      { href: "/record", label: "기록" },
    ],
  },
  {
    id: "plan",
    label: "일정",
    screens: [
      { href: "/couple", label: "우리" },
      { href: "/calendar", label: "캘린더" },
    ],
  },
  {
    id: "money",
    label: "돈",
    screens: [
      { href: "/assets", label: "자산" },
      { href: "/spending", label: "가계부" },
    ],
  },
  {
    id: "life",
    label: "생활",
    screens: [
      { href: "/life", label: "나" },
      { href: "/photos", label: "사진" },
      { href: "/location", label: "위치" },
    ],
  },
  {
    id: "more",
    label: "더보기",
    list: true,
    screens: [
      { href: "/kakao", label: "카톡" },
      { href: "/screen", label: "화면" },
      { href: "/status", label: "상태" },
    ],
  },
];

/**
 * The guest's places, grouped as the operator's are. Five places keep their
 * words; eight screens would be eight wordless icons, and neither the bar nor
 * the header would say where you are. These screens are also every page a
 * guest may open (lib/role.ts GUEST_PAGES).
 */
export const GUEST_HUBS: Hub[] = [
  { id: "talk", label: "대화", screens: [{ href: "/", label: "대화" }] },
  {
    id: "today",
    label: "오늘",
    screens: [
      { href: "/couple", label: "우리" },
      { href: "/calendar", label: "캘린더" },
      { href: "/jobs", label: "작업" },
      { href: "/goals", label: "목표" },
      { href: "/transit", label: "출발" },
    ],
  },
  { id: "workout", label: "운동", screens: [{ href: "/workout", label: "운동" }] },
  { id: "location", label: "위치", screens: [{ href: "/location", label: "위치" }] },
  { id: "edit", label: "편집", screens: [{ href: "/edit", label: "편집" }] },
];

/** The places a role is shown. The middleware is what actually refuses the rest. */
export const hubsFor = (role: Role) => (role === "guest" ? GUEST_HUBS : OWNER_HUBS);

/** The screen a page is one of: a block of the couple's space is 우리's. */
export const screenOf = (pathname: string) => (pathname.startsWith("/couple/") ? "/couple" : pathname);

/** The place a screen belongs to. 편집 is reached from 사진 and is no screen of its own there. */
export const hubOf = (hubs: Hub[], pathname: string) => {
  const screen = screenOf(pathname);
  return hubs.find((h) => h.screens.some((s) => s.href === screen)) ?? (pathname === "/edit" ? hubs.find((h) => h.id === "life") : undefined);
};

/**
 * Where a waiting decision is announced: the conversation, where the card
 * stands and is decided - the mark sat on 더보기, two taps from a card that
 * was a tab away.
 */
export const pendingHome = "/";
