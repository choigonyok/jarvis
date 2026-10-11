"""Fixes to graphiti-core 0.30.2 (pinned in requirements.txt), applied before
anything uses it. All were found on 2026-10-11.

Edge range indices. FalkorDB 4.22 answers an edge range-index scan that starts
from a label scan without the edges of some nodes: in the owner graph, every
edge out of '나' - 222 of 405 facts - so a cosine search, a lookup by edge id,
and add_triplet looking for the facts a new one contradicts never saw them.
Each group already has a graph of its own, so the indices narrowed nothing;
without them FalkorDB walks the edges and finds every one. Graphiti creates
indices whenever a driver is made (each per-group clone schedules it), so the
edge ones are taken out of the list it creates, and Graph.setup drops any left
from before.

Label propagation. Graphiti's version moves every node to its neighbours'
label from the previous round all at once, and two nodes can trade labels
forever: on a five-node graph build_communities never returned, and being
plain Python on the event loop it stopped the whole service with it. This one
moves a node as soon as it decides (so neighbours see its new label), which
settles, and stops after MAX_ROUNDS whatever happens.

Topics. Graphiti sums a cluster up from its members' summaries alone, never
their names; nodes written straight from records (a shop, a stock) often have
no summary yet, and a cluster of them came back as "the input summaries were
empty". A member without a summary is summed up by its name. And a topic is
three or more things together: smaller clusters each cost a call to name and
say little.
"""

from collections import defaultdict

from graphiti_core.driver import falkordb_driver
from graphiti_core.utils.maintenance import community_operations

MAX_ROUNDS = 50
MIN_TOPIC = 3

_range_indices = falkordb_driver.get_range_indices


def node_range_indices(provider):
    return [q for q in _range_indices(provider) if "()-[" not in q]


def label_propagation(projection) -> list[list[str]]:
    community = {uid: i for i, uid in enumerate(projection)}
    for _ in range(MAX_ROUNDS):
        moved = False
        for uid, neighbors in projection.items():
            votes: dict[int, int] = defaultdict(int)
            for n in neighbors:
                if n.node_uuid in community:
                    votes[community[n.node_uuid]] += n.edge_count
            if not votes:
                continue
            top = max(votes.values())
            # As Graphiti's: ties go to the larger label, and a single link only
            # ever pulls a node towards a larger label than its own.
            choice = max(c for c, v in votes.items() if v == top)
            if top <= 1:
                choice = max(choice, community[uid])
            if choice != community[uid]:
                community[uid] = choice
                moved = True
        if not moved:
            break
    clusters: dict[int, list[str]] = defaultdict(list)
    for uid, c in community.items():
        clusters[c].append(uid)
    return list(clusters.values())


_build_community = community_operations.build_community
_community_clusters = community_operations.get_community_clusters


async def build_community(llm_client, cluster):
    for n in cluster:
        if not (n.summary or "").strip():
            n.summary = n.name
    return await _build_community(llm_client, cluster)


async def get_community_clusters(*args, **kwargs):
    return [c for c in await _community_clusters(*args, **kwargs) if len(c) >= MIN_TOPIC]


falkordb_driver.get_range_indices = node_range_indices
community_operations.label_propagation = label_propagation
community_operations.build_community = build_community
community_operations.get_community_clusters = get_community_clusters
