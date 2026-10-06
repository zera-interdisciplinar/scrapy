// Package cluster talks to the Kubernetes API to turn the QA namespace on/off
// (scale unprotected Deployments to 1 or 0). Scrapy and Kong stay up so the
// toggle itself remains reachable through the gateway.
package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("not running in-cluster")

const (
	saTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	saCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

type Deployment struct {
	Name      string `json:"name"`
	Replicas  int    `json:"replicas"`
	Protected bool   `json:"protected"`
}

type Status struct {
	Namespace   string       `json:"namespace"`
	Enabled     bool         `json:"enabled"`
	Deployments []Deployment `json:"deployments"`
}

type Client struct {
	http      *http.Client
	baseURL   string
	token     string
	Namespace string
}

// Protected deployments are never scaled: scrapy (and its postgres), plus anything
// whose name contains "kong". Turning QA off must not take down the control plane
// or the gateway used to turn it back on.
func Protected(name string) bool {
	n := strings.ToLower(name)
	if n == "scrapy" || strings.HasPrefix(n, "scrapy-") {
		return true
	}
	return strings.Contains(n, "kong")
}

func New(baseURL, token, namespace string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	if namespace == "" {
		namespace = "qa"
	}
	return &Client{
		http:      hc,
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		Namespace: namespace,
	}
}

// InCluster builds a client from the pod's ServiceAccount. Returns ErrUnavailable
// when the process is not inside Kubernetes (local docker compose).
func InCluster(namespace string) (*Client, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, ErrUnavailable
	}
	token, err := os.ReadFile(saTokenPath)
	if err != nil {
		return nil, fmt.Errorf("%w: read sa token: %v", ErrUnavailable, err)
	}
	caPEM, err := os.ReadFile(saCAPath)
	if err != nil {
		return nil, fmt.Errorf("%w: read sa ca: %v", ErrUnavailable, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("kubernetes SA CA is not valid PEM")
	}
	hc := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	return New("https://"+host+":"+port, strings.TrimSpace(string(token)), namespace, hc), nil
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	deps, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	out := &Status{Namespace: c.Namespace, Deployments: deps}
	for _, d := range deps {
		if !d.Protected && d.Replicas > 0 {
			out.Enabled = true
			break
		}
	}
	return out, nil
}

func (c *Client) SetEnabled(ctx context.Context, enabled bool) (*Status, error) {
	want := 0
	if enabled {
		want = 1
	}
	deps, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range deps {
		if d.Protected || d.Replicas == want {
			continue
		}
		if err := c.scale(ctx, d.Name, want); err != nil {
			return nil, err
		}
	}
	return c.Status(ctx)
}

func (c *Client) list(ctx context.Context) ([]Deployment, error) {
	body, code, err := c.do(ctx, http.MethodGet, "/apis/apps/v1/namespaces/"+c.Namespace+"/deployments", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("list deployments: %s", k8sError(code, body))
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas *int `json:"replicas"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode deployment list: %w", err)
	}
	out := make([]Deployment, 0, len(list.Items))
	for _, item := range list.Items {
		replicas := 1
		if item.Spec.Replicas != nil {
			replicas = *item.Spec.Replicas
		}
		out = append(out, Deployment{
			Name:      item.Metadata.Name,
			Replicas:  replicas,
			Protected: Protected(item.Metadata.Name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Client) scale(ctx context.Context, name string, replicas int) error {
	payload, _ := json.Marshal(map[string]any{"spec": map[string]any{"replicas": replicas}})
	path := "/apis/apps/v1/namespaces/" + c.Namespace + "/deployments/" + name + "/scale"
	body, code, err := c.do(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return fmt.Errorf("scale %s: %s", name, k8sError(code, body))
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, payload []byte) ([]byte, int, error) {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/merge-patch+json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, res.StatusCode, err
	}
	return body, res.StatusCode, nil
}

func k8sError(code int, body []byte) string {
	var st struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &st) == nil && st.Message != "" {
		return fmt.Sprintf("http %d: %s", code, st.Message)
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return fmt.Sprintf("http %d: %s", code, bytes.TrimSpace(body))
}
