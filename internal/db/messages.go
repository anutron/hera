package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MessagesDAO is the typed accessor for the messages table.
type MessagesDAO struct{ db *sql.DB }

// CreateMessageInput captures the fields needed to insert a new message.
type CreateMessageInput struct {
	FromRoleID int64
	ToRoleID   int64
	Body       string
	Tldr       string
	InReplyTo  *int64
}

// Create inserts a new message row with delivery_mode=pending.
func (m *MessagesDAO) Create(ctx context.Context, in CreateMessageInput) (*Message, error) {
	if in.ToRoleID == 0 {
		return nil, fmt.Errorf("messages.Create: to_role_id is required")
	}
	if in.FromRoleID == 0 {
		return nil, fmt.Errorf("messages.Create: from_role_id is required")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := m.db.ExecContext(ctx,
		`INSERT INTO messages
		 (from_role_id, to_role_id, body, tldr, in_reply_to, sent_at, delivery_mode)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.FromRoleID, in.ToRoleID, in.Body, in.Tldr, nullable(in.InReplyTo), now, string(DeliveryPending),
	)
	if err != nil {
		return nil, fmt.Errorf("messages.Create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339Nano, now)
	return &Message{
		ID:           id,
		FromRoleID:   in.FromRoleID,
		ToRoleID:     in.ToRoleID,
		Body:         in.Body,
		Tldr:         in.Tldr,
		InReplyTo:    in.InReplyTo,
		SentAt:       t,
		DeliveryMode: DeliveryPending,
	}, nil
}

// SetDelivered updates a message row with its final delivery mode and
// timestamp.
func (m *MessagesDAO) SetDelivered(ctx context.Context, messageID int64, mode DeliveryMode) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := m.db.ExecContext(ctx,
		`UPDATE messages SET delivery_mode = ?, delivered_at = ? WHERE id = ?`,
		string(mode), now, messageID,
	)
	if err != nil {
		return fmt.Errorf("messages.SetDelivered: %w", err)
	}
	return nil
}

// UnreadForRole returns every unread message addressed to roleID ordered by
// sent_at ascending (oldest first, matching inbox semantics).
func (m *MessagesDAO) UnreadForRole(ctx context.Context, roleID int64) ([]*Message, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, from_role_id, to_role_id, body, tldr, in_reply_to,
		        sent_at, read_at, delivery_mode, delivered_at
		 FROM messages
		 WHERE to_role_id = ? AND read_at IS NULL
		 ORDER BY sent_at ASC`, roleID)
	if err != nil {
		return nil, fmt.Errorf("messages.UnreadForRole: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Message
	for rows.Next() {
		msg, err := scanMessageRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

// CountUnreadForRole returns the count of unread messages for a given role.
func (m *MessagesDAO) CountUnreadForRole(ctx context.Context, roleID int64) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages
		 WHERE to_role_id = ? AND read_at IS NULL`,
		roleID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("messages.CountUnreadForRole: %w", err)
	}
	return n, nil
}

// MarkRead marks the given message ids as read for roleID. Messages
// belonging to other roles are silently skipped (UPDATE matches 0 rows).
// Returns the count of rows actually updated.
func (m *MessagesDAO) MarkRead(ctx context.Context, roleID int64, messageIDs []int64) (int, error) {
	if len(messageIDs) == 0 {
		return 0, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := `UPDATE messages SET read_at = ?
	          WHERE read_at IS NULL AND to_role_id = ? AND id IN (`
	args := []any{now, roleID}
	for i, id := range messageIDs {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, id)
	}
	query += ")"

	res, err := m.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("messages.MarkRead: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetByID loads a single message by id.
func (m *MessagesDAO) GetByID(ctx context.Context, id int64) (*Message, error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT id, from_role_id, to_role_id, body, tldr, in_reply_to,
		        sent_at, read_at, delivery_mode, delivered_at
		 FROM messages WHERE id = ?`, id)
	msg, err := scanMessageRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return msg, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMessageRow(rs rowScanner) (*Message, error) {
	var msg Message
	var inReplyTo sql.NullInt64
	var readAt, deliveredAt sql.NullString
	var sentAt, deliveryMode string
	if err := rs.Scan(
		&msg.ID, &msg.FromRoleID, &msg.ToRoleID, &msg.Body, &msg.Tldr, &inReplyTo,
		&sentAt, &readAt, &deliveryMode, &deliveredAt,
	); err != nil {
		return nil, err
	}
	msg.DeliveryMode = DeliveryMode(deliveryMode)
	msg.SentAt, _ = time.Parse(time.RFC3339Nano, sentAt)
	if inReplyTo.Valid {
		v := inReplyTo.Int64
		msg.InReplyTo = &v
	}
	if readAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, readAt.String)
		msg.ReadAt = &t
	}
	if deliveredAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, deliveredAt.String)
		msg.DeliveredAt = &t
	}
	return &msg, nil
}

func nullable(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
