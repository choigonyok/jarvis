import { useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { failureText } from "@/lib/say";
import { dropPush, ensurePush } from "@/lib/use-push";

const FAILED = "로그아웃하지 못했어요.";

/**
 * 로그아웃: the header's button on a wide screen and on the guest's phone,
 * and on a phone 더보기's - its sheet, and the list a held 더보기 brings up.
 *
 * It waits on the network two or three times before the page goes; the
 * button said nothing all that while, and was pressed again. busy stands from
 * the press until the page goes. failed is what to say, in red, where it was
 * pressed: the session is still there, and going to the login anyway had the
 * login send the console straight back, as if signing out had worked.
 */
export function useSignOut(): { signOut: () => Promise<void>; busy: boolean; failed: string | null } {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const going = useRef(false);
  // A sign-out that did not go makes this device's subscription again; one
  // pressed again waits for that, or the subscription made could land after
  // the one taken off and keep the alerts coming to a signed-out device.
  const restoring = useRef<Promise<unknown>>(Promise.resolve());

  const signOut = async () => {
    if (going.current) return;
    going.current = true;
    setBusy(true);
    setFailed(null);
    const stop = (why: string) => {
      going.current = false;
      setBusy(false);
      setFailed(why);
      restoring.current = ensurePush();
    };
    await restoring.current;
    // 세션이 남아 있을 때 지운다 - 로그아웃한 뒤에는 notify 에 닿지 못한다.
    await dropPush().catch(() => {});
    let res: Response;
    try {
      res = await fetch("/api/auth/logout", { method: "POST" });
    } catch (err) {
      stop(failureText(err, FAILED));
      return;
    }
    if (!res.ok) {
      stop(FAILED);
      return;
    }
    const body = (await res.json().catch(() => null)) as { next?: string } | null;
    if (body?.next && body.next !== "/login") {
      // Cloudflare Access's own sign-out: a full page load, not a client route.
      window.location.href = body.next;
      return;
    }
    router.replace("/login");
    router.refresh();
  };

  return { signOut, busy, failed };
}
