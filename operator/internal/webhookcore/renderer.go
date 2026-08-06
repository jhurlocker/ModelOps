package webhookcore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretKeyRef identifies a single key within a named Secret.
type SecretKeyRef struct {
	Name string
	Key  string
}

// CallConfig describes one outbound HTTP call.
type CallConfig struct {
	Method string
	URL    string
	Body   string
	Header string
}

// CallResult is the raw result of an HTTP call.
type CallResult struct {
	StatusCode int
	Body       []byte
}

// Caller executes HTTP calls. The interface seam exists so tests can
// swap in a fake that returns scripted responses without a real network
// -- same interface-injection pattern as stagecommon.StageRunner.
type Caller interface {
	Call(ctx context.Context, cfg CallConfig) (CallResult, error)
}

// DefaultCaller is the production HTTP caller. Nil Client means
// http.DefaultClient.
type DefaultCaller struct {
	Client *http.Client
}

func (c *DefaultCaller) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

// Call executes an HTTP request. RetryPolicy is applied by the
// StageRunner through repeated Call invocations -- keeping this
// interface minimal.
func (c *DefaultCaller) Call(ctx context.Context, cfg CallConfig) (CallResult, error) {
	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, nil)
	if err != nil {
		return CallResult{}, fmt.Errorf("building request: %w", err)
	}
	if cfg.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.Header != "" {
		req.Header.Set("Authorization", cfg.Header)
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return CallResult{}, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CallResult{}, fmt.Errorf("reading response body: %w", err)
	}
	return CallResult{StatusCode: resp.StatusCode, Body: body}, nil
}

// Renderer executes Go templates against an arbitrary data context using
// a strict, allowlisted function map.
type Renderer struct{}

// Execute renders tmplString against data using the allowlisted function
// map. An empty tmplString returns ("", nil).
func (Renderer) Execute(tmplString string, data any) (string, error) {
	if tmplString == "" {
		return "", nil
	}
	tmpl, err := template.New("").Funcs(rendererFuncMap).Parse(tmplString)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var w tmplWriter
	if err := tmpl.Execute(&w, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return string(w.buf), nil
}

type tmplWriter struct{ buf []byte }

func (w *tmplWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// rendererFuncMap is the explicit allowlist of Go template functions
// available in requestTemplate, messageTemplate, and detailsUrlTemplate.
// Go template built-ins that are NOT in this map are explicitly
// overridden by disabledFuncs (see below) at parse time -- including
// call, html, js, and slice -- so they produce a clear error instead of
// silently executing.
//
// Explicitly overridden (disabled) built-ins and rationale:
//   - call: the main arbitrary-code-execution vector -- can invoke any Go
//     function value reachable from the template context. Overridden to
//     an always-erroring function.
//   - html/js: not useful for API/JSON body rendering and could mislead
//     about the output format.
//   - slice: array-manipulation logic that belongs in the provider's own
//     service, not in a data-access template.
var rendererFuncMap = template.FuncMap{
	"eq":       func(a, b any) bool { return a == b },
	"ne":       func(a, b any) bool { return a != b },
	"lt":       tmplLessThan,
	"le":       tmplLessOrEqual,
	"gt":       tmplGreaterThan,
	"ge":       tmplGreaterOrEqual,
	"and":      func(a, b bool) bool { return a && b },
	"or":       func(a, b bool) bool { return a || b },
	"not":      func(a bool) bool { return !a },
	"print":    fmt.Sprint,
	"printf":   fmt.Sprintf,
	"println":  fmt.Sprintln,
	"index":    tmplIndex,
	"len":      tmplLen,
	"urlquery": url.QueryEscape,
	"json":     func(v any) (string, error) { b, e := json.Marshal(v); return string(b), e },
	// Explicitly override Go template built-ins that are dangerous or
	// misleading in a data-access template context:
	"call":  disabledFunc("call"),
	"html":  disabledFunc("html"),
	"js":    disabledFunc("js"),
	"slice": disabledFunc("slice"),
}

func disabledFunc(name string) func(...any) (string, error) {
	return func(args ...any) (string, error) {
		return "", fmt.Errorf("template function %q is disabled for security", name)
	}
}

func tmplLessThan(a, b any) bool {
	av, aok := toFloat(a)
	bv, bok := toFloat(b)
	if aok && bok {
		return av < bv
	}
	return false
}
func tmplLessOrEqual(a, b any) bool {
	if av, aok := toFloat(a); aok {
		if bv, bok := toFloat(b); bok {
			return av <= bv
		}
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return as <= bs
		}
	}
	return false
}
func tmplGreaterThan(a, b any) bool {
	if av, aok := toFloat(a); aok {
		if bv, bok := toFloat(b); bok {
			return av > bv
		}
	}
	return false
}
func tmplGreaterOrEqual(a, b any) bool {
	if av, aok := toFloat(a); aok {
		if bv, bok := toFloat(b); bok {
			return av >= bv
		}
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return as >= bs
		}
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch vv := v.(type) {
	case float64:
		return vv, true
	case int:
		return float64(vv), true
	case int64:
		return float64(vv), true
	}
	return 0, false
}

func toInt(v any) (int, error) {
	switch vv := v.(type) {
	case int:
		return vv, nil
	case int64:
		return int(vv), nil
	case float64:
		return int(vv), nil
	}
	return 0, fmt.Errorf("cannot convert %T to int", v)
}

func tmplIndex(item any, indices ...any) (any, error) {
	v := reflect.ValueOf(item)
	for _, idx := range indices {
		index, err := toInt(idx)
		if err != nil {
			return nil, fmt.Errorf("index: %w", err)
		}
		switch v.Kind() {
		case reflect.Array, reflect.Slice, reflect.String:
			v = v.Index(index)
		case reflect.Map:
			v = v.MapIndex(reflect.ValueOf(index))
		default:
			return nil, fmt.Errorf("index of type %s", v.Type())
		}
	}
	return v.Interface(), nil
}

func tmplLen(item any) (int, error) {
	v := reflect.ValueOf(item)
	switch v.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return v.Len(), nil
	}
	return 0, fmt.Errorf("len of type %T", item)
}

// BuildAuthHeader reads the Secret identified by ref in namespace and
// constructs headerValue: if a provided scheme like "Bearer " is
// non-empty, it's prepended. e.g. BuildAuthHeader(ctx, c, "ns", ref,
// "Bearer ") returns the raw Secret value with "Bearer " prefixed.
func BuildAuthHeader(ctx context.Context, c client.Client, namespace string, ref SecretKeyRef, scheme string) (string, error) {
	var secret corev1.Secret
	key := types.NamespacedName{Name: ref.Name, Namespace: namespace}
	if err := c.Get(ctx, key, &secret); err != nil {
		return "", fmt.Errorf("reading auth secret %s/%s: %w", namespace, ref.Name, err)
	}
	val, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("auth secret %s/%s has no key %q", namespace, ref.Name, ref.Key)
	}
	return scheme + string(val), nil
}
