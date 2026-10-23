# Builds stations.json, the subway stations context-svc keeps: each station by
# the name the real-time feed knows it by, where it is, and on each line (the
# feed's subwayId) the stations next to it - from Wikidata: P81 connecting
# line, and P197 adjacent station with its P81 line and P5051 towards
# qualifiers. Express runs (shortcuts over local ones) are dropped.
#
#   python3 stations.py stations.json [station names to print...]
import json, re, sys, math, collections, urllib.parse, urllib.request

BOX = """SERVICE wikibase:box {
    ?st wdt:P625 ?coord .
    bd:serviceParam wikibase:cornerSouthWest "Point(126.3 36.7)"^^geo:wktLiteral .
    bd:serviceParam wikibase:cornerNorthEast "Point(127.9 38.2)"^^geo:wktLiteral .
  }"""
ADJACENT = """SELECT ?st ?stLabel ?coord ?adj ?adjLabel ?lineQ ?towards ?towardsLabel WHERE {
  %s
  ?st p:P197 ?s . ?s ps:P197 ?adj .
  OPTIONAL { ?s pq:P81 ?lineQ }
  OPTIONAL { ?s pq:P5051 ?towards }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ko,en". }
}""" % BOX
LINES = """SELECT ?st ?stLabel ?coord ?line ?lineLabel WHERE {
  %s
  ?st wdt:P81 ?line .
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ko,en". }
}""" % BOX

def ask(query):
    req = urllib.request.Request(
        "https://query.wikidata.org/sparql?" + urllib.parse.urlencode({"query": query}),
        headers={"Accept": "application/sparql-results+json", "User-Agent": "jarvis-context/1 (personal; station list)"})
    with urllib.request.urlopen(req, timeout=180) as r:
        return json.load(r)["results"]["bindings"]

out = sys.argv[1]
q3, q4 = ask(ADJACENT), ask(LINES)

SERVICE = {
    "Q20280": "1001", "Q92501": "1001",
    "Q17501": "1002",
    "Q20393": "1003", "Q20391": "1003", "Q626574": "1003",
    "Q489253": "1004", "Q92502": "1004",
    "Q92530": "1005", "Q21060694": "1005",
    "Q92549": "1006", "Q22910": "1007", "Q45086": "1008", "Q17503": "1009",
    "Q18233162": "1063", "Q695145": "1063",
    "Q487643": "1065", "Q20192": "1067",
    "Q84389881": "1075", "Q16266": "1075", "Q16948": "1075",
    "Q20388": "1077", "Q3832795": "1092", "Q109116480": "1093",
    "Q23933857": "1081", "Q24044969": "1081",
    "Q3832768": "1094", "Q22974067": "1032",
}
EXPRESS = {"Q110471087", "Q110471077"}  # 경부·경원 급행

def rail(q, lat, lon):
    if q in ("Q17200", "Q20998", "Q167708", "Q54312"):
        return "1001"  # 경부·경인·장항선, 병점기지선
    if q in ("Q17488", "Q1033905"):
        return "1063"  # 경의선, 용산선
    if q == "Q32713" and lon < 127.70:
        return "1063"  # 중앙선 as far as 지평
    if q == "Q483052":
        return "1063" if lat < 37.592 else "1001"  # 경원선: 용산-회기, then 1호선
    return None

# Gaps Wikidata leaves on stations near the people this serves.
OVERRIDE = {("수원", "1075"): ["매교", "고색"]}

def qid(b, k):
    return b.get(k, {}).get("value", "").rsplit("/", 1)[-1]

def norm(label):
    s = re.sub(r"\s*\(.*?\)\s*$", "", label.strip())
    s = re.sub(r"^지하철\s+", "", s)
    if len(s) > 2 and s.endswith("역"):
        s = s[:-1]
    return s

def point(wkt):
    m = re.match(r"Point\(([-\d.]+) ([-\d.]+)\)", wkt)
    return (round(float(m.group(2)), 6), round(float(m.group(1)), 6)) if m else None

def meters(a, b):
    la1, lo1, la2, lo2 = map(math.radians, (a[0], a[1], b[0], b[1]))
    h = math.sin((la2 - la1) / 2) ** 2 + math.cos(la1) * math.cos(la2) * math.sin((lo2 - lo1) / 2) ** 2
    return 2 * 6371000 * math.asin(math.sqrt(h))

name, where = {}, {}
for b in q4 + q3:
    for k in ("st", "adj", "towards"):
        if k in b:
            name.setdefault(qid(b, k), norm(b.get(k + "Label", {}).get("value", qid(b, k))))
    p = point(b["coord"]["value"])
    if p:
        where.setdefault(name[qid(b, "st")], p)
bad = lambda n: not n or re.match(r"^Q\d+$", n) or re.search(r"[A-Za-z]", n)

member = collections.defaultdict(set)  # by name
for b in q4:
    n = name[qid(b, "st")]
    p = where.get(n)
    f = SERVICE.get(qid(b, "line")) or (rail(qid(b, "line"), *p) if p else None)
    if f:
        member[n].add(f)

edges = collections.defaultdict(set)
dropped = 0
for b in q3:
    a, n = name[qid(b, "st")], name.get(qid(b, "adj"), "")
    lq, t = qid(b, "lineQ"), name.get(qid(b, "towards"), "")
    if bad(a) or bad(n) or a == n or a not in where or n not in where or lq in EXPRESS:
        dropped += 1
        continue
    far = meters(where[a], where[n])
    lines = set()
    if lq in SERVICE:
        lines = {SERVICE[lq]}
    elif lq:
        f = rail(lq, *where[a])
        if f and far <= 6000:
            lines = {f}
    elif t:
        lines = member[a] & member[n] & member[t] if far <= 8000 else set()
    elif far <= 6000:
        lines = (member[a] & member[n]) if member[a] else set(member[n])
    if t and lines and not (lines & member[t]):
        lines = set()  # towards a terminal off this line: another service's run
    if len(lines) != 1:
        dropped += 1
        continue
    edges[lines.pop()].add(tuple(sorted((a, n))))

def local(es):
    adj = collections.defaultdict(set)
    for a, b in es:
        adj[a].add(b); adj[b].add(a)
    keep = set()
    for a, b in es:
        direct = meters(where[a], where[b])
        best, frontier, seen = None, [(a, 0.0, 0)], {a}
        while frontier and best is None:
            nxt = []
            for x, dist, hops in frontier:
                for y in adj[x]:
                    if {x, y} == {a, b} or y in seen:
                        continue
                    d = dist + meters(where[x], where[y])
                    if y == b:
                        best = d if best is None else min(best, d)
                    elif hops + 1 < 10 and d < direct * 1.6:
                        seen.add(y); nxt.append((y, d, hops + 1))
            frontier = nxt
        if best is None or best > direct * 1.6:
            keep.add((a, b))
    return keep

lines = collections.defaultdict(dict)  # name -> line -> neighbours
for f, es in edges.items():
    for a, b in local(es):
        lines[a].setdefault(f, set()).add(b)
        lines[b].setdefault(f, set()).add(a)
for (n, f), ns in OVERRIDE.items():
    lines[n][f] = set(ns)

rows = [{"n": n, "p": list(where[n]), "l": {f: sorted(ns) for f, ns in sorted(ls.items())}} for n, ls in sorted(lines.items())]
json.dump(rows, open(out, "w"), ensure_ascii=False, separators=(",", ":"))

per = collections.defaultdict(list)
for r in rows:
    for f, ns in r["l"].items():
        per[f].append((r["n"], len(ns)))
print("역", len(rows), "버린 기록", dropped)
for f in sorted(per):
    v = per[f]
    deg = collections.Counter(d for _, d in v)
    print(f, "역", len(v), dict(sorted(deg.items())), "갈래", [n for n, d in v if d >= 3][:12])
by = {r["n"]: r["l"] for r in rows}
for n in sys.argv[2:]:
    print(n, by.get(n))
