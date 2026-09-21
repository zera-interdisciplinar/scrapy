package com.zera.scrapy;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.env.EnvironmentPostProcessor;
import org.springframework.core.env.ConfigurableEnvironment;
import org.springframework.core.env.MapPropertySource;

import java.util.HashMap;
import java.util.Map;

/**
 * Runs before any Spring bean is created. Pulls the full config set for this pod's scope
 * from scrapy's {@code /v1/bootstrap} and adds it as the highest-priority property source,
 * so {@code ${DB_PASSWORD}} and friends in application.properties resolve from scrapy
 * without any change to the properties files themselves.
 *
 * Requires SCRAPY_URL, SCRAPY_API_KEY and SCRAPY_SCOPE in the pod's own env — that's the
 * only thing left in the Deployment YAML.
 *
 * If the fetch fails, boot fails loudly (see plan: "subir um pod com configuração errada
 * é pior do que não subir"). Set SCRAPY_OPTIONAL=true to fall back to existing env/defaults
 * instead — useful for local dev without a scrapy instance running.
 */
public class ScrapyEnvironmentPostProcessor implements EnvironmentPostProcessor {

    @Override
    public void postProcessEnvironment(ConfigurableEnvironment environment, SpringApplication application) {
        String url = environment.getProperty("SCRAPY_URL");
        String apiKey = environment.getProperty("SCRAPY_API_KEY");
        String scope = environment.getProperty("SCRAPY_SCOPE");
        boolean optional = Boolean.parseBoolean(environment.getProperty("SCRAPY_OPTIONAL", "false"));

        if (url == null || apiKey == null || scope == null) {
            if (optional) return;
            throw new IllegalStateException(
                    "SCRAPY_URL, SCRAPY_API_KEY and SCRAPY_SCOPE must be set (or SCRAPY_OPTIONAL=true for local dev)");
        }

        ScrapyConfig config = new ScrapyConfig(url, apiKey, scope);
        try {
            Map<String, Object> props = new HashMap<>(config.bootstrap());
            environment.getPropertySources().addFirst(new MapPropertySource("scrapy", props));
            ScrapyHolder.set(config); // ScrapyConfig.get(...) at runtime reuses this instance
        } catch (Exception e) {
            if (optional) return;
            throw new IllegalStateException("scrapy bootstrap failed, refusing to start", e);
        }

        String instance = environment.getProperty("HOSTNAME", "unknown-pod");
        String image = environment.getProperty("IMAGE_TAG", "unknown");
        config.connect(instance, image);
    }
}
