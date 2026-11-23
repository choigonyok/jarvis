// 둥지마다 하나: 지금 들어와 있는 사람(불 켜진 창), 둘 사이 통화의 신호, 상대에게 알릴 한 일.
// 소리는 여기를 지나지 않는다. 브라우저 둘이 WebRTC 로 직접 주고받고, 여기서는
// 연결을 맺는 데 필요한 말(SDP·ICE)만 상대에게 건넨다.
//
// 둘 중 한 사람이 우리 둥지에서 무언가 하면(worker/index.ts 의 notify) 들어와 있는 창에 바로 알리고
// (changed - 상대의 창은 한 줄로 띄운다), 상대의 jarvis 로 갈 것은 장부(worker/tell.ts)에 모았다가
// 한 통으로 보낸다. 보낼 때 상대가 둥지를 보고 있으면 보내지 않는다: 이미 눈앞에서 봤다.
// 상대가 jarvis 에서 들어온 사람이 아니면(다른 커플) 보낼 곳이 없다.

import { DurableObject } from "cloudflare:workers";
import type { LiveMessage } from "../shared/api";
import { notice, UNNAMED, type Act } from "../shared/news";
import { dayKey } from "../shared/rules";
import { add, again, GRACE, nextWake, take, type Book, type JarvisDoor, type Pending } from "./tell";

interface Attachment {
  userId: string;
  name: string;
  sid: string;
  /** 이 창이 보이나: 화면이 켜져 있고, 콘솔 안이면 둥지 칸이 열려 있다(src/lib/live.tsx 의 seen). */
  seen?: boolean;
  /** 연결했거나 seen 을 마지막으로 들은 때 */
  at?: number;
}

type Relay = Extract<LiveMessage, { type: "call" | "answer" | "ice" | "hangup" | "decline" }>;
const RELAYED = new Set(["call", "answer", "ice", "hangup", "decline"]);

/** 창은 25초마다 ping 한다(src/lib/live.tsx). 이만큼 아무 소식이 없으면 잠든 창이다. */
const FRESH = 75_000;

export class HomeLive extends DurableObject<Env> {
  /** 떠난 뒤 기다리는 시간. 시험에서는 TELL_AFTER_MS 로 줄인다. */
  private readonly grace: number;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    // 30초마다 오는 ping 에는 깨지 않고 답한다.
    ctx.setWebSocketAutoResponse(new WebSocketRequestResponsePair("ping", "pong"));
    const after = Number((env as { TELL_AFTER_MS?: string }).TELL_AFTER_MS);
    this.grace = Number.isFinite(after) && after > 0 ? after : GRACE;
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    switch (url.pathname) {
      case "/connect": {
        if (request.headers.get("Upgrade") !== "websocket") return new Response("websocket only", { status: 426 });
        const userId = request.headers.get("x-user-id") ?? "";
        const name = decodeURIComponent(request.headers.get("x-user-name") ?? "");
        const pair = new WebSocketPair();
        const [client, server] = Object.values(pair);
        const sid = crypto.randomUUID();
        this.ctx.acceptWebSocket(server, [userId]);
        server.serializeAttachment({ userId, name, sid, seen: true, at: Date.now() } satisfies Attachment);
        server.send(JSON.stringify({ type: "hello", sid } satisfies LiveMessage));
        this.presence();
        await this.moved(userId);
        return new Response(null, { status: 101, webSocket: client });
      }
      case "/presence":
        return Response.json({ online: this.online() });
      case "/notify": {
        const body = (await request.json()) as {
          what: "home" | "diary" | "guestbook";
          by: string;
          name?: string;
          couple?: string;
          acts?: Act[];
        };
        const acts = Array.isArray(body.acts) ? body.acts : [];
        this.broadcast({ type: "changed", what: body.what, by: body.by, ...(acts.length ? { acts, name: body.name ?? UNNAMED } : {}) });
        if (acts.length && body.by && body.couple) await this.note(body.couple, body.by, acts);
        return new Response("ok");
      }
      default:
        return new Response("not found", { status: 404 });
    }
  }

  async webSocketMessage(ws: WebSocket, raw: string | ArrayBuffer) {
    if (typeof raw !== "string" || raw.length > 64_000) return;
    let msg: Relay | { type: "seen"; on: boolean };
    try {
      msg = JSON.parse(raw) as typeof msg;
    } catch {
      return;
    }
    if (!msg) return;
    const me = ws.deserializeAttachment() as Attachment;
    if (msg.type === "seen") {
      ws.serializeAttachment({ ...me, seen: msg.on !== false, at: Date.now() } satisfies Attachment);
      await this.moved(me.userId);
      return;
    }
    if (!RELAYED.has(msg.type)) return;
    const out = JSON.stringify({ ...msg, from: me.userId, fromSid: me.sid, name: me.name });
    const to = "to" in msg ? msg.to : undefined;
    for (const other of this.ctx.getWebSockets()) {
      if (other === ws) continue;
      const a = other.deserializeAttachment() as Attachment;
      // 받는 사람이 정해진 말은 그 창에만, 아니면 상대의 모든 창에(같은 사람의 다른 창은 빼고).
      if (to ? a.sid === to : a.userId !== me.userId) send(other, out);
    }
    // 전화를 받은 창이 정해지면 같은 사람의 다른 창은 울리지 않게 한다.
    if (msg.type === "answer" || msg.type === "decline") {
      const taken = JSON.stringify({ type: "taken", from: me.userId, fromSid: me.sid } satisfies LiveMessage);
      for (const other of this.ctx.getWebSockets(me.userId)) if (other !== ws) send(other, taken);
    }
  }

  async webSocketClose(ws: WebSocket, code: number) {
    try {
      ws.close(code === 1005 || code === 1006 ? 1000 : code, "bye");
    } catch {
      // 이미 닫혔다.
    }
    this.presence(ws);
    await this.moved((ws.deserializeAttachment() as Attachment).userId, ws);
  }

  async webSocketError(ws: WebSocket) {
    this.presence(ws);
    await this.moved((ws.deserializeAttachment() as Attachment).userId, ws);
  }

  /** 상대에게 알릴 것을 보낼 때가 됐다. */
  async alarm() {
    const now = Date.now();
    const couple = await this.ctx.storage.get<string>("couple");
    const { ready, rest } = take(await this.book(), now, this.grace);
    // 보내는 동안(바깥을 기다리는 사이) 들어온 한 일은 새 장부에 쌓이게 먼저 떼어 둔다.
    await this.ctx.storage.put("book", rest);
    for (const [by, sent] of ready) {
      let ok: boolean;
      try {
        ok = await this.tell(couple, by, sent, now);
      } catch (err) {
        console.warn("상대에게 알리지 못했습니다", { err: String(err) });
        ok = false;
      }
      if (ok) continue;
      const book = await this.book();
      const next = again(sent, book[by], Date.now());
      if (next) book[by] = next;
      else delete book[by];
      await this.ctx.storage.put("book", book);
    }
    await this.wake(await this.book());
  }

  /** 한 사람이 한 일을 장부에 */
  private async note(couple: string, by: string, acts: Act[]) {
    const book = await this.book();
    book[by] = add(book[by], acts, Date.now(), this.looking(by));
    await this.ctx.storage.put({ book, couple });
    await this.wake(book);
  }

  /** 한 사람이 둥지를 보기 시작했거나 그만뒀을 수 있다: 떠난 때를 적거나 지운다. */
  private async moved(userId: string, closing?: WebSocket) {
    const book = await this.book();
    const p = book[userId];
    if (!p || p.retry !== undefined) return;
    const here = this.looking(userId, Date.now(), closing);
    if (here === (p.left === undefined)) return;
    const next: Pending = { ...p };
    if (here) delete next.left;
    else next.left = Date.now();
    book[userId] = next;
    await this.ctx.storage.put("book", book);
    await this.wake(book);
  }

  /** 상대에게 한 통. 보낼 곳이 없거나 보낼 필요가 없으면 보낸 것으로 친다(다시 보내지 않는다). */
  private async tell(couple: string | undefined, by: string, p: Pending, now: number): Promise<boolean> {
    // 생성된 Env 는 서비스 바인딩을 fetch 로만 안다: Nest 문은 RPC 메서드 tell 이다.
    const door = (this.env as unknown as { JARVIS?: JarvisDoor }).JARVIS;
    if (!couple || !door) return true;
    const rows = await this.env.DB.prepare(
      `SELECT u.id, u.name, u.named, l.subject FROM users u
       LEFT JOIN sso_links l ON l.user_id = u.id AND l.provider = 'jarvis'
       WHERE u.couple_id = ?`,
    )
      .bind(couple)
      .all<{ id: string; name: string; named: number; subject: string | null }>();
    const actor = rows.results.find((r) => r.id === by);
    const partner = rows.results.find((r) => r.id !== by);
    if (!actor || !partner || (partner.subject !== "owner" && partner.subject !== "guest")) return true;
    if (this.looking(partner.id, now)) return true;
    const words = notice(actor.named ? actor.name : UNNAMED, p.acts, dayKey(now));
    if (!words) return true;
    const r = await door.tell({ to: partner.subject, title: words.title, body: words.body, key: `${by}:${p.first}` });
    // 거절(4xx)은 다시 보내도 같다.
    return r.ok || (r.status >= 400 && r.status < 500);
  }

  private async book(): Promise<Book> {
    return (await this.ctx.storage.get<Book>("book")) ?? {};
  }

  private async wake(book: Book) {
    const at = nextWake(book, this.grace);
    if (at === null) await this.ctx.storage.deleteAlarm();
    else await this.ctx.storage.setAlarm(at);
  }

  /** 그 사람이 지금 둥지를 보고 있나: 보이는 창이 있고, 그 창이 잠들지 않았다. */
  private looking(userId: string, now = Date.now(), except?: WebSocket) {
    for (const ws of this.ctx.getWebSockets(userId)) {
      if (ws === except || ws.readyState !== WebSocket.OPEN) continue;
      const a = ws.deserializeAttachment() as Attachment;
      if (a.seen === false) continue;
      const pinged = this.ctx.getWebSocketAutoResponseTimestamp(ws)?.getTime() ?? 0;
      if (now - Math.max(a.at ?? 0, pinged) < FRESH) return true;
    }
    return false;
  }

  private online(except?: WebSocket) {
    const ids = new Set<string>();
    for (const ws of this.ctx.getWebSockets()) {
      if (ws === except || ws.readyState !== WebSocket.OPEN) continue;
      ids.add((ws.deserializeAttachment() as Attachment).userId);
    }
    return [...ids];
  }

  private presence(except?: WebSocket) {
    this.broadcast({ type: "presence", online: this.online(except) }, except);
  }

  private broadcast(msg: LiveMessage, except?: WebSocket) {
    const out = JSON.stringify(msg);
    for (const ws of this.ctx.getWebSockets()) if (ws !== except) send(ws, out);
  }
}

function send(ws: WebSocket, data: string) {
  try {
    ws.send(data);
  } catch {
    // 닫히는 중인 소켓
  }
}
