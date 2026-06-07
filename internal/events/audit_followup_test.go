package events

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// withCapturedLog returns a logger backed by a text handler whose output
// the test can inspect. Use to assert on log lines emitted by handlers.
func withCapturedLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h), &buf
}

func TestAdopt_PromptAbsent_EmptyString(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)
	parentTask := "task-coord"
	childTask := "task-bare"
	fixtureCoordinator(t, e, parentTask)
	e.fake.addTask(argus.Task{ID: childTask, Name: "bare-worker", Project: "frontend", WorktreePath: "/tmp/bare"})
	e.fake.setMeta(childTask, MetaKeyRole, string(db.KindWorker))
	// NO prompt meta set.

	e.handler.HandleEvent(ctx, linkCreatedEvent(parentTask, childTask, 200))

	orch, _ := e.db.Orchestrators.GetByName(ctx, "foo")
	role, err := e.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, "bare-worker")
	if err != nil {
		t.Fatalf("role lookup: %v", err)
	}
	if role.Prompt != "" {
		t.Fatalf("prompt = %q, want empty string (not NULL)", role.Prompt)
	}
}

func TestAdopt_SkippedAdoption_NoMeta_LogIncludesParentAndKey(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)
	logger, buf := withCapturedLog()
	// Replace the handler's logger with our captured one.
	e.handler = NewAdoptHandler(e.client, e.db, logger)
	parentTask := "task-coord"
	childTask := "task-no-meta"
	fixtureCoordinator(t, e, parentTask)
	e.fake.addTask(argus.Task{ID: childTask, Project: "p", WorktreePath: "/tmp/x"})
	// No meta set at all – falls into the no-role branch.

	e.handler.HandleEvent(ctx, linkCreatedEvent(parentTask, childTask, 300))

	log := buf.String()
	if !strings.Contains(log, "skipped adoption") {
		t.Fatalf("log missing skipped-adoption message: %s", log)
	}
	for _, want := range []string{"child=" + childTask, "parent=" + parentTask, "missing_key=role"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q; got: %s", want, log)
		}
	}
}

func TestAdopt_SkippedAdoption_WrongValue_LogIncludesParentAndKey(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)
	logger, buf := withCapturedLog()
	e.handler = NewAdoptHandler(e.client, e.db, logger)
	parentTask := "task-coord"
	childTask := "task-wrong-meta"
	fixtureCoordinator(t, e, parentTask)
	e.fake.addTask(argus.Task{ID: childTask, Project: "p", WorktreePath: "/tmp/x"})
	e.fake.setMeta(childTask, MetaKeyRole, "freelance") // wrong value for adoption

	e.handler.HandleEvent(ctx, linkCreatedEvent(parentTask, childTask, 400))

	log := buf.String()
	if !strings.Contains(log, "skipped adoption") {
		t.Fatalf("log missing skipped-adoption message: %s", log)
	}
	for _, want := range []string{"child=" + childTask, "parent=" + parentTask, "missing_key=role", "value=freelance"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q; got: %s", want, log)
		}
	}
}
