// 상점에서 파는 모든 것. 서버(값·크기 검사)와 화면(그림·이름)이 같은 목록을 본다.

/** 방 바닥은 GRID × GRID 칸이다. */
export const GRID = 7;

export type ItemKind = "floor" | "wall" | "plant" | "tank";

export interface ItemDef {
  key: string;
  name: string;
  kind: ItemKind;
  price: number;
  /** 바닥 물건: x 방향(오른쪽 아래)으로 w 칸, y 방향(왼쪽 아래)으로 d 칸. 벽 물건: 벽을 따라 w 칸. */
  w: number;
  d: number;
  /** 러그처럼 바닥에 깔려 다른 물건 아래에 놓이는 것 */
  under?: boolean;
  /** 그림의 대략적인 높이(칸 단위): 뒤쪽 칸을 얼마나 가리는지 */
  h: number;
  blurb: string;
}

export interface PlantDef {
  key: string;
  /** 물을 가득 준 뒤 다 마를 때까지 걸리는 시간 */
  waterHours: number;
  /** 단계마다 그 단계에 닿는 데 필요한 자란 시간(시간). 마지막 단계가 다 자란 것. */
  stages: number[];
  /** 다 자랐을 때 거두면 받는 깃털 */
  reward: number;
  /** 거두는 것의 이름: 꽃, 열매, 새 잎 */
  harvest: string;
}

export interface TankDef {
  key: string;
  capacity: number;
}

export interface FishDef {
  key: string;
  name: string;
  price: number;
  body: string;
  fin: string;
}

export interface ThemeDef {
  key: string;
  name: string;
  kind: "wallpaper" | "floor";
  price: number;
}

export const ITEMS: ItemDef[] = [
  // 바닥 가구
  { key: "bed_wood", name: "원목 침대", kind: "floor", price: 240, w: 2, d: 3, h: 1.1, blurb: "둘이 누워도 넉넉한 침대" },
  { key: "sofa_two", name: "둘이 앉는 소파", kind: "floor", price: 180, w: 2, d: 1, h: 0.9, blurb: "영화 볼 때 딱 붙어 앉는 자리" },
  { key: "table_low", name: "낮은 탁자", kind: "floor", price: 70, w: 1, d: 1, h: 0.55, blurb: "귤 까먹기 좋은 높이" },
  { key: "table_dining", name: "둘이 밥 먹는 식탁", kind: "floor", price: 150, w: 2, d: 1, h: 0.85, blurb: "마주 앉는 작은 식탁" },
  { key: "chair_wood", name: "나무 의자", kind: "floor", price: 50, w: 1, d: 1, h: 1.05, blurb: "등받이가 둥근 의자" },
  { key: "shelf_book", name: "책장", kind: "floor", price: 120, w: 1, d: 1, h: 2.2, blurb: "같이 읽은 책이 꽂힌 책장" },
  { key: "lamp_floor", name: "스탠드", kind: "floor", price: 80, w: 1, d: 1, h: 1.8, blurb: "밤이 되면 불이 켜진다" },
  { key: "desk", name: "책상", kind: "floor", price: 160, w: 2, d: 1, h: 1.2, blurb: "노트북이 올라간 책상" },
  { key: "fridge_mini", name: "작은 냉장고", kind: "floor", price: 140, w: 1, d: 1, h: 1.42, blurb: "야식 보관용" },
  { key: "record_player", name: "레코드 플레이어", kind: "floor", price: 130, w: 1, d: 1, h: 0.7, blurb: "판이 천천히 돈다" },
  { key: "tv_stand", name: "TV 장", kind: "floor", price: 200, w: 2, d: 1, h: 1.45, blurb: "주말 밤의 단골 자리" },
  { key: "wardrobe", name: "옷장", kind: "floor", price: 220, w: 1, d: 2, h: 2.4, blurb: "둘의 옷이 반반" },
  { key: "cat_tower", name: "캣타워", kind: "floor", price: 150, w: 1, d: 1, h: 2.0, blurb: "고양이는 아직 없지만" },
  { key: "easel", name: "이젤", kind: "floor", price: 90, w: 1, d: 1, h: 1.6, blurb: "그리다 만 그림 하나" },
  { key: "cushion", name: "방석 두 개", kind: "floor", price: 30, w: 1, d: 1, h: 0.15, blurb: "바닥에 앉을 때" },
  { key: "snack_box", name: "간식 상자", kind: "floor", price: 25, w: 1, d: 1, h: 0.6, blurb: "뚜껑이 반쯤 열려 있다" },
  { key: "rug_round", name: "동그란 러그", kind: "floor", price: 60, w: 2, d: 2, under: true, h: 0, blurb: "가구 아래에 깔 수 있다" },
  { key: "rug_stripe", name: "줄무늬 러그", kind: "floor", price: 80, w: 2, d: 3, under: true, h: 0, blurb: "가구 아래에 깔 수 있다" },
  // 화분 (키우기)
  { key: "plant_tulip", name: "튤립 화분", kind: "plant", price: 60, w: 1, d: 1, h: 1.5, blurb: "사흘이면 꽃이 핀다" },
  { key: "plant_sunflower", name: "해바라기 화분", kind: "plant", price: 90, w: 1, d: 1, h: 1.8, blurb: "물을 자주 마신다" },
  { key: "plant_monstera", name: "몬스테라", kind: "plant", price: 120, w: 1, d: 1, h: 1.5, blurb: "느긋하게 새 잎을 낸다" },
  { key: "plant_cactus", name: "선인장", kind: "plant", price: 40, w: 1, d: 1, h: 1.2, blurb: "닷새에 한 번 물을 줘도 된다" },
  { key: "plant_tomato", name: "방울토마토", kind: "plant", price: 100, w: 1, d: 1, h: 1.6, blurb: "익으면 따서 깃털로 바꾼다" },
  // 어항
  { key: "tank_small", name: "작은 어항", kind: "tank", price: 150, w: 1, d: 1, h: 1.3, blurb: "물고기 세 마리까지" },
  { key: "tank_big", name: "큰 수조", kind: "tank", price: 400, w: 2, d: 1, h: 1.3, blurb: "물고기 여섯 마리까지" },
  // 벽 장식
  { key: "wall_window", name: "창문", kind: "wall", price: 120, w: 2, d: 0, h: 0, blurb: "바깥 하늘이 시간 따라 바뀐다" },
  { key: "wall_clock", name: "벽시계", kind: "wall", price: 70, w: 1, d: 0, h: 0, blurb: "지금 시각을 가리킨다" },
  { key: "wall_frame", name: "그림 액자", kind: "wall", price: 50, w: 1, d: 0, h: 0, blurb: "바다 그림" },
  { key: "wall_shelf", name: "벽 선반", kind: "wall", price: 60, w: 1, d: 0, h: 0, blurb: "작은 화분과 컵" },
  { key: "wall_garland", name: "가랜드", kind: "wall", price: 40, w: 2, d: 0, h: 0, blurb: "삼각 깃발 줄" },
  { key: "wall_polaroid", name: "사진 줄", kind: "wall", price: 55, w: 2, d: 0, h: 0, blurb: "집게로 건 사진 세 장" },
  { key: "wall_poster", name: "포스터", kind: "wall", price: 45, w: 1, d: 0, h: 0, blurb: "좋아하는 영화" },
];

export const PLANTS: PlantDef[] = [
  { key: "plant_tulip", waterHours: 36, stages: [0, 8, 24, 48, 72], reward: 40, harvest: "꽃" },
  { key: "plant_sunflower", waterHours: 28, stages: [0, 10, 30, 60, 96], reward: 60, harvest: "꽃" },
  { key: "plant_monstera", waterHours: 48, stages: [0, 12, 36, 72, 120], reward: 50, harvest: "새 잎" },
  { key: "plant_cactus", waterHours: 120, stages: [0, 24, 72, 144, 240], reward: 45, harvest: "꽃" },
  { key: "plant_tomato", waterHours: 30, stages: [0, 10, 30, 60, 90], reward: 70, harvest: "열매" },
];

export const TANKS: TankDef[] = [
  { key: "tank_small", capacity: 3 },
  { key: "tank_big", capacity: 6 },
];

export const FISH: FishDef[] = [
  { key: "fish_gold", name: "금붕어", price: 30, body: "#F08A3C", fin: "#F6B46B" },
  { key: "fish_guppy", name: "구피", price: 40, body: "#7BB6E8", fin: "#E86FA0" },
  { key: "fish_neon", name: "네온테트라", price: 50, body: "#3FA9C9", fin: "#E5484D" },
  { key: "fish_betta", name: "베타", price: 90, body: "#8E4FC2", fin: "#C77DDB" },
  { key: "fish_puffer", name: "복어", price: 120, body: "#E8D27A", fin: "#B9A24E" },
];

export const THEMES: ThemeDef[] = [
  { key: "paper_plain", name: "하얀 벽", kind: "wallpaper", price: 0 },
  { key: "paper_stripe", name: "세로 줄무늬", kind: "wallpaper", price: 80 },
  { key: "paper_dot", name: "물방울", kind: "wallpaper", price: 80 },
  { key: "paper_check", name: "체크", kind: "wallpaper", price: 100 },
  { key: "paper_wood", name: "나무 벽", kind: "wallpaper", price: 120 },
  { key: "paper_star", name: "밤하늘", kind: "wallpaper", price: 150 },
  { key: "floor_wood", name: "원목 마루", kind: "floor", price: 0 },
  { key: "floor_tile", name: "하얀 타일", kind: "floor", price: 80 },
  { key: "floor_carpet", name: "카펫", kind: "floor", price: 100 },
  { key: "floor_herring", name: "헤링본 마루", kind: "floor", price: 150 },
  { key: "floor_grass", name: "잔디", kind: "floor", price: 200 },
];

const byKey = <T extends { key: string }>(list: T[]) => new Map(list.map((x) => [x.key, x]));
const ITEM_MAP = byKey(ITEMS);
const PLANT_MAP = byKey(PLANTS);
const TANK_MAP = byKey(TANKS);
const FISH_MAP = byKey(FISH);
const THEME_MAP = byKey(THEMES);

export const itemDef = (key: string) => ITEM_MAP.get(key);
export const plantDef = (key: string) => PLANT_MAP.get(key);
export const tankDef = (key: string) => TANK_MAP.get(key);
export const fishDef = (key: string) => FISH_MAP.get(key);
export const themeDef = (key: string) => THEME_MAP.get(key);

/** 처음 둥지를 만들면 이 물건들이 놓여 있다. */
export const STARTER: { key: string; x: number; y: number; flip?: boolean; wall?: "left" | "right" }[] = [
  { key: "rug_round", x: 3, y: 3 },
  { key: "bed_wood", x: 0, y: 1 },
  { key: "lamp_floor", x: 0, y: 4 },
  { key: "plant_tulip", x: 5, y: 0 },
  { key: "wall_window", x: 3, y: 0, wall: "right" },
  { key: "wall_clock", x: 4, y: 0, wall: "left" },
];
