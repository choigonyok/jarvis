// 둘이 들어와 있을 때 거는 음성통화. 브라우저에 들어 있는 WebRTC(오픈소스 libwebrtc)로
// 두 폰이 직접 소리를 주고받고, 연결에 필요한 말만 둥지의 실시간 통로로 건넨다.

import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import type { IceInit, LiveMessage } from "../../shared/api";
import { get } from "./api";
import { useLive } from "./live";
import { useToast } from "./toast";
import { Icon } from "../ui/icons";

type CallState =
  | { s: "idle" }
  | { s: "outgoing"; name: string }
  | { s: "incoming"; name: string; fromSid: string; sdp: string }
  | { s: "connecting"; name: string }
  | { s: "active"; name: string; since: number };

interface CallApi {
  state: CallState;
  muted: boolean;
  start: () => void;
  accept: () => void;
  decline: () => void;
  hangup: () => void;
  toggleMute: () => void;
}

const CallCtx = createContext<CallApi | null>(null);
export const useCall = () => useContext(CallCtx)!;

const RING_TIMEOUT = 45_000;

export function CallProvider({ partner, children }: { partner: { id: string; name: string } | null; children: ReactNode }) {
  const live = useLive();
  const toast = useToast();
  const [state, setState] = useState<CallState>({ s: "idle" });
  const [muted, setMuted] = useState(false);
  const stateRef = useRef(state);
  stateRef.current = state;
  const pc = useRef<RTCPeerConnection | null>(null);
  const local = useRef<MediaStream | null>(null);
  const peerSid = useRef<string | null>(null);
  const localIce = useRef<IceInit[]>([]);
  const remoteIce = useRef<IceInit[]>([]);
  const audio = useRef<HTMLAudioElement | null>(null);
  const tone = useRef<Tone | null>(null);
  const ringTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    const el = document.createElement("audio");
    el.autoplay = true;
    el.setAttribute("playsinline", "");
    el.dataset.role = "call-audio";
    document.body.appendChild(el);
    audio.current = el;
    return () => el.remove();
  }, []);

  const cleanup = useCallback(() => {
    clearTimeout(ringTimer.current);
    tone.current?.stop();
    tone.current = null;
    pc.current?.close();
    pc.current = null;
    local.current?.getTracks().forEach((t) => t.stop());
    local.current = null;
    if (audio.current) audio.current.srcObject = null;
    peerSid.current = null;
    localIce.current = [];
    remoteIce.current = [];
    setMuted(false);
    setState({ s: "idle" });
  }, []);

  const end = useCallback(
    (message?: string) => {
      cleanup();
      if (message) toast.show(message);
    },
    [cleanup, toast],
  );

  /** 마이크와 연결 하나를 준비한다. 실패하면 이유를 알리고 null. */
  const prepare = useCallback(async (): Promise<RTCPeerConnection | null> => {
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
    } catch {
      end("마이크를 쓸 수 없어요. 브라우저 설정에서 마이크를 허용해 주세요.");
      return null;
    }
    let iceServers: RTCIceServer[] = [{ urls: "stun:stun.cloudflare.com:3478" }];
    try {
      iceServers = (await get<{ iceServers: RTCIceServer[] }>("/call/ice")).iceServers;
    } catch {
      // STUN 기본값으로 시도
    }
    const conn = new RTCPeerConnection({ iceServers });
    local.current = stream;
    stream.getTracks().forEach((t) => conn.addTrack(t, stream));
    conn.onicecandidate = (e) => {
      if (!e.candidate) return;
      const c = e.candidate.toJSON();
      if (peerSid.current) live.send({ type: "ice", to: peerSid.current, candidate: c });
      else localIce.current.push(c);
    };
    conn.ontrack = (e) => {
      if (audio.current) {
        audio.current.srcObject = e.streams[0] ?? new MediaStream([e.track]);
        void audio.current.play().catch(() => undefined);
      }
    };
    conn.onconnectionstatechange = () => {
      const st = conn.connectionState;
      if (st === "connected") {
        tone.current?.stop();
        tone.current = null;
        clearTimeout(ringTimer.current);
        const cur = stateRef.current;
        if (cur.s !== "active" && cur.s !== "idle") setState({ s: "active", name: "name" in cur ? cur.name : "", since: Date.now() });
      } else if (st === "failed") {
        end("통화가 이어지지 않았어요. 둘 중 한 곳의 인터넷이 막혀 있을 수 있어요.");
      }
    };
    pc.current = conn;
    return conn;
  }, [end, live]);

  const flushRemoteIce = useCallback(async () => {
    const conn = pc.current;
    if (!conn?.remoteDescription) return;
    for (const c of remoteIce.current.splice(0)) await conn.addIceCandidate(c as RTCIceCandidateInit).catch(() => undefined);
  }, []);

  const start = useCallback(async () => {
    if (!partner || stateRef.current.s !== "idle") return;
    setState({ s: "outgoing", name: partner.name });
    tone.current = playTone("ringback");
    const conn = await prepare();
    if (!conn) return;
    const offer = await conn.createOffer();
    await conn.setLocalDescription(offer);
    if (!live.send({ type: "call", sdp: offer.sdp ?? "" })) {
      end("연결이 잠깐 끊겼어요. 다시 걸어 주세요.");
      return;
    }
    ringTimer.current = setTimeout(() => {
      live.send({ type: "hangup" });
      end(`${partner.name}님이 받지 않았어요.`);
    }, RING_TIMEOUT);
  }, [end, live, partner, prepare]);

  const accept = useCallback(async () => {
    const cur = stateRef.current;
    if (cur.s !== "incoming") return;
    tone.current?.stop();
    tone.current = null;
    clearTimeout(ringTimer.current);
    setState({ s: "connecting", name: cur.name });
    peerSid.current = cur.fromSid;
    const conn = await prepare();
    if (!conn) {
      live.send({ type: "decline", to: cur.fromSid });
      return;
    }
    await conn.setRemoteDescription({ type: "offer", sdp: cur.sdp });
    await flushRemoteIce();
    const answer = await conn.createAnswer();
    await conn.setLocalDescription(answer);
    live.send({ type: "answer", to: cur.fromSid, sdp: answer.sdp ?? "" });
  }, [flushRemoteIce, live, prepare]);

  const decline = useCallback(() => {
    const cur = stateRef.current;
    if (cur.s === "incoming") live.send({ type: "decline", to: cur.fromSid });
    cleanup();
  }, [cleanup, live]);

  const hangup = useCallback(() => {
    live.send({ type: "hangup", ...(peerSid.current ? { to: peerSid.current } : {}) });
    end();
  }, [end, live]);

  const toggleMute = useCallback(() => {
    const track = local.current?.getAudioTracks()[0];
    if (!track) return;
    track.enabled = !track.enabled;
    setMuted(!track.enabled);
  }, []);

  useEffect(
    () =>
      live.on(async (m: LiveMessage) => {
        const cur = stateRef.current;
        switch (m.type) {
          case "call":
            if (cur.s !== "idle") {
              live.send({ type: "decline", to: m.fromSid });
              return;
            }
            setState({ s: "incoming", name: m.name, fromSid: m.fromSid, sdp: m.sdp });
            tone.current = playTone("ring");
            navigator.vibrate?.([300, 200, 300]);
            clearTimeout(ringTimer.current);
            ringTimer.current = setTimeout(() => cleanup(), RING_TIMEOUT + 5_000);
            return;
          case "answer": {
            if (cur.s !== "outgoing" || !pc.current) return;
            peerSid.current = m.fromSid;
            setState({ s: "connecting", name: cur.name });
            await pc.current.setRemoteDescription({ type: "answer", sdp: m.sdp });
            for (const c of localIce.current.splice(0)) live.send({ type: "ice", to: m.fromSid, candidate: c });
            await flushRemoteIce();
            return;
          }
          case "ice":
            if (pc.current?.remoteDescription) await pc.current.addIceCandidate(m.candidate as RTCIceCandidateInit).catch(() => undefined);
            else remoteIce.current.push(m.candidate);
            return;
          case "decline":
            if (cur.s === "outgoing") end(`${cur.name}님이 지금은 받을 수 없대요.`);
            return;
          case "hangup":
            if (cur.s === "incoming" && m.fromSid === cur.fromSid) cleanup();
            else if (cur.s !== "idle" && cur.s !== "incoming" && (!peerSid.current || m.fromSid === peerSid.current)) end("통화가 끝났어요.");
            return;
          case "taken":
            if (cur.s === "incoming") cleanup();
            return;
        }
      }),
    [cleanup, end, flushRemoteIce, live],
  );

  // 창을 닫으면 상대에게 끊었다고 알린다.
  useEffect(() => {
    const bye = () => {
      if (stateRef.current.s !== "idle") live.send({ type: "hangup", ...(peerSid.current ? { to: peerSid.current } : {}) });
    };
    window.addEventListener("pagehide", bye);
    return () => window.removeEventListener("pagehide", bye);
  }, [live]);

  return (
    <CallCtx.Provider value={{ state, muted, start, accept, decline, hangup, toggleMute }}>
      {children}
      <CallOverlay />
    </CallCtx.Provider>
  );
}

function CallOverlay() {
  const call = useCall();
  const [, tick] = useState(0);
  useEffect(() => {
    if (call.state.s !== "active") return;
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [call.state.s]);
  const st = call.state;
  if (st.s === "idle") return null;
  if (st.s === "incoming")
    return (
      <div className="fixed inset-0 z-50 grid place-items-center bg-[oklch(0_0_0/55%)] p-4" role="alertdialog" aria-label={`${st.name}님의 전화`}>
        <div className="w-full max-w-sm rounded-2xl border border-edge-soft bg-popover p-6 text-center">
          <div className="mx-auto mb-4 grid size-16 place-items-center rounded-full bg-glass-raised text-foreground">
            <Icon name="phone" className="size-7" />
          </div>
          <p className="text-heading font-semibold">{st.name}님의 전화</p>
          <p className="mt-1 text-body text-dim">둘만 듣는 음성통화예요</p>
          <div className="mt-6 grid grid-cols-2 gap-3">
            <button type="button" onClick={call.decline} className="btn btn-refuse h-12">
              거절
            </button>
            <button type="button" onClick={call.accept} className="btn btn-approve h-12" autoFocus>
              받기
            </button>
          </div>
        </div>
      </div>
    );
  const label =
    st.s === "outgoing"
      ? `${st.name}님에게 거는 중`
      : st.s === "connecting"
        ? `${st.name}님과 잇는 중`
        : `${st.name}님과 통화 중 ${clock(Date.now() - st.since)}`;
  return (
    <div className="call-bar fixed inset-x-0 z-40 flex justify-center px-4" role="status">
      <div className="liquid-glass flex w-full max-w-md items-center gap-2 rounded-full py-2 pr-2 pl-4 text-foreground">
        <span className={`size-[7px] shrink-0 rounded-full ${st.s === "active" ? "bg-online" : "anim-breathe bg-dim"}`} />
        <span className="min-w-0 flex-1 truncate text-body">{label}</span>
        {st.s === "active" && (
          <button type="button" onClick={call.toggleMute} className="call-btn focus-ring bg-glass-raised" aria-pressed={call.muted} aria-label={call.muted ? "마이크 켜기" : "마이크 끄기"}>
            <Icon name={call.muted ? "micOff" : "mic"} className="size-5" />
          </button>
        )}
        <button type="button" onClick={call.hangup} className="call-btn focus-ring bg-reject-wash text-reject" aria-label="끊기">
          <Icon name="hangup" className="size-5" />
        </button>
      </div>
    </div>
  );
}

const clock = (ms: number) => {
  const s = Math.floor(ms / 1000);
  return `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
};

// ── 소리: 웹 오디오로 직접 만든 짧은 음(파일 없음) ──

interface Tone {
  stop: () => void;
}

let audioCtx: AudioContext | null = null;

function playTone(kind: "ring" | "ringback"): Tone {
  try {
    audioCtx ??= new AudioContext();
    const ctx = audioCtx;
    void ctx.resume();
    const notes = kind === "ring" ? [784, 988, 1175] : [523, 659];
    let stopped = false;
    const play = () => {
      if (stopped) return;
      const t0 = ctx.currentTime;
      notes.forEach((f, i) => {
        const o = ctx.createOscillator();
        const g = ctx.createGain();
        o.type = "sine";
        o.frequency.value = f;
        g.gain.setValueAtTime(0, t0 + i * 0.16);
        g.gain.linearRampToValueAtTime(kind === "ring" ? 0.16 : 0.07, t0 + i * 0.16 + 0.02);
        g.gain.exponentialRampToValueAtTime(0.001, t0 + i * 0.16 + 0.5);
        o.connect(g).connect(ctx.destination);
        o.start(t0 + i * 0.16);
        o.stop(t0 + i * 0.16 + 0.55);
      });
    };
    play();
    const timer = setInterval(play, kind === "ring" ? 2200 : 3000);
    return {
      stop: () => {
        stopped = true;
        clearInterval(timer);
      },
    };
  } catch {
    return { stop: () => undefined };
  }
}
