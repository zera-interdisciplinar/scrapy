package cluster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtected(t *testing.T) {
	cases := map[string]bool{
		"scrapy":                 true,
		"scrapy-postgres":        true,
		"kong":                   true,
		"kong-proxy":             true,
		"infra-gtw-kong":         true,
		"ms-inventory":           false,
		"ms-administrative-core": false,
		"ms-inventory-postgres":  false,
	}
	for name, want := range cases {
		if got := Protected(name); got != want {
			t.Errorf("Protected(%q)=%v want %v", name, got, want)
		}
	}
}

func TestStatusAndScale(t *testing.T) {
	replicas := map[string]int{
		"ms-inventory":    1,
		"scrapy":          1,
		"scrapy-postgres": 1,
		"kong-proxy":      2,
	}
	patched := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing bearer, got %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/qa/deployments":
			items := []any{}
			for name, n := range replicas {
				items = append(items, map[string]any{
					"metadata": map[string]any{"name": name},
					"spec":     map[string]any{"replicas": n},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case r.Method == http.MethodPatch:
			const prefix = "/apis/apps/v1/namespaces/qa/deployments/"
			const suffix = "/scale"
			path := r.URL.Path
			if len(path) < len(prefix)+len(suffix) || path[:len(prefix)] != prefix || path[len(path)-len(suffix):] != suffix {
				http.NotFound(w, r)
				return
			}
			name := path[len(prefix) : len(path)-len(suffix)]
			body, _ := io.ReadAll(r.Body)
			var patch struct {
				Spec struct {
					Replicas int `json:"replicas"`
				} `json:"spec"`
			}
			if err := json.Unmarshal(body, &patch); err != nil {
				t.Errorf("bad patch body: %v", err)
			}
			if Protected(name) {
				t.Errorf("scaled protected deployment %s", name)
			}
			replicas[name] = patch.Spec.Replicas
			patched[name] = patch.Spec.Replicas
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token", "qa", srv.Client())
	ctx := context.Background()

	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled {
		t.Fatal("expected QA enabled when ms-inventory has replicas")
	}

	st, err = c.SetEnabled(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled {
		t.Fatal("expected QA disabled after scale to 0")
	}
	if patched["ms-inventory"] != 0 {
		t.Fatalf("ms-inventory replicas=%d want 0", patched["ms-inventory"])
	}
	if _, ok := patched["scrapy"]; ok {
		t.Fatal("scrapy must not be scaled")
	}
	if _, ok := patched["scrapy-postgres"]; ok {
		t.Fatal("scrapy-postgres must not be scaled")
	}
	if _, ok := patched["kong-proxy"]; ok {
		t.Fatal("kong-proxy must not be scaled")
	}

	st, err = c.SetEnabled(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled {
		t.Fatal("expected QA enabled after scale to 1")
	}
	if patched["ms-inventory"] != 1 {
		t.Fatalf("ms-inventory replicas=%d want 1", patched["ms-inventory"])
	}
}
