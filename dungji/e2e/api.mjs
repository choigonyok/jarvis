// 로컬 Worker(wrangler dev)에 실제 요청을 보내 기능 하나하나를 확인한다.
// 시간이 흘러야 생기는 일(먼지, 마른 화분, 배고픈 물고기)은 로컬 D1 파일을 직접 고쳐 앞당긴다.
//
//   node --experimental-sqlite e2e/api.mjs [http://127.0.0.1:8788] [.wrangler-test]

import { DatabaseSync } from "node:sqlite";
import { readdirSync } from "node:fs";
import { join } from "node:path";

const BASE = process.argv[2] ?? "http://127.0.0.1:8788";
const PERSIST = process.argv[3] ?? ".wrangler-test";
const d1dir = join(PERSIST, "v3/d1/miniflare-D1DatabaseObject");
const dbFile = join(d1dir, readdirSync(d1dir).find((f) => f.endsWith(".sqlite") && f !== "metadata.sqlite"));
const db = new DatabaseSync(dbFile);
const sql = (q, ...args) => db.prepare(q).run(...args);
const one = (q, ...args) => db.prepare(q).get(...args);
// 시험은 같은 주소에서 여러 번 가입한다: 하루 가입 시도 수를 비운다.
db.prepare("DELETE FROM claims WHERE kind = 'signup'").run();

let failed = 0;
let passed = 0;
function check(name, ok, detail) {
  if (ok) passed++;
  else failed++;
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${!ok && detail !== undefined ? `\n     ${JSON.stringify(detail)}` : ""}`);
}

async function pbkdf2(login, pw) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(pw), "PBKDF2", false, ["deriveBits"]);
  const bits = await crypto.subtle.deriveBits(
    { name: "PBKDF2", hash: "SHA-256", salt: new TextEncoder().encode(`dungji:v1:${login}`), iterations: 600_000 },
    key,
    256,
  );
  return [...new Uint8Array(bits)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

class Client {
  constructor(label) {
    this.label = label;
    this.cookie = "";
  }
  async req(method, path, body) {
    const res = await fetch(BASE + "/api" + path, {
      method,
      headers: { "content-type": "application/json", "x-dungji": "1", cookie: this.cookie },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const set = res.headers.get("set-cookie");
    if (set) this.cookie = set.split(";")[0];
    const json = await res.json().catch(() => null);
    return { status: res.status, ...json };
  }
  get = (p) => this.req("GET", p);
  post = (p, b = {}) => this.req("POST", p, b);
  put = (p, b = {}) => this.req("PUT", p, b);
  patch = (p, b = {}) => this.req("PATCH", p, b);
  del = (p) => this.req("DELETE", p);
}

const stamp = Date.now().toString(36).slice(-5);
const a = new Client("a");
const b = new Client("b");
const c = new Client("c");
const d = new Client("d");

// ── 가입 ──
const resA = await a.post("/auth/signup", { login: `yun_${stamp}`, name: "윤", pw: await pbkdf2(`yun_${stamp}`, "pw-a-123"), mode: "create", homeName: "햇살 둥지" });
check("둥지를 만들며 가입", resA.status === 200 && resA.me?.home, resA);
const slugA = resA.me.home;

let home = (await a.get(`/homes/${slugA}`)).home;
check("처음 둥지: 깃털 300 + 첫 방문 20", home.coins === 320, home.coins);
check("처음 둥지: 물건 여섯 개가 방에", home.items.filter((i) => i.place === "room").length === 6, home.items.length);
check("둘째 사람을 기다리는 초대 코드", /^[A-Z2-9]{6}$/.test(home.invite ?? ""), home.invite);
check("첫 방문 깃털은 하루 한 번", (await a.get(`/homes/${slugA}`)).rewards.length === 0);

const bad = await b.post("/auth/signup", { login: `eun_${stamp}`, name: "은", pw: await pbkdf2(`eun_${stamp}`, "pw-b-123"), mode: "join", invite: "ZZZZZZ" });
check("틀린 초대 코드는 거절", bad.status === 404, bad);
const resB = await b.post("/auth/signup", { login: `eun_${stamp}`, name: "은", pw: await pbkdf2(`eun_${stamp}`, "pw-b-123"), mode: "join", invite: home.invite.toLowerCase() });
check("초대 코드로 같은 둥지에", resB.me?.home === slugA, resB);
const third = await d.post("/auth/signup", { login: `third_${stamp}`, name: "셋", pw: await pbkdf2(`third_${stamp}`, "x-123456"), mode: "join", invite: home.invite });
check("쓴 초대 코드는 다시 못 쓴다", third.status === 404, third);

home = (await b.get(`/homes/${slugA}`)).home;
check("둘이 사는 둥지(초대 코드 사라짐)", home.members.length === 2 && home.invite === null, home.members);
check("상대도 첫 방문 깃털", home.coins === 340, home.coins);

const resC = await c.post("/auth/signup", { login: `moon_${stamp}`, name: "달", pw: await pbkdf2(`moon_${stamp}`, "pw-c-123"), mode: "create", homeName: "달빛 둥지" });
const slugC = resC.me.home;
await d.post("/auth/signup", { login: `star_${stamp}`, name: "별", pw: await pbkdf2(`star_${stamp}`, "pw-d-123"), mode: "create", homeName: "별 둥지" });

// ── 로그인 ──
const login = new Client("a2");
const wrong = await login.post("/auth/login", { login: `yun_${stamp}`, pw: await pbkdf2(`yun_${stamp}`, "nope") });
check("틀린 비밀번호", wrong.status === 401, wrong);
const right = await login.post("/auth/login", { login: `yun_${stamp}`, pw: await pbkdf2(`yun_${stamp}`, "pw-a-123") });
check("맞는 비밀번호", right.status === 200 && right.me?.home === slugA, right);
const noHeader = await fetch(BASE + "/api/auth/logout", { method: "POST", headers: { cookie: login.cookie } });
check("우리 머리글 없는 쓰기는 막는다", noHeader.status === 403);

// ── 상점·창고 ──
let r = await a.post("/shop/item", { key: "desk" });
check("책상 사기 → 창고", r.status === 200 && r.home.items.some((i) => i.key === "desk" && i.place === "storage"), r);
check("깃털이 줄었다(340-160)", r.home.coins === 180, r.home.coins);
const desk = r.home.items.find((i) => i.key === "desk");
r = await a.post("/shop/item", { key: "bed_wood" });
check("깃털이 모자라면 못 산다", r.status === 409 && r.error === "no_coins", r);

sql("UPDATE couples SET coins = 5000 WHERE slug = ?", slugA);
for (const key of ["snack_box", "cushion", "chair_wood"]) await a.post("/shop/item", { key });
r = await a.get(`/homes/${slugA}`);
check("창고 네 칸이 찼다", r.home.items.filter((i) => i.place === "storage").length === 4);
r = await a.post("/shop/item", { key: "table_low" });
check("창고가 꽉 차면 방에 바로 놓는다", r.status === 200 && r.home.items.some((i) => i.key === "table_low" && i.place === "room"), r.rewards);
const table = r.home.items.find((i) => i.key === "table_low");
r = await a.post(`/items/${table.id}/store`);
check("꽉 찬 창고에는 못 넣는다", r.status === 409 && r.error === "storage_full", r);
r = await a.post("/storage/expand");
check("창고 넓히기(80 깃털, 5칸)", r.status === 200 && r.home.storageSlots === 5 && r.rewards[0].delta === -80, r);
r = await a.post(`/items/${table.id}/store`);
check("넓힌 창고에 넣기", r.status === 200 && r.home.items.find((i) => i.id === table.id).place === "storage", r);
r = await a.post(`/items/${desk.id}/place`);
const placedDesk = r.home.items.find((i) => i.id === desk.id);
check("창고에서 꺼내 빈 자리에 놓기", placedDesk.place === "room", placedDesk);
r = await a.post(`/items/${desk.id}/move`, { x: 0, y: 1, flip: false });
check("침대 자리로는 못 옮긴다", r.status === 409, r);
const before = r;
r = await a.post(`/items/${desk.id}/move`, { x: 5, y: 2, flip: true });
check("빈 자리로 옮기고 뒤집기", r.status === 200 && r.home.items.find((i) => i.id === desk.id).flip === true, r.message ?? before.message);
const snack = r.home.items.find((i) => i.key === "snack_box");
const coinsBefore = r.home.coins;
r = await a.post(`/items/${snack.id}/sell`);
check("창고의 물건 팔기(반값)", r.status === 200 && r.home.coins === coinsBefore + 12 && !r.home.items.some((i) => i.id === snack.id), r);
r = await a.post("/shop/theme", { key: "paper_dot" });
check("벽지 사서 바르기", r.home.wallpaper === "paper_dot" && r.home.themes.includes("paper_dot"), r);
const c1 = r.home.coins;
r = await a.post("/shop/theme", { key: "paper_plain" });
check("가진 벽지로 바꾸기는 공짜", r.home.wallpaper === "paper_plain" && r.home.coins === c1, r);

// ── 화분 ──
const tulip = r.home.items.find((i) => i.key === "plant_tulip");
r = await a.post(`/items/${tulip.id}/water`);
check("촉촉한 화분에는 물을 안 준다", r.status === 409, r);
const st = JSON.parse(one("SELECT state FROM items WHERE id = ?", tulip.id).state);
sql("UPDATE items SET state = ? WHERE id = ?", JSON.stringify({ ...st, water: 0, dryAt: Date.now() - 30 * 3600_000, t: Date.now() }), tulip.id);
r = await a.get(`/homes/${slugA}`);
check("마른 지 하루가 지난 화분은 시든다", r.home.items.find((i) => i.id === tulip.id).plant.wilted === true);
r = await a.post(`/items/${tulip.id}/water`);
check("물 주기(+2)", r.status === 200 && r.home.items.find((i) => i.id === tulip.id).plant.water === 100 && r.rewards[0].delta === 2, r);
sql("UPDATE items SET state = ? WHERE id = ?", JSON.stringify({ water: 100, growth: 72, dryAt: null, t: Date.now() }), tulip.id);
r = await a.post(`/items/${tulip.id}/harvest`);
check("다 핀 튤립 거두기(+40)", r.status === 200 && r.rewards[0].delta === 40 && r.home.items.find((i) => i.id === tulip.id).plant.stage === 3, r);
r = await a.post(`/items/${tulip.id}/harvest`);
check("덜 자란 화분은 못 거둔다", r.status === 409, r);

// ── 어항 ──
r = await a.post("/shop/item", { key: "tank_small" });
let tank = r.home.items.find((i) => i.key === "tank_small");
if (tank.place === "storage") r = await a.post(`/items/${tank.id}/place`);
tank = r.home.items.find((i) => i.key === "tank_small");
check("어항을 방에", tank.place === "room", tank);
r = await a.post(`/items/${tank.id}/feed`);
check("물고기 없는 어항에는 먹이를 못 준다", r.status === 409, r);
for (const species of ["fish_gold", "fish_neon", "fish_betta"]) r = await a.post("/shop/fish", { species, tankId: tank.id });
check("물고기 세 마리", r.home.items.find((i) => i.id === tank.id).tank.fish.length === 3, r);
r = await a.post("/shop/fish", { species: "fish_guppy", tankId: tank.id });
check("작은 어항은 세 마리까지", r.status === 409 && r.error === "tank_full", r);
r = await a.post(`/items/${tank.id}/feed`);
check("배부른 물고기", r.status === 409, r);
sql("UPDATE fish SET hunger = 10, t = ? WHERE tank_id = ?", Date.now(), tank.id);
r = await a.post(`/items/${tank.id}/feed`);
check("먹이 주기(+1)", r.status === 200 && r.home.items.find((i) => i.id === tank.id).tank.fish.every((f) => f.hunger === 100), r);
sql("UPDATE items SET state = ? WHERE id = ?", JSON.stringify({ clarity: 30, t: Date.now() }), tank.id);
r = await a.post(`/items/${tank.id}/scrub`);
check("어항 물 갈기(+3)", r.status === 200 && r.home.items.find((i) => i.id === tank.id).tank.clarity === 100, r);
const fish = r.home.items.find((i) => i.id === tank.id).tank.fish[0];
r = await a.patch(`/fish/${fish.id}`, { name: "주황이" });
check("물고기 이름 짓기", r.home.items.find((i) => i.id === tank.id).tank.fish[0].name === "주황이", r);

// ── 먼지·청소 ──
const homeRow = one("SELECT id, dirt_slot FROM couples WHERE slug = ?", slugA);
sql("UPDATE couples SET dirt_slot = dirt_slot - 9 WHERE id = ?", homeRow.id);
r = await a.get(`/homes/${slugA}`);
check("아홉 시간 넘게 비우면 먼지가 쌓인다", r.home.dirt.length === 9 && r.home.cleanliness === 25, { n: r.home.dirt.length, c: r.home.cleanliness });
r = await a.get(`/homes/${slugA}`);
check("다시 읽어도 먼지가 늘지 않는다", r.home.dirt.length === 9, r.home.dirt.length);
const spot = r.home.dirt[0];
r = await a.post(`/homes/${slugA}/dirt/${spot.id}/clean`);
check("주인이 치우기(+3)", r.status === 200 && r.home.dirt.length === 8 && r.rewards[0].delta === 3, r);
r = await a.post(`/homes/${slugA}/dirt/${spot.id}/clean`);
check("이미 치운 먼지", r.status === 404, r);

// ── 놀러 가기 ──
let visit = await c.get(`/homes/${slugA}`);
check("이웃이 놀러 오면 방은 보이지만 창고·깃털은 안 보인다", visit.status === 200 && visit.home.coins === undefined && visit.home.items.every((i) => i.place === "room"), visit.home);
check("놀러 온 수", visit.home.visits.today === 1 && visit.home.visits.total === 1 && visit.home.visits.recent[0]?.slug === slugC, visit.home.visits);
visit = await c.get(`/homes/${slugA}`);
check("같은 날 다시 와도 한 번", visit.home.visits.total === 1);
check("이웃은 하루 세 개까지 치울 수 있다", visit.home.helpLeft === 3);
const cCoins = (await c.get(`/homes/${slugC}`)).home.coins;
for (const s of visit.home.dirt.slice(0, 3)) r = await c.post(`/homes/${slugA}/dirt/${s.id}/clean`);
check("이웃 청소 도와주기", r.status === 200 && r.home.dirt.length === 5 && r.home.helpLeft === 0, r);
r = await c.post(`/homes/${slugA}/dirt/${visit.home.dirt[3].id}/clean`);
check("넷째부터는 막힌다", r.status === 409 && r.error === "limit", r);
check("치워 준 깃털은 이웃의 둥지로(+6)", (await c.get(`/homes/${slugC}`)).home.coins === cCoins + 6);
r = await c.post(`/items/${tank.id}/feed`);
check("이웃은 남의 물고기에게 먹이를 못 준다", r.status === 404, r);

// ── 방명록 ──
r = await c.post(`/homes/${slugA}/guestbook`, { body: "방이 따뜻하네요! 또 올게요" });
check("방명록 쓰기(+5)", r.status === 200 && r.entries[0].body.includes("따뜻") && r.rewards[0]?.delta === 5, r);
r = await c.post(`/homes/${slugA}/guestbook`, { body: "비밀 이야기", secret: true });
const secretId = r.entries[0].id;
r = await d.get(`/homes/${slugA}/guestbook`);
check("비밀글은 다른 이웃에게 가려진다", r.entries.find((e) => e.id === secretId).hidden === true && r.entries.find((e) => e.id === secretId).body === "", r.entries[0]);
r = await b.get(`/homes/${slugA}/guestbook`);
check("비밀글도 주인에게는 보인다", r.entries.find((e) => e.id === secretId).body === "비밀 이야기");
r = await a.post(`/homes/${slugA}/guestbook`, { body: "내 방명록" });
check("주인은 자기 방명록에 새 글 대신 답글", r.status === 409, r);
r = await a.post(`/guestbook/${secretId}/reply`, { body: "고마워요" });
check("답글", r.entries.find((e) => e.id === secretId).reply === "고마워요", r);
r = await d.del(`/guestbook/${secretId}`);
check("남의 글은 못 지운다", r.status === 404, r);
r = await c.del(`/guestbook/${secretId}`);
check("쓴 사람이 지우기", r.status === 200 && !r.entries.some((e) => e.id === secretId), r);

// ── 커플 일기 ──
const today = new Date(Date.now() + 9 * 3600_000).toISOString().slice(0, 10);
r = await a.put(`/diary/${today}`, { mood: "heart", body: "오늘은 같이 떡볶이를 먹었다." });
check("오늘 일기(+15)", r.status === 200 && r.entries.length === 1 && r.rewards[0]?.delta === 15, r);
r = await a.put(`/diary/${today}`, { mood: "sun", body: "오늘은 같이 떡볶이를 먹었다. 맛있었다." });
check("고쳐 써도 깃털은 한 번", r.rewards.length === 0 && r.entries[0].mood === "sun", r);
r = await b.put(`/diary/${today}`, { mood: "cloud", body: "조금 매웠다" });
check("상대도 같은 날 일기", r.entries.length === 2, r);
r = await b.post(`/diary/${today}/comments`, { body: "다음엔 덜 맵게!" });
check("일기 댓글", r.comments.length === 1 && r.comments[0].author === "은", r);
r = await a.get(`/diary?month=${today.slice(0, 7)}`);
check("달력에 둘 다 쓴 날", r.days[today]?.authors.length === 2 && r.days[today].comments === 1, r);
r = await c.get(`/diary/${today}`);
check("다른 둥지는 우리 일기를 못 본다", r.status === 200 && r.entries.length === 0, r);
r = await a.put(`/diary/2999-01-01`, { mood: "sun", body: "미래" });
check("앞날의 일기는 못 쓴다", r.status === 400, r);

// ── 이웃 목록 ──
r = await c.get(`/neighbors`);
check("이웃 목록에 다른 둥지", r.neighbors.some((n) => n.slug === slugA) && !r.neighbors.some((n) => n.slug === slugC), r.neighbors.map((n) => n.name));
r = await c.get(`/neighbors?q=${encodeURIComponent("햇살")}`);
check("이름으로 찾기", r.neighbors.length >= 1 && r.neighbors.every((n) => n.name.includes("햇살") || n.members.some((m) => m.includes("햇살"))), r);
r = await c.get(`/neighbors?q=${encodeURIComponent("은")}`);
check("사람 이름으로 찾기", r.neighbors.some((n) => n.slug === slugA), r);
r = await c.get(`/neighbors/random`);
check("아무 둥지나", r.status === 200 && r.slug && r.slug !== slugC, r);
r = await a.patch("/home", { listed: false });
r = await c.get(`/neighbors`);
check("숨긴 둥지는 목록에서 빠진다", !r.neighbors.some((n) => n.slug === slugA));
await a.patch("/home", { listed: true, intro: "둘이 사는 작은 방", since: "2024-05-01" });
r = await a.get(`/neighbors/visitors`);
check("우리 둥지에 다녀간 이웃", r.visitors.some((v) => v.slug === slugC), r);

// ── 깃털 내역 ──
r = await a.get("/ledger");
check("깃털 내역", r.rows.length > 5 && r.rows.some((x) => x.reason === "물 주기"), r.rows?.slice(0, 3));

// ── 실시간: 들어와 있음, 통화 신호 ──
function open(client) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(BASE.replace("http", "ws") + "/api/live", { headers: { cookie: client.cookie } });
    const inbox = [];
    const waiters = [];
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data);
      const w = waiters.findIndex((x) => x.pred(m));
      if (w >= 0) waiters.splice(w, 1)[0].resolve(m);
      else inbox.push(m);
    };
    ws.onerror = () => reject(new Error("ws error"));
    ws.onopen = () =>
      resolve({
        ws,
        send: (m) => ws.send(JSON.stringify(m)),
        wait: (pred, ms = 3000) =>
          new Promise((res, rej) => {
            const i = inbox.findIndex(pred);
            if (i >= 0) return res(inbox.splice(i, 1)[0]);
            const t = setTimeout(() => rej(new Error("timeout")), ms);
            waiters.push({ pred, resolve: (m) => (clearTimeout(t), res(m)) });
          }),
      });
  });
}

const wa = await open(a);
const hello = await wa.wait((m) => m.type === "hello");
check("실시간 통로가 열린다", Boolean(hello.sid));
const wb = await open(b);
const helloB = await wb.wait((m) => m.type === "hello");
const pres = await wa.wait((m) => m.type === "presence" && m.online.length === 2);
check("둘 다 들어와 있으면 둘 다 보인다", pres.online.length === 2);
r = await c.get(`/homes/${slugA}`);
check("이웃에게도 불 켜진 창(둘 다 들어와 있음)", r.home.members.every((m) => m.online), r.home.members);

wb.send({ type: "call", sdp: "v=0 offer" });
const ring = await wa.wait((m) => m.type === "call");
check("전화가 상대에게 간다", ring.sdp === "v=0 offer" && ring.fromSid === helloB.sid && ring.name === "은", ring);
const wa2 = await open(a); // 같은 사람의 다른 창
await wa2.wait((m) => m.type === "hello");
wb.send({ type: "call", sdp: "v=0 offer2" });
await wa.wait((m) => m.type === "call" && m.sdp === "v=0 offer2");
await wa2.wait((m) => m.type === "call" && m.sdp === "v=0 offer2");
wa.send({ type: "answer", to: helloB.sid, sdp: "v=0 answer" });
const ans = await wb.wait((m) => m.type === "answer");
check("받은 쪽의 답이 건 쪽에만", ans.sdp === "v=0 answer" && ans.to === helloB.sid, ans);
const taken = await wa2.wait((m) => m.type === "taken");
check("같은 사람의 다른 창은 울림을 멈춘다", Boolean(taken));
wb.send({ type: "ice", to: hello.sid, candidate: { candidate: "candidate:1 1 udp 1 1.2.3.4 5 typ host", sdpMid: "0", sdpMLineIndex: 0 } });
const ice = await wa.wait((m) => m.type === "ice");
check("ICE 후보 전달", ice.candidate.sdpMid === "0", ice);
let leak = false;
wa2.wait((m) => m.type === "ice", 500).then(() => (leak = true)).catch(() => {});
await new Promise((res) => setTimeout(res, 600));
check("받는 사람이 정해진 말은 다른 창에 안 간다", !leak);
await a.post(`/homes/${slugA}/dirt/${(await a.get(`/homes/${slugA}`)).home.dirt[0].id}/clean`);
const changed = await wb.wait((m) => m.type === "changed");
check("상대가 바꾸면 '바뀌었어요'가 온다", changed.what === "home", changed);
wb.ws.close();
const gone = await wa.wait((m) => m.type === "presence" && m.online.length === 1);
check("상대가 나가면 불이 꺼진다", gone.online.length === 1);
wa.ws.close();
wa2.ws.close();
r = await a.get("/call/ice");
check("STUN 서버 목록", r.iceServers?.[0]?.urls?.some((u) => u.startsWith("stun:")), r);

// ── jarvis 에서 들어오기(입장권) ──
const SSO_SECRET = process.env.JARVIS_SSO_SECRET ?? "local-test-secret-for-dungji";
async function ticket(who, { exp = Math.floor(Date.now() / 1000) + 60, secret = SSO_SECRET } = {}) {
  const nonce = [...crypto.getRandomValues(new Uint8Array(16))].map((b) => b.toString(16).padStart(2, "0")).join("");
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = [...new Uint8Array(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`dungji:${who}:${exp}:${nonce}`)))]
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  return `v1.${who}.${exp}.${nonce}.${sig}`;
}
async function enter(t) {
  const res = await fetch(`${BASE}/api/sso/jarvis?ticket=${encodeURIComponent(t)}`, { redirect: "manual" });
  const set = res.headers.get("set-cookie");
  return { status: res.status, location: res.headers.get("location"), cookie: set ? set.split(";")[0] : "", body: res.status >= 400 ? await res.text() : "" };
}
db.prepare("DELETE FROM sso_links").run();
const tOwner = await ticket("owner");
const owner1 = await enter(tOwner);
check("jarvis 입장권으로 들어오면 둥지로 보낸다", owner1.status === 302 && /^\/h\/[a-z0-9]+$/.test(owner1.location ?? "") && owner1.cookie.startsWith("dj="), owner1);
check("같은 입장권은 두 번 못 쓴다", (await enter(tOwner)).status === 401);
check("만료된 입장권", (await enter(await ticket("owner", { exp: Math.floor(Date.now() / 1000) - 5 }))).status === 401);
check("다른 비밀로 서명한 입장권", (await enter(await ticket("owner", { secret: "nope" }))).status === 401);
check("모양이 틀린 입장권", (await enter("v1.owner.1.2.3")).status === 400);
const jOwner = new Client("jo");
jOwner.cookie = owner1.cookie;
let jm = (await jOwner.get("/me")).me;
check("처음 온 운영자: 이름은 아직, jarvis 로 들어옴", jm.named === false && jm.via === "jarvis" && jm.partner === null, jm);
const guest1 = await enter(await ticket("guest"));
check("손님은 운영자의 둥지로 들어간다", guest1.status === 302 && guest1.location === owner1.location, { o: owner1.location, g: guest1.location });
const jGuest = new Client("jg");
jGuest.cookie = guest1.cookie;
r = await jGuest.patch("/me", { name: "손님" });
check("이름을 정하면 named", r.me.named === true && r.me.name === "손님" && r.me.partner?.id === jm.id, r.me);
jm = (await jOwner.get("/me")).me;
check("운영자에게 상대가 생긴다", jm.partner?.name === "손님", jm);
const owner2 = await enter(await ticket("owner"));
const again = new Client("jo2");
again.cookie = owner2.cookie;
check("다시 들어와도 같은 사람", (await again.get("/me")).me.id === jm.id);
r = await new Client("x").post("/auth/login", { login: jm.login, pw: "0".repeat(64) });
check("jarvis 계정은 비밀번호로 못 들어온다", r.status === 401, r);

// ── 가입 시도 제한 ──
db.prepare("DELETE FROM claims WHERE kind = 'signup'").run();
let blocked = null;
for (let i = 0; i < 22; i++) {
  const r = await new Client("x").post("/auth/signup", { login: `guess_${i}_${stamp}`, name: "x", pw: "0".repeat(64), mode: "join", invite: "AAAAAA" });
  if (r.status === 429) {
    blocked = i;
    break;
  }
}
check("한 주소에서 하루 스무 번 넘게 가입을 시도하면 막힌다", blocked === 20, blocked);
db.prepare("DELETE FROM claims WHERE kind = 'signup'").run();

console.log(`\n${passed} passed, ${failed} failed`);
db.close();
process.exit(failed ? 1 : 0);
