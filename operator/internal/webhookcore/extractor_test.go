package webhookcore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONPathExtractor_String_SimplePath(t *testing.T) {
	e := JSONPathExtractor{}
	v, err := e.String([]byte(`{"status":"completed"}`), "{.status}")
	require.NoError(t, err)
	require.Equal(t, "completed", v)
}

func TestJSONPathExtractor_String_NestedPath(t *testing.T) {
	e := JSONPathExtractor{}
	v, err := e.String([]byte(`{"data":{"state":"RUNNING"}}`), "{.data.state}")
	require.NoError(t, err)
	require.Equal(t, "RUNNING", v)
}

func TestJSONPathExtractor_String_ArrayIndex(t *testing.T) {
	e := JSONPathExtractor{}
	v, err := e.String([]byte(`{"jobs":[{"id":"j1"},{"id":"j2"}]}`), "{.jobs[0].id}")
	require.NoError(t, err)
	require.Equal(t, "j1", v)
}

func TestJSONPathExtractor_String_PathNotFound(t *testing.T) {
	e := JSONPathExtractor{}
	_, err := e.String([]byte(`{"status":"ok"}`), "{.missing}")
	require.Error(t, err)
}

func TestJSONPathExtractor_String_InvalidJSON(t *testing.T) {
	e := JSONPathExtractor{}
	_, err := e.String([]byte("not json"), "{.status}")
	require.Error(t, err)
}

func TestJSONPathExtractor_String_EmptyBody(t *testing.T) {
	e := JSONPathExtractor{}
	_, err := e.String([]byte("{}"), "{.status}")
	require.Error(t, err)
}

func TestJSONPathExtractor_String_NumericValue(t *testing.T) {
	e := JSONPathExtractor{}
	v, err := e.String([]byte(`{"count":200}`), "{.count}")
	require.NoError(t, err)
	require.Equal(t, "200", v)
}

func TestJSONPathExtractor_String_BooleanValue(t *testing.T) {
	e := JSONPathExtractor{}
	v, err := e.String([]byte(`{"passed":true}`), "{.passed}")
	require.NoError(t, err)
	require.Equal(t, "true", v)
}

func TestJSONPathExtractor_String_NullValue(t *testing.T) {
	e := JSONPathExtractor{}
	v, err := e.String([]byte(`{"phase":null}`), "{.phase}")
	require.NoError(t, err)
	require.Equal(t, "null", v)
}
