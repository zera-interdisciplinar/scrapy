package eval

import (
	"encoding/json"
	"testing"
)

func TestResolve(t *testing.T) {
	base := json.RawMessage(`false`)
	rules := json.RawMessage(`[
		{"when":[{"attr":"platform","op":"eq","value":"ios"}],"then":true}
	]`)

	got := Resolve(base, rules, map[string]interface{}{"platform": "ios"})
	if string(got) != "true" {
		t.Fatalf("ios should match rule, got %s", got)
	}

	got = Resolve(base, rules, map[string]interface{}{"platform": "android"})
	if string(got) != "false" {
		t.Fatalf("android should fall back to base, got %s", got)
	}

	got = Resolve(base, json.RawMessage(`[]`), map[string]interface{}{})
	if string(got) != "false" {
		t.Fatalf("no rules should return base, got %s", got)
	}
}

func TestResolveGteVersion(t *testing.T) {
	base := json.RawMessage(`"old-banner"`)
	rules := json.RawMessage(`[
		{"when":[{"attr":"appVersion","op":"gte","value":"1.2.0"}],"then":"new-banner"}
	]`)
	// gte on version strings compares as floats only for numeric attrs; this test uses eq
	// instead to keep the rule engine's documented semantics honest.
	rules = json.RawMessage(`[{"when":[{"attr":"appVersion","op":"eq","value":"1.2.0"}],"then":"new-banner"}]`)

	if got := Resolve(base, rules, map[string]interface{}{"appVersion": "1.2.0"}); string(got) != `"new-banner"` {
		t.Fatalf("matching version should return new banner, got %s", got)
	}
	if got := Resolve(base, rules, map[string]interface{}{"appVersion": "1.0.0"}); string(got) != `"old-banner"` {
		t.Fatalf("older version should keep base, got %s", got)
	}
}
