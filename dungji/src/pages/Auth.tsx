import { useMemo, useState, type FormEvent } from "react";
import { STARTER } from "../../shared/catalog";
import type { HomeView, Me } from "../../shared/api";
import { ApiError, hashPassword, post } from "../lib/api";
import { phaseOf } from "../lib/time";
import { Room, SKY } from "../room/Room";

/** 처음 둥지와 같은 방(로그인 화면에 보여 줄 견본) */
function sampleHome(): HomeView {
  const items = STARTER.map((s, i) => ({
    id: `s${i}`,
    key: s.key,
    place: "room" as const,
    x: s.x,
    y: s.y,
    flip: Boolean(s.flip),
    wall: s.wall ?? null,
    plant: s.key.startsWith("plant_") ? { water: 80, stage: 4, maxStage: 4, ready: false, wilted: false, nextHours: null, thirsty: false } : undefined,
  }));
  items.push({ id: "s-sofa", key: "sofa_two", place: "room", x: 3, y: 0, flip: false, wall: null, plant: undefined });
  items.push({ id: "s-shelf", key: "shelf_book", place: "room", x: 2, y: 0, flip: false, wall: null, plant: undefined });
  return {
    id: "sample",
    slug: "sample",
    name: "견본 둥지",
    intro: "",
    since: null,
    wallpaper: "paper_stripe",
    floor: "floor_wood",
    members: [],
    cleanliness: 92,
    dirt: [{ id: "d1", x: 5, y: 5, kind: "sock" }],
    items,
    visits: { today: 0, total: 0, recent: [] },
    owner: false,
  };
}

export function AuthPage({ onDone }: { onDone: (me: Me) => void }) {
  const params = new URLSearchParams(location.search);
  const invited = params.get("invite") ?? "";
  const [mode, setMode] = useState<"login" | "signup">(invited || location.pathname === "/signup" ? "signup" : "login");
  const now = useMemo(() => new Date(), []);
  const phase = phaseOf(now);
  const home = useMemo(sampleHome, []);

  return (
    <div className="min-h-dvh lg:grid lg:grid-cols-[1.1fr_1fr]">
      <div className="sky relative flex flex-col justify-end px-4 pt-[calc(env(safe-area-inset-top)+20px)] pb-4 lg:min-h-dvh lg:justify-center lg:px-12" style={{ background: SKY[phase] }}>
        <p className={`text-display-lg leading-none font-semibold tracking-tight ${phase === "night" ? "text-[oklch(0.945_0.002_265)]" : "text-[oklch(0.2_0.006_265)]"}`}>둥지</p>
        <p className={`mt-2 max-w-[22rem] text-body lg:text-title ${phase === "night" ? "text-[oklch(0.82_0.004_265)]" : "text-[oklch(0.4_0.006_265)]"}`}>
          둘이 꾸미고 돌보는 작은 방이에요. 같이 일기를 쓰고, 이웃 커플의 방에 놀러 가요.
        </p>
        <div className="mx-auto mt-2 w-full max-w-[440px] lg:mt-8 lg:max-w-[560px]">
          <Room home={home} phase={phase} now={now} label="둥지 견본 방" />
        </div>
      </div>

      <div className="flex justify-center px-4 py-8 lg:items-center">
        <div className="w-full max-w-[400px]">
          <div className="mb-6 grid grid-cols-2 rounded-xl bg-glass p-1" role="tablist" aria-label="로그인 또는 가입">
            {(["login", "signup"] as const).map((m) => (
              <button
                key={m}
                type="button"
                role="tab"
                aria-selected={mode === m}
                onClick={() => setMode(m)}
                className={`h-11 rounded-xl text-body font-semibold ${mode === m ? "bg-glass-raised text-foreground" : "text-dim hover:text-foreground"}`}
              >
                {m === "login" ? "들어가기" : "처음이에요"}
              </button>
            ))}
          </div>
          {mode === "login" ? <LoginForm onDone={onDone} /> : <SignupForm onDone={onDone} invited={invited} />}
        </div>
      </div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-ui font-semibold">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-aside text-dim">{hint}</span>}
    </label>
  );
}

function LoginForm({ onDone }: { onDone: (me: Me) => void }) {
  const [login, setLogin] = useState("");
  const [pw, setPw] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const id = login.trim().toLowerCase();
      const r = await post<{ me: Me }>("/auth/login", { login: id, pw: await hashPassword(id, pw) });
      onDone(r.me);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "들어가지 못했어요.");
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <Field label="아이디">
        <input className="field" value={login} onChange={(e) => setLogin(e.target.value)} autoComplete="username" autoCapitalize="none" required />
      </Field>
      <Field label="비밀번호">
        <input className="field" type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" required />
      </Field>
      {error && (
        <p className="text-ui text-reject" role="alert">
          {error}
        </p>
      )}
      <button className="btn btn-approve h-12 w-full" disabled={busy}>
        {busy ? "확인하는 중" : "둥지로 들어가기"}
      </button>
    </form>
  );
}

function SignupForm({ onDone, invited }: { onDone: (me: Me) => void; invited: string }) {
  const [how, setHow] = useState<"create" | "join">(invited ? "join" : "create");
  const [name, setName] = useState("");
  const [login, setLogin] = useState("");
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [homeName, setHomeName] = useState("");
  const [code, setCode] = useState(invited);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    if (pw.length < 8) return setError("비밀번호는 8자 이상으로 정해 주세요.");
    if (pw !== pw2) return setError("두 비밀번호가 달라요.");
    setBusy(true);
    try {
      const id = login.trim().toLowerCase();
      const r = await post<{ me: Me }>("/auth/signup", {
        login: id,
        name,
        pw: await hashPassword(id, pw),
        mode: how,
        homeName,
        invite: code,
      });
      history.replaceState(null, "", `/h/${r.me.home}`);
      onDone(r.me);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "가입하지 못했어요.");
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <fieldset>
        <legend className="mb-1.5 text-ui font-semibold">둥지</legend>
        <div className="grid grid-cols-2 gap-2">
          {(
            [
              ["create", "새 둥지 만들기", "내가 먼저 만들고 상대를 불러요"],
              ["join", "초대받았어요", "상대가 준 코드로 들어가요"],
            ] as const
          ).map(([v, title, sub]) => (
            <label key={v} className={`cursor-pointer rounded-xl border-[1.5px] p-3 ${how === v ? "border-foreground/40 bg-glass-raised" : "border-edge"}`}>
              <input type="radio" name="how" value={v} checked={how === v} onChange={() => setHow(v)} className="sr-only" />
              <span className="block text-body font-semibold">{title}</span>
              <span className="mt-0.5 block text-aside leading-snug text-dim">{sub}</span>
            </label>
          ))}
        </div>
      </fieldset>
      {how === "create" ? (
        <Field label="둥지 이름" hint="이웃에게 보이는 이름이에요. 나중에 바꿀 수 있어요.">
          <input className="field" value={homeName} onChange={(e) => setHomeName(e.target.value)} maxLength={16} placeholder="햇살 드는 둥지" required />
        </Field>
      ) : (
        <Field label="초대 코드" hint="상대의 둥지 설정에 있는 여섯 글자예요.">
          <input
            className="field tracking-[0.3em] uppercase"
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())}
            maxLength={6}
            autoCapitalize="characters"
            required
          />
        </Field>
      )}
      <Field label="내 이름" hint="둥지에서 불릴 이름이에요.">
        <input className="field" value={name} onChange={(e) => setName(e.target.value)} maxLength={10} autoComplete="nickname" required />
      </Field>
      <Field label="아이디" hint="영어 소문자, 숫자, 밑줄(_)로 3~20자">
        <input
          className="field"
          value={login}
          onChange={(e) => setLogin(e.target.value)}
          autoComplete="username"
          autoCapitalize="none"
          pattern="[a-zA-Z0-9_]{3,20}"
          required
        />
      </Field>
      <Field label="비밀번호" hint="8자 이상">
        <input className="field" type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" required />
      </Field>
      <Field label="비밀번호 한 번 더">
        <input className="field" type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" required />
      </Field>
      {error && (
        <p className="text-ui text-reject" role="alert">
          {error}
        </p>
      )}
      <button className="btn btn-approve h-12 w-full" disabled={busy}>
        {busy ? "둥지를 짓는 중" : how === "create" ? "둥지 만들기" : "둥지에 들어가기"}
      </button>
    </form>
  );
}
