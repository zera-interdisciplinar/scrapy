package com.zera.scrapy;

/**
 * Process-wide handle to the single {@link ScrapyConfig} instance created at boot by
 * {@link ScrapyEnvironmentPostProcessor}. Application code reads live values with
 * {@code ScrapyHolder.get().getBool("feature.x", false)} at hot paths — not via injected
 * beans, since Spring Boot 4 has no {@code @RefreshScope} and a bean built once in a
 * constructor would never see later changes.
 */
public final class ScrapyHolder {
    private static volatile ScrapyConfig instance;

    private ScrapyHolder() {}

    static void set(ScrapyConfig config) {
        instance = config;
    }

    public static ScrapyConfig get() {
        if (instance == null) {
            throw new IllegalStateException("ScrapyConfig not initialized; is ScrapyEnvironmentPostProcessor registered?");
        }
        return instance;
    }
}
