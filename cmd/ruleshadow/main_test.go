package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadJSONReturnsExactBodyAndDecodesKnownFields(t *testing.T) {
	body := []byte("{\n  \"name\": \"baseline\"\n}\n")
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var target struct {
		Name string `json:"name"`
	}
	got, err := readJSON(path, &target)
	if err != nil {
		t.Fatalf("readJSON() error = %v", err)
	}
	if !bytes.Equal(got, body) || target.Name != "baseline" {
		t.Fatalf("readJSON() body=%q target=%+v", got, target)
	}
}

func TestReadJSONRejectsMalformedUnknownTrailingAndOversizedInputs(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
		want string
	}{
		{name: "malformed", body: []byte(`{"name":`), want: "unexpected EOF"},
		{name: "unknown field", body: []byte(`{"name":"x","extra":true}`), want: "unknown field"},
		{name: "trailing value", body: []byte(`{"name":"x"} {"name":"y"}`), want: "trailing fixture content"},
		{name: "trailing text", body: []byte(`{"name":"x"} trailing`), want: "trailing fixture content"},
		{name: "size limit", body: bytes.Repeat([]byte(" "), 2*1024*1024+1), want: "fixture exceeds 2 MiB"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.json")
			if err := os.WriteFile(path, test.body, 0o600); err != nil {
				t.Fatal(err)
			}
			var target struct {
				Name string `json:"name"`
			}
			_, err := readJSON(path, &target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readJSON() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestReadJSONReturnsFilesystemError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	var target any
	if _, err := readJSON(path, &target); !os.IsNotExist(err) {
		t.Fatalf("readJSON(missing path) error = %v, want not-exist", err)
	}
}
