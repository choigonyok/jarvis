"use client";

import { useRef } from "react";
import { Info } from "lucide-react";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import type { Role } from "@/lib/role";
import { cn } from "@/lib/utils";

/**
 * Something Jarvis can be asked in words. A “quoted” phrase in body is shown
 * as a thing to say, in the look of one's own message.
 */
type Ask = {
  title: string;
  /** As the operator's account does it. */
  body: string;
  /** The guest's own wording, where their account does it differently. */
  guest?: string;
  /** The guest's agent has no tool for it (agent cmd/agent/main.go). */
  ownerOnly?: true;
};

const ASKS: Ask[] = [
  { title: "일정 보기", body: "“이번 주 일정 뭐 있어?”처럼 물으면 바로 알려 줘요." },
  {
    title: "일정 넣기·고치기·지우기",
    body: "“토요일 9시 캠핑 넣어줘”처럼 말하면 카드가 올라오고, 승인하면 공유 캘린더에 반영돼요.",
  },
  {
    title: "리마인드 걸기",
    body: "“3시에 알려줘”, “집에 가면 말해줘”라고 하면 그때 폰과 대화로 알려 줘요. 방해 금지 중에도 울려요.",
    // The guest's places are the regions on the guest's own phone, not the
    // operator's.
    guest: "“3시에 알려줘”, “집에 가면 말해줘”라고 하면 그때 폰으로 알려 줘요. 장소는 위치 탭에 등록한 곳 기준이에요.",
  },
  { title: "리마인드 고치기·취소", body: "“5시로 바꿔줘”, “그거 취소해”라고 하면 맡긴 알림을 고치거나 취소해요." },
  {
    title: "체크리스트 만들기",
    body: "“캠핑 준비물 정리해줘”처럼 말하면 하나씩 체크하는 목록으로 만들어요. 날짜를 정하면 전날 저녁에 남은 것만 다시 알려 줘요.",
  },
  { title: "체크리스트 다시 보기·체크", body: "“뭐 남았어?”, “텐트 챙겼어”라고 하면 목록을 다시 띄우거나 체크해요." },
  {
    title: "작업 맡기기",
    body: "“중고나라에 팔아줘”, “가격 떨어지면 알려줘”처럼 시간이 걸리는 일을 맡기면 뒤에서 해요.",
    // The guest's jobs open no website: jobs-svc refuses one.
    guest: "“매일 아침 뉴스 요약해줘”처럼 시간이 걸리거나 되풀이할 일을 맡기면 뒤에서 해요. 웹사이트 로그인·구매는 못 해요.",
  },
  {
    title: "사진 붙여서 작업 맡기기",
    body: "사진을 붙여 “중고나라에 올려줘”라고 하면 물건과 상태를 보고 작업으로 맡겨요. 가격처럼 정할 값은 카드로 물어요.",
    ownerOnly: true,
  },
  {
    title: "진행 중인 작업에 지시하기",
    body: "“가격 2만 원 내려”, “그 작업 그만해”라고 하면 하던 작업에 전해요.",
    guest: "“요약은 세 줄로 해줘”, “그 작업 그만해”라고 하면 하던 작업에 전해요.",
  },
  {
    title: "쇼핑·결제",
    body: "“쿠팡에서 휴지 사줘”처럼 말하면 결제 직전까지 가고, 결제 카드를 승인하면 마무리해요.",
    ownerOnly: true,
  },
  {
    title: "로그인 정보 저장",
    body: "“쿠팡 로그인 저장해줘”라고 하면 카드가 올라오고, 아이디·비밀번호는 카드에 직접 넣어요. Jarvis는 그 값을 못 봐요.",
    ownerOnly: true,
  },
  {
    title: "웹에서 찾아보기",
    body: "“성수 팝업 찾아줘”처럼 물으면 네이버 블로그·뉴스·카페의 원문까지 읽고 출처와 함께 답해요.",
  },
  {
    title: "근처 가게 찾기",
    body: "“지금 연 술집 있어?”처럼 물으면 지금 있는 곳 근처를 네이버 지도로 찾아요.",
    ownerOnly: true,
  },
  {
    title: "자산 조회",
    body: "“내 자산 어때?”라고 물으면 업비트·한국투자증권·금의 손익과 목표 비중을 알려 줘요. 주문은 대신 못 해요.",
    ownerOnly: true,
  },
  { title: "지출 조회", body: "“이번 달 얼마 썼어?”라고 물으면 지난달 같은 날까지와 비교해 알려 줘요.", ownerOnly: true },
  {
    title: "지난 일 떠올리기",
    body: "“지난번 캠핑 때 뭐 샀지?”처럼 물으면 지난 대화·카톡·일정·지출에서 찾아 답해요.",
    // The guest's memory is drawn from the guest's own conversation only.
    guest: "“지난번에 말한 식당 어디였지?”처럼 물으면 Jarvis와 나눈 대화에서 기억한 것을 찾아 답해요.",
  },
  {
    title: "메시지 원문 찾기",
    body: "“그때 정확히 뭐라고 했지?”처럼 물으면 카톡·iMessage 원문과 앞뒤 대화를 찾아요.",
    ownerOnly: true,
  },
  {
    title: "지금 상황 기준으로 답 받기",
    body: "내 폰 위치와 오늘 일정을 보고 답해요. 틀렸으면 “나 벌써 나왔어”처럼 고쳐 주세요.",
  },
  {
    title: "기억해 두라고 하기",
    body: "“이거 기억해 둬”라고 하면 기억에 적어 두고, 적은 내용은 알림으로 알려 줘요.",
    guest: "“이거 기억해 둬”, “앞으로 반말로 해줘”라고 하면 기억에 적어 두고, 적은 내용은 알림으로 알려 줘요.",
  },
  { title: "사진 보여 주고 묻기", body: "사진을 붙여서 물으면 사진을 보고 답해요." },
];

/** The ⓘ in the chat's header, left of search. */
export function GuideToggle({ open, onOpen }: { open: boolean; onOpen: () => void }) {
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label="말로 시킬 수 있는 일"
      aria-haspopup="dialog"
      aria-expanded={open}
      title="말로 시킬 수 있는 일"
      className={cn(
        "flex size-11 items-center justify-center rounded-lg transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:size-8",
        open ? "text-foreground" : "text-faint hover:text-dim",
      )}
    >
      <Info aria-hidden className="size-[18px] mouse:size-4" />
    </button>
  );
}

/**
 * What can be asked of Jarvis in words, for this account: the guest's agent
 * is built without the operator's money, messages, logins and browser, so
 * the guest is not told of them.
 */
export function GuideSheet({
  open,
  onOpenChange,
  role,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  role: Role;
}) {
  const asks = ASKS.filter((a) => role === "owner" || !a.ownerOnly);
  // Where focus was when it opened - the ⓘ, opened from the keyboard. The
  // sheet has no Radix trigger to hand focus back to (the ⓘ lives in the
  // header, the sheet outside it), so it is handed back here.
  const back = useRef<HTMLElement | null>(null);
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        onOpenAutoFocus={() => {
          back.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        }}
        onCloseAutoFocus={(e) => {
          e.preventDefault();
          back.current?.focus();
        }}
        className="inset-x-safe pb-safe max-h-[88dvh] overflow-y-auto border-edge-soft bg-background"
      >
        <SheetHeader className="mx-auto w-full max-w-[40rem] px-4 pt-4 pb-1 sm:px-6">
          <SheetTitle className="text-[15px] font-medium text-foreground">말로 시킬 수 있는 일</SheetTitle>
          <SheetDescription className="text-[12.5px] text-faint">대화창에 이렇게 말해 보세요.</SheetDescription>
        </SheetHeader>
        <dl className="mx-auto w-full max-w-[40rem] space-y-4 px-4 pb-8 sm:px-6">
          {asks.map((a) => (
            <div key={a.title}>
              <dt className="text-[14px] font-medium text-foreground">{a.title}</dt>
              <dd className="mt-0.5 text-[13px] leading-[1.7] break-keep text-pretty text-dim">
                <Said text={role === "guest" && a.guest ? a.guest : a.body} />
              </dd>
            </div>
          ))}
        </dl>
      </SheetContent>
    </Sheet>
  );
}

/** A line with its “quoted” phrases set apart: what to say looks like a message you sent. */
function Said({ text }: { text: string }) {
  const parts = text.split(/“([^”]+)”/);
  return (
    <>
      {parts.map((part, i) =>
        i % 2 ? (
          <q key={i} className="rounded-md bg-glass-raised px-1.5 py-px whitespace-nowrap text-foreground [quotes:none]">
            {part}
          </q>
        ) : part === ", " && i > 0 && i < parts.length - 1 ? (
          // Between two phrases their own shapes part them; a comma would float.
          " "
        ) : (
          part
        ),
      )}
    </>
  );
}
