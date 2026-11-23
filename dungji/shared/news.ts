// 둘 중 한 사람이 우리 둥지에서 한 일, 그리고 그것을 상대에게 알리는 말.
// 서버(worker/live.ts)는 한 번 들른 동안 한 일을 모아 상대의 jarvis 알림 한 통으로 보내고,
// 화면(src/pages/Home.tsx)은 상대가 지금 둥지를 보고 있으면 둥지 안의 알림 한 줄로 띄운다.

export type Act =
  | { k: "diary"; day: string; first: boolean }
  | { k: "comment" }
  | { k: "reply" }
  | { k: "buy"; name: string }
  | { k: "fish"; name: string }
  | { k: "theme"; part: "wallpaper" | "floor"; name: string }
  | { k: "harvest"; name: string; what: string }
  | { k: "water"; name: string }
  | { k: "feed" }
  | { k: "scrub" }
  | { k: "clean" }
  | { k: "decorate" }
  | { k: "expand" }
  | { k: "sell"; name: string }
  | { k: "naming" }
  | { k: "release"; name: string }
  | { k: "home"; field: "name" | "intro" | "since" };

/** 눈앞에서 보이는 일: 상대가 둥지를 보고 있을 때는 한 줄로 띄우지 않는다(옮기는 대로 방이 바뀐다). */
const QUIET = new Set<Act["k"]>(["decorate", "clean"]);

/** 받침: 0 없음, 1 있음, 2 ㄹ. 한글이 아니면 없는 것으로 본다. */
function batchim(word: string) {
  const code = word.charCodeAt(word.length - 1);
  if (code < 0xac00 || code > 0xd7a3) return 0;
  const end = (code - 0xac00) % 28;
  return end === 0 ? 0 : end === 8 ? 2 : 1;
}

const subject = (w: string) => w + (batchim(w) ? "이" : "가");
const object = (w: string) => w + (batchim(w) ? "을" : "를");
const and = (w: string) => w + (batchim(w) ? "과" : "와");
const toward = (w: string) => w + (batchim(w) === 1 ? "으로" : "로");

/** 일기의 날: 오늘, 어제, 11월 20일 */
function dayWord(day: string, today: string) {
  if (day === today) return "오늘";
  const yesterday = new Date(Date.parse(`${today}T00:00:00Z`) - 86_400_000).toISOString().slice(0, 10);
  if (day === yesterday) return "어제";
  return `${Number(day.slice(5, 7))}월 ${Number(day.slice(8, 10))}일`;
}

function only<K extends Act["k"]>(acts: Act[], k: K) {
  return acts.filter((a): a is Extract<Act, { k: K }> => a.k === k);
}

const distinct = <T>(xs: T[]) => [...new Set(xs)];

/** 산 물건, 판 물건: 하나면 이름, 둘이면 둘 다, 셋부터는 '… 등 n개' */
function things(names: string[], verb: string) {
  const kinds = distinct(names);
  if (names.length === 1) return `${object(names[0])} ${verb}`;
  if (kinds.length === 1) return `${names[0]} ${names.length}개를 ${verb}`;
  if (names.length === 2) return `${and(names[0])} ${object(names[1])} ${verb}`;
  return `${names[0]} 등 ${names.length}개를 ${verb}`;
}

/** 한 일을 말로: 중요한 것부터(일기, 댓글, 산 것, 돌본 것, 꾸민 것). 같은 일은 한 줄로 모은다. */
export function phrases(acts: Act[], today: string): string[] {
  const said: string[] = [];
  const diaries = only(acts, "diary");
  const written = distinct(diaries.filter((d) => d.first).map((d) => d.day));
  const edited = distinct(diaries.filter((d) => !d.first && !written.includes(d.day)).map((d) => d.day));
  if (written.length === 1) said.push(`${dayWord(written[0], today)} 일기를 썼어요`);
  if (written.length > 1) said.push(`일기 ${written.length}편을 썼어요`);

  const comments = only(acts, "comment").length;
  if (comments) said.push(comments === 1 ? "일기에 댓글을 달았어요" : `일기에 댓글 ${comments}개를 달았어요`);
  const replies = only(acts, "reply").length;
  if (replies) said.push(replies === 1 ? "방명록에 답글을 달았어요" : `방명록에 답글 ${replies}개를 달았어요`);

  const bought = only(acts, "buy").map((a) => a.name);
  if (bought.length) said.push(things(bought, "샀어요"));
  const fish = only(acts, "fish").map((a) => a.name);
  if (fish.length) said.push(fish.length === 1 ? `${object(fish[0])} 어항에 들였어요` : `물고기 ${fish.length}마리를 어항에 들였어요`);
  const themes = only(acts, "theme");
  const paper = themes.filter((t) => t.part === "wallpaper").at(-1);
  const floor = themes.filter((t) => t.part === "floor").at(-1);
  if (paper && floor) said.push("벽지와 바닥을 바꿨어요");
  else if (paper) said.push(`벽지를 ${toward(paper.name)} 바꿨어요`);
  else if (floor) said.push(`바닥을 ${toward(floor.name)} 바꿨어요`);

  const harvests = only(acts, "harvest");
  if (harvests.length === 1) said.push(`${harvests[0].name}에서 ${object(harvests[0].what)} 거뒀어요`);
  if (harvests.length > 1) said.push(`화분 ${harvests.length}개에서 거뒀어요`);
  const watered = only(acts, "water").map((a) => a.name);
  if (watered.length === 1) said.push(`${watered[0]}에 물을 줬어요`);
  else if (watered.length && distinct(watered).length === 1) said.push(`${watered[0]} ${watered.length}개에 물을 줬어요`);
  else if (watered.length) said.push(`화분 ${watered.length}개에 물을 줬어요`);
  if (only(acts, "feed").length) said.push("물고기 밥을 줬어요");
  if (only(acts, "scrub").length) said.push("어항 물을 갈았어요");
  const cleaned = only(acts, "clean").length;
  if (cleaned) said.push(cleaned === 1 ? "먼지를 치웠어요" : `먼지 ${cleaned}개를 치웠어요`);

  if (only(acts, "decorate").length) said.push("방을 꾸몄어요");
  const expanded = only(acts, "expand").length;
  if (expanded) said.push(expanded === 1 ? "창고를 한 칸 넓혔어요" : `창고를 ${expanded}칸 넓혔어요`);
  const sold = only(acts, "sell").map((a) => a.name);
  if (sold.length) said.push(things(sold, "팔았어요"));
  if (only(acts, "naming").length) said.push("물고기에게 이름을 지어 줬어요");
  const released = only(acts, "release").map((a) => a.name);
  if (released.length) said.push(released.length === 1 ? `${object(released[0])} 보내 줬어요` : `물고기 ${released.length}마리를 보내 줬어요`);
  const fields = distinct(only(acts, "home").map((a) => a.field));
  if (fields.length > 1) said.push("둥지 정보를 바꿨어요");
  else if (fields[0] === "name") said.push("둥지 이름을 바꿨어요");
  else if (fields[0] === "intro") said.push("한 줄 소개를 바꿨어요");
  else if (fields[0] === "since") said.push("처음 만난 날을 적었어요");

  if (edited.length) said.push(edited.length === 1 ? `${dayWord(edited[0], today)} 일기를 고쳤어요` : "일기를 고쳤어요");
  return said;
}

/** 본문에 다 적는 한 일의 수. 넘는 것은 '외 n가지'로. */
const BODY_PHRASES = 3;

/**
 * 상대의 jarvis 로 가는 알림 한 통: 제목은 가장 중요한 한 일, 본문은 '둥지'와 나머지.
 * who 는 한 사람의 이름(이름을 아직 안 정했으면 '상대').
 */
export function notice(who: string, acts: Act[], today: string): { title: string; body: string } | null {
  const [first, ...rest] = phrases(acts, today);
  if (!first) return null;
  const shown = rest.slice(0, BODY_PHRASES);
  const more = rest.length - shown.length;
  return { title: `${subject(who)} ${first}`, body: ["둥지", ...shown, ...(more > 0 ? [`외 ${more}가지`] : [])].join(" · ") };
}

/** 상대가 둥지를 보고 있을 때 띄우는 한 줄. 눈앞에서 보이는 일뿐이면 null. */
export function liveLine(who: string, acts: Act[], today: string): string | null {
  const [first] = phrases(
    acts.filter((a) => !QUIET.has(a.k)),
    today,
  );
  return first ? `${subject(who)} ${first}` : null;
}

/** 이름을 아직 정하지 않은 사람(jarvis 로 처음 들어와 이름을 묻는 중)을 상대에게 부르는 말 */
export const UNNAMED = "상대";
