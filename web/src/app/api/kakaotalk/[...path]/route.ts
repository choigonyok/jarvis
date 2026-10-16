import { pathProxy } from "@/lib/proxy";

// The KakaoTalk collector. Read-only surface: GET only.
const KAKAOTALK_URL = process.env.KAKAOTALK_URL ?? "http://localhost:8090";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(KAKAOTALK_URL, { label: "카카오톡 수집기" });

export const GET = proxy;
