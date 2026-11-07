import {
  Activity,
  BookUser,
  Brush,
  CalendarDays,
  ClipboardCheck,
  Dumbbell,
  Ellipsis,
  type LucideIcon,
  MapPin,
  MessageCircle,
  MessagesSquare,
  MonitorPlay,
  ReceiptText,
  ListChecks,
  Images,
  Sun,
  Target,
  TrainFront,
  Wallet,
} from "lucide-react";
import type { Hub, HubId } from "@/lib/hubs";

/**
 * What the shell adds to the places of lib/hubs.ts: their icons, and the
 * screen each was left on, on this device.
 */

/** Line icons, the system's own weight: on the glass bar a label alone reads as a button row. */
export const HUB_ICON: Record<HubId, LucideIcon> = {
  talk: MessageCircle,
  today: Sun,
  money: Wallet,
  life: BookUser,
  more: Ellipsis,
  workout: Dumbbell,
  location: MapPin,
  edit: Brush,
};

/** A screen's own icon, where a list names screens rather than places (더보기, a held place's menu). */
export const SCREEN_ICON: Record<string, LucideIcon> = {
  "/": MessageCircle,
  "/workout": Dumbbell,
  "/record": ClipboardCheck,
  "/assets": Wallet,
  "/spending": ReceiptText,
  "/jobs": ListChecks,
  "/goals": Target,
  "/photos": Images,
  "/edit": Brush,
  "/life": BookUser,
  "/location": MapPin,
  "/transit": TrainFront,
  "/calendar": CalendarDays,
  "/screen": MonitorPlay,
  "/kakao": MessagesSquare,
  "/status": Activity,
};

const LAST = "jarvis:hub:";

/** A place opens on the screen left open there, on this device. */
export function lastIn(hub: Hub): string {
  try {
    const saved = localStorage.getItem(LAST + hub.id);
    if (saved && hub.screens.some((s) => s.href === saved)) return saved;
  } catch {}
  return hub.screens[0].href;
}

export function remember(hub: Hub, pathname: string) {
  if (hub.screens.length < 2 || !hub.screens.some((s) => s.href === pathname)) return;
  try {
    localStorage.setItem(LAST + hub.id, pathname);
  } catch {}
}
