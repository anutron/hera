package argus

import (
	"context"
	"net/http"
	"testing"
)

// TestClient_ListProjectsFull verifies the GET /api/projects/full envelope is
// parsed into Project records carrying name, branch, and backend.
func TestClient_ListProjectsFull(t *testing.T) {
	var gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"projects":[
			{"name":"ARGUS","path":"/p/argus","branch":"main","backend":"claude"},
			{"name":"Hera","path":"/p/hera"}
		]}`))
	})
	defer srv.Close()

	got, err := c.ListProjectsFull(context.Background())
	if err != nil {
		t.Fatalf("ListProjectsFull: %v", err)
	}
	if gotPath != "/api/projects/full" {
		t.Fatalf("path = %s, want /api/projects/full", gotPath)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Name != "ARGUS" || got[0].Branch != "main" || got[0].Backend != "claude" {
		t.Fatalf("project[0] = %+v, want ARGUS/main/claude", got[0])
	}
	if got[1].Name != "Hera" || got[1].Branch != "" || got[1].Backend != "" {
		t.Fatalf("project[1] = %+v, want Hera with empty branch/backend", got[1])
	}
}

// TestClient_ListProjects_DerivesNamesFromFull verifies ListProjects sources
// its name list from /api/projects/full (the single discovery endpoint), not a
// separate /api/projects call.
func TestClient_ListProjects_DerivesNamesFromFull(t *testing.T) {
	var gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"projects":[
			{"name":"ARGUS","path":"/p/argus"},
			{"name":"Iris","path":"/p/iris"}
		]}`))
	})
	defer srv.Close()

	names, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if gotPath != "/api/projects/full" {
		t.Fatalf("path = %s, want /api/projects/full", gotPath)
	}
	if len(names) != 2 || names[0] != "ARGUS" || names[1] != "Iris" {
		t.Fatalf("names = %v, want [ARGUS Iris]", names)
	}
}
