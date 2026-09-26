package com.zera.scrapy;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.net.http.WebSocket;
import java.time.Duration;
import java.util.Map;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Runtime client for scrapy: opens the WebSocket at boot, keeps the last known value of
 * every key in memory, and never blocks a caller on network I/O after the initial connect.
 *
 * Precedence documented in the plan: scrapy (runtime) &gt; scrapy (boot env) &gt; default in code.
 * If scrapy dies after the pod is already up, {@link #get} keeps returning the last value
 * received — it never throws and never blocks the caller.
 */
public final class ScrapyConfig {

    private final String baseUrl;
    private final String apiKey;
    private final String scope;
    private final ObjectMapper mapper = new ObjectMapper();
    private final Map<String, JsonNode> cache = new ConcurrentHashMap<>();
    private final AtomicInteger version = new AtomicInteger(0);
    private final HttpClient http = HttpClient.newHttpClient();
    private WebSocket ws;

    public ScrapyConfig(String baseUrl, String apiKey, String scope) {
        this.baseUrl = baseUrl;
        this.apiKey = apiKey;
        this.scope = scope;
    }

    /** Blocking bootstrap fetch, used before the framework starts reading config. */
    public Map<String, String> bootstrap() throws Exception {
        HttpRequest req = HttpRequest.newBuilder(URI.create(baseUrl + "/v1/bootstrap?scope=" + scope))
                .header("Authorization", "Bearer " + apiKey)
                .timeout(Duration.ofSeconds(5))
                .GET().build();
        HttpResponse<String> res = http.send(req, HttpResponse.BodyHandlers.ofString());
        if (res.statusCode() != 200) {
            throw new IllegalStateException("scrapy bootstrap failed: HTTP " + res.statusCode());
        }
        JsonNode root = mapper.readTree(res.body());
        Map<String, String> out = new ConcurrentHashMap<>();
        root.fields().forEachRemaining(e -> {
            String v = e.getValue().isTextual() ? e.getValue().asText() : e.getValue().toString();
            out.put(e.getKey(), v);
            cache.put(e.getKey(), e.getValue());
        });
        return out;
    }

    /** Opens the live WebSocket; call after {@link #bootstrap()} succeeds. Never throws. */
    public void connect(String instanceId, String image) {
        String url = baseUrl.replaceFirst("^http", "ws") + "/v1/connect?scope=" + scope;
        CountDownLatch helloSent = new CountDownLatch(1);
        WebSocket.Builder builder = http.newWebSocketBuilder()
                .header("Authorization", "Bearer " + apiKey);
        builder.buildAsync(URI.create(url), new WebSocket.Listener() {
            final StringBuilder buf = new StringBuilder();

            @Override
            public void onOpen(WebSocket webSocket) {
                ws = webSocket;
                String hello = String.format(
                        "{\"type\":\"hello\",\"scope\":\"%s\",\"instance\":\"%s\",\"image\":\"%s\"}",
                        scope, instanceId, image);
                webSocket.sendText(hello, true);
                helloSent.countDown();
                WebSocket.Listener.super.onOpen(webSocket);
            }

            @Override
            public CompletionStage<?> onText(WebSocket webSocket, CharSequence data, boolean last) {
                buf.append(data);
                if (last) {
                    handleMessage(buf.toString(), webSocket);
                    buf.setLength(0);
                }
                webSocket.request(1);
                return null;
            }

            @Override
            public CompletionStage<?> onClose(WebSocket webSocket, int statusCode, String reason) {
                scheduleReconnect(instanceId, image);
                return null;
            }

            @Override
            public void onError(WebSocket webSocket, Throwable error) {
                scheduleReconnect(instanceId, image);
            }
        }).exceptionally(ex -> {
            scheduleReconnect(instanceId, image);
            return null;
        });
    }

    private void handleMessage(String raw, WebSocket webSocket) {
        try {
            JsonNode msg = mapper.readTree(raw);
            String type = msg.path("type").asText();
            if ("state".equals(type)) {
                msg.path("entries").fields().forEachRemaining(e -> cache.put(e.getKey(), e.getValue()));
                version.set(msg.path("version").asInt());
            } else if ("change".equals(type)) {
                cache.put(msg.path("key").asText(), msg.path("value"));
                int v = msg.path("version").asInt();
                version.set(v);
                webSocket.sendText("{\"type\":\"ack\",\"version\":" + v + "}", true);
            }
        } catch (Exception ignored) {
            // malformed message from server: keep last known good state
        }
    }

    private volatile boolean reconnecting = false;

    private void scheduleReconnect(String instanceId, String image) {
        if (reconnecting) return;
        reconnecting = true;
        Thread t = new Thread(() -> {
            int attempt = 0;
            while (true) {
                attempt++;
                long backoffMs = Math.min(30_000, (long) (1000 * Math.pow(2, attempt)))
                        + (long) (Math.random() * 1000); // jitter so pods don't reconnect in lockstep
                try {
                    TimeUnit.MILLISECONDS.sleep(backoffMs);
                } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    return;
                }
                reconnecting = false;
                connect(instanceId, image);
                return;
            }
        }, "scrapy-reconnect");
        t.setDaemon(true);
        t.start();
    }

    /** Returns the current value for {@code key}, or {@code defaultValue} if unknown. */
    public String get(String key, String defaultValue) {
        JsonNode v = cache.get(key);
        if (v == null) return defaultValue;
        return v.isTextual() ? v.asText() : v.toString();
    }

    public boolean getBool(String key, boolean defaultValue) {
        JsonNode v = cache.get(key);
        return v == null ? defaultValue : v.asBoolean(defaultValue);
    }

    public double getNumber(String key, double defaultValue) {
        JsonNode v = cache.get(key);
        return v == null ? defaultValue : v.asDouble(defaultValue);
    }

    /** Closes the WebSocket cleanly, e.g. on application shutdown. */
    public void close() {
        if (ws != null) {
            ws.sendClose(WebSocket.NORMAL_CLOSURE, "shutdown");
        }
    }
}
