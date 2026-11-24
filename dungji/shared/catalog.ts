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
  /** 바닥 물건이 나오는 상점 칸(없으면 가구). 러그·화분·어항·벽 장식은 종류로 정한다. */
  group?: ItemGroup;
  /** 밤이 되면 불빛이 번진다 */
  glow?: boolean;
}

/** 바닥 물건의 상점 칸: 가구, 주방·가전, 조명, 장식·취미 */
export type ItemGroup = "furniture" | "kitchen" | "light" | "hobby";

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
  { key: "lamp_floor", name: "스탠드", kind: "floor", group: "light", glow: true, price: 80, w: 1, d: 1, h: 1.8, blurb: "밤이 되면 불이 켜진다" },
  { key: "desk", name: "책상", kind: "floor", price: 160, w: 2, d: 1, h: 1.2, blurb: "노트북이 올라간 책상" },
  { key: "fridge_mini", name: "작은 냉장고", kind: "floor", group: "kitchen", price: 140, w: 1, d: 1, h: 1.42, blurb: "야식 보관용" },
  { key: "record_player", name: "레코드 플레이어", kind: "floor", group: "hobby", price: 130, w: 1, d: 1, h: 0.7, blurb: "판이 천천히 돈다" },
  { key: "tv_stand", name: "TV 장", kind: "floor", group: "kitchen", price: 200, w: 2, d: 1, h: 1.45, blurb: "주말 밤의 단골 자리" },
  { key: "wardrobe", name: "옷장", kind: "floor", price: 220, w: 1, d: 2, h: 2.4, blurb: "둘의 옷이 반반" },
  { key: "cat_tower", name: "캣타워", kind: "floor", group: "hobby", price: 150, w: 1, d: 1, h: 2.0, blurb: "고양이는 아직 없지만" },
  { key: "easel", name: "이젤", kind: "floor", group: "hobby", price: 90, w: 1, d: 1, h: 1.6, blurb: "그리다 만 그림 하나" },
  { key: "cushion", name: "방석 두 개", kind: "floor", group: "hobby", price: 30, w: 1, d: 1, h: 0.15, blurb: "바닥에 앉을 때" },
  { key: "snack_box", name: "간식 상자", kind: "floor", group: "hobby", price: 25, w: 1, d: 1, h: 0.6, blurb: "뚜껑이 반쯤 열려 있다" },
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
  // ── 더한 물건 ──
  // 가구
  { key: "bed_single", name: "싱글 침대", kind: "floor", price: 200, w: 1, d: 2, h: 1.0, blurb: "혼자 뒹굴기 딱 좋은 폭" },
  { key: "bed_bunk", name: "이층 침대", kind: "floor", price: 320, w: 1, d: 2, h: 2.3, blurb: "위층은 가위바위보로" },
  { key: "futon", name: "요와 이불", kind: "floor", price: 90, w: 2, d: 2, h: 0.3, blurb: "펴 두면 하루 종일 눕게 된다" },
  { key: "sofa_corner", name: "코너 소파", kind: "floor", price: 300, w: 2, d: 2, h: 0.9, blurb: "모서리에 쏙 들어간다" },
  { key: "armchair", name: "1인 안락의자", kind: "floor", price: 130, w: 1, d: 1, h: 0.95, blurb: "푹 꺼지는 쿠션" },
  { key: "beanbag", name: "빈백", kind: "floor", price: 70, w: 1, d: 1, h: 0.6, blurb: "앉는 순간 녹아내린다" },
  { key: "rocking_chair", name: "흔들의자", kind: "floor", price: 140, w: 1, d: 1, h: 1.1, blurb: "책 읽다 졸기 좋다" },
  { key: "stool", name: "동그란 스툴", kind: "floor", price: 35, w: 1, d: 1, h: 0.5, blurb: "어디든 들고 다닌다" },
  { key: "bench", name: "나무 벤치", kind: "floor", price: 90, w: 2, d: 1, h: 0.5, blurb: "창가에 두면 쉼터" },
  { key: "gaming_chair", name: "게임 의자", kind: "floor", price: 170, w: 1, d: 1, h: 1.45, blurb: "등받이가 뒤로 쭉 넘어간다" },
  { key: "table_round", name: "작은 원탁", kind: "floor", price: 80, w: 1, d: 1, h: 0.75, blurb: "둘이 마주 앉는 크기" },
  { key: "table_coffee", name: "거실 탁자", kind: "floor", price: 100, w: 2, d: 1, h: 0.45, blurb: "리모컨의 자리" },
  { key: "table_side", name: "협탁", kind: "floor", price: 45, w: 1, d: 1, h: 0.6, blurb: "커피 한 잔 놓을 자리" },
  { key: "table_floor", name: "좌식 밥상", kind: "floor", price: 60, w: 2, d: 1, h: 0.35, blurb: "바닥에 앉아 먹는 밥" },
  { key: "warm_table", name: "이불 덮은 탁자", kind: "floor", price: 150, w: 2, d: 2, h: 0.45, blurb: "겨울엔 여기서 못 나온다" },
  { key: "desk_computer", name: "컴퓨터 책상", kind: "floor", price: 230, w: 2, d: 1, h: 1.35, blurb: "모니터 두 대, 일도 게임도" },
  { key: "vanity", name: "화장대", kind: "floor", price: 180, w: 1, d: 1, h: 1.55, blurb: "거울 앞에서 오래 머문다" },
  { key: "dresser", name: "서랍장", kind: "floor", price: 150, w: 2, d: 1, h: 1.0, blurb: "양말은 맨 위 칸" },
  { key: "drawer_tall", name: "높은 서랍장", kind: "floor", price: 130, w: 1, d: 1, h: 1.6, blurb: "칸칸이 정리 중" },
  { key: "cabinet_low", name: "낮은 수납장", kind: "floor", price: 110, w: 2, d: 1, h: 0.7, blurb: "위에는 작은 소품들" },
  { key: "shoe_rack", name: "신발장", kind: "floor", price: 75, w: 1, d: 1, h: 1.1, blurb: "운동화와 구두가 나란히" },
  { key: "coat_rack", name: "옷걸이 스탠드", kind: "floor", price: 60, w: 1, d: 1, h: 1.8, blurb: "외투와 가방을 건다" },
  { key: "hanger_rack", name: "행거", kind: "floor", price: 110, w: 2, d: 1, h: 1.6, blurb: "내일 입을 옷 미리 걸기" },
  { key: "bookshelf_low", name: "낮은 책장", kind: "floor", price: 95, w: 2, d: 1, h: 0.9, blurb: "만화책 전집" },
  { key: "bookcase_wide", name: "큰 책장", kind: "floor", price: 210, w: 2, d: 1, h: 2.2, blurb: "읽은 책, 읽을 책" },
  { key: "island_bar", name: "바 테이블", kind: "floor", price: 190, w: 2, d: 1, h: 1.05, blurb: "높은 의자에 나란히" },
  { key: "swing_chair", name: "달걀 그네 의자", kind: "floor", price: 260, w: 1, d: 1, h: 1.9, blurb: "매달려 흔들흔들" },
  // 주방·가전
  { key: "fridge_big", name: "양문 냉장고", kind: "floor", group: "kitchen", price: 280, w: 2, d: 1, h: 1.9, blurb: "야식은 오른쪽 문에" },
  { key: "kitchen_counter", name: "주방 조리대", kind: "floor", group: "kitchen", price: 160, w: 2, d: 1, h: 0.95, blurb: "도마와 양념통" },
  { key: "stove", name: "가스레인지", kind: "floor", group: "kitchen", price: 120, w: 1, d: 1, h: 0.95, blurb: "라면 끓는 소리" },
  { key: "sink", name: "개수대", kind: "floor", group: "kitchen", price: 120, w: 1, d: 1, h: 0.95, blurb: "설거지는 가위바위보로" },
  { key: "microwave_cart", name: "전자레인지 선반", kind: "floor", group: "kitchen", price: 95, w: 1, d: 1, h: 1.2, blurb: "데우는 중 1분 30초" },
  { key: "coffee_station", name: "커피 머신", kind: "floor", group: "kitchen", price: 140, w: 1, d: 1, h: 1.15, blurb: "아침의 첫 잔" },
  { key: "washer", name: "세탁기", kind: "floor", group: "kitchen", price: 170, w: 1, d: 1, h: 0.95, blurb: "빙글빙글 돌아간다" },
  { key: "washer_dryer", name: "세탁기와 건조기", kind: "floor", group: "kitchen", price: 290, w: 1, d: 1, h: 1.9, blurb: "포근한 수건이 나온다" },
  { key: "air_purifier", name: "공기청정기", kind: "floor", group: "kitchen", price: 120, w: 1, d: 1, h: 0.85, blurb: "초록 불이면 맑음" },
  { key: "humidifier", name: "가습기", kind: "floor", group: "kitchen", price: 55, w: 1, d: 1, h: 0.55, blurb: "겨울밤의 하얀 김" },
  { key: "fan", name: "선풍기", kind: "floor", group: "kitchen", price: 60, w: 1, d: 1, h: 1.15, blurb: "회전 버튼은 꼭 누른다" },
  { key: "heater", name: "전기난로", kind: "floor", group: "kitchen", glow: true, price: 75, w: 1, d: 1, h: 0.8, blurb: "발끝부터 따뜻해진다" },
  { key: "aircon_stand", name: "스탠드 에어컨", kind: "floor", group: "kitchen", price: 260, w: 1, d: 1, h: 1.85, blurb: "여름의 구세주" },
  { key: "robot_vacuum", name: "로봇 청소기", kind: "floor", group: "kitchen", price: 150, w: 1, d: 1, h: 0.15, blurb: "먼지는 결국 손으로 치운다" },
  // 조명
  { key: "lamp_arc", name: "아치형 스탠드", kind: "floor", group: "light", glow: true, price: 150, w: 1, d: 1, h: 2.0, blurb: "휘어진 불빛이 소파 위로" },
  { key: "lamp_paper", name: "한지 등", kind: "floor", group: "light", glow: true, price: 90, w: 1, d: 1, h: 1.2, blurb: "은은한 종이 불빛" },
  { key: "lamp_mushroom", name: "버섯 무드등", kind: "floor", group: "light", glow: true, price: 45, w: 1, d: 1, h: 0.55, blurb: "밤에 보면 더 귀엽다" },
  { key: "candles", name: "초 세 자루", kind: "floor", group: "light", glow: true, price: 30, w: 1, d: 1, h: 0.45, blurb: "생일이 아니어도 켠다" },
  { key: "lantern", name: "캠핑 랜턴", kind: "floor", group: "light", glow: true, price: 55, w: 1, d: 1, h: 0.6, blurb: "주말엔 캠핑 기분" },
  { key: "firefly_jar", name: "반딧불 병", kind: "floor", group: "light", glow: true, price: 40, w: 1, d: 1, h: 0.5, blurb: "작은 불빛을 모아 둔 병" },
  { key: "lamp_moon", name: "달 조명", kind: "floor", group: "light", glow: true, price: 110, w: 1, d: 1, h: 0.9, blurb: "방 안에 뜬 보름달" },
  // 장식·취미
  { key: "piano", name: "업라이트 피아노", kind: "floor", group: "hobby", price: 380, w: 2, d: 1, h: 1.3, blurb: "젓가락 행진곡은 칠 수 있다" },
  { key: "guitar_stand", name: "통기타", kind: "floor", group: "hobby", price: 120, w: 1, d: 1, h: 1.1, blurb: "코드 세 개면 충분" },
  { key: "keyboard_synth", name: "전자 키보드", kind: "floor", group: "hobby", price: 160, w: 2, d: 1, h: 0.85, blurb: "헤드폰 끼고 연습" },
  { key: "teddy_big", name: "큰 곰 인형", kind: "floor", group: "hobby", price: 110, w: 1, d: 1, h: 1.0, blurb: "안으면 다 괜찮아진다" },
  { key: "plush_pile", name: "인형 무더기", kind: "floor", group: "hobby", price: 60, w: 1, d: 1, h: 0.5, blurb: "하나하나 이름이 있다" },
  { key: "globe", name: "지구본", kind: "floor", group: "hobby", price: 70, w: 1, d: 1, h: 0.9, blurb: "다음 여행지 고르기" },
  { key: "telescope", name: "망원경", kind: "floor", group: "hobby", price: 140, w: 1, d: 1, h: 1.3, blurb: "별 보러 가는 밤" },
  { key: "treadmill", name: "러닝머신", kind: "floor", group: "hobby", price: 300, w: 1, d: 2, h: 1.3, blurb: "옷걸이가 되지 않게" },
  { key: "bicycle", name: "자전거", kind: "floor", group: "hobby", price: 180, w: 2, d: 1, h: 1.0, blurb: "주말엔 강변으로" },
  { key: "tent", name: "실내 텐트", kind: "floor", group: "hobby", price: 200, w: 2, d: 2, h: 1.6, blurb: "방 안의 비밀 기지" },
  { key: "suitcase", name: "여행 가방", kind: "floor", group: "hobby", price: 85, w: 1, d: 1, h: 0.85, blurb: "언제든 떠날 준비" },
  { key: "gift_boxes", name: "선물 상자", kind: "floor", group: "hobby", price: 50, w: 1, d: 1, h: 0.65, blurb: "뭐가 들었을까" },
  { key: "xmas_tree", name: "크리스마스트리", kind: "floor", group: "hobby", glow: true, price: 160, w: 1, d: 1, h: 2.0, blurb: "전구가 반짝반짝" },
  { key: "pumpkin", name: "호박 장식", kind: "floor", group: "hobby", price: 35, w: 1, d: 1, h: 0.5, blurb: "가을이 왔다" },
  { key: "vase_pampas", name: "팜파스 꽃병", kind: "floor", group: "hobby", price: 65, w: 1, d: 1, h: 1.3, blurb: "말린 풀이 살랑" },
  { key: "tree_fig", name: "고무나무", kind: "floor", group: "hobby", price: 130, w: 1, d: 1, h: 1.9, blurb: "돌보지 않아도 늘 푸르다" },
  { key: "bonsai", name: "분재", kind: "floor", group: "hobby", price: 95, w: 1, d: 1, h: 0.6, blurb: "가위질은 조심조심" },
  { key: "terrarium", name: "테라리움", kind: "floor", group: "hobby", price: 75, w: 1, d: 1, h: 0.6, blurb: "유리병 속 작은 숲" },
  { key: "dog_house", name: "강아지 집", kind: "floor", group: "hobby", price: 140, w: 1, d: 1, h: 0.95, blurb: "언젠가 올 강아지를 위해" },
  { key: "cat_bed", name: "고양이 방석", kind: "floor", group: "hobby", price: 45, w: 1, d: 1, h: 0.25, blurb: "동그랗게 말린 자리" },
  { key: "book_stack", name: "책 더미", kind: "floor", group: "hobby", price: 30, w: 1, d: 1, h: 0.5, blurb: "다 읽은 척" },
  { key: "chess_table", name: "체스 탁자", kind: "floor", group: "hobby", price: 120, w: 1, d: 1, h: 0.75, blurb: "지는 사람이 설거지" },
  { key: "punching_bag", name: "샌드백", kind: "floor", group: "hobby", price: 110, w: 1, d: 1, h: 1.9, blurb: "스트레스는 여기에" },
  { key: "laundry_basket", name: "빨래 바구니", kind: "floor", group: "hobby", price: 30, w: 1, d: 1, h: 0.55, blurb: "주말에 몰아서" },
  { key: "trash_bin", name: "휴지통", kind: "floor", group: "hobby", price: 20, w: 1, d: 1, h: 0.5, blurb: "분리수거는 같이" },
  // 러그
  { key: "rug_check", name: "체크 러그", kind: "floor", price: 90, w: 2, d: 2, under: true, h: 0, blurb: "보드게임판 같은 무늬" },
  { key: "rug_heart", name: "하트 러그", kind: "floor", price: 100, w: 2, d: 2, under: true, h: 0, blurb: "가운데 서면 기분이 좋아진다" },
  { key: "rug_cloud", name: "구름 러그", kind: "floor", price: 110, w: 2, d: 2, under: true, h: 0, blurb: "폭신한 구름 위" },
  { key: "rug_runner", name: "긴 러그", kind: "floor", price: 70, w: 1, d: 3, under: true, h: 0, blurb: "복도처럼 길게" },
  { key: "rug_rainbow", name: "무지개 러그", kind: "floor", price: 120, w: 2, d: 2, under: true, h: 0, blurb: "반원 무지개" },
  { key: "rug_wave", name: "물결 러그", kind: "floor", price: 95, w: 2, d: 3, under: true, h: 0, blurb: "파도 무늬" },
  { key: "rug_doormat", name: "현관 매트", kind: "floor", price: 25, w: 1, d: 1, under: true, h: 0, blurb: "어서 와요" },
  { key: "yoga_mat", name: "요가 매트", kind: "floor", price: 40, w: 1, d: 2, under: true, h: 0, blurb: "스트레칭 십 분" },
  // 벽 장식
  { key: "wall_mirror", name: "둥근 거울", kind: "wall", price: 70, w: 1, d: 0, h: 0, blurb: "나갈 때 한 번 더" },
  { key: "wall_neon", name: "하트 네온", kind: "wall", glow: true, price: 90, w: 1, d: 0, h: 0, blurb: "밤에는 분홍빛" },
  { key: "wall_lights", name: "줄 전구", kind: "wall", glow: true, price: 60, w: 2, d: 0, h: 0, blurb: "반짝이는 전구 줄" },
  { key: "wall_calendar", name: "달력", kind: "wall", price: 30, w: 1, d: 0, h: 0, blurb: "기념일에 동그라미" },
  { key: "wall_corkboard", name: "메모 보드", kind: "wall", price: 55, w: 2, d: 0, h: 0, blurb: "장 볼 것 목록" },
  { key: "wall_ivy", name: "걸이 화분", kind: "wall", price: 65, w: 1, d: 0, h: 0, blurb: "덩굴이 늘어진다" },
  { key: "wall_guitar", name: "벽걸이 기타", kind: "wall", price: 110, w: 1, d: 0, h: 0, blurb: "치는 것보다 거는 게 예쁘다" },
  { key: "wall_dartboard", name: "다트판", kind: "wall", price: 50, w: 1, d: 0, h: 0, blurb: "한가운데는 아직" },
  { key: "wall_tapestry", name: "태피스트리", kind: "wall", price: 75, w: 2, d: 0, h: 0, blurb: "산과 해가 그려진 천" },
  { key: "wall_map", name: "세계 지도", kind: "wall", price: 80, w: 2, d: 0, h: 0, blurb: "가 본 곳에 핀" },
  { key: "wall_frames", name: "액자 세 개", kind: "wall", price: 70, w: 2, d: 0, h: 0, blurb: "우리 사진" },
  { key: "wall_sconce", name: "벽등", kind: "wall", glow: true, price: 65, w: 1, d: 0, h: 0, blurb: "벽에 핀 작은 불빛" },
  { key: "wall_tv", name: "벽걸이 TV", kind: "wall", price: 260, w: 2, d: 0, h: 0, blurb: "큰 화면으로 영화" },
  { key: "wall_aircon", name: "벽걸이 에어컨", kind: "wall", price: 220, w: 2, d: 0, h: 0, blurb: "제습 모드로" },
  { key: "wall_round_window", name: "동그란 창", kind: "wall", price: 100, w: 1, d: 0, h: 0, blurb: "작은 하늘 한 조각" },
  { key: "wall_records", name: "레코드 선반", kind: "wall", price: 85, w: 2, d: 0, h: 0, blurb: "좋아하는 앨범 세 장" },
  { key: "wall_wreath", name: "리스", kind: "wall", price: 45, w: 1, d: 0, h: 0, blurb: "문 대신 벽에" },
  { key: "wall_letters", name: "LOVE 글자", kind: "wall", glow: true, price: 70, w: 2, d: 0, h: 0, blurb: "불 들어오는 네 글자" },
  { key: "wall_cuckoo", name: "뻐꾸기시계", kind: "wall", price: 120, w: 1, d: 0, h: 0, blurb: "정각마다 뻐꾹" },
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
export const STARTER: { key: string; x: number; y: number; turn?: number; wall?: "left" | "right" }[] = [
  { key: "rug_round", x: 3, y: 3 },
  { key: "bed_wood", x: 0, y: 1 },
  { key: "lamp_floor", x: 0, y: 4 },
  { key: "plant_tulip", x: 5, y: 0 },
  { key: "wall_window", x: 3, y: 0, wall: "right" },
  { key: "wall_clock", x: 4, y: 0, wall: "left" },
];
