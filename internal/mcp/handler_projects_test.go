package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
)

// TestProjects_ListsConfiguredProjects verifies hera_projects returns every
// configured project with name + configured branch/backend and a count.
func TestProjects_ListsConfiguredProjects(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.mu.Lock()
	e.fake.projectsFull = []argus.Project{
		{Name: "ARGUS", Branch: "main", Backend: "claude"},
		{Name: "Hera"},
	}
	e.fake.mu.Unlock()

	h := NewProjectsHandler(e.client)
	resp := h.Handle(ctx, mustMarshal(t, ProjectsInput{Cwd: "/anywhere"}))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}
	var out ProjectsOutput
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &out); err != nil {
		t.Fatalf("decode ProjectsOutput: %v", err)
	}
	if out.Count != 2 || len(out.Projects) != 2 {
		t.Fatalf("count/len = %d/%d, want 2/2: %+v", out.Count, len(out.Projects), out)
	}
	if out.Projects[0].Name != "ARGUS" || out.Projects[0].Branch != "main" || out.Projects[0].Backend != "claude" {
		t.Fatalf("project[0] = %+v, want ARGUS/main/claude", out.Projects[0])
	}
	if out.Projects[1].Name != "Hera" || out.Projects[1].Branch != "" || out.Projects[1].Backend != "" {
		t.Fatalf("project[1] = %+v, want Hera with empty defaults", out.Projects[1])
	}
}

// TestProjects_NoCallerRoleRequired verifies hera_projects works from a cwd
// that maps to no tracked argus task (it does no role resolution).
func TestProjects_NoCallerRoleRequired(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.mu.Lock()
	e.fake.projectsFull = []argus.Project{{Name: "ARGUS"}}
	e.fake.mu.Unlock()

	h := NewProjectsHandler(e.client)
	resp := h.Handle(ctx, mustMarshal(t, ProjectsInput{Cwd: "/totally/unknown/path"}))
	if resp.IsError {
		t.Fatalf("hera_projects must not require a resolvable caller role; got error: %s", resp.Content[0].Text)
	}
	var out ProjectsOutput
	_ = json.Unmarshal([]byte(resp.Content[0].Text), &out)
	if out.Count != 1 {
		t.Fatalf("count = %d, want 1", out.Count)
	}
}

// TestSpawnWorker_UnknownProjectListsValidNames verifies the enriched error
// names the rejected project AND lists the configured project names, and that
// no argus task is created.
func TestSpawnWorker_UnknownProjectListsValidNames(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.projectsFull = []argus.Project{{Name: "ARGUS"}, {Name: "Hera"}, {Name: "Iris"}}
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "ARGUS")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:     "/tmp/c1",
		Prompt:  "do work",
		Project: "argus", // wrong case — not configured
	}))
	if !resp.IsError {
		t.Fatalf("expected error for unknown project, got success")
	}
	msg := resp.Content[0].Text
	if !strings.Contains(msg, `"argus"`) {
		t.Fatalf("error must name the rejected project; got %q", msg)
	}
	for _, want := range []string{"ARGUS", "Hera", "Iris"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error must list valid project %q; got %q", want, msg)
		}
	}
	// No task created.
	e.fake.mu.Lock()
	n := len(e.fake.createInputs)
	e.fake.mu.Unlock()
	if n != 0 {
		t.Fatalf("createInputs = %d, want 0 (no task on rejected project)", n)
	}
}

// TestSpawnWorker_KnownProjectPasses verifies a configured project passes the
// up-front validation and spawns normally.
func TestSpawnWorker_KnownProjectPasses(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.projectsFull = []argus.Project{{Name: "ARGUS"}, {Name: "Hera"}}
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "ARGUS")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:     "/tmp/c1",
		Prompt:  "do work",
		Project: "Hera",
	}))
	out := decodeSpawnOutput(t, resp)
	if out.ArgusTaskID != "w1" {
		t.Fatalf("ArgusTaskID = %q, want w1", out.ArgusTaskID)
	}
}

// TestSpawnWorker_DiscoveryFailureDoesNotBlock verifies that when the project
// list cannot be fetched (fake 404s /api/projects/full), the spawn falls
// through to CreateTask rather than hard-failing on validation.
func TestSpawnWorker_DiscoveryFailureDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	// projectsFull left nil → GET /api/projects/full 404s → discovery errors.
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "myproject")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/c1",
		Prompt: "do work",
	}))
	if resp.IsError {
		t.Fatalf("discovery failure must not block spawning; got error: %s", resp.Content[0].Text)
	}
	out := decodeSpawnOutput(t, resp)
	if out.ArgusTaskID != "w1" {
		t.Fatalf("ArgusTaskID = %q, want w1", out.ArgusTaskID)
	}
}
