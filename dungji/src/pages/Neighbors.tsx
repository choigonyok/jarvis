// 이웃: 다른 커플의 둥지를 찾아 놀러 간다.

import { useEffect, useState } from "react";
import type { Neighbor } from "../../shared/api";
import { ApiError, get } from "../lib/api";
import { go, linkTo } from "../lib/router";
import { ago } from "../lib/time";
import { useToast } from "../lib/toast";
import { Icon } from "../ui/icons";

interface Visitor {
  slug: string;
  home: string;
  at: number;
  names: string[];
}

export function NeighborsPanel() {
  const toast = useToast();
  const [q, setQ] = useState("");
  const [list, setList] = useState<Neighbor[] | null>(null);
  const [visitors, setVisitors] = useState<Visitor[]>([]);

  useEffect(() => {
    const t = setTimeout(() => {
      get<{ neighbors: Neighbor[] }>(`/neighbors${q.trim() ? `?q=${encodeURIComponent(q.trim())}` : ""}`)
        .then((r) => setList(r.neighbors))
        .catch(() => setList([]));
    }, 250);
    return () => clearTimeout(t);
  }, [q]);

  useEffect(() => {
    get<{ visitors: Visitor[] }>("/neighbors/visitors")
      .then((r) => setVisitors(r.visitors))
      .catch(() => undefined);
  }, []);

  async function random() {
    try {
      const r = await get<{ slug: string }>("/neighbors/random");
      go(`/h/${r.slug}`);
    } catch (e) {
      toast.show(e instanceof ApiError ? e.message : "놀러 갈 둥지를 찾지 못했어요.");
    }
  }

  return (
    <div className="space-y-5">
      <div>
        <h2 className="mb-2 text-[17px] font-bold">이웃 둥지에 놀러 가기</h2>
        <div className="flex gap-2">
          <label className="relative flex-1">
            <span className="sr-only">둥지나 사람 이름으로 찾기</span>
            <Icon name="search" className="pointer-events-none absolute top-1/2 left-3.5 size-5 -translate-y-1/2 text-muted" />
            <input className="field pl-11" value={q} onChange={(e) => setQ(e.target.value)} placeholder="둥지나 사람 이름" maxLength={20} />
          </label>
          <button type="button" onClick={random} className="btn btn-primary shrink-0 px-3.5">
            <Icon name="shuffle" className="size-[18px]" />
            아무 둥지나
          </button>
        </div>
      </div>

      {visitors.length > 0 && !q && (
        <div>
          <h3 className="mb-2 text-[15px] font-bold">우리 둥지에 다녀간 이웃</h3>
          <ul className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1">
            {visitors.map((v) => (
              <li key={v.slug} className="shrink-0">
                <a className="flex h-full min-w-[150px] flex-col rounded-2xl bg-card px-4 py-3" {...linkTo(`/h/${v.slug}`)}>
                  <span className="hand text-[20px] leading-tight font-bold">{v.home}</span>
                  <span className="text-[13px] text-muted">
                    {v.names.join(", ")} {ago(v.at)}
                  </span>
                  <span className="mt-1 text-[13px] font-bold text-egg-deep">답방 가기</span>
                </a>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div>
        <h3 className="mb-2 text-[15px] font-bold">{q ? `'${q}' 찾은 둥지` : "요즘 북적이는 둥지"}</h3>
        {list === null ? (
          <div className="h-28 rounded-2xl bg-card" aria-busy="true" />
        ) : list.length === 0 ? (
          <p className="rounded-2xl bg-card px-4 py-4 text-[15px] text-muted">{q ? "그런 이름의 둥지가 없어요." : "아직 이웃이 없어요. 친구 커플에게 둥지를 알려 주세요."}</p>
        ) : (
          <ul className="grid gap-2.5 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2">
            {list.map((n) => (
              <li key={n.slug}>
                <a className="block rounded-2xl bg-card p-4 transition-colors hover:bg-well" {...linkTo(`/h/${n.slug}`)}>
                  <div className="flex items-start gap-2">
                    <p className="hand min-w-0 flex-1 truncate text-[23px] leading-tight font-bold">{n.name}</p>
                    <span className="shrink-0 pt-1 text-[12px] text-muted">{ago(n.activeAt)}</span>
                  </div>
                  <p className="text-[14px] text-muted">{n.members.join(", ")}</p>
                  {n.intro && <p className="hand mt-1 truncate text-[18px]">{n.intro}</p>}
                  <div className="mt-2 flex items-center gap-3 text-[13px]">
                    <span className="flex flex-1 items-center gap-2">
                      <span className="h-1.5 w-16 overflow-hidden rounded-full bg-well">
                        <span className={`block h-full rounded-full ${n.cleanliness >= 70 ? "bg-egg-deep" : n.cleanliness >= 40 ? "bg-straw" : "bg-blush"}`} style={{ width: `${Math.max(6, n.cleanliness)}%` }} />
                      </span>
                      {n.cleanliness < 40 ? "청소가 필요해 보여요" : `청결 ${n.cleanliness}%`}
                    </span>
                    <span className="text-muted">놀러 온 이웃 {n.visits.toLocaleString("ko-KR")}</span>
                  </div>
                </a>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
