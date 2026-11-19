// 비스듬히 내려다본 방의 좌표. 화면(그리기)과 서버(먼지가 가려지는 칸 피하기)가 같이 쓴다.

export const TW = 56; // 칸의 화면 너비
export const TH = 28; // 칸의 화면 높이
export const UH = 28; // 높이 1 의 화면 길이

/** 바닥 칸 (gx, gy) 와 높이 z 를 화면 좌표로. gx 는 오른쪽 아래, gy 는 왼쪽 아래로 늘어난다. */
export function proj(gx: number, gy: number, z = 0): [number, number] {
  return [(gx - gy) * (TW / 2), (gx + gy) * (TH / 2) - z * UH];
}

/** 다각형 안에 점이 있나(짝수-홀수 규칙) */
export function inside(x: number, y: number, poly: [number, number][]) {
  let hit = false;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
    const [xi, yi] = poly[i];
    const [xj, yj] = poly[j];
    if (yi > y !== yj > y && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) hit = !hit;
  }
  return hit;
}
