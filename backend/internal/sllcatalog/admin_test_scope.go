package sllcatalog

import (
	"context"

	"yingce/backend/internal/model"
)

// An admin may preview unsaved models. Once a channel has imported its group,
// testing a Seedance SKU on that channel must use its verified, saved limits.
func (s *Store) ManagedTestTarget(ctx context.Context, channelID, modelKey, upstream string) (bool, error) {
	if seedance25.MatchString(modelKey) || seedance25.MatchString(upstream) {
		var count int64
		err := s.db.WithContext(ctx).Model(&model.SLLCatalogImport{}).Where("channel_id = ?", channelID).Count(&count).Error
		return count > 0, err
	}
	return s.HasReceiptRoute(ctx, channelID, modelKey, upstream)
}
