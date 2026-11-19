// 서버 부르기. 쓰기에는 우리 화면이라는 머리글을 붙인다.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`/api${path}`, {
      method,
      headers: { "content-type": "application/json", "x-dungji": "1" },
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "same-origin",
    });
  } catch {
    throw new ApiError(0, "offline", "인터넷에 연결되지 않았어요.");
  }
  const json = (await res.json().catch(() => null)) as (T & { error?: string; message?: string }) | null;
  if (!res.ok) throw new ApiError(res.status, json?.error ?? "server", json?.message ?? "잠깐 문제가 생겼어요. 다시 해 주세요.");
  return json as T;
}

export const get = <T>(path: string) => api<T>("GET", path);
export const post = <T>(path: string, body: unknown = {}) => api<T>("POST", path, body);
export const put = <T>(path: string, body: unknown = {}) => api<T>("PUT", path, body);
export const patch = <T>(path: string, body: unknown = {}) => api<T>("PATCH", path, body);
export const del = <T>(path: string) => api<T>("DELETE", path);

/**
 * 비밀번호는 이 브라우저에서 PBKDF2-SHA256 60만 번으로 다진 값만 보낸다.
 * 소금은 아이디에서 만들어 로그인 전에 서버에 묻지 않아도 된다(서버는 한 번 더 다진다).
 */
export async function hashPassword(login: string, password: string) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(password), "PBKDF2", false, ["deriveBits"]);
  const bits = await crypto.subtle.deriveBits(
    { name: "PBKDF2", hash: "SHA-256", salt: new TextEncoder().encode(`dungji:v1:${login}`), iterations: 600_000 },
    key,
    256,
  );
  return [...new Uint8Array(bits)].map((b) => b.toString(16).padStart(2, "0")).join("");
}
