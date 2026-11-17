package repository

import (
	"database/sql"
	"errors"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// Resolve retired video inventory by its durable owner and ID. Old tasks
// without a model ID can use a key only if its history identifies one row.
func (r *Repository) VideoReceiptModel(channelID, modelID, key string) (*model.ChannelModel, error) {
	q := r.db.Unscoped().Where("channel_id = ? AND model_key = ? AND capability = ?", channelID, key, "video")
	if modelID != "" {
		q = q.Where("id = ?", modelID)
	}
	var items []model.ChannelModel
	if err := q.Limit(2).Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, errors.New("原视频渠道模型不存在或历史标识不唯一")
	}
	return &items[0], nil
}

func (r *Repository) VideoReceiptPriceTier(modelID, tierID string) (*model.ChannelModelPriceTier, error) {
	var tier model.ChannelModelPriceTier
	err := r.db.Unscoped().Where("channel_model_id = ? AND id = ?", modelID, tierID).First(&tier).Error
	return &tier, err
}

func (r *Repository) CheckpointOriginalVideoReceipt(task *model.Task, originalID string) error {
	update := taskLeaseWriter(r.db.Model(&model.Task{}), task.LeaseOwner).
		Where("id = ? AND user_id = ? AND route_run = ? AND status = ?", task.ID, task.UserID, task.RouteRun, model.TaskStatusRunning).
		Where("COALESCE(provider_request_id, '') IN ?", []string{"", originalID}).
		UpdateColumn("provider_request_id", originalID)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return ErrTaskStateConflict
	}
	return nil
}

// Earlier chain retries shared a task ID. Only a creation or status receipt
// belonging to this generation may supply its missing provider job ID.
func (r *Repository) VideoChainProviderRequestID(userID, taskID, billingOrderID string, since time.Time, channelID string) (string, error) {
	query := r.originalVideoReceiptLogs(userID, taskID, billingOrderID, channelID).
		Where("request_kind IN ? AND provider_request_id <> ''", []string{"create", "poll"})
	values, err := videoReceiptLogValues(query, "provider_request_id", since)
	if err != nil || len(values) == 0 {
		return "", err
	}
	for _, id := range values[1:] {
		if values[0] != id {
			return "", errors.New("同一次视频尝试存在不同的上游受理号，请核对原回执")
		}
	}
	return values[0], nil
}

func (r *Repository) VideoReceiptCreateURLs(userID, taskID, billingOrderID, channelID, jobID string, since time.Time) ([]string, error) {
	query := r.originalVideoReceiptLogs(userID, taskID, billingOrderID, channelID).
		Where("source = ? AND request_kind = ? AND provider_request_id = ? AND upstream_url <> '' AND status = ?",
			"backend-task", "create", jobID, model.ApiCallStatusSucceeded)
	return videoReceiptLogValues(query, "upstream_url", since)
}

func (r *Repository) originalVideoReceiptLogs(userID, taskID, billingOrderID, channelID string) *gorm.DB {
	return r.db.Model(&model.ApiCallLog{}).Where("user_id = ? AND task_id = ? AND capability = ? AND COALESCE(billing_order_id, '') = ? AND COALESCE(channel_id, '') = ?",
		userID, taskID, "video", billingOrderID, channelID)
}

func videoReceiptLogValues(query *gorm.DB, field string, since time.Time) ([]string, error) {
	if !since.IsZero() {
		query = query.Where("(created_at_epoch >= ? OR COALESCE(created_at_epoch, 0) = 0)", since.Unix())
	}
	rows, err := query.Select(field, "created_at", "started_at").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		var loggedAt time.Time
		var startedAt sql.NullTime
		if err := rows.Scan(&value, &loggedAt, &startedAt); err != nil {
			return nil, err
		}
		// SQLite timestamps can have mixed UTC offsets. Epoch bounds narrow
		// the scan and the timestamp comparison preserves subsecond precision.
		if loggedAt.Before(since) || startedAt.Valid && !startedAt.Time.IsZero() && startedAt.Time.Before(since) {
			continue
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
