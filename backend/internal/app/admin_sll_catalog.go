package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/sllcatalog"
)

func (s *Service) adminSLLChannel(actor *model.User, channelID string) (*model.ModelChannel, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	channel, err := s.repo.AdminSystemChannel(channelID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.NotFound("找不到系统渠道")
	}
	if err != nil {
		return nil, err
	}
	if !sllcatalog.IsSLLChannel(*channel) {
		return nil, BadAuthRequest("该渠道未连接 SLL 公开目录对应的供应商")
	}
	return channel, nil
}

func (s *Service) AdminSLLCatalog(ctx context.Context, actor *model.User, channelID string) (sllcatalog.View, error) {
	if _, err := s.adminSLLChannel(actor, channelID); err != nil {
		return sllcatalog.View{}, err
	}
	return s.repo.SLLCatalog().View(ctx, channelID, time.Now().UTC())
}

func (s *Service) SyncAdminSLLCatalog(ctx context.Context, actor *model.User, channelID, group string) (sllcatalog.View, error) {
	channel, err := s.adminSLLChannel(actor, channelID)
	if err != nil {
		return sllcatalog.View{}, err
	}
	group = strings.TrimSpace(group)
	if group == "" {
		return sllcatalog.View{}, BadAuthRequest("请填写此渠道凭据实际使用的 SLL 调用分组")
	}
	startedAt := time.Now().UTC()
	catalog, err := sllcatalog.FetchPricing(ctx, s.sllHTTPClient(), group)
	if err != nil {
		return sllcatalog.View{}, kernel.WrapAppError(http.StatusBadGateway, "读取 SLL 公开价目失败，请核对调用分组及公开价目", err)
	}
	var view sllcatalog.View
	err = s.repo.WithContext(ctx).Transaction(func(tx *repository.Repository) error {
		result, err := tx.SLLCatalog().Import(ctx, channelID, group, catalog, startedAt, *channel)
		if err != nil {
			return err
		}
		view = result
		return tx.SyncChannelModelNames(channelID, time.Now())
	})
	if err != nil {
		return sllcatalog.View{}, err
	}
	s.invalidateRouteCatalog()
	return view, nil
}

func (s *Service) RefreshAdminSLLMetrics(ctx context.Context, actor *model.User, channelID string) error {
	if _, err := s.adminSLLChannel(actor, channelID); err != nil {
		return err
	}
	return s.refreshSLLMetrics(ctx, time.Now().UTC())
}

func (s *Service) sllHTTPClient() *http.Client {
	if s.sllPublicClient != nil {
		return s.sllPublicClient
	}
	return OutboundHTTPClient(25 * time.Second)
}

func (s *Service) refreshSLLMetrics(ctx context.Context, at time.Time) error {
	store := s.repo.SLLCatalog()
	enabled, err := store.HasImportedChannels(ctx)
	if err != nil || !enabled {
		return err
	}
	metrics, fetchErr := sllcatalog.FetchMetrics(ctx, s.sllHTTPClient())
	if err := store.RecordMetrics(ctx, at, metrics, fetchErr); err != nil {
		return err
	}
	if fetchErr != nil {
		return kernel.WrapAppError(http.StatusBadGateway, "采集 SLL 官方近 24 小时成功率失败", fetchErr)
	}
	return nil
}

func (s *Service) startSLLMetricsWorker() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if err := s.refreshSLLMetrics(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
				slog.Warn("SLL success rate refresh failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}
