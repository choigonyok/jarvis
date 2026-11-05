import type { FixedAsset } from "./types.js";

/**
 * 조회할 곳이 없는 고정 자산(주택청약 등). 더 넣지도 빼지도 않을 돈이라 값이
 * 고정이다. 원금에도 같은 금액이 들어가므로 수익은 0으로 남고, 비중 계산에서는
 * 빠진다 - 옮길 수 없는 돈을 리밸런싱 대상으로 세면 목표가 왜곡된다.
 *
 * 그만둔 것(해지한 청약)은 지우지 않고 until(마지막으로 가진 날)을 둔다. 그날까지
 * 찍힌 스냅숏의 총액은 그 금액을 품고 있어서, 원금도 그날까지는 같이 세야 지난
 * 그래프가 맞는다 - 지우면 지난날의 수익이 그만큼 부풀어 보인다. 다음 날부터는
 * 총액·원금·목록 어디에도 없다.
 *
 * 금액은 개인 정보라 코드가 아니라 환경변수에 둔다(공개 저장소):
 *   FIXED_ASSETS='[{"id":"housing-subscription","label":"주택청약","valueKrw":1000000,"until":"2026-11-05"}]'
 * 형식이 틀리면 없는 것으로 보고 로그를 남긴다 - 자산 조회 전체를 멈출 일은 아니다.
 */
export function fixedAssetsFrom(raw: string | undefined): FixedAsset[] {
  const text = raw?.trim();
  if (!text) return [];
  try {
    const parsed = JSON.parse(text) as unknown;
    if (!Array.isArray(parsed)) throw new Error("배열이 아닙니다");
    return parsed.map((f, i) => {
      const { id, label, valueKrw, until } = f as Record<string, unknown>;
      if (typeof label !== "string" || typeof valueKrw !== "number" || !Number.isFinite(valueKrw)) {
        throw new Error(`${i}번째 항목에 label·valueKrw 가 없습니다`);
      }
      if (until !== undefined && (typeof until !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(until))) {
        throw new Error(`${i}번째 항목의 until 은 YYYY-MM-DD 입니다`);
      }
      const asset: FixedAsset = { id: typeof id === "string" ? id : `fixed-${i}`, label, valueKrw };
      if (typeof until === "string") asset.until = until;
      return asset;
    });
  } catch (e) {
    console.error("FIXED_ASSETS 를 읽지 못해 고정 자산 없이 계산합니다:", (e as Error).message);
    return [];
  }
}

/** Whether a fixed asset was held on a day (YYYY-MM-DD): one given up is not, after its last day. */
export function heldOn(f: FixedAsset, date: string): boolean {
  return !f.until || date <= f.until;
}

/** What the fixed assets held on a day add up to. */
export function fixedOn(list: FixedAsset[], date: string): number {
  return list.filter((f) => heldOn(f, date)).reduce((sum, f) => sum + f.valueKrw, 0);
}
