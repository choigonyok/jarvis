/**
 * Words as a notification shows them: one line however many they came in
 * (" · " between them), without the marks of a document - a list's "-" or
 * "1.", a heading's "#", **bold**, `code`, a link's brackets - and without
 * the name the writer opened with ("고뇩님,"). The same as the agent's
 * gocommon/notify Brief, for what was sent before it. How much shows is the
 * screen's to clip (line-clamp), not this.
 */
export function brief(text: string): string {
  const lines = text
    .split("\n")
    .map((line) =>
      line
        .trim()
        .replace(/^(?:[-*•]\s+|\d{1,2}[.)]\s+)/, "")
        .replace(/^#{1,6}\s+/, "")
        .replace(/\*\*(.+?)\*\*/g, "$1")
        .replace(/__(.+?)__/g, "$1")
        .replace(/`([^`]+)`/g, "$1")
        .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
        .trim(),
    )
    .filter(Boolean);
  return lines
    .join(" · ")
    .replace(/^[^\s,]{1,12}님,\s*/, "")
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * The titles older notifications have, from before they said what they were
 * about: the kind's word, with the card's or the job's name first in the
 * body, before " - ". Such a one is read as the newer ones are said.
 */
const KIND_WORDS: Record<string, string> = {
  "작업을 끝냈어요": "끝냈어요: ",
  "작업이 멈췄어요": "멈췄어요: ",
  "결재 대기": "",
  제안: "",
  "작업이 기다려요": "",
  가계부: "",
  자산: "",
};

/** A notification's title and body, as the inbox and the banner show them. */
export function readable(title: string, body: string): { title: string; body: string } {
  const prefix = KIND_WORDS[title];
  const cut = body.indexOf(" - ");
  if (prefix !== undefined && cut > 0) {
    return { title: brief(prefix + body.slice(0, cut)), body: brief(body.slice(cut + 3)) };
  }
  return { title: brief(title), body: brief(body) };
}
