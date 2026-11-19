// 둥지마다 하나: 지금 들어와 있는 사람(불 켜진 창)과 둘 사이 통화의 신호를 잇는다.
// 소리는 여기를 지나지 않는다. 브라우저 둘이 WebRTC 로 직접 주고받고, 여기서는
// 연결을 맺는 데 필요한 말(SDP·ICE)만 상대에게 건넨다.

import { DurableObject } from "cloudflare:workers";
import type { LiveMessage } from "../shared/api";

interface Attachment {
  userId: string;
  name: string;
  sid: string;
}

type Relay = Extract<LiveMessage, { type: "call" | "answer" | "ice" | "hangup" | "decline" }>;
const RELAYED = new Set(["call", "answer", "ice", "hangup", "decline"]);

export class HomeLive extends DurableObject {
  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    // 30초마다 오는 ping 에는 깨지 않고 답한다.
    ctx.setWebSocketAutoResponse(new WebSocketRequestResponsePair("ping", "pong"));
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
        server.serializeAttachment({ userId, name, sid } satisfies Attachment);
        server.send(JSON.stringify({ type: "hello", sid } satisfies LiveMessage));
        this.presence();
        return new Response(null, { status: 101, webSocket: client });
      }
      case "/presence":
        return Response.json({ online: this.online() });
      case "/notify": {
        const body = (await request.json()) as { what: "home" | "diary" | "guestbook"; by: string };
        this.broadcast({ type: "changed", what: body.what, by: body.by });
        return new Response("ok");
      }
      default:
        return new Response("not found", { status: 404 });
    }
  }

  async webSocketMessage(ws: WebSocket, raw: string | ArrayBuffer) {
    if (typeof raw !== "string" || raw.length > 64_000) return;
    let msg: Relay;
    try {
      msg = JSON.parse(raw) as Relay;
    } catch {
      return;
    }
    if (!msg || !RELAYED.has(msg.type)) return;
    const me = ws.deserializeAttachment() as Attachment;
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
  }

  async webSocketError(ws: WebSocket) {
    this.presence(ws);
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
