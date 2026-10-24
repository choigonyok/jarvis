# Builds stations.json, the subway stations context-svc keeps: each station by
# the exact name the real-time feed knows it by (천호(풍납토성), not 천호), and
# on each line (the feed's subwayId) the stations next to it, as the feed
# itself has them - every train's previous and next station (statnFid,
# statnTid) in realtimeStationArrival/ALL. Where a station is comes from
# Wikidata (P625, matched by name and line); a station Wikidata does not place
# is still found by name, not by nearness.
#
# The feed tells of the stations trains are coming to now, so take it in
# service hours, a few times over a while - a shuttle (광명, 서동탄) is
# missed by a snapshot taken between its runs:
#
#   SEOUL_SUBWAY_KEY=… python3 stations.py --fetch 8 --every 360 --save feed.jsonl stations.json
#   python3 stations.py --feed feed.jsonl --feed more.jsonl stations.json [names to print…]
import argparse, collections, json, math, re, sys, time, urllib.parse, urllib.request

LINES = """SELECT ?st ?stLabel ?coord ?line WHERE {
  SERVICE wikibase:box {
    ?st wdt:P625 ?coord .
    bd:serviceParam wikibase:cornerSouthWest "Point(126.3 36.7)"^^geo:wktLiteral .
    bd:serviceParam wikibase:cornerNorthEast "Point(127.9 38.2)"^^geo:wktLiteral .
  }
  ?st wdt:P81 ?line .
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ko,en". }
}"""

# Wikidata's line items, as the feed's subwayId - to place a station of a
# name two lines share (신촌 and 신촌(경의중앙선)) on the right one.
WIKI_LINES = {
    "Q20280": "1001", "Q92501": "1001", "Q17200": "1001", "Q20998": "1001", "Q167708": "1001",
    "Q17501": "1002",
    "Q20393": "1003", "Q20391": "1003", "Q626574": "1003",
    "Q489253": "1004", "Q92502": "1004",
    "Q92530": "1005", "Q21060694": "1005",
    "Q92549": "1006", "Q22910": "1007", "Q45086": "1008", "Q17503": "1009",
    "Q18233162": "1063", "Q695145": "1063", "Q17488": "1063", "Q1033905": "1063", "Q32713": "1063",
    "Q487643": "1065", "Q20192": "1067",
    "Q84389881": "1075", "Q16266": "1075", "Q16948": "1075",
    "Q20388": "1077", "Q3832795": "1092", "Q109116480": "1093",
    "Q23933857": "1081", "Q24044969": "1081", "Q3832768": "1094", "Q22974067": "1032",
}

# The feed's name, where Wikidata calls the station otherwise.
WIKI_NAMES = {
    "건대입구": ["건국대입구", "건국대"], "응암순환(상선)": ["응암"], "지제": ["평택지제"],
    "4.19 민주묘지": ["4.19민주묘지", "4·19민주묘지"], "뚝섬유원지": ["자양"], "세종왕릉": ["세종대왕릉"], "신길온천": ["능길"],
}

# In service, but too rarely coming in for a few snapshots to catch: the
# shuttles to 광명 and 서동탄, and 4호선 past 불암산 (진접선).
EXTRA = {
    "1001": [("금천구청", "광명"), ("병점", "서동탄")],
    "1004": [("불암산", "별내별가람"), ("별내별가람", "오남"), ("오남", "진접")],
}

FIELDS = ("statnNm", "statnId", "statnFid", "statnTid", "subwayId", "updnLine", "trainLineNm", "btrainSttus")


def fetch(key, snaps, every):
    rows = []
    for i in range(snaps):
        for start in range(0, 4000, 1000):
            url = "http://swopenAPI.seoul.go.kr/api/subway/%s/json/realtimeStationArrival/%d/%d/ALL" % (
                key, start + (1 if start else 0), start + 1000)
            try:
                with urllib.request.urlopen(url, timeout=40) as r:
                    d = json.load(r)
            except Exception as e:
                print("feed:", e, file=sys.stderr)
                continue
            got = d.get("realtimeArrivalList") or []
            rows += [{k: x.get(k) for k in FIELDS} for x in got]
            if len(got) < 1000:
                break
        if i < snaps - 1:
            time.sleep(every)
    return rows


def ask(query):
    req = urllib.request.Request(
        "https://query.wikidata.org/sparql?" + urllib.parse.urlencode({"query": query}),
        headers={"Accept": "application/sparql-results+json", "User-Agent": "jarvis-context/1 (personal; station list)"})
    with urllib.request.urlopen(req, timeout=180) as r:
        return json.load(r)["results"]["bindings"]


def base(name):
    """천호 for 천호(풍납토성); the words in the brackets are another name for it."""
    return re.sub(r"\s*\(.*?\)\s*$", "", name).strip()


def norm(label):
    s = re.sub(r"\s*\(.*?\)\s*$", "", label.strip())
    s = re.sub(r"^지하철\s+", "", s)
    if len(s) > 2 and s.endswith("역"):
        s = s[:-1]
    return s


def next_of(train_line):
    m = re.search(r"-\s*(.+?)\s*방면", train_line or "")
    return m.group(1).strip() if m else None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("out")
    ap.add_argument("show", nargs="*")
    ap.add_argument("--feed", action="append", default=[])
    ap.add_argument("--fetch", type=int, default=0)
    ap.add_argument("--every", type=int, default=360)
    ap.add_argument("--save")
    a = ap.parse_args()

    rows = []
    for f in a.feed:
        rows += [json.loads(l) for l in open(f) if l.strip()]
    if a.fetch:
        import os
        got = fetch(os.environ["SEOUL_SUBWAY_KEY"], a.fetch, a.every)
        if a.save:
            with open(a.save, "a") as w:
                for r in got:
                    w.write(json.dumps(r, ensure_ascii=False) + "\n")
        rows += got
    if not rows:
        sys.exit("no feed rows: --feed or --fetch")

    # Each station id's name: as the feed names it, or as a train names its next stop.
    name = {}
    for r in rows:
        name[r["statnId"]] = r["statnNm"]
    for r in rows:
        n = next_of(r["trainLineNm"])
        if r["statnTid"] and r["statnTid"] not in name and n:
            name[r["statnTid"]] = n
    adj = collections.defaultdict(lambda: collections.defaultdict(set))  # line -> id -> ids
    for r in rows:
        line, here = r["subwayId"], r["statnId"]
        for x, y in ((r["statnFid"], here), (here, r["statnTid"])):
            if x and y and x != y and x in name and y in name and name[x] != name[y]:
                adj[line][x].add(y)
                adj[line][y].add(x)
    stations = collections.defaultdict(lambda: collections.defaultdict(set))  # name -> line -> names
    for line, ids in adj.items():
        for x, ys in ids.items():
            stations[name[x]][line].update(name[y] for y in ys)

    for line, pairs in EXTRA.items():
        for x, y in pairs:
            stations[x][line].add(y)
            stations[y][line].add(x)

    # Where each is: the Wikidata station of the same name on one of its lines.
    places = collections.defaultdict(list)  # name -> [(lines, point)]
    for b in ask(LINES):
        label = b.get("stLabel", {}).get("value", "")
        m = re.match(r"Point\(([-\d.]+) ([-\d.]+)\)", b["coord"]["value"])
        if not m or re.match(r"^Q\d+$", label):
            continue
        point = (round(float(m.group(2)), 6), round(float(m.group(1)), 6))
        line = WIKI_LINES.get(b["line"]["value"].rsplit("/", 1)[-1])
        places[norm(label)].append((line, point))
    rows_out, unplaced = [], []
    for n in sorted(stations):
        lines = stations[n]
        want = [base(n), norm(n)] + WIKI_NAMES.get(n, [])
        inner = re.search(r"\((.*?)\)\s*$", n)
        if inner:
            want.append(inner.group(1))
        best = None
        for w in want:
            cands = places.get(w, [])
            on = [p for l, p in cands if l in lines]
            if on:
                best = collections.Counter(on).most_common(1)[0][0]
                break
            if cands and best is None:
                best = collections.Counter(p for _, p in cands).most_common(1)[0][0]
        row = {"n": n, "l": {l: sorted(v) for l, v in sorted(lines.items())}}
        if best:
            row["p"] = list(best)
        else:
            unplaced.append(n)
        rows_out.append(row)
    json.dump(rows_out, open(a.out, "w"), ensure_ascii=False, separators=(",", ":"))

    per = collections.defaultdict(list)
    for r in rows_out:
        for l, ns in r["l"].items():
            per[l].append((r["n"], len(ns)))
    print("역", len(rows_out), "좌표 없음", len(unplaced), unplaced[:30])
    for l in sorted(per):
        v = per[l]
        deg = collections.Counter(d for _, d in v)
        print(l, "역", len(v), dict(sorted(deg.items())), "갈래", [n for n, d in v if d >= 3][:8], "끝", [n for n, d in v if d == 1][:8])
    by = {r["n"]: r for r in rows_out}
    for n in a.show:
        print(n, by.get(n))


if __name__ == "__main__":
    main()
