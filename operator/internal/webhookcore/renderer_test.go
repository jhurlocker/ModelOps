package webhookcore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderer_Execute_EmptyTemplate(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("", map[string]any{"foo": "bar"})
	require.NoError(t, err)
	require.Equal(t, "", out)
}

func TestRenderer_Execute_SimpleFieldAccess(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("Hello {{.Name}}", map[string]any{"Name": "World"})
	require.NoError(t, err)
	require.Equal(t, "Hello World", out)
}

func TestRenderer_Execute_NestedFieldAccess(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("model={{.Model.Name}} version={{.Model.Version}}", map[string]any{
		"Model": map[string]any{"Name": "granite", "Version": "v1"},
	})
	require.NoError(t, err)
	require.Equal(t, "model=granite version=v1", out)
}

func TestRenderer_Execute_Conditional_Eq(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("{{if eq .Status \"RUNNING\"}}active{{else}}idle{{end}}", map[string]any{
		"Status": "RUNNING",
	})
	require.NoError(t, err)
	require.Equal(t, "active", out)
}

func TestRenderer_Execute_Conditional_Ne(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("{{if ne .Status \"RUNNING\"}}idle{{else}}active{{end}}", map[string]any{
		"Status": "FAILED",
	})
	require.NoError(t, err)
	require.Equal(t, "idle", out)
}

func TestRenderer_Execute_Index(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("{{index .Items 0}}", map[string]any{
		"Items": []any{"first", "second"},
	})
	require.NoError(t, err)
	require.Equal(t, "first", out)
}

func TestRenderer_Execute_Len(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("count={{len .Items}}", map[string]any{
		"Items": []any{"a", "b", "c"},
	})
	require.NoError(t, err)
	require.Equal(t, "count=3", out)
}

func TestRenderer_Execute_URLQueryEscape(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("q={{urlquery \"foo bar\"}}", nil)
	require.NoError(t, err)
	require.Equal(t, "q=foo+bar", out)
}

func TestRenderer_Execute_JSONMarshal(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("payload={{json .Data}}", map[string]any{
		"Data": map[string]any{"key": "val"},
	})
	require.NoError(t, err)
	require.Equal(t, `payload={"key":"val"}`, out)
}

func TestRenderer_Execute_Printf(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("{{printf \"%s-%d\" .Name .Count}}", map[string]any{
		"Name": "job", "Count": 42,
	})
	require.NoError(t, err)
	require.Equal(t, "job-42", out)
}

func TestRenderer_Execute_ForbiddenFunction_Call(t *testing.T) {
	r := Renderer{}
	// The 'call' function is excluded from the allowlist.
	_, err := r.Execute("{{call .Func}}", map[string]any{
		"Func": func() string { return "executed" },
	})
	require.Error(t, err, "call must not be in the function allowlist")
}

func TestRenderer_Execute_ForbiddenFunction_Slice(t *testing.T) {
	r := Renderer{}
	_, err := r.Execute("{{slice .Items 0 1}}", map[string]any{
		"Items": []any{"a", "b"},
	})
	require.Error(t, err, "slice must not be in the function allowlist")
}

func TestRenderer_Execute_InvalidSyntax(t *testing.T) {
	r := Renderer{}
	_, err := r.Execute("{{.Name", nil)
	require.Error(t, err)
}

func TestRenderer_Execute_And_Or_Not(t *testing.T) {
	r := Renderer{}
	out, err := r.Execute("{{if and .A .B}}both{{end}}{{if or .C .D}}either{{end}}{{if not .E}}neither{{end}}", map[string]any{
		"A": true, "B": true, "C": false, "D": true, "E": false,
	})
	require.NoError(t, err)
	require.Equal(t, "botheitherneither", out)
}
