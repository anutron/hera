package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anutron/hera/internal/db"
)

// TreeUpdatesHandler implements hera_tree_updates. Returns TLDR-only subject
// lines for all messages sent within the caller's orchestrator subtree since
// a cursor, then advances the stored cursor.
type TreeUpdatesHandler struct {
	resolver *Resolver
	db       *db.DB
}

// NewTreeUpdatesHandler constructs a TreeUpdatesHandler.
func NewTreeUpdatesHandler(r *Resolver, database *db.DB) *TreeUpdatesHandler {
	return &TreeUpdatesHandler{resolver: r, db: database}
}

// TreeUpdatesInput is the tool's input schema.
type TreeUpdatesInput struct {
	Cwd          string `json:"cwd"`
	Orchestrator string `json:"orchestrator,omitempty"`
	Since        *int64 `json:"since,omitempty"`
}

// TreeUpdateMessage is one row in the response — TLDR only, no body.
type TreeUpdateMessage struct {
	ID               int64  `json:"id"`
	SentAt           string `json:"sent_at"`
	FromRole         string `json:"from_role"`
	FromOrchestrator string `json:"from_orchestrator"`
	ToRole           string `json:"to_role"`
	ToOrchestrator   string `json:"to_orchestrator"`
	Tldr             string `json:"tldr"`
}

// TreeUpdatesOutput is the success payload.
type TreeUpdatesOutput struct {
	Count            int                 `json:"count"`
	NextCursor       int64               `json:"next_cursor"`
	TruncatedAtDepth bool                `json:"truncated_at_depth,omitempty"`
	Messages         []TreeUpdateMessage `json:"messages"`
}

// Handle implements Handler.
func (h *TreeUpdatesHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in TreeUpdatesInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_tree_updates: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_tree_updates: cwd is required")
	}

	_, role, _, err := h.resolver.CallerRole(ctx, in.Cwd, in.Orchestrator)
	if err != nil {
		return ErrorResponse("hera_tree_updates: " + err.Error())
	}

	orchIDs, truncated, err := db.SubtreeOrchIDs(ctx, h.db.Raw(), role.OrchestratorID, 6)
	if err != nil {
		return ErrorResponse("hera_tree_updates: subtree: " + err.Error())
	}

	// Determine effective cursor.
	var cursor int64
	if in.Since != nil {
		cursor = *in.Since
	} else {
		cursor, err = h.db.TreeCursors.GetTreeCursor(ctx, role.ID)
		if err != nil {
			return ErrorResponse("hera_tree_updates: cursor: " + err.Error())
		}
	}

	msgs, err := h.querySubtreeMessages(ctx, orchIDs, cursor)
	if err != nil {
		return ErrorResponse("hera_tree_updates: query: " + err.Error())
	}

	nextCursor := cursor
	if len(msgs) > 0 {
		nextCursor = msgs[len(msgs)-1].ID
	}
	if err := h.db.TreeCursors.UpsertTreeCursor(ctx, role.ID, nextCursor); err != nil {
		return ErrorResponse("hera_tree_updates: save cursor: " + err.Error())
	}

	out := TreeUpdatesOutput{
		Count:            len(msgs),
		NextCursor:       nextCursor,
		TruncatedAtDepth: truncated,
		Messages:         make([]TreeUpdateMessage, 0, len(msgs)),
	}
	out.Messages = append(out.Messages, msgs...)
	return jsonText(out)
}

func (h *TreeUpdatesHandler) querySubtreeMessages(ctx context.Context, orchIDs []int64, since int64) ([]TreeUpdateMessage, error) {
	if len(orchIDs) == 0 {
		return nil, nil
	}

	// Build IN clause placeholders.
	placeholders := make([]string, len(orchIDs))
	args := []any{since}
	for i, id := range orchIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	inClause := strings.Join(placeholders, ",")
	// Duplicate the args for the second IN (to_role's orchestrator_id).
	for _, id := range orchIDs {
		args = append(args, id)
	}

	rows, err := h.db.Raw().QueryContext(ctx, fmt.Sprintf(`
SELECT m.id, m.sent_at, m.tldr,
       fr.name, fo.name, tr.name, too.name
FROM messages m
JOIN roles fr  ON fr.id  = m.from_role_id
JOIN orchestrators fo ON fo.id = fr.orchestrator_id
JOIN roles tr  ON tr.id  = m.to_role_id
JOIN orchestrators too ON too.id = tr.orchestrator_id
WHERE m.id > ?
  AND (fr.orchestrator_id IN (%s) OR tr.orchestrator_id IN (%s))
ORDER BY m.id ASC
LIMIT 200`, inClause, inClause), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []TreeUpdateMessage
	for rows.Next() {
		var m TreeUpdateMessage
		if err := rows.Scan(&m.ID, &m.SentAt, &m.Tldr, &m.FromRole, &m.FromOrchestrator, &m.ToRole, &m.ToOrchestrator); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
