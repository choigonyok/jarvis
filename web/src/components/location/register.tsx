"use client";

import { useEffect, useRef, useState } from "react";
import { LocateFixed, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { Spot } from "@/components/location/day-map";
import { type LatLon, type Who, ago, object, registerRegion, toward } from "@/lib/track";

/** Where the place is taken from: the browser, now - or what the phone last sent. */
export type Here = LatLon & { acc: number; at: number; from: "browser" | "phone" };

/** Every region's radius, here and on the phones (context-svc's place.RegionRadius). */
export const RADIUS = 50;

/** Names offered first: the two places most people register. */
const COMMON = ["집", "회사"];


/**
 * 위치 등록, in the panel under the map: where the viewer is now becomes one of
 * their own phone's regions - 50 m, by the name given here (one of that name
 * already there moves). Meanwhile the map shows the circle it will be.
 *
 * The place is the browser's own fix, asked for at once; without one (no
 * permission, no fix), what the phone last sent. Done, it says how the region
 * reaches the phone: with its next report, or now through OwnTracks' own link.
 * That link is a second tap on purpose - iOS Safari follows a jump to another
 * app only straight from a tap, not after a request (see PhoneSetup).
 */
export function RegisterPanel({
  who,
  names,
  fallback,
  readAt,
  onSpot,
  onClose,
  onMade,
}: {
  who: Who;
  /** The person's regions already there: offered as names, and moved if chosen. */
  names: string[];
  /** The phone's last trusted fix, for when the browser gives none. */
  fallback: Here | null;
  /** When the phone's fixes were read: how old the fallback is is said from it. */
  readAt: number;
  onSpot: (spot: Spot | null) => void;
  onClose: () => void;
  onMade: () => void;
}) {
  const [here, setHere] = useState<Here | null>(null);
  const [finding, setFinding] = useState(true);
  const [why, setWhy] = useState<string | null>(null);
  const [name, setName] = useState("");
  // What the browser's answer, which comes later, draws with.
  const named = useRef("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [made, setMade] = useState<{ name: string; link?: string } | null>(null);

  const show = (h: Here | null, n: string, done = false) =>
    onSpot(h ? { lat: h.lat, lon: h.lon, radius: RADIUS, name: n.trim(), tone: "mine", made: done } : null);

  function miss(reason: string) {
    setFinding(false);
    setWhy(reason);
    if (fallback) {
      setHere(fallback);
      show(fallback, named.current);
    }
  }

  function locate() {
    if (!navigator.geolocation) {
      void Promise.resolve().then(() => miss("이 브라우저는 위치를 알려 주지 않아요."));
      return;
    }
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        const h: Here = {
          lat: pos.coords.latitude,
          lon: pos.coords.longitude,
          acc: Math.round(pos.coords.accuracy),
          at: pos.timestamp,
          from: "browser",
        };
        setHere(h);
        setWhy(null);
        setFinding(false);
        show(h, named.current);
      },
      (err) => miss(err.code === err.PERMISSION_DENIED ? "브라우저의 위치 권한이 꺼져 있어요." : "지금 위치를 잡지 못했어요."),
      { enableHighAccuracy: true, timeout: 15_000, maximumAge: 0 },
    );
  }

  useEffect(() => {
    locate();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- asked once, when the panel opens; again only by hand
  }, []);

  function rename(next: string) {
    setName(next);
    named.current = next;
    setError(null);
    show(here, next);
  }

  async function submit() {
    const n = name.trim();
    if (!here || !n || busy) return;
    if (/[:|]/.test(n) || n.startsWith("+")) {
      // + would make it the region that follows the phone (OwnTracks' +follow).
      setError("이름에 : 나 | 는 쓸 수 없고, + 로 시작할 수 없어요.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await registerRegion(who, n, here);
      setMade({ name: n, link: res.link });
      show(here, n, true);
      onMade();
    } catch (e) {
      setError(e instanceof Error ? e.message : "등록하지 못했어요.");
    } finally {
      setBusy(false);
    }
  }

  const close = (
    <button
      type="button"
      onClick={onClose}
      aria-label="닫기"
      className="tap -mt-1 -mr-2 flex shrink-0 items-center justify-center rounded-full text-dim transition-colors hover:text-foreground focus-ring"
    >
      <X aria-hidden className="size-[18px]" strokeWidth={1.8} />
    </button>
  );

  if (made) {
    return (
      <div aria-live="polite">
        <div className="flex items-start justify-between gap-3">
          <p className="text-title font-medium text-foreground">{object(made.name)} 등록했어요</p>
          {close}
        </div>
        {made.link ? (
          <>
            <p className="mt-1 text-ui leading-[1.6] break-keep text-pretty text-dim">
              OwnTracks에 바로 넣으면 지금부터 이 원 안에서 Significant로 지내요. OwnTracks가 바뀌는 내용을 보여 주면
              Continue를 누르세요.
            </p>
            <Button variant="raised" size="touch" asChild className="mt-3 w-full">
              <a href={made.link}>OwnTracks에 바로 넣기</a>
            </Button>
            <p className="mt-2 text-aside leading-[1.55] text-faint">안 눌러도 폰이 다음에 위치를 보낼 때 들어가요.</p>
          </>
        ) : (
          <p className="mt-1 text-ui leading-[1.6] break-keep text-pretty text-dim">
            폰이 다음에 위치를 보낼 때 OwnTracks에 들어가요. 폰의 영역 목록을 먼저 확인해서, 같은 이름이 있으면 그 영역을 이
            자리로 옮겨요.
          </p>
        )}
      </div>
    );
  }

  const chips = [...new Set([...COMMON, ...names])].slice(0, 6);
  const n = name.trim();
  const moving = n !== "" && names.includes(n);
  const rough = here?.from === "browser" && here.acc > RADIUS;

  return (
    <div>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-title font-medium text-foreground">지금 있는 곳을 내 영역으로</p>
          <p aria-live="polite" className="mt-0.5 text-aside leading-[1.55] break-keep text-dim">
            {finding ? (
              "지금 위치를 찾는 중…"
            ) : here?.from === "browser" ? (
              <>
                지금 위치 <span className="tnum">±{here.acc}m</span>
                {rough ? " · 정확하지 않아요. 창가나 밖에서 다시 찾아 보세요." : ""}
              </>
            ) : here ? (
              `${why ?? ""} 폰이 ${ago(Math.max(0, readAt - here.at))} 보낸 위치를 써요.`
            ) : (
              `${why ?? ""} 위치를 알아야 등록할 수 있어요.`
            )}
          </p>
        </div>
        {close}
      </div>
      {!finding && (here?.from !== "browser" || rough) ? (
        <Button
          variant="quiet"
          size="touch-sm"
          onClick={() => {
            setFinding(true);
            locate();
          }}
          className="-ml-3"
        >
          <LocateFixed aria-hidden strokeWidth={1.8} />
          다시 찾기
        </Button>
      ) : null}

      {/* The chip lit is the name written: typing another one lights none. */}
      <ToggleGroup
        type="single"
        aria-label="이름"
        variant="chip"
        tone="mine"
        size="chip"
        spacing={1.5}
        value={chips.includes(n) ? n : ""}
        onValueChange={(v) => {
          if (v) rename(v);
        }}
        className="mt-3 flex-wrap"
      >
        {chips.map((c) => (
          <ToggleGroupItem key={c} value={c} className="px-3.5 text-ui mouse:px-3">
            {c}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <Input
        surface="glass"
        value={name}
        onChange={(e) => rename(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && void submit()}
        placeholder="다른 이름"
        aria-label="영역 이름"
        maxLength={30}
        enterKeyHint="done"
        className="mt-2"
      />
      <Button variant="raised" size="touch" onClick={() => void submit()} disabled={!here || !n || busy} className="mt-3 w-full">
        {busy ? "등록하는 중…" : n ? `${toward(n)} 등록` : "이름을 정하면 등록할 수 있어요"}
      </Button>
      {error ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {error}
        </p>
      ) : (
        <p className="mt-2 text-aside leading-[1.55] break-keep text-pretty text-faint">
          {moving
            ? `이미 있는 ${object(n)} 이 자리로 옮겨요. 반경은 ${RADIUS}m예요.`
            : `반경 ${RADIUS}m. 이 안에서는 Significant로 배터리를 아끼고, 나서면 바로 Move로 길을 자세히 그려요.`}
        </p>
      )}
    </div>
  );
}
