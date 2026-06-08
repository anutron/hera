package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SubtreeOrchIDs returns all orchestrator IDs reachable from rootOrchID via
// coordinator-binding joins, up to maxDepth levels. The root ID is always
// included in the result. truncated is true if the search stopped at maxDepth
// with a non-empty frontier (i.e., there may be more descendants).
func SubtreeOrchIDs(ctx context.Context, sqldb *sql.DB, rootOrchID int64, maxDepth int) (ids []int64, truncated bool, err error) {
	seen := map[int64]struct{}{rootOrchID: {}}
	frontier := []int64{rootOrchID}
	result := []int64{rootOrchID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		placeholders := make([]string, len(frontier))
		args := make([]any, len(frontier))
		for i, id := range frontier {
			placeholders[i] = "?"
			args[i] = id
		}
		inClause := strings.Join(placeholders, ",")

		query := fmt.Sprintf(`
SELECT DISTINCT child_orch.id
FROM orchestrators child_orch
JOIN roles child_coord
    ON child_coord.orchestrator_id = child_orch.id
    AND child_coord.kind = 'coordinator'
    AND child_coord.archived_at IS NULL
JOIN bindings child_coord_bnd
    ON child_coord_bnd.role_id = child_coord.id
    AND child_coord_bnd.ended_at IS NULL
JOIN bindings parent_bnd
    ON parent_bnd.argus_task_id = child_coord_bnd.argus_task_id
    AND parent_bnd.orchestrator_id IN (%s)
    AND parent_bnd.ended_at IS NULL
WHERE child_orch.archived_at IS NULL`, inClause)

		rows, queryErr := sqldb.QueryContext(ctx, query, args...)
		if queryErr != nil {
			return nil, false, fmt.Errorf("subtree BFS depth %d: %w", depth, queryErr)
		}

		var nextFrontier []int64
		for rows.Next() {
			var childID int64
			if scanErr := rows.Scan(&childID); scanErr != nil {
				_ = rows.Close()
				return nil, false, fmt.Errorf("subtree BFS scan: %w", scanErr)
			}
			if _, alreadySeen := seen[childID]; !alreadySeen {
				seen[childID] = struct{}{}
				result = append(result, childID)
				nextFrontier = append(nextFrontier, childID)
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, false, fmt.Errorf("subtree BFS rows: %w", rowsErr)
		}
		_ = rows.Close()

		if depth == maxDepth-1 && len(nextFrontier) > 0 {
			truncated = true
		}
		frontier = nextFrontier
	}

	return result, truncated, nil
}
