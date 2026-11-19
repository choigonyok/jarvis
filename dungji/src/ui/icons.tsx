// 선 하나로 그린 작은 그림들(24 × 24). 색은 글자색을 따른다.

const PATHS: Record<string, string> = {
  feather: "M20 4c-6 0-11 4-12.5 10.5L6 20 M20 4c0 6-4 10.5-10 11 M8.6 13.5 13 13 M10.3 10.3 15.4 9.8 M12.5 7.3 17.6 6.8",
  phone: "M5 4h3.5l1.8 4.4-2.3 1.6a12 12 0 0 0 6 6l1.6-2.3L20 15.5V19a1.5 1.5 0 0 1-1.6 1.5C10.6 20 4 13.4 3.5 5.6A1.5 1.5 0 0 1 5 4Z",
  hangup: "M3.2 13.8c4.9-4.3 12.7-4.3 17.6 0l-1.9 2.6-3.6-1.3-.3-2.4a10 10 0 0 0-6 0l-.3 2.4-3.6 1.3Z",
  mic: "M12 3a3 3 0 0 1 3 3v5a3 3 0 0 1-6 0V6a3 3 0 0 1 3-3Z M6 11a6 6 0 0 0 12 0 M12 17v4 M9 21h6",
  micOff: "M9 6.5V6a3 3 0 0 1 6 0v5a3 3 0 0 1-.6 1.8 M9 9.8V11a3 3 0 0 0 4.6 2.5 M6 11a6 6 0 0 0 9.6 4.8 M18 11a6 6 0 0 1-.6 2.6 M12 17v4 M9 21h6 M4 4l16 16",
  gear: "M4 7h9 M17 7h3 M15 4.5v5 M4 17h3 M11 17h9 M9 14.5v5",
  back: "M15 5l-7 7 7 7",
  forward: "M9 5l7 7-7 7",
  search: "M10.5 4a6.5 6.5 0 1 1 0 13 6.5 6.5 0 0 1 0-13Z M15.5 15.5 20 20",
  lock: "M7 11V8a5 5 0 0 1 10 0v3 M5.5 11h13v9h-13Z",
  trash: "M4.5 7h15 M9.5 7V4.5h5V7 M6.5 7l1 13h9l1-13",
  reply: "M10 6 4 12l6 6 M4 12h10a6 6 0 0 1 6 6",
  plus: "M12 5v14 M5 12h14",
  flip: "M12 3v18 M8 7 3 12l5 5Z M16 7l5 5-5 5Z",
  box: "M3.5 8 12 4l8.5 4v8.5L12 20.5l-8.5-4Z M3.5 8 12 12l8.5-4 M12 12v8.5",
  bag: "M5 8h14l-1 12H6Z M9 8V6.5a3 3 0 0 1 6 0V8",
  pencil: "M4 20l1-4.5L15.5 5a2 2 0 0 1 3 3L8 18.5Z M13.5 7l3 3",
  drop: "M12 3.5c3.5 4.4 5.5 7.6 5.5 10a5.5 5.5 0 0 1-11 0c0-2.4 2-5.6 5.5-10Z",
  broom: "M14.5 3.5 10 12 M7 12h7l3 8.5H4Z M8.5 16.5l-.8 4 M11.5 16.5l-.3 4 M14 16.5l.6 4",
  fish: "M3 12c3-4 9-5.5 13-2.5l4-2.5v10l-4-2.5C12 17.5 6 16 3 12Z M8.5 11.3v.1",
  home: "M4 11 12 4.5l8 6.5 M6 9.5V20h12V9.5",
  book: "M5 4.5h5.5A1.5 1.5 0 0 1 12 6v14a1.5 1.5 0 0 0-1.5-1.5H5Z M19 4.5h-5.5A1.5 1.5 0 0 0 12 6v14a1.5 1.5 0 0 1 1.5-1.5H19Z",
  chat: "M4.5 5.5h15v10h-9l-4.5 4v-4H4.5Z",
  people: "M9 11a3.5 3.5 0 1 1 0-7 3.5 3.5 0 0 1 0 7Z M2.8 20c.6-3.5 3.1-5.5 6.2-5.5s5.6 2 6.2 5.5 M16 4.4a3.4 3.4 0 0 1 0 6.3 M17.5 14.7c2 .6 3.3 2.4 3.7 5.3",
  x: "M6 6l12 12 M18 6 6 18",
  check: "M5 12.5 10 17.5 19.5 7",
  shuffle: "M3.5 7h3.5c4 0 6 10 10 10h3.5 M18 14.5l2.5 2.5-2.5 2.5 M3.5 17h3.5c1.6 0 2.8-1.6 3.8-3.6 M13.2 10.6C14.2 8.6 15.4 7 17 7h3.5 M18 4.5 20.5 7 18 9.5",
  logout: "M14 4.5H6.5v15H14 M10.5 12H20 M16.5 8.5 20 12l-3.5 3.5",
  sparkle: "M12 3.5 13.8 10.2 20.5 12 13.8 13.8 12 20.5 10.2 13.8 3.5 12 10.2 10.2Z",
  leaf: "M5 19c0-8 5-13 14-14 0 9-5 14-13 14Z M5 19l7-7",
  receipt: "M6 3.5h12V20.5l-2-1.3-2 1.3-2-1.3-2 1.3-2-1.3-2 1.3Z M9 8h6 M9 11.5h6 M9 15h3.5",
  door: "M6 20.5V4.5h10v16 M3.5 20.5h17 M13 12.5v.1",
};

export function Icon({ name, className = "size-5", title }: { name: keyof typeof PATHS | string; className?: string; title?: string }) {
  const d = PATHS[name] ?? PATHS.sparkle;
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden={title ? undefined : true}
      role={title ? "img" : undefined}
    >
      {title && <title>{title}</title>}
      <path d={d} />
    </svg>
  );
}

/** 깃털 수: 둥지의 돈 */
export function Feathers({ n, className = "" }: { n: number; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-1 tabular-nums ${className}`}>
      <Icon name="feather" className="size-[1.05em] text-straw-deep" />
      {n.toLocaleString("ko-KR")}
    </span>
  );
}
