package repository

import (
	"errors"
	"time"

	"yingce/backend/internal/model"
)

// Earlier chain retries shared a task ID. Only a creation or status receipt
// belonging to this generation may supply its missing provider job ID.
func (r *Repository) VideoChainProviderRequestID(userID, taskID, billingOrderID string, since time.Time) (string, error) {
	query := r.db.Model(&model.ApiCallLog{}).Where("user_id = ? AND task_id = ? AND capability = ? AND request_kind IN ? AND provider_request_id <> ''",
		userID, taskID, "video", []string{"create", "poll"})
	if billingOrderID != "" {
		query = query.Where("billing_order_id = ?", billingOrderID)
	}
	if !since.IsZero() {
		query = query.Where("(created_at_epoch >= ? OR COALESCE(created_at_epoch, 0) = 0)", since.Unix())
	}
	rows, err := query.Select("provider_request_id", "created_at").Rows()
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var originalID string
	for rows.Next() {
		var id string
		var loggedAt time.Time
		if err := rows.Scan(&id, &loggedAt); err != nil {
			return "", err
		}
		// SQLite timestamps can have mixed UTC offsets. Epoch bounds narrow
		// the scan and the timestamp comparison preserves subsecond precision.
		if loggedAt.Before(since) {
			continue
		}
		if originalID != "" && originalID != id {
			return "", errors.New("同一次视频尝试存在不同的上游受理号，请核对原回执")
		}
		originalID = id
	}
	return originalID, rows.Err()
}
