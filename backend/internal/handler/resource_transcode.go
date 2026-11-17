package handler

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	_ "golang.org/x/image/webp"
	"yingce/backend/internal/assets"
	"yingce/backend/internal/service"

	// 资源可能以 PNG/WebP 存储；注册解码器供 image.Decode 使用。
	_ "image/png"
)

// Bound compressed input size and decoded dimensions independently.
const convertibleSourceSize = int64(32 * 1024 * 1024)
const convertibleSourcePixels = 16_000_000

// imageTranscodeEligible 判断原件是否值得转码：仅 PNG/WebP 位图、大小在限制内。
func imageTranscodeEligible(mimeType string, size int64, targetFormat string) bool {
	if size <= 0 || size > convertibleSourceSize {
		return false
	}
	if targetFormat != "jpeg" {
		// Go 标准库没有 WebP 编码器；webp 目标由前端在可读字节时自行转码。
		return false
	}
	return mimeType == "image/png" || mimeType == "image/webp"
}

// transcodeImageToJPEG 把 PNG/WebP 图片字节转码为 JPEG（透明像素铺白底）。
// 部分模型/渠道会忽略 output_format 参数仍返回 PNG，下载侧需要平台兜底兑现用户的选择。
func transcodeImageToJPEG(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, convertibleSourceSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > convertibleSourceSize {
		return nil, errors.New("图片文件超过转码大小限制")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if (format != "png" && format != "webp") || config.Width <= 0 || config.Height <= 0 || config.Width > convertibleSourcePixels/config.Height {
		return nil, errors.New("图片尺寸或格式不支持转码")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	// jpeg 没有透明通道，先铺白底再叠加原图，避免透明像素变成黑底。
	canvas := image.NewRGBA(bounds)
	draw.Draw(canvas, bounds, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(canvas, bounds, source, bounds.Min, draw.Over)
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, canvas, &jpeg.Options{Quality: 92}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func tryServeTranscodedDelivery(c *gin.Context, svc *service.Service, delivery *service.ResourceDelivery) bool {
	format := strings.ToLower(strings.TrimSpace(c.Query("format")))
	if delivery == nil || delivery.Resource == nil || delivery.Access == nil || delivery.Access.RequestedVariant != assets.VariantOriginal || delivery.Access.ActualVariant != assets.VariantOriginal {
		return false
	}
	resource := delivery.Resource
	if !imageTranscodeEligible(resource.MimeType, resource.Size, format) {
		return false
	}
	// PrepareResourceDelivery authorized this exact resource and supplied the storage owner.
	// Keep the original delivery stream untouched in case the decoder falls back.
	stream, err := svc.OpenResourceRange(resource.UserID, resource.ID, "")
	if err != nil {
		return false
	}
	defer stream.Body.Close()
	converted, err := transcodeImageToJPEG(stream.Body)
	if err != nil {
		return false
	}
	if delivery.Stream != nil {
		delivery.Stream.Body.Close()
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "image/jpeg", converted)
	return true
}
