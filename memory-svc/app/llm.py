"""Graphiti's extraction LLM: Claude Haiku 5.5 through the official SDK.

Graphiti ships an Anthropic client, but it sends sampling parameters and
reads the first content block as the answer - both wrong for Haiku 5.5,
where non-default `temperature` is a 400 and a response can begin with
thinking blocks. So this client is Graphiti's LLMClient implemented on the
SDK directly: structured outputs (`messages.parse`) for every schema'd call,
`effort: low` because extraction is high-volume and simple, and usage counted
so a daily budget can stop it.
"""

import logging
import typing

import anthropic
from anthropic import AsyncAnthropic
from pydantic import BaseModel

from graphiti_core.llm_client.client import LLMClient
from graphiti_core.llm_client.config import DEFAULT_MAX_TOKENS, LLMConfig, ModelSize
from graphiti_core.llm_client.errors import RateLimitError, RefusalError
from graphiti_core.prompts.models import Message

log = logging.getLogger("memory.llm")

MODEL = "claude-haiku-5-5"


class HaikuClient(LLMClient):
    def __init__(self, api_key: str, on_usage: typing.Callable[[int, int], None] | None = None):
        super().__init__(LLMConfig(api_key=api_key, model=MODEL, small_model=MODEL, max_tokens=16000))
        self.client = AsyncAnthropic(api_key=api_key, max_retries=4)
        self.on_usage = on_usage

    async def _generate_response(
        self,
        messages: list[Message],
        response_model: type[BaseModel] | None = None,
        max_tokens: int = DEFAULT_MAX_TOKENS,
        model_size: ModelSize = ModelSize.medium,
    ) -> dict[str, typing.Any]:
        system = "\n\n".join(m.content for m in messages if m.role == "system")
        convo = [{"role": m.role, "content": m.content} for m in messages if m.role in ("user", "assistant")]
        if not convo or convo[-1]["role"] != "user":
            convo.append({"role": "user", "content": "위 지시대로 답하세요."})
        params: dict[str, typing.Any] = {
            "model": MODEL,
            # Room for adaptive thinking on top of the JSON itself.
            "max_tokens": max(int(max_tokens or 0), 8192),
            "messages": convo,
            "output_config": {"effort": "low"},
        }
        if system:
            params["system"] = system
        try:
            if response_model is not None:
                resp = await self.client.messages.parse(output_format=response_model, **params)
            else:
                resp = await self.client.messages.create(**params)
        except anthropic.RateLimitError as e:
            raise RateLimitError(str(e)) from e

        if self.on_usage:
            self.on_usage(resp.usage.input_tokens, resp.usage.output_tokens)
        if resp.stop_reason == "refusal":
            raise RefusalError("Haiku 가 이 내용의 추출을 거절했습니다")

        if response_model is not None:
            parsed = resp.parsed_output
            if parsed is None:
                raise ValueError(f"구조화 출력이 비어 있습니다 (stop_reason={resp.stop_reason})")
            return parsed.model_dump()
        text = "".join(b.text for b in resp.content if b.type == "text")
        return {"content": text}
