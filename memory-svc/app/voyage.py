"""Voyage AI, the search-side models (embeddings, reranking), through MongoDB.

A key made in Atlas (al-...) works only on ai.mongodb.com; one made on Voyage's
own platform (pa-...) only on api.voyageai.com. Neither has an API for what a
key has used - Atlas shows it in its UI - so every call here hands the tokens
the response says it billed to `on_usage`, and memory-svc keeps the count
(voyage_usage, migration 036).
"""

import os
import typing

import httpx

# USD per 1M tokens, Atlas list prices (2026-10). Each comes with 200M free
# tokens; Atlas bills only past them.
PRICE = {
    "voyage-4-large": 0.12,
    "voyage-4": 0.06,
    "voyage-4-lite": 0.02,
    "rerank-3": 0.05,
    "rerank-3-lite": 0.02,
}
FREE_TOKENS = 200_000_000


def price(model: str) -> float:
    """USD per token. A model not listed (older ones have no free tokens
    either) is counted at the highest price, so it is never under-reported."""
    return PRICE.get(model, max(PRICE.values())) / 1_000_000


def free_tokens(model: str) -> int:
    return FREE_TOKENS if model in PRICE else 0


def base_url(key: str) -> str:
    if url := os.environ.get("VOYAGE_URL"):
        return url.rstrip("/")
    return "https://ai.mongodb.com/v1" if key.startswith("al-") else "https://api.voyageai.com/v1"


class Voyage:
    def __init__(self, key: str, on_usage: typing.Callable[[str, int], typing.Awaitable[None]] | None = None):
        self.base = base_url(key)
        self.on_usage = on_usage
        self.http = httpx.AsyncClient(timeout=60, headers={"Authorization": f"Bearer {key}"})

    async def _post(self, path: str, body: dict) -> dict:
        r = await self.http.post(self.base + path, json=body)
        if r.status_code >= 300:
            raise RuntimeError(f"Voyage {path} {r.status_code}: {r.text[:200]}")
        out = r.json()
        if self.on_usage:
            await self.on_usage(body["model"], int((out.get("usage") or {}).get("total_tokens") or 0))
        return out

    async def embed(
        self, texts: list[str], model: str = "voyage-4", input_type: str | None = None, dimension: int | None = None
    ) -> list[list[float]]:
        """input_type "query" or "document" lets the model embed the two sides
        differently; None embeds both the same way."""
        body: dict = {"input": texts, "model": model}
        if input_type:
            body["input_type"] = input_type
        if dimension:
            body["output_dimension"] = dimension
        out = await self._post("/embeddings", body)
        return [d["embedding"] for d in sorted(out["data"], key=lambda d: d["index"])]

    async def rerank(
        self, query: str, documents: list[str], model: str = "rerank-3-lite", top_k: int | None = None
    ) -> list[tuple[int, float]]:
        """(index into documents, relevance), best first. Billed as query
        tokens times the number of documents plus the documents' tokens."""
        body: dict = {"query": query, "documents": documents, "model": model}
        if top_k:
            body["top_k"] = top_k
        out = await self._post("/rerank", body)
        return [(d["index"], d["relevance_score"]) for d in out["data"]]
