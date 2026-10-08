package app

// 声明式协议的图床前置步骤。
//
// 有些聚合网关只接受自己域名下的参考素材地址（自带免费图床），把应用签发的公网地址
// 或内联 base64 交给它们都会被拒。协议可以声明 mediaUpload，宿主就在创建任务前把内联
// 媒体上传到供应商图床，用返回的地址替换内联数据。
//
// 上传与创建走同一条宿主出站通道：最终 URL 校验、凭证注入、SSRF、超时、响应大小限制
// 和审计仍由 executeProtocolRequest 统一执行，插件不能借图床绕过这些边界。

import (
	"context"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/protocol"
)

// prepareDeclarativeProtocolMedia 把请求里内联的媒体替换成供应商图床地址。
// 已经是 URL 的媒体不动；协议没有声明图床能力时原样返回。
func prepareDeclarativeProtocolMedia(ctx context.Context, input canvasGenerationInput, request *protocol.GenerationRequest, adapter protocol.Adapter) error {
	uploader, ok := adapter.(protocol.MediaUploader)
	if !ok {
		return nil
	}
	plan, ok := uploader.MediaUploadPlan()
	if !ok {
		return nil
	}
	kinds := make(map[string]struct{}, len(plan.Kinds))
	for _, kind := range plan.Kinds {
		if trimmed := strings.TrimSpace(kind); trimmed != "" {
			kinds[trimmed] = struct{}{}
		}
	}
	if len(kinds) == 0 {
		kinds["image"] = struct{}{}
	}
	for _, group := range []struct {
		kind  string
		items []protocol.MediaReference
	}{
		{kind: "image", items: request.Images},
		{kind: "video", items: request.Videos},
		{kind: "audio", items: request.Audios},
	} {
		if _, wanted := kinds[group.kind]; !wanted {
			continue
		}
		for index := range group.items {
			media := &group.items[index]
			if strings.TrimSpace(media.URL) != "" || strings.TrimSpace(media.DataURL) == "" {
				continue
			}
			spec, err := uploader.BuildMediaUpload(protocol.RequestContext{BaseURL: input.Config.BaseURL, Request: *request}, *media)
			if err != nil {
				return err
			}
			body, err := executeProtocolRequest(withProviderRequestKind(ctx, "upload"), input.Config, spec)
			if err != nil {
				return fmt.Errorf("上传参考素材到供应商图床失败：%w", err)
			}
			uploaded, err := protocol.MediaUploadURL(body, plan.URLPath)
			if err != nil {
				return err
			}
			media.URL = uploaded
			media.DataURL = ""
		}
	}
	return nil
}
