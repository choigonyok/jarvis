export class HttpError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

export function fail(status: number, code: string, message: string): never {
  throw new HttpError(status, code, message);
}

const ID_CHARS = "abcdefghijkmnpqrstuvwxyz23456789";
const CODE_CHARS = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";

function randomFrom(chars: string, len: number) {
  const bytes = crypto.getRandomValues(new Uint8Array(len));
  let out = "";
  for (const b of bytes) out += chars[b % chars.length];
  return out;
}

export const newId = (len = 16) => randomFrom(ID_CHARS, len);
/** 상대에게 알려 줄 초대 코드: 헷갈리는 글자(0·O·1·I)는 뺐다. */
export const inviteCode = () => randomFrom(CODE_CHARS, 6);

export async function sha256hex(text: string) {
  const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export async function hmacHex(secret: string, message: string) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(message));
  return [...new Uint8Array(sig)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

/** 같은 길이의 두 문자열을 시간 차이 없이 비교한다. */
export function sameText(a: string, b: string) {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

/** 받침이 있으면 a, 없으면 b: 조사 고르기(을/를, 은/는, 이/가). */
export function josa(word: string, a: string, b: string) {
  const code = word.charCodeAt(word.length - 1);
  const hangul = code >= 0xac00 && code <= 0xd7a3;
  return word + (hangul && (code - 0xac00) % 28 !== 0 ? a : b);
}

/** 앞뒤 공백을 지우고 길이를 잰다. 글자 수는 화면에 보이는 글자(자모 묶음)로 센다. */
export function text(value: unknown, field: string, min: number, max: number): string {
  const v = typeof value === "string" ? value.trim() : "";
  const len = [...new Intl.Segmenter("ko", { granularity: "grapheme" }).segment(v)].length;
  if (len < Math.max(min, 1) && min > 0) fail(400, "bad_input", `${josa(field, "을", "를")} 적어 주세요.`);
  if (len > max) fail(400, "bad_input", `${josa(field, "은", "는")} ${max}자까지 쓸 수 있어요.`);
  return v;
}

export const isDay = (v: unknown): v is string => typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v);
