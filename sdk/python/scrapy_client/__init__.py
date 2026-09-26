"""scrapy live-config client.

Bootstrap replaces process env before the app's Settings/Pydantic model reads it; the
runtime WebSocket keeps values fresh with no restart. Same precedence and resilience
contract as the Java SDK: scrapy (runtime) > scrapy (boot env) > default in code, and if
scrapy dies after the process is already up, `get()` keeps returning the last known value.
"""
import asyncio
import json
import logging
import os
import random
import threading
from typing import Any

import httpx
import websockets

logger = logging.getLogger("scrapy_client")


class ScrapyConfig:
    def __init__(self, base_url: str, api_key: str, scope: str):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.scope = scope
        self._cache: dict[str, Any] = {}
        self._version = 0
        self._thread: threading.Thread | None = None
        self._stop = threading.Event()

    def bootstrap(self, timeout: float = 5.0) -> dict[str, str]:
        """Blocking fetch of the full config set. Call before app settings are read."""
        resp = httpx.get(
            f"{self.base_url}/v1/bootstrap",
            params={"scope": self.scope},
            headers={"Authorization": f"Bearer {self.api_key}"},
            timeout=timeout,
        )
        resp.raise_for_status()
        data = resp.json()
        self._cache.update(data)
        out = {}
        for k, v in data.items():
            out[k] = v if isinstance(v, str) else json.dumps(v)
        return out

    def bootstrap_into_environ(self) -> None:
        """Injects the full config set into os.environ, before Settings/Pydantic import."""
        for k, v in self.bootstrap().items():
            os.environ[k] = v

    def connect_background(self, instance: str, image: str = "") -> None:
        """Starts the WebSocket client on a background thread with its own event loop,
        so it never blocks the caller (an asyncio app or a sync one)."""
        self._thread = threading.Thread(
            target=lambda: asyncio.run(self._run(instance, image)),
            name="scrapy-ws", daemon=True,
        )
        self._thread.start()

    async def _run(self, instance: str, image: str) -> None:
        attempt = 0
        url = self.base_url.replace("http", "ws", 1) + f"/v1/connect?scope={self.scope}"
        while not self._stop.is_set():
            try:
                async with websockets.connect(
                    url, additional_headers={"Authorization": f"Bearer {self.api_key}"}
                ) as ws:
                    attempt = 0
                    await ws.send(json.dumps({
                        "type": "hello", "scope": self.scope, "instance": instance, "image": image,
                    }))
                    async for raw in ws:
                        await self._handle(ws, raw)
            except Exception as exc:  # noqa: BLE001 - never let a network blip kill the app
                logger.warning("scrapy connection lost, reconnecting: %s", exc)
            attempt += 1
            backoff = min(30, 2 ** attempt) + random.random()  # jitter: no thundering herd
            await asyncio.sleep(backoff)

    async def _handle(self, ws, raw: str) -> None:
        try:
            msg = json.loads(raw)
        except json.JSONDecodeError:
            return
        if msg.get("type") == "state":
            self._cache.update(msg.get("entries", {}))
            self._version = msg.get("version", self._version)
        elif msg.get("type") == "change":
            self._cache[msg["key"]] = msg["value"]
            self._version = msg.get("version", self._version)
            await ws.send(json.dumps({"type": "ack", "version": self._version}))

    def get(self, key: str, default: Any = None) -> Any:
        return self._cache.get(key, default)

    def stop(self) -> None:
        self._stop.set()


_instance: ScrapyConfig | None = None


def init(base_url: str, api_key: str, scope: str, instance: str, image: str = "") -> ScrapyConfig:
    """Convenience singleton, mirroring the Java SDK's ScrapyHolder."""
    global _instance
    cfg = ScrapyConfig(base_url, api_key, scope)
    cfg.bootstrap_into_environ()
    cfg.connect_background(instance, image)
    _instance = cfg
    return cfg


def get(key: str, default: Any = None) -> Any:
    if _instance is None:
        raise RuntimeError("scrapy_client.init() was not called")
    return _instance.get(key, default)
