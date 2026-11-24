// 선 아이콘: 콘솔과 같은 lucide(같은 판), 같은 굵기. 색은 글자색을 따른다.

import {
  Archive,
  ArrowDownLeft,
  ArrowDownRight,
  ArrowUpLeft,
  ArrowUpRight,
  Armchair,
  BookOpen,
  BrushCleaning,
  CalendarDays,
  Check,
  ChevronLeft,
  ChevronRight,
  CornerDownRight,
  DoorOpen,
  Droplet,
  Feather,
  Fish,
  House,
  Leaf,
  Lock,
  LogOut,
  type LucideIcon,
  MessageSquare,
  Mic,
  MicOff,
  Pencil,
  Phone,
  PhoneOff,
  Plus,
  ReceiptText,
  RotateCcw,
  RotateCw,
  Search,
  ShoppingBag,
  Shuffle,
  SlidersHorizontal,
  Sparkles,
  Trash2,
  Users,
  X,
} from "lucide-react";

const ICONS: Record<string, LucideIcon> = {
  feather: Feather,
  upLeft: ArrowUpLeft,
  upRight: ArrowUpRight,
  downLeft: ArrowDownLeft,
  downRight: ArrowDownRight,
  phone: Phone,
  hangup: PhoneOff,
  mic: Mic,
  micOff: MicOff,
  gear: SlidersHorizontal,
  back: ChevronLeft,
  forward: ChevronRight,
  search: Search,
  lock: Lock,
  trash: Trash2,
  reply: CornerDownRight,
  plus: Plus,
  turnLeft: RotateCcw,
  turnRight: RotateCw,
  box: Archive,
  bag: ShoppingBag,
  pencil: Pencil,
  decorate: Armchair,
  drop: Droplet,
  broom: BrushCleaning,
  fish: Fish,
  home: House,
  book: BookOpen,
  calendar: CalendarDays,
  chat: MessageSquare,
  people: Users,
  x: X,
  check: Check,
  shuffle: Shuffle,
  logout: LogOut,
  sparkle: Sparkles,
  leaf: Leaf,
  receipt: ReceiptText,
  door: DoorOpen,
};

export function Icon({ name, className = "size-5", title, strong }: { name: string; className?: string; title?: string; strong?: boolean }) {
  const C = ICONS[name] ?? Sparkles;
  return title ? (
    <C className={className} strokeWidth={strong ? 2.1 : 1.8} role="img" aria-label={title} />
  ) : (
    <C className={className} strokeWidth={strong ? 2.1 : 1.8} aria-hidden />
  );
}

/** 깃털 수: 둥지의 돈. 숫자는 표 숫자(tnum). */
export function Feathers({ n, className = "" }: { n: number; className?: string }) {
  return (
    <span className={`tnum inline-flex items-center gap-1 ${className}`}>
      <Feather className="size-[1em] text-dim" strokeWidth={1.8} aria-hidden />
      {n.toLocaleString("ko-KR")}
    </span>
  );
}
