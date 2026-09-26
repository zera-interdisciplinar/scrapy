import asyncio
import json

from scrapy_client import ScrapyConfig


def test_handle_state_and_change():
    cfg = ScrapyConfig("http://localhost:8080", "sk_test", "ms-inventory")

    class FakeWS:
        def __init__(self):
            self.sent = []

        async def send(self, msg):
            self.sent.append(msg)

    ws = FakeWS()
    asyncio.run(cfg._handle(ws, json.dumps({
        "type": "state", "entries": {"search.timeout_ms": 3000}, "version": 5,
    })))
    assert cfg.get("search.timeout_ms") == 3000
    assert cfg.get("missing.key", "fallback") == "fallback"

    asyncio.run(cfg._handle(ws, json.dumps({
        "type": "change", "key": "search.timeout_ms", "value": 500, "version": 6,
    })))
    assert cfg.get("search.timeout_ms") == 500
    assert json.loads(ws.sent[0]) == {"type": "ack", "version": 6}


def test_get_before_bootstrap_returns_default():
    cfg = ScrapyConfig("http://localhost:8080", "sk_test", "ms-inventory")
    assert cfg.get("anything", "default") == "default"


if __name__ == "__main__":
    test_handle_state_and_change()
    test_get_before_bootstrap_returns_default()
    print("ok")
