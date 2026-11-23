import { describe, expect, it } from "vitest";
import { liveLine, notice, phrases, type Act } from "../shared/news";
import { add, again, CAP, due, GRACE, IDLE, nextWake, RETRY, take, type Book } from "../worker/tell";

const TODAY = "2026-11-23";

describe("한 일을 말로", () => {
  it("한 가지는 제목에, 본문은 둥지", () => {
    expect(notice("바다", [{ k: "water", name: "몬스테라" }], TODAY)).toEqual({ title: "바다가 몬스테라에 물을 줬어요", body: "둥지" });
    expect(notice("하늘", [{ k: "diary", day: TODAY, first: true }], TODAY)?.title).toBe("하늘이 오늘 일기를 썼어요");
  });

  it("중요한 것이 제목, 나머지는 본문에 차례로", () => {
    const acts: Act[] = [
      { k: "clean" },
      { k: "clean" },
      { k: "water", name: "튤립 화분" },
      { k: "decorate" },
      { k: "diary", day: TODAY, first: true },
      { k: "clean" },
    ];
    expect(notice("바다", acts, TODAY)).toEqual({
      title: "바다가 오늘 일기를 썼어요",
      body: "둥지 · 튤립 화분에 물을 줬어요 · 먼지 3개를 치웠어요 · 방을 꾸몄어요",
    });
  });

  it("본문은 세 가지까지, 나머지는 '외 n가지'", () => {
    const acts: Act[] = [{ k: "comment" }, { k: "buy", name: "스탠드" }, { k: "feed" }, { k: "scrub" }, { k: "clean" }, { k: "expand" }];
    expect(notice("바다", acts, TODAY)?.body).toBe("둥지 · 스탠드를 샀어요 · 물고기 밥을 줬어요 · 어항 물을 갈았어요 · 외 2가지");
  });

  it("같은 일은 모아서 센다", () => {
    expect(phrases([{ k: "buy", name: "스탠드" }], TODAY)).toEqual(["스탠드를 샀어요"]);
    expect(phrases([{ k: "buy", name: "책장" }, { k: "buy", name: "스탠드" }], TODAY)).toEqual(["책장과 스탠드를 샀어요"]);
    expect(phrases([{ k: "buy", name: "스탠드" }, { k: "buy", name: "책장" }, { k: "buy", name: "옷장" }], TODAY)).toEqual(["스탠드 등 3개를 샀어요"]);
    expect(phrases([{ k: "buy", name: "나무 의자" }, { k: "buy", name: "나무 의자" }], TODAY)).toEqual(["나무 의자 2개를 샀어요"]);
    expect(phrases([{ k: "water", name: "튤립 화분" }, { k: "water", name: "튤립 화분" }], TODAY)).toEqual(["튤립 화분 2개에 물을 줬어요"]);
    expect(phrases([{ k: "water", name: "선인장" }, { k: "water", name: "몬스테라" }], TODAY)).toEqual(["화분 2개에 물을 줬어요"]);
    expect(phrases([{ k: "comment" }, { k: "comment" }], TODAY)).toEqual(["일기에 댓글 2개를 달았어요"]);
    expect(phrases([{ k: "clean" }], TODAY)).toEqual(["먼지를 치웠어요"]);
    expect(phrases([{ k: "fish", name: "금붕어" }], TODAY)).toEqual(["금붕어를 어항에 들였어요"]);
    expect(phrases([{ k: "fish", name: "금붕어" }, { k: "fish", name: "베타" }], TODAY)).toEqual(["물고기 2마리를 어항에 들였어요"]);
    expect(phrases([{ k: "harvest", name: "방울토마토", what: "열매" }], TODAY)).toEqual(["방울토마토에서 열매를 거뒀어요"]);
    expect(phrases([{ k: "release", name: "뽀삐" }], TODAY)).toEqual(["뽀삐를 보내 줬어요"]);
    expect(phrases([{ k: "expand" }, { k: "expand" }], TODAY)).toEqual(["창고를 2칸 넓혔어요"]);
  });

  it("벽지·바닥은 마지막에 고른 것으로, 받침에 맞는 조사", () => {
    expect(phrases([{ k: "theme", part: "wallpaper", name: "체크" }, { k: "theme", part: "wallpaper", name: "밤하늘" }], TODAY)).toEqual([
      "벽지를 밤하늘로 바꿨어요",
    ]);
    expect(phrases([{ k: "theme", part: "floor", name: "카펫" }], TODAY)).toEqual(["바닥을 카펫으로 바꿨어요"]);
    expect(phrases([{ k: "theme", part: "floor", name: "카펫" }, { k: "theme", part: "wallpaper", name: "체크" }], TODAY)).toEqual([
      "벽지와 바닥을 바꿨어요",
    ]);
  });

  it("일기: 날, 처음 쓴 것과 고친 것", () => {
    expect(phrases([{ k: "diary", day: "2026-11-22", first: true }], TODAY)).toEqual(["어제 일기를 썼어요"]);
    expect(phrases([{ k: "diary", day: "2026-11-02", first: true }], TODAY)).toEqual(["11월 2일 일기를 썼어요"]);
    // 같은 날을 쓰고 고쳤으면 쓴 것만
    expect(phrases([{ k: "diary", day: TODAY, first: true }, { k: "diary", day: TODAY, first: false }], TODAY)).toEqual(["오늘 일기를 썼어요"]);
    expect(notice("바다", [{ k: "diary", day: TODAY, first: false }, { k: "clean" }], TODAY)?.title).toBe("바다가 먼지를 치웠어요");
    expect(phrases([{ k: "diary", day: "2026-12-31", first: true }], "2027-01-01")).toEqual(["어제 일기를 썼어요"]);
  });

  it("둥지 정보", () => {
    expect(phrases([{ k: "home", field: "intro" }], TODAY)).toEqual(["한 줄 소개를 바꿨어요"]);
    expect(phrases([{ k: "home", field: "name" }, { k: "home", field: "since" }], TODAY)).toEqual(["둥지 정보를 바꿨어요"]);
  });

  it("보고 있을 때 띄우는 한 줄은 눈앞에서 보이는 일(꾸미기, 청소)을 빼고", () => {
    expect(liveLine("바다", [{ k: "decorate" }], TODAY)).toBeNull();
    expect(liveLine("바다", [{ k: "clean" }], TODAY)).toBeNull();
    expect(liveLine("바다", [{ k: "water", name: "몬스테라" }], TODAY)).toBe("바다가 몬스테라에 물을 줬어요");
  });

  it("한 일이 없으면 알리지 않는다", () => {
    expect(notice("바다", [], TODAY)).toBeNull();
  });
});

describe("언제 보내나", () => {
  const T = 1_000_000;

  it("보고 있는 동안은 IDLE·CAP, 떠나면 GRACE 뒤", () => {
    let p = add(undefined, [{ k: "clean" }], T, true);
    expect(p.left).toBeUndefined();
    expect(due(p)).toBe(T + IDLE);
    p = add(p, [{ k: "feed" }], T + 60_000, true);
    expect(due(p)).toBe(T + 60_000 + IDLE);
    // 들어와 있지 않은 채 한 일(창 없이 부른 것)은 떠난 것으로 센다.
    p = add(p, [{ k: "scrub" }], T + 120_000, false);
    expect(due(p)).toBe(T + 120_000 + GRACE);
    // 계속 무언가 해도 처음에서 CAP 이 지나면 보낸다.
    let q = add(undefined, [{ k: "clean" }], T, true);
    for (let i = 1; i <= 40; i++) q = add(q, [{ k: "clean" }], T + i * 60_000, true);
    expect(due(q)).toBe(T + CAP);
  });

  it("꾸미기는 몇 번을 옮겨도 하나", () => {
    let p = add(undefined, [{ k: "decorate" }], T, true);
    p = add(p, [{ k: "decorate" }, { k: "decorate" }], T + 1, true);
    expect(p.acts).toEqual([{ k: "decorate" }]);
  });

  it("때가 된 것만 떼어 내고, 다음에 깰 때를 안다", () => {
    const book: Book = {
      a: add(undefined, [{ k: "clean" }], T, false),
      b: add(undefined, [{ k: "clean" }], T, true),
    };
    expect(nextWake(book)).toBe(T + GRACE);
    const { ready, rest } = take(book, T + GRACE);
    expect(ready.map(([id]) => id)).toEqual(["a"]);
    expect(Object.keys(rest)).toEqual(["b"]);
    expect(nextWake(rest)).toBe(T + IDLE);
    expect(nextWake({})).toBeNull();
  });

  it("못 보낸 것은 간격을 두고 다시, 그사이 한 일과 합쳐서, 다 실패하면 버린다", () => {
    const sent = add(undefined, [{ k: "clean" }], T, false);
    const later = add(undefined, [{ k: "feed" }], T + 5_000, true);
    const first = again(sent, later, T + 10_000)!;
    expect(first.tries).toBe(1);
    expect(due(first)).toBe(T + 10_000 + RETRY[0]);
    expect(first.acts).toEqual([{ k: "clean" }, { k: "feed" }]);
    expect(first.first).toBe(T);
    let p = first;
    for (let i = 1; i < RETRY.length; i++) p = again(p, undefined, T)!;
    expect(p.tries).toBe(RETRY.length);
    expect(again(p, undefined, T)).toBeUndefined();
    expect(again(p, later, T)).toBe(later);
  });
});
