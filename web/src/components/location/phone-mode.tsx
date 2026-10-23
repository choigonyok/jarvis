"use client";

import type { ReactNode } from "react";
import { BatteryLow, LocateFixed } from "lucide-react";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { RADIUS } from "@/components/location/register";
import { type Track, type Watch, type Who, MOVE, SIGNIFICANT, ago, span } from "@/lib/track";
import { cn } from "@/lib/utils";

/** A mode as OwnTracks names it - the name its own settings show. */
export function modeName(mode: number): string {
  if (mode === MOVE) return "Move";
  if (mode === SIGNIFICANT) return "Significant";
  return mode === 0 ? "Manual" : "Quiet";
}

/** Why a phone watches as it does, said plainly. */
function reason(w: Watch): string {
  if (w.downgraded) {
    return `배터리가 ${w.batt}%라 Move를 쉬고 Significant로 지내요. 충전하거나 20%를 넘으면 다시 Move로 돌아가요. 그동안 길은 듬성듬성 그려져요.`;
  }
  switch (w.why) {
    case "region":
      return `${w.region ? `${w.region} 안` : "등록한 영역 안"}이라 위치를 가끔만 보내요.`;
    case "still":
      return "한자리(150m 안)에 15분 넘게 있어서 위치를 가끔만 보내요.";
    case "car":
      return "차로 움직이는 중이라 1분이나 50m마다 위치를 보내요.";
    case "left":
      return "영역을 막 나서서 1분이나 50m마다 위치를 보내요.";
    case "moving":
      return "움직이는 중이라 1분이나 50m마다 위치를 보내요.";
    case "held":
      return "직접 고른 모드라 한 시간 동안 그대로예요.";
  }
}

/**
 * The mode at the end of a person's line, today: a quiet word - or, when the
 * battery keeps the phone out of Move, a red battery and its level, the way
 * the phone itself shows a battery under 20%. A tap says why.
 */
export function ModeTag({ watch, onOpen }: { watch?: Watch; onOpen: () => void }) {
  if (!watch) return null;
  const label = watch.downgraded ? `배터리 ${watch.batt}%, Move 쉬는 중` : `${modeName(watch.mode)} 모드`;
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={`${label} - 이유 보기`}
      title={label}
      className="-my-1 ml-auto flex shrink-0 items-center gap-1 rounded-md px-1.5 py-1 text-[11.5px] text-faint transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {watch.downgraded ? (
        <>
          <BatteryLow aria-hidden className="size-[15px] text-reject" strokeWidth={1.8} />
          <span className="tnum text-reject">{watch.batt}%</span>
        </>
      ) : (
        modeName(watch.mode)
      )}
    </button>
  );
}

/**
 * ⓘ: what 위치 등록 does, how each phone watches now and why, when the mode
 * changes, and the settings both phones are put back to every day.
 */
export function ModesSheet({
  open,
  onOpenChange,
  phones,
  today,
  readAt,
  onRegister,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The viewer's phone first. */
  phones: { who: Who; me: boolean; track?: Track }[];
  today: boolean;
  /** When the phones' state was read. */
  readAt: number;
  onRegister: () => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="bottom" className="inset-x-safe pb-safe max-h-[88dvh] overflow-y-auto border-edge-soft bg-background">
        <SheetHeader className="px-4 pt-4 pb-1 sm:px-6">
          <SheetTitle className="text-[15px] font-medium text-foreground">위치 등록과 모드</SheetTitle>
        </SheetHeader>
        <div className="mx-auto w-full max-w-[40rem] space-y-6 px-4 pb-8 text-[13px] leading-[1.65] text-dim sm:px-6">
          {today ? (
            <Part title="지금">
              <ul className="space-y-3">
                {phones.map((p) => (
                  <Phone key={p.who} me={p.me} track={p.track} readAt={readAt} />
                ))}
              </ul>
            </Part>
          ) : null}

          <Part title="위치 등록">
            <p className="break-keep text-pretty">
              지금 있는 곳을 반경 {RADIUS}m 영역으로 내 폰의 OwnTracks에 넣어요. 이 원 안에 있는 동안 폰은 Significant로
              위치를 가끔만 보내 배터리를 아끼고, 원을 나서는 순간 Move로 바뀌어 길을 자세히 그려요.
            </p>
            <p className="mt-2 break-keep text-pretty">
              이름은 지도의 머문 곳 이름과 도착·출발 알림에 쓰여요. 같은 이름이 이미 있으면 그 영역을 지금 자리로 옮겨요.
            </p>
            <button
              type="button"
              onClick={onRegister}
              className="mt-3 inline-flex h-11 items-center gap-1.5 rounded-lg bg-glass-raised px-4 text-[13.5px] text-foreground transition-colors outline-none hover:bg-white/12 focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-9"
            >
              <LocateFixed aria-hidden className="size-4 text-mine" strokeWidth={1.8} />
              지금 위치 등록하기
            </button>
          </Part>

          <Part title="모드가 바뀌는 때">
            <dl className="space-y-2.5">
              <Rule term="Significant">
                등록한 영역 안에 있을 때, 또는 영역 밖이라도 150m 안에 15분 넘게 머물 때. 위치를 가끔만 보내요.
              </Rule>
              <Rule term="Move">
                영역을 나서는 순간부터, 영역 밖에서 움직이는 동안. 차 안이면 막혀 서 있어도 Move예요. 1분이나 50m마다 위치를
                보내요.
              </Rule>
              <Rule term="배터리 20% 아래">
                충전 중이 아니면 Move 대신 Significant로 지내요. OwnTracks의 다운그레이드와 같은 기준이고, 충전하거나 20%를
                넘으면 다시 Move로 돌아가요.
              </Rule>
            </dl>
          </Part>

          <Part title="매일 맞추는 OwnTracks 설정">
            <p className="break-keep text-pretty">
              하루의 첫 위치 보고 때 두 폰 모두 이 값으로 돌려놔요. 앱에서 손으로 바꿔도 다음 날 다시 맞춰져요.
            </p>
            <dl className="mt-2.5 grid grid-cols-[auto_1fr] gap-x-5 gap-y-1.5">
              <Setting term="확장 데이터">켬 · 모드와 움직임이 함께 와요</Setting>
              <Setting term="다운그레이드">배터리 20%</Setting>
              <Setting term="부정확한 위치 무시">100m 넘게 틀린 위치</Setting>
              <Setting term="Move 간격">1분 · 50m</Setting>
              <Setting term="영역 반경">모두 {RADIUS}m</Setting>
            </dl>
          </Part>
        </div>
      </SheetContent>
    </Sheet>
  );
}

function Part({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h3 className="mb-1.5 text-[13.5px] font-medium text-foreground">{title}</h3>
      {children}
    </section>
  );
}

function Rule({ term, children }: { term: string; children: ReactNode }) {
  return (
    <div>
      <dt className="text-foreground/90">{term}</dt>
      <dd className="break-keep text-pretty">{children}</dd>
    </div>
  );
}

function Setting({ term, children }: { term: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-foreground/85">{term}</dt>
      <dd className="tnum">{children}</dd>
    </>
  );
}

/** One phone, now: its mode and why, its battery, and what is on its way to it. */
function Phone({ me, track, readAt }: { me: boolean; track?: Track; readAt: number }) {
  const w = track?.watch;
  const pending = track?.pending ?? [];
  const since = w ? Math.max(0, readAt - Date.parse(w.at)) : 0;
  return (
    <li>
      <p className="flex items-center gap-2 text-foreground">
        <span aria-hidden className={cn("size-[6px] shrink-0 rounded-full", me ? "bg-mine" : "bg-partner")} />
        {me ? "내 폰" : "상대 폰"}
        {w ? (
          <span className={cn("flex items-center gap-1", w.downgraded ? "text-reject" : "text-dim")}>
            {w.downgraded ? <BatteryLow aria-hidden className="size-[15px]" strokeWidth={1.8} /> : null}
            {modeName(w.mode)}
          </span>
        ) : null}
      </p>
      <div className="pl-[14px]">
        {w ? (
          <>
            <p className="break-keep text-pretty">{reason(w)}</p>
            <p className="tnum text-[12px] text-faint">
              {w.batt ? `배터리 ${w.batt}%${w.charging ? " 충전 중" : ""} · ` : ""}
              {ago(since)} 판단
              {since > 6 * 3_600_000 ? ` · 그 뒤로 ${span(since)} 동안 위치가 없어요` : ""}
            </p>
          </>
        ) : (
          <p>아직 위치가 들어오지 않아 모드를 정하지 않았어요.</p>
        )}
        {pending.length ? (
          <p className="mt-1 break-keep text-pretty">
            OwnTracks에 넣는 중: <span className="text-foreground/85">{pending.join(", ")}</span> · 폰이 다음에 위치를 보낼 때
            들어가요.
          </p>
        ) : null}
      </div>
    </li>
  );
}
