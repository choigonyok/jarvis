import { useRouter } from "next/navigation";
import { dropPush } from "@/lib/use-push";

/** 로그아웃: the header's button on a wide screen, 더보기's on a phone. */
export function useSignOut() {
  const router = useRouter();
  return async () => {
    // 세션이 남아 있을 때 지운다 - 로그아웃한 뒤에는 notify 에 닿지 못한다.
    await dropPush();
    const res = await fetch("/api/auth/logout", { method: "POST" }).catch(() => null);
    const body = (await res?.json().catch(() => null)) as { next?: string } | null;
    if (body?.next && body.next !== "/login") {
      // Cloudflare Access's own sign-out: a full page load, not a client route.
      window.location.href = body.next;
      return;
    }
    router.replace("/login");
    router.refresh();
  };
}
