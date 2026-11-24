// 주방·가전·조명 물건의 그림. 등은 y = 0(벽) 쪽, 앞은 +y 쪽(왼쪽 아래)이다.

import { C, legs, shade, type Art, type Build, type P3 } from "../kit";

// ── 같이 쓰는 색과 조각 ──────────────────────────────────────────

const WHITE = "#F2F4F3";
const STEEL = "#C3CBD0";
const LINE = "#9AA3A8";
const SCREEN = "#2F3A3F";
const DIGIT = "#7FE0D0";
const CAB = "#D3E6DF";
const BRASS = "#B98B52";
const LIT = "#FFE3A3";
const SHADE_DAY = "#F2E2C2";

/** 둥근 물건 아래의 둥근 그림자 */
const blob = (a: Art, x: number, y: number, r: number) =>
  a.dot(x, y, 0, (sx, sy) => <ellipse cx={sx + 1.5} cy={sy + 1} rx={r * 40 + 2} ry={r * 20 + 1} fill="rgba(40,30,20,0.16)" />, r, 0);

/** 문짝(면 좌표): 테두리와 손잡이. knob 은 손잡이 자리(-1 왼쪽, 1 오른쪽, 0 가운데 가로). */
function panel(x: number, z: number, w: number, h: number, c: string, knob: -1 | 0 | 1, grip = BRASS) {
  return (
    <g key={`${x}-${z}`}>
      <rect x={x} y={z} width={w} height={h} rx={0.015} fill="rgba(255,255,255,.18)" stroke={shade(c, -0.28)} strokeWidth={0.014} />
      {knob === 0 ? (
        <rect x={x + w / 2 - 0.08} y={z + h / 2 - 0.014} width={0.16} height={0.028} rx={0.012} fill={grip} />
      ) : (
        <rect x={knob < 0 ? x + 0.04 : x + w - 0.068} y={z + h - 0.2} width={0.028} height={0.13} rx={0.012} fill={grip} />
      )}
    </g>
  );
}

/** 뚜껑 있는 작은 통(양념통·원두 병) */
function jar(a: Art, x: number, y: number, z: number, r: number, h: number, body: string, lid: string) {
  a.cyl({ cx: x, cy: y, z, r, h, c: body });
  a.cyl({ cx: x, cy: y, z: z + h, r: r + 0.01, h: 0.04, c: lid });
}

/** 등 쪽 면(y = at)의 환기구 줄: 뒷모습에서만 보인다. */
function vent(a: Art, at: number, x0: number, x1: number, z0: number, n: number) {
  a.face(
    "y",
    at,
    { u: [x0, x1], z: [z0, z0 + n * 0.05], out: -1 },
    <g stroke={LINE} strokeWidth={0.016}>
      {Array.from({ length: n }, (_, i) => (
        <line key={i} x1={x0} y1={z0 + 0.025 + i * 0.05} x2={x1} y2={z0 + 0.025 + i * 0.05} />
      ))}
    </g>,
  );
}

/** 김·안개: 겹친 동그라미의 바깥 테두리만 남긴다. */
function puff(sx: number, sy: number, list: [number, number, number][], fill = "#FFFFFF", line = "#D3DCE0") {
  return (
    <g opacity={0.92}>
      {list.map(([dx, dy, r], i) => (
        <circle key={`o${i}`} cx={sx + dx} cy={sy + dy} r={r + 0.7} fill={line} />
      ))}
      {list.map(([dx, dy, r], i) => (
        <circle key={`i${i}`} cx={sx + dx} cy={sy + dy} r={r} fill={fill} />
      ))}
    </g>
  );
}

/** 밤 불빛이 번지는 그라데이션(가운데가 밝고 바깥으로 사라진다) */
const glowDef = (gid: string, color: string, alpha = 0.7) => (
  <radialGradient id={gid}>
    <stop offset="0" stopColor={color} stopOpacity={alpha} />
    <stop offset="1" stopColor={color} stopOpacity={0} />
  </radialGradient>
);

/** 밤 불빛 번짐 하나(화면 좌표): 밑의 색을 밝게 섞는다. flat 은 세로 비율(바닥에 고인 빛은 납작하게). */
const halo = (gid: string, cx: number, cy: number, r: number, color: string, alpha = 0.7, flat = 1) => (
  <>
    {glowDef(gid, color, alpha)}
    <ellipse cx={cx} cy={cy} rx={r} ry={r * flat} fill={`url(#${gid})`} style={{ mixBlendMode: "screen" }} />
  </>
);

// ── 주방 ─────────────────────────────────────────────────────────

/** 주방 하부장: 수납장과 나무 상판(윗면 0.94) */
function counterBase(a: Art, w: number) {
  a.shadow();
  a.box({ x: 0.04, y: 0.12, w: w - 0.08, d: 0.76, h: 0.86, c: CAB });
  a.box({ x: 0.02, y: 0.06, z: 0.86, w: w - 0.04, d: 0.88, h: 0.08, c: C.woodLight });
}

const kick = (x0: number, x1: number) => <rect x={x0} y={0} width={x1 - x0} height={0.06} fill="rgba(0,0,0,.1)" />;

const fridgeBig: Build = (a) => {
  const F = "#E6ECEE";
  a.shadow();
  a.box({ x: 0.05, y: 0.06, w: 1.9, d: 0.8, h: 1.88, c: F });
  // 왼쪽 냉동실 문, 오른쪽 냉장실 문, 가운데의 긴 손잡이
  a.box({ x: 0.05, y: 0.86, z: 0.03, w: 0.935, d: 0.05, h: 1.83, c: F });
  a.box({ x: 1.015, y: 0.86, z: 0.03, w: 0.935, d: 0.05, h: 1.83, c: F });
  for (const x of [0.89, 1.07]) a.box({ x, y: 0.91, z: 0.72, w: 0.04, d: 0.04, h: 0.74, c: "#AEB8BE" });
  a.face(
    "y",
    0.91,
    { u: [0.28, 0.72], z: [0.98, 1.42] },
    <>
      <rect x={0.3} y={0.98} width={0.4} height={0.44} rx={0.04} fill="#C4CDD2" stroke={LINE} strokeWidth={0.014} />
      <rect x={0.35} y={1.03} width={0.3} height={0.26} rx={0.03} fill="#8E9AA2" />
      <rect x={0.47} y={1.2} width={0.06} height={0.07} fill="#DDE3E6" />
      <rect x={0.38} y={1.33} width={0.24} height={0.05} rx={0.01} fill="#9FD3E6" />
    </>,
  );
  // 메모와 자석, 둘이 찍은 사진
  a.face(
    "y",
    0.91,
    { u: [1.25, 1.85], z: [1.0, 1.56] },
    <>
      <rect x={1.28} y={1.18} width={0.26} height={0.32} fill={C.white} stroke="#C9C6BE" strokeWidth={0.01} />
      <line x1={1.32} y1={1.38} x2={1.5} y2={1.38} stroke={C.slate} strokeWidth={0.016} />
      <line x1={1.32} y1={1.29} x2={1.46} y2={1.29} stroke={C.slate} strokeWidth={0.016} />
      <circle cx={1.41} cy={1.5} r={0.045} fill={C.blush} />
      <g transform="rotate(-6 1.7 1.14)">
        <rect x={1.6} y={1.02} width={0.2} height={0.24} fill={C.white} stroke="#C9C6BE" strokeWidth={0.01} />
        <rect x={1.63} y={1.08} width={0.14} height={0.15} fill={C.sky} />
        <circle cx={1.7} cy={1.13} r={0.035} fill={C.blush} />
      </g>
      <circle cx={1.7} cy={1.42} r={0.04} fill={C.straw} />
    </>,
  );
  vent(a, 0.06, 0.3, 1.7, 0.08, 5);
};

const kitchenCounter: Build = (a) => {
  counterBase(a, 2);
  a.face(
    "y",
    0.88,
    { u: [0.04, 1.96], z: [0, 0.82] },
    <>
      {kick(0.04, 1.96)}
      {panel(0.08, 0.62, 0.9, 0.2, CAB, 0)}
      {panel(1.02, 0.62, 0.9, 0.2, CAB, 0)}
      {[0.08, 0.54, 1.02, 1.48].map((x, i) => panel(x, 0.09, 0.44, 0.49, CAB, i % 2 ? -1 : 1))}
    </>,
  );
  // 도마 위의 칼과 토마토
  const z = 0.976;
  a.box({ x: 0.28, y: 0.34, z: 0.94, w: 0.66, d: 0.42, h: 0.036, c: "#F5F1E8" });
  a.add([0.38, 0.45, z, 0.88, 0.66, z], (v) => (
    <>
      <polygon points={v.pts([[0.38, 0.66, z], [0.68, 0.5, z], [0.72, 0.58, z]])} fill="#E3E8EA" stroke={LINE} strokeWidth={0.5} />
      <path d={`M ${v.p(0.71, 0.54, z)} L ${v.p(0.88, 0.45, z)}`} stroke={C.ink} strokeWidth={2.6} strokeLinecap="round" />
    </>
  ));
  a.dot(
    0.56,
    0.43,
    z,
    (sx, sy) => (
      <>
        <circle cx={sx} cy={sy - 2.6} r={3} fill="#E8574A" stroke={shade("#E8574A", -0.3)} strokeWidth={0.6} />
        <path d={`M ${sx - 1.6} ${sy - 5.4} L ${sx} ${sy - 4.6} L ${sx + 1.6} ${sy - 5.4}`} stroke={C.leaf} strokeWidth={1} fill="none" />
      </>
    ),
    0.06,
    0.1,
  );
  // 양념통 셋과 조리도구 통
  (
    [
      ["#C8553D", C.blush],
      ["#F4F1EA", C.egg],
      ["#D9B77A", C.straw],
    ] as const
  ).forEach(([c, lid], i) => jar(a, 1.18 + i * 0.17, 0.27, 0.94, 0.065, 0.15, c, lid));
  a.cyl({ cx: 1.78, cy: 0.3, z: 0.94, r: 0.08, h: 0.16, c: C.egg });
  a.line([1.76, 0.29, 1.08], [1.72, 0.25, 1.32], C.woodDark, 1.6);
  a.dot(1.72, 0.25, 1.32, (sx, sy) => <ellipse cx={sx} cy={sy - 2} rx={2.2} ry={3.2} fill={C.woodLight} stroke={C.woodDark} strokeWidth={0.6} />, 0.03, 0.08);
  a.line([1.8, 0.31, 1.08], [1.86, 0.34, 1.27], LINE, 1.4);
  a.dot(1.86, 0.34, 1.27, (sx, sy) => <circle cx={sx + 1} cy={sy - 1.5} r={2.2} fill={STEEL} stroke={LINE} strokeWidth={0.6} />, 0.03, 0.06);
};

/** 가스 화구 하나 */
const burner = (a: Art, x: number, y: number) =>
  a.dot(
    x,
    y,
    0.92,
    (sx, sy) => (
      <>
        <ellipse cx={sx} cy={sy} rx={6.6} ry={3.3} fill="#2C3135" stroke="#6B747A" strokeWidth={0.6} />
        <ellipse cx={sx} cy={sy} rx={2.8} ry={1.4} fill="#8A9399" />
      </>
    ),
    0.17,
    0.01,
  );

const stove: Build = (a) => {
  a.shadow();
  a.box({ x: 0.05, y: 0.1, w: 0.9, d: 0.78, h: 0.86, c: WHITE });
  a.box({ x: 0.03, y: 0.07, z: 0.86, w: 0.94, d: 0.84, h: 0.06, c: "#4A5157" });
  a.face(
    "y",
    0.88,
    { u: [0.05, 0.95], z: [0, 0.84] },
    <>
      {kick(0.05, 0.95)}
      <rect x={0.05} y={0.68} width={0.9} height={0.16} fill="rgba(0,0,0,.05)" />
      {[0.2, 0.37, 0.63, 0.8].map((x) => (
        <g key={x}>
          <circle cx={x} cy={0.76} r={0.05} fill={C.white} stroke={LINE} strokeWidth={0.014} />
          <rect x={x - 0.008} y={0.76} width={0.016} height={0.045} fill={C.ink} />
        </g>
      ))}
      <rect x={0.12} y={0.09} width={0.76} height={0.53} rx={0.03} fill="rgba(255,255,255,.2)" stroke={LINE} strokeWidth={0.016} />
      <rect x={0.2} y={0.17} width={0.6} height={0.27} rx={0.02} fill="#3B4349" />
      <path d="M 0.26 0.44 L 0.4 0.44 L 0.26 0.25 Z" fill="rgba(255,255,255,.14)" />
      <rect x={0.22} y={0.5} width={0.56} height={0.04} rx={0.02} fill={LINE} />
    </>,
  );
  burner(a, 0.3, 0.3);
  burner(a, 0.7, 0.3);
  burner(a, 0.7, 0.7);
  // 라면 끓는 양은 냄비
  a.cyl({ cx: 0.3, cy: 0.68, z: 0.92, r: 0.19, h: 0.15, c: "#E9C25E", top: "#E07B4F" });
  a.dot(
    0.3,
    0.68,
    1.07,
    (sx, sy) => (
      <>
        <path d={`M ${sx - 4} ${sy} q 2 -1.6 4 0 t 4 0`} stroke="#F6E3A1" strokeWidth={0.9} fill="none" />
        <rect x={sx - 10.6} y={sy + 0.6} width={3.4} height={1.6} rx={0.8} fill="#D9A93F" />
        <rect x={sx + 7.2} y={sy + 0.6} width={3.4} height={1.6} rx={0.8} fill="#D9A93F" />
        <path d={`M ${sx - 2} ${sy - 3} q -2 -3 0 -6 t 0 -6 M ${sx + 3} ${sy - 4} q 2 -3 0 -6 t 0 -5`} stroke="#CFD8DC" strokeWidth={1.4} fill="none" strokeLinecap="round" />
      </>
    ),
    0.19,
    0.3,
  );
};

const sink: Build = (a) => {
  counterBase(a, 1);
  a.face(
    "y",
    0.88,
    { u: [0.04, 0.96], z: [0, 0.82] },
    <>
      {kick(0.04, 0.96)}
      {panel(0.08, 0.09, 0.41, 0.73, CAB, 1)}
      {panel(0.51, 0.09, 0.41, 0.73, CAB, -1)}
    </>,
  );
  // 상판에 파인 개수대: 안쪽 바닥을 조금 낮춰 그려 깊어 보이게
  const z = 0.941;
  const rim: P3[] = [
    [0.1, 0.26, z],
    [0.64, 0.26, z],
    [0.64, 0.8, z],
    [0.1, 0.8, z],
  ];
  const pit: P3[] = rim.map(([x, y]) => [x < 0.3 ? x + 0.09 : x - 0.09, y < 0.5 ? y + 0.09 : y - 0.09, z - 0.08]);
  a.add([0.1, 0.26, z, 0.64, 0.8, z], (v) => {
    const [dx, dy] = v.p(0.37, 0.53, z - 0.08);
    return (
      <>
        <polygon points={v.pts(rim)} fill="#9EA8AE" stroke="#7F8A90" strokeWidth={0.6} />
        <polygon points={v.pts(pit)} fill="#CBD2D6" />
        <ellipse cx={dx} cy={dy} rx={2.2} ry={1.1} fill="#7F8A90" />
      </>
    );
  });
  // 수도꼭지
  a.cyl({ cx: 0.37, cy: 0.15, z: 0.94, r: 0.04, h: 0.22, c: STEEL });
  a.add([0.35, 0.13, 1.08, 0.39, 0.45, 1.3], (v) => {
    const d = `M ${v.p(0.37, 0.15, 1.12)} C ${v.p(0.37, 0.15, 1.32)} ${v.p(0.37, 0.42, 1.32)} ${v.p(0.37, 0.43, 1.1)}`;
    return (
      <>
        <path d={d} fill="none" stroke="#7F8A90" strokeWidth={3.4} strokeLinecap="round" />
        <path d={d} fill="none" stroke="#DCE2E5" strokeWidth={2} strokeLinecap="round" />
      </>
    );
  });
  // 포갠 접시와 주방 세제
  for (let i = 0; i < 3; i++) a.cyl({ cx: 0.8, cy: 0.6, z: 0.94 + i * 0.03, r: 0.13 - i * 0.005, h: 0.03, c: i === 1 ? C.sky : C.white });
  a.cyl({ cx: 0.82, cy: 0.24, z: 0.94, r: 0.05, h: 0.14, c: C.blush });
  a.cyl({ cx: 0.82, cy: 0.24, z: 1.08, r: 0.02, h: 0.04, c: C.white });
};

const microwaveCart: Build = (a) => {
  a.shadow();
  legs(a, 0.1, 0.12, 0.83, 0.81, 0.72, C.woodDark);
  a.box({ x: 0.08, y: 0.1, z: 0.12, w: 0.84, d: 0.8, h: 0.05, c: C.wood });
  a.box({ x: 0.06, y: 0.08, z: 0.72, w: 0.88, d: 0.84, h: 0.06, c: C.woodLight });
  // 아래 칸: 밥솥과 감자 바구니
  a.cyl({ cx: 0.34, cy: 0.5, z: 0.17, r: 0.15, h: 0.2, c: "#F6F4EF", top: "#E4E1DA" });
  a.dot(0.34, 0.56, 0.37, (sx, sy) => <ellipse cx={sx} cy={sy} rx={2} ry={1} fill={C.blush} />, 0.03, 0.01);
  a.box({ x: 0.56, y: 0.32, z: 0.17, w: 0.3, d: 0.4, h: 0.12, c: C.straw });
  a.dot(
    0.71,
    0.52,
    0.29,
    (sx, sy) => (
      <>
        <ellipse cx={sx - 2.5} cy={sy - 1} rx={3} ry={2.2} fill="#C9A06A" stroke="#9C7748" strokeWidth={0.5} />
        <ellipse cx={sx + 2.5} cy={sy} rx={3} ry={2.2} fill="#C9A06A" stroke="#9C7748" strokeWidth={0.5} />
      </>
    ),
    0.12,
    0.06,
  );
  // 전자레인지: 불 켜진 창, 시간 표시, 버튼
  a.box({ x: 0.12, y: 0.18, z: 0.78, w: 0.76, d: 0.64, h: 0.4, c: WHITE });
  a.face(
    "y",
    0.82,
    { u: [0.14, 0.86], z: [0.81, 1.15] },
    <>
      <rect x={0.15} y={0.82} width={0.5} height={0.32} rx={0.02} fill="rgba(0,0,0,.04)" stroke={LINE} strokeWidth={0.014} />
      <rect x={0.2} y={0.86} width={0.4} height={0.24} rx={0.02} fill="#F5D88E" stroke="#6E777D" strokeWidth={0.02} />
      <path d="M 0.3 0.93 L 0.5 0.93 Q 0.48 0.88 0.4 0.88 Q 0.32 0.88 0.3 0.93 Z" fill={C.blush} />
      <rect x={0.69} y={1.03} width={0.16} height={0.08} rx={0.01} fill={SCREEN} />
      <rect x={0.71} y={1.05} width={0.05} height={0.04} fill={DIGIT} />
      <rect x={0.78} y={1.05} width={0.05} height={0.04} fill={DIGIT} />
      {[0, 1, 2].flatMap((r) =>
        [0, 1].map((c) => <rect key={`${r}${c}`} x={0.69 + c * 0.085} y={0.85 + r * 0.055} width={0.07} height={0.035} rx={0.008} fill="#D9DEE1" stroke={LINE} strokeWidth={0.008} />),
      )}
    </>,
  );
};

const coffeeStation: Build = (a) => {
  const M = "#EFC2B8";
  a.shadow();
  a.box({ x: 0.08, y: 0.14, w: 0.84, d: 0.72, h: 0.6, c: C.wood });
  a.face(
    "y",
    0.86,
    { u: [0.12, 0.88], z: [0.06, 0.56] },
    <>
      {panel(0.12, 0.06, 0.37, 0.5, C.wood, 1, C.white)}
      {panel(0.51, 0.06, 0.37, 0.5, C.wood, -1, C.white)}
    </>,
  );
  a.box({ x: 0.05, y: 0.1, z: 0.6, w: 0.9, d: 0.8, h: 0.05, c: C.woodLight });
  // 에스프레소 머신: 물받이, 몸통, 앞으로 나온 머리, 그 아래 머그
  a.box({ x: 0.16, y: 0.26, z: 0.65, w: 0.52, d: 0.54, h: 0.04, c: STEEL });
  a.box({ x: 0.16, y: 0.22, z: 0.65, w: 0.52, d: 0.3, h: 0.48, c: M });
  a.box({ x: 0.16, y: 0.52, z: 0.94, w: 0.52, d: 0.26, h: 0.19, c: M });
  a.cyl({ cx: 0.42, cy: 0.64, z: 0.86, r: 0.06, h: 0.08, c: "#8C969C" });
  a.cyl({ cx: 0.42, cy: 0.64, z: 0.69, r: 0.075, h: 0.11, c: C.white, top: "#6B4A33" });
  a.face(
    "y",
    0.78,
    { u: [0.2, 0.64], z: [0.97, 1.1] },
    <>
      <circle cx={0.28} cy={1.035} r={0.05} fill={C.white} stroke={shade(M, -0.3)} strokeWidth={0.012} />
      <line x1={0.28} y1={1.035} x2={0.31} y2={1.06} stroke={C.ink} strokeWidth={0.01} />
      {[0.42, 0.5, 0.58].map((x) => (
        <circle key={x} cx={x} cy={1.035} r={0.025} fill={shade(M, -0.25)} />
      ))}
    </>,
  );
  a.line([0.7, 0.6, 0.98], [0.72, 0.66, 0.8], LINE, 1.6);
  jar(a, 0.82, 0.34, 0.65, 0.08, 0.17, "#7A5236", C.woodDark);
};

// ── 가전 ─────────────────────────────────────────────────────────

/** 앞에서 여는 세탁기·건조기 한 대(z0 부터 높이 0.92): 위에 조작부, 가운데 둥근 문 */
function frontLoader(a: Art, z0: number, glass: string, load: [string, string]) {
  const t = z0 + 0.92;
  const c = z0 + 0.38;
  a.box({ x: 0.06, y: 0.08, z: z0, w: 0.88, d: 0.8, h: 0.92, c: WHITE });
  a.face(
    "y",
    0.88,
    { u: [0.06, 0.94], z: [z0, t] },
    <>
      <line x1={0.06} y1={t - 0.17} x2={0.94} y2={t - 0.17} stroke={LINE} strokeWidth={0.012} />
      <rect x={0.12} y={t - 0.13} width={0.22} height={0.08} rx={0.012} fill="none" stroke={LINE} strokeWidth={0.012} />
      <circle cx={0.56} cy={t - 0.085} r={0.045} fill={C.white} stroke={LINE} strokeWidth={0.012} />
      <rect x={0.68} y={t - 0.125} width={0.18} height={0.075} rx={0.012} fill={SCREEN} />
      <rect x={0.71} y={t - 0.105} width={0.08} height={0.035} fill={DIGIT} />
      <circle cx={0.5} cy={c} r={0.29} fill="#E1E6E8" stroke={LINE} strokeWidth={0.016} />
      <circle cx={0.5} cy={c} r={0.21} fill={glass} stroke="#7C868C" strokeWidth={0.016} />
      <ellipse cx={0.43} cy={c - 0.1} rx={0.1} ry={0.055} fill={load[0]} />
      <ellipse cx={0.57} cy={c - 0.12} rx={0.08} ry={0.05} fill={load[1]} />
      <path d={`M 0.36 ${c + 0.05} Q 0.38 ${c + 0.15} 0.48 ${c + 0.17}`} stroke="rgba(255,255,255,.7)" strokeWidth={0.025} fill="none" strokeLinecap="round" />
      <rect x={0.8} y={c - 0.07} width={0.03} height={0.14} rx={0.015} fill={LINE} />
    </>,
  );
  vent(a, 0.08, 0.3, 0.7, z0 + 0.6, 4);
}

const washer: Build = (a) => {
  a.shadow();
  frontLoader(a, 0, "#A9CFDD", [C.blush, C.straw]);
};

const washerDryer: Build = (a) => {
  a.shadow();
  frontLoader(a, 0, "#A9CFDD", [C.blush, C.straw]);
  frontLoader(a, 0.92, "#9AA6AE", [C.white, C.egg]);
};

const airPurifier: Build = (a, { night }) => {
  const R = 0.25;
  const rx = R * 39.6;
  blob(a, 0.5, 0.5, R);
  a.cyl({ cx: 0.5, cy: 0.5, r: R - 0.01, h: 0.04, c: "#D5DADC" });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.04, r: R, h: 0.46, c: "#E9EDED" });
  // 흡입구 줄: 둥근 몸을 따라 앞쪽 반원
  a.add([0.5 - R, 0.5 - R, 0.04, 0.5 + R, 0.5 + R, 0.5], (v) => {
    const [sx] = v.p(0.5, 0.5);
    return (
      <g stroke="#C2C9CC" strokeWidth={0.7} fill="none">
        {[0.1, 0.16, 0.22, 0.28, 0.34, 0.4].map((z) => {
          const sy = v.p(0.5, 0.5, z)[1];
          return <path key={z} d={`M ${sx - rx} ${sy} A ${rx} ${rx / 2} 0 0 0 ${sx + rx} ${sy}`} />;
        })}
      </g>
    );
  });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.5, r: R, h: 0.27, c: WHITE });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.77, r: R, h: 0.05, c: WHITE, taper: 0.03 });
  a.dot(
    0.5,
    0.5,
    0.82,
    (sx, sy) => (
      <>
        <ellipse cx={sx} cy={sy} rx={rx * 0.62} ry={rx * 0.31} fill="#DCE1E3" stroke="#BCC4C8" strokeWidth={0.5} />
        <ellipse cx={sx} cy={sy} rx={rx * 0.38} ry={rx * 0.19} fill="none" stroke="#BCC4C8" strokeWidth={0.5} />
        <circle cx={sx} cy={sy + rx * 0.42} r={1.5} fill={night ? "#7DFFA4" : "#4CC46E"} />
      </>
    ),
    R,
    0.01,
  );
};

const humidifier: Build = (a) => {
  blob(a, 0.5, 0.5, 0.22);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.22, h: 0.12, c: WHITE });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.12, r: 0.2, h: 0.2, c: "#BFE0EE", taper: 0.03 });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.32, r: 0.17, h: 0.05, c: WHITE, taper: 0.03 });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.37, r: 0.05, h: 0.03, c: "#DDE2E4" });
  a.dot(
    0.5,
    0.5,
    0.4,
    (sx, sy) =>
      puff(sx, sy, [
        [0, -3.5, 2.6],
        [2.8, -7.5, 3.4],
        [-2.2, -10.5, 3.6],
        [1.6, -13.5, 2.8],
      ]),
    0.12,
    0.45,
  );
};

const fan: Build = (a) => {
  const [y, z, r] = [0.62, 0.86, 0.27];
  blob(a, 0.5, 0.5, 0.22);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.22, h: 0.05, c: WHITE });
  a.dot(
    0.5,
    0.64,
    0.05,
    (sx, sy) => (
      <>
        {[-3, 0, 3].map((dx) => (
          <ellipse key={dx} cx={sx + dx} cy={sy} rx={1.2} ry={0.7} fill={dx > 0 ? C.blush : C.egg} />
        ))}
      </>
    ),
    0.05,
    0.005,
  );
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.05, r: 0.035, h: 0.4, c: "#E3E7E8" });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.45, r: 0.024, h: 0.33, c: STEEL });
  // 머리 뒤의 둥근 모터
  a.dot(0.5, 0.53, z - 0.07, (sx, sy) => <ellipse cx={sx} cy={sy - 2} rx={4.4} ry={4.2} fill={WHITE} stroke={shade(WHITE, -0.32)} strokeWidth={0.6} />, 0.07, 0.14);
  // 앞(+y)을 보는 둥근 머리: 날개 위에 망
  a.add([0.5 - r, y - 0.02, z - r, 0.5 + r, y + 0.02, z + r], (v) => (
    <g transform={v.faceT("y", y)}>
      <circle cx={0.5} cy={z} r={r} fill="rgba(238,246,247,.75)" stroke="#BFC7CB" strokeWidth={0.03} />
      {[0, 120, 240].map((d) => (
        <ellipse key={d} cx={0.5} cy={z + 0.12} rx={0.075} ry={0.12} transform={`rotate(${d} 0.5 ${z})`} fill={C.egg} stroke={C.eggDeep} strokeWidth={0.008} />
      ))}
      <g stroke="#C9D0D3" strokeWidth={0.01} fill="none">
        <circle cx={0.5} cy={z} r={0.18} />
        <circle cx={0.5} cy={z} r={0.09} />
        {[0, 45, 90, 135].map((d) => (
          <line key={d} x1={0.5 - r} y1={z} x2={0.5 + r} y2={z} transform={`rotate(${d} 0.5 ${z})`} />
        ))}
      </g>
      <circle cx={0.5} cy={z} r={0.045} fill={C.white} stroke={C.eggDeep} strokeWidth={0.014} />
    </g>
  ));
};

const heater: Build = (a, { night, id }) => {
  a.shadow({ x: 0.12, y: 0.24, w: 0.76, d: 0.54 });
  if (night) a.dot(0.5, 0.88, 0, (sx, sy) => halo(`${id}-heat`, sx, sy, 22, "#FF9F4A", 0.75, 0.45), 0.1, 0);
  for (const x of [0.18, 0.74]) a.box({ x, y: 0.26, w: 0.08, d: 0.5, h: 0.06, c: "#A79F94" });
  a.box({ x: 0.14, y: 0.32, z: 0.06, w: 0.72, d: 0.36, h: 0.7, c: "#F1EDE6" });
  // 앞의 발열 창: 밤에는 환하게
  a.face(
    "y",
    0.68,
    { u: [0.2, 0.8], z: [0.12, 0.66] },
    <>
      <rect x={0.2} y={0.13} width={0.6} height={0.5} rx={0.05} fill={night ? "#FF9A42" : "#E8A070"} stroke="#BDB4A8" strokeWidth={0.016} />
      {[0.22, 0.34, 0.46].map((zz) => (
        <rect key={zz} x={0.25} y={zz} width={0.5} height={0.06} rx={0.03} fill={night ? "#FFEAA6" : "#D9714A"} />
      ))}
      {[0.3, 0.4, 0.5, 0.6, 0.7].map((x) => (
        <line key={x} x1={x} y1={0.13} x2={x} y2={0.63} stroke="#CFC6BA" strokeWidth={0.012} />
      ))}
    </>,
  );
  // 위의 손잡이 홈과 다이얼
  a.add([0.34, 0.45, 0.76, 0.6, 0.55, 0.76], (v) => (
    <polygon
      points={v.pts([
        [0.34, 0.46, 0.761],
        [0.6, 0.46, 0.761],
        [0.6, 0.54, 0.761],
        [0.34, 0.54, 0.761],
      ])}
      fill="#CFC6BA"
    />
  ));
  a.cyl({ cx: 0.74, cy: 0.5, z: 0.76, r: 0.05, h: 0.03, c: C.slate });
  vent(a, 0.32, 0.24, 0.76, 0.2, 7);
};

const airconStand: Build = (a) => {
  a.shadow({ x: 0.14, y: 0.18, w: 0.72, d: 0.64 });
  a.box({ x: 0.18, y: 0.22, w: 0.64, d: 0.56, h: 0.06, c: "#D6DBDD" });
  a.box({ x: 0.2, y: 0.24, z: 0.06, w: 0.6, d: 0.52, h: 1.78, c: WHITE });
  a.face(
    "y",
    0.76,
    { u: [0.24, 0.76], z: [0.25, 1.73] },
    <>
      <rect x={0.25} y={1.33} width={0.5} height={0.39} rx={0.04} fill="rgba(0,0,0,.05)" stroke="#C3CACE" strokeWidth={0.012} />
      {[1.39, 1.46, 1.53, 1.6, 1.67].map((zz) => (
        <line key={zz} x1={0.28} y1={zz} x2={0.72} y2={zz} stroke="#A9B2B7" strokeWidth={0.018} />
      ))}
      <line x1={0.24} y1={1.27} x2={0.76} y2={1.27} stroke={C.egg} strokeWidth={0.02} />
      <rect x={0.4} y={1.07} width={0.2} height={0.11} rx={0.02} fill={SCREEN} />
      <rect x={0.43} y={1.1} width={0.05} height={0.05} fill={DIGIT} />
      <rect x={0.5} y={1.1} width={0.05} height={0.05} fill={DIGIT} />
      {Array.from({ length: 24 }, (_, i) => (
        <circle key={i} cx={0.3 + (i % 6) * 0.08} cy={0.3 + Math.floor(i / 6) * 0.1} r={0.018} fill="#D3D9DC" />
      ))}
    </>,
  );
  vent(a, 0.24, 0.26, 0.74, 0.3, 10);
};

const robotVacuum: Build = (a, { night }) => {
  // 벽에 붙은 충전대
  a.box({ x: 0.33, y: 0.03, w: 0.34, d: 0.12, h: 0.2, c: "#E9EBEA" });
  a.box({ x: 0.35, y: 0.15, w: 0.3, d: 0.08, h: 0.015, c: "#D3D8DA" });
  a.face("y", 0.15, { u: [0.46, 0.54], z: [0.12, 0.17] }, <circle cx={0.5} cy={0.145} r={0.02} fill={night ? "#8DFFB0" : "#59C27A"} />);
  // 본체: 회색 범퍼, 흰 윗판, 뒤쪽 센서, 앞쪽 단추
  blob(a, 0.5, 0.58, 0.3);
  a.cyl({ cx: 0.5, cy: 0.58, r: 0.3, h: 0.06, c: "#8E979C" });
  a.cyl({ cx: 0.5, cy: 0.58, z: 0.06, r: 0.3, h: 0.035, c: WHITE });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.095, r: 0.075, h: 0.035, c: C.slate });
  a.dot(0.5, 0.74, 0.095, (sx, sy) => <ellipse cx={sx} cy={sy} rx={2.6} ry={1.3} fill={C.egg} stroke={C.eggDeep} strokeWidth={0.5} />, 0.04, 0.005);
};

// ── 조명 ─────────────────────────────────────────────────────────

const lampArc: Build = (a, { night, id }) => {
  const arm = "#B8914F";
  const R = 0.24;
  blob(a, 0.5, 0.2, 0.17);
  // 밤에는 갓 아래 바닥에 빛이 고인다
  if (night) a.dot(0.5, 0.7, 0, (sx, sy) => halo(`${id}-pool`, sx, sy, 22, "#FFD98A", 0.65, 0.5), 0.25, 0);
  a.cyl({ cx: 0.5, cy: 0.2, r: 0.17, h: 0.08, c: "#E4E0D8" });
  // 뒤에서 솟아 앞으로 휘는 팔
  a.add([0.48, 0.18, 0.08, 0.52, 0.72, 1.95], (v) => (
    <path
      d={`M ${v.p(0.5, 0.2, 0.08)} L ${v.p(0.5, 0.2, 1.25)} C ${v.p(0.5, 0.2, 2.02)} ${v.p(0.5, 0.66, 2.08)} ${v.p(0.5, 0.7, 1.82)}`}
      fill="none"
      stroke={arm}
      strokeWidth={2.2}
      strokeLinecap="round"
    />
  ));
  a.cyl({ cx: 0.5, cy: 0.7, z: 1.54, r: R, h: 0.26, c: night ? LIT : SHADE_DAY, top: night ? "#FFF2CC" : undefined, taper: 0.17 });
  a.cyl({ cx: 0.5, cy: 0.7, z: 1.8, r: 0.045, h: 0.04, c: arm });
  if (night)
    a.dot(
      0.5,
      0.7,
      1.54,
      (sx, sy) => <path d={`M ${sx - R * 39.6} ${sy} A ${R * 39.6} ${R * 19.8} 0 0 0 ${sx + R * 39.6} ${sy}`} stroke="#FFF8E0" strokeWidth={1.6} fill="none" />,
      R,
      0.01,
    );
};

const lampPaper: Build = (a, { night, id }) => {
  const paper = night ? "#FFE4AE" : "#F6EEDD";
  const frame = C.woodDark;
  a.shadow({ x: 0.2, y: 0.2, w: 0.6, d: 0.6 });
  a.box({ x: 0.24, y: 0.24, w: 0.52, d: 0.52, h: 0.06, c: frame });
  // 밤에는 종이 몸 뒤로 빛이 번진다(몸보다 먼저 넣어 뒤에 깔린다)
  if (night) a.dot(0.5, 0.5, 0.06, (sx, sy) => halo(`${id}-paper`, sx, sy - 16, 34, "#FFD98A", 0.55), 0.21, 1.04);
  a.box({ x: 0.29, y: 0.29, z: 0.06, w: 0.42, d: 0.42, h: 1.04, c: paper });
  // 모서리 기둥과 네 면의 창살
  for (const x of [0.25, 0.71]) for (const y of [0.25, 0.71]) a.box({ x, y, z: 0.06, w: 0.04, d: 0.04, h: 1.04, c: frame });
  const bars = (
    <g stroke={frame} strokeWidth={0.02}>
      <line x1={0.29} y1={0.42} x2={0.71} y2={0.42} />
      <line x1={0.29} y1={0.76} x2={0.71} y2={0.76} />
      <line x1={0.5} y1={0.06} x2={0.5} y2={1.1} />
    </g>
  );
  const span = { u: [0.29, 0.71] as [number, number], z: [0.06, 1.1] as [number, number] };
  a.face("y", 0.71, span, bars);
  a.face("y", 0.29, { ...span, out: -1 }, bars);
  a.face("x", 0.71, span, bars);
  a.face("x", 0.29, { ...span, out: -1 }, bars);
  a.box({ x: 0.24, y: 0.24, z: 1.1, w: 0.52, d: 0.52, h: 0.04, c: frame });
  a.add([0.3, 0.3, 1.14, 0.7, 0.7, 1.14], (v) => (
    <polygon
      points={v.pts([
        [0.3, 0.3, 1.141],
        [0.7, 0.3, 1.141],
        [0.7, 0.7, 1.141],
        [0.3, 0.7, 1.141],
      ])}
      fill={night ? "#FFF3D2" : paper}
    />
  ));
};

/** 버섯 하나: (x, y) 의 z 에서 줄기 높이 h, 갓 반지름 r. 밤에는 갓이 안에서 빛난다. */
function mushroom(a: Art, x: number, y: number, z: number, h: number, r: number, night: boolean, gid: string) {
  const cap = night ? "#FFBFA2" : "#EF8F84";
  a.cyl({ cx: x, cy: y, z, r: r * 0.34, h, c: night ? "#FFF1CF" : "#F4EAD8", taper: r * 0.06 });
  const R = r * 39.6;
  const H = Math.hypot(R / 2, r * 0.62 * 28);
  a.dot(
    x,
    y,
    z + h,
    (sx, sy) => (
      <>
        {night && halo(gid, sx, sy - H * 0.3, R * 1.9, "#FFC98F", 0.6)}
        <path d={`M ${sx - R} ${sy} A ${R} ${H} 0 0 1 ${sx + R} ${sy} A ${R} ${R / 2} 0 0 1 ${sx - R} ${sy} Z`} fill={cap} stroke={shade(cap, -0.3)} strokeWidth={0.6} />
        {(
          [
            [-0.45, -0.45, 0.17],
            [0.3, -0.7, 0.13],
            [0.55, -0.15, 0.12],
            [-0.05, -0.12, 0.1],
          ] as const
        ).map(([dx, dy, s]) => (
          <ellipse key={dx} cx={sx + dx * R} cy={sy + dy * H} rx={s * R} ry={s * R * 0.7} fill="#FFF8EE" />
        ))}
      </>
    ),
    r,
    r * 0.62,
  );
}

const lampMushroom: Build = (a, { night, id }) => {
  blob(a, 0.5, 0.5, 0.32);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.33, h: 0.05, c: C.wood, top: C.woodLight });
  mushroom(a, 0.42, 0.42, 0.05, 0.3, 0.24, night, `${id}-big`);
  mushroom(a, 0.74, 0.7, 0.05, 0.13, 0.12, night, `${id}-small`);
};

/** 초 불꽃(화면 좌표): 밤에는 크고 밝게, 둘레로 빛이 번진다. */
function flame(x: number, y: number, night: boolean, gid: string) {
  const top = night ? 10 : 8;
  return (
    <>
      {night && halo(gid, x, y - 6, 13, "#FFC25E", 0.75)}
      <line x1={x} y1={y} x2={x} y2={y - 2} stroke={C.ink} strokeWidth={0.8} />
      <path d={`M ${x} ${y - 1} Q ${x + 3.2} ${y - 4.5} ${x} ${y - top} Q ${x - 3.2} ${y - 4.5} ${x} ${y - 1} Z`} fill={night ? "#FFB43C" : "#F6A43A"} />
      <path d={`M ${x} ${y - 2} Q ${x + 1.4} ${y - 4} ${x} ${y - 6} Q ${x - 1.4} ${y - 4} ${x} ${y - 2} Z`} fill={night ? "#FFF6C8" : "#FFE39A"} />
    </>
  );
}

const candles: Build = (a, { night, id }) => {
  blob(a, 0.5, 0.52, 0.32);
  a.cyl({ cx: 0.5, cy: 0.52, r: 0.33, h: 0.04, c: C.wood, top: C.woodLight });
  (
    [
      [0.38, 0.4, 0.09, 0.34, "#F6EEDC"],
      [0.64, 0.44, 0.08, 0.24, "#F3C9C0"],
      [0.46, 0.67, 0.08, 0.15, "#D8E6CB"],
    ] as const
  ).forEach(([x, y, r, h, c], i) => {
    a.cyl({ cx: x, cy: y, z: 0.04, r, h, c });
    a.dot(x, y, 0.04 + h, (sx, sy) => flame(sx, sy, night, `${id}-flame${i}`), 0.03, 0.12);
  });
};

const lantern: Build = (a, { night, id }) => {
  const body = "#5E8A6E";
  blob(a, 0.5, 0.5, 0.22);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.2, h: 0.1, c: body });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.1, r: 0.16, h: 0.27, c: night ? "#FFDB8E" : "#E3EFEF", top: night ? "#FFF0C4" : undefined });
  // 유리 안의 심지와 바깥 철사
  a.dot(
    0.5,
    0.5,
    0.1,
    (sx, sy) => (
      <>
        {night && halo(`${id}-lamp`, sx, sy - 4, 20, "#FFD27A", 0.7)}
        <ellipse cx={sx} cy={sy - 3.6} rx={3} ry={3.8} fill={night ? "#FFF8DE" : "#F1E3BF"} />
        <g stroke={shade(body, -0.25)} strokeWidth={0.7}>
          <line x1={sx - 4.2} y1={sy + 2.6} x2={sx - 4.2} y2={sy - 4.4} />
          <line x1={sx + 4.2} y1={sy + 2.6} x2={sx + 4.2} y2={sy - 4.4} />
        </g>
      </>
    ),
    0.16,
    0.27,
  );
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.37, r: 0.19, h: 0.08, c: body, taper: 0.09 });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.45, r: 0.045, h: 0.03, c: shade(body, -0.2) });
  // 들고 다니는 철사 손잡이
  a.add([0.32, 0.48, 0.42, 0.68, 0.52, 0.62], (v) => (
    <g transform={v.faceT("y", 0.5)}>
      <path d="M 0.33 0.44 A 0.17 0.18 0 0 0 0.67 0.44" fill="none" stroke={C.ink} strokeWidth={0.024} />
    </g>
  ));
};

const fireflyJar: Build = (a, { night, id }) => {
  const glass = night ? "#3E5A5E" : "#D6EBEA";
  blob(a, 0.5, 0.5, 0.22);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.21, h: 0.3, c: glass });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.3, r: 0.21, h: 0.06, c: glass, taper: 0.06 });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.36, r: 0.15, h: 0.03, c: glass });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.39, r: 0.16, h: 0.07, c: "#C9A27A" });
  // 병 안의 반딧불과 유리 반짝임
  const bugs: [number, number, number][] = [
    [-4, -3, 1.3],
    [3, -7, 1.2],
    [5, -1, 1.4],
    [-1, -10, 1.1],
    [-5, -8, 1],
    [1, 1, 1.2],
  ];
  a.dot(
    0.5,
    0.5,
    0.02,
    (sx, sy) => (
      <>
        {night && glowDef(`${id}-bug`, "#E2FF7A", 0.85)}
        {bugs.map(([dx, dy, r]) => (
          <g key={`${dx},${dy}`}>
            {night && <circle cx={sx + dx} cy={sy + dy} r={r * 3.4} fill={`url(#${id}-bug)`} style={{ mixBlendMode: "screen" }} />}
            <circle cx={sx + dx} cy={sy + dy} r={r} fill={night ? "#F4FFB0" : "#B5C94A"} />
          </g>
        ))}
        <path d={`M ${sx - 6} ${sy - 1} L ${sx - 6} ${sy - 8}`} stroke="rgba(255,255,255,.6)" strokeWidth={1.3} strokeLinecap="round" />
      </>
    ),
    0.2,
    0.34,
  );
  // 밤에는 한 마리가 뚜껑 위로 빠져나와 있다
  if (night)
    a.dot(0.5, 0.5, 0.5, (sx, sy) => (
      <>
        {halo(`${id}-out`, sx + 7, sy - 6, 4.4, "#E2FF7A", 0.85)}
        <circle cx={sx + 7} cy={sy - 6} r={1.2} fill="#F4FFB0" />
      </>
    ));
};

const lampMoon: Build = (a, { night, id }) => {
  const R = 0.34;
  const zc = 0.07 + R * 0.92;
  const rr = R * 37;
  blob(a, 0.5, 0.5, 0.24);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.2, h: 0.07, c: C.wood, top: C.woodLight });
  a.dot(
    0.5,
    0.5,
    zc - R,
    (sx, sy) => {
      const cy = sy - R * 28;
      const crater = night ? "#F0D98F" : "#E0D8C6";
      return (
        <>
          {night && halo(`${id}-moon`, sx, cy, rr + 12, "#FFE9A8", 0.6)}
          <circle cx={sx} cy={cy} r={rr} fill={night ? "#FFF1C4" : "#F3EFE4"} stroke={night ? "#E8C978" : "#CFC8B8"} strokeWidth={0.7} />
          <path d={`M ${sx} ${cy - rr} A ${rr} ${rr} 0 0 1 ${sx} ${cy + rr} A ${rr * 0.55} ${rr} 0 0 0 ${sx} ${cy - rr} Z`} fill="rgba(120,100,60,.1)" />
          {(
            [
              [-0.35, -0.3, 0.18],
              [0.25, 0.2, 0.13],
              [-0.1, 0.45, 0.1],
              [0.35, -0.45, 0.08],
              [-0.5, 0.2, 0.07],
            ] as const
          ).map(([dx, dy, s]) => (
            <circle key={dx} cx={sx + dx * rr} cy={cy + dy * rr} r={s * rr} fill={crater} />
          ))}
        </>
      );
    },
    R,
    2 * R,
  );
  // 받침의 앞 테두리가 달 아랫부분을 감싼다
  a.dot(
    0.5,
    0.5,
    0,
    (sx, sy) => {
      const rx = 0.2 * 39.6;
      const t = sy - 0.07 * 28;
      return (
        <path
          d={`M ${sx - rx} ${t} A ${rx} ${rx / 2} 0 0 0 ${sx + rx} ${t} L ${sx + rx} ${sy} A ${rx} ${rx / 2} 0 0 1 ${sx - rx} ${sy} Z`}
          fill={shade(C.wood, -0.06)}
          stroke={shade(C.wood, -0.32)}
          strokeWidth={0.6}
        />
      );
    },
    0.2,
    0.07,
  );
};

export const BUILDS: Record<string, Build> = {
  fridge_big: fridgeBig,
  kitchen_counter: kitchenCounter,
  stove,
  sink,
  microwave_cart: microwaveCart,
  coffee_station: coffeeStation,
  washer,
  washer_dryer: washerDryer,
  air_purifier: airPurifier,
  humidifier,
  fan,
  heater,
  aircon_stand: airconStand,
  robot_vacuum: robotVacuum,
  lamp_arc: lampArc,
  lamp_paper: lampPaper,
  lamp_mushroom: lampMushroom,
  candles,
  lantern,
  firefly_jar: fireflyJar,
  lamp_moon: lampMoon,
};
