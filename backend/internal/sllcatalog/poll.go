package sllcatalog

import (
	"context"

	"yingce/backend/internal/model"
)

// HasReceiptRoute includes disabled/retired SKUs. It is exclusively for
// polling an already accepted upstream job, never for admission or create.
func (s *Store) HasReceiptRoute(ctx context.Context, channelID, modelKey, upstream string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.SLLCatalogLine{}).
		Joins("JOIN channel_models ON channel_models.id = sll_catalog_lines.channel_model_id").
		Where("sll_catalog_lines.channel_id = ? AND sll_catalog_lines.model_id = ? AND channel_models.model_key = ?", channelID, upstream, modelKey).
		Count(&count).Error
	return count > 0, err
}
