import type { Proposal } from "@/lib/thread";

/**
 * How a card's body is set. A literal command the CLI would run (a shell
 * line, a file's content) keeps its line breaks and is set in mono; anything
 * else - a module's sentence, or the arguments of a tool the CLI runs itself
 * (a job, the browser), which the agent writes as "항목: 값" lines - is prose
 * and wraps.
 */
export function isLiteral(p: Proposal): boolean {
  const kind = p.action.kind;
  return kind.startsWith("claude.") && !kind.startsWith("claude.mcp__");
}

// The agent's own labels (agent/internal/adapter/permission/render.go), for
// cards written before it wrote them.
const LABELS: Record<string, string> = {
  title: "제목", name: "이름", goal: "목표", text: "내용", instructions: "지시", question: "질문",
  sites: "쓰는 사이트", photos: "사진", url: "주소", query: "검색어", element: "대상", index: "번호",
  value: "값", date: "날짜", start: "시작", end: "끝", place: "장소", reason: "이유", id: "번호", new_tab: "새 탭",
};
const ORDER = ["title", "name", "goal", "question", "text", "instructions", "url", "query", "element", "value", "date", "start", "end", "place", "sites", "photos", "reason"];

// What the agent now calls the tools whose old cards were titled by name.
const TITLES: Record<string, string> = {
  create_job: "작업을 맡깁니다",
  instruct_job: "작업에 지시를 더합니다",
  list_jobs: "작업 목록을 봅니다",
  browser_navigate: "브라우저로 주소를 엽니다",
  browser_click: "브라우저에서 누릅니다",
  browser_type: "브라우저에 입력합니다",
};

/**
 * A card's title as words: before 2026-10-20 a tool with no card of its own
 * was titled by its name ("mcp__jobs__instruct_job 실행을 요청합니다").
 */
export function readableTitle(title: string): string {
  const m = /^mcp__([a-z0-9_-]+)__([a-z0-9_]+) 실행을 요청합니다$/i.exec(title.trim());
  if (!m) return title;
  return TITLES[m[2]] ?? `${m[2].replace(/_/g, " ")} 실행을 요청합니다`;
}

/** What approving does, without naming the program that does it (old cards said "Claude Code가"). */
export function readableConsequence(text: string): string {
  return text.replace("승인하면 Claude Code가 바로 실행합니다", "승인하면 바로 실행합니다");
}

/**
 * A card's body as words. Cards made before 2026-10-20 carried some tools'
 * arguments as JSON - a job made or instructed showed its braces; those read
 * here as the agent now writes them. Any other body is left as it is.
 */
export function readableBody(body: string): string {
  const text = body.trim();
  if (!text.startsWith("{") || !text.endsWith("}")) return body;
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return body;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return body;
  return lines(parsed as Record<string, unknown>) || body;
}

function lines(fields: Record<string, unknown>): string {
  const rank = (k: string) => (ORDER.includes(k) ? ORDER.indexOf(k) : ORDER.length);
  return Object.keys(fields)
    .filter((k) => k !== "ref" && k !== "description")
    .sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
    .flatMap((k) => {
      const v = words(fields[k]);
      if (!v) return [];
      const label = LABELS[k] ?? k;
      return v.includes("\n") ? [`${label}:\n${v.replace(/^/gm, "  ")}`] : [`${label}: ${v}`];
    })
    .join("\n");
}

function words(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v.trim();
  if (typeof v === "boolean") return v ? "예" : "아니요";
  if (typeof v === "number") return String(v);
  if (Array.isArray(v)) {
    const parts = v.map(words).filter(Boolean);
    if (parts.every((p) => !p.includes("\n")) && v.every((e) => typeof e !== "object" || e === null)) {
      return parts.join(", ");
    }
    return parts.map((p) => `- ${p.replace(/\n/g, "\n  ")}`).join("\n");
  }
  if (typeof v === "object") return lines(v as Record<string, unknown>);
  return String(v);
}
