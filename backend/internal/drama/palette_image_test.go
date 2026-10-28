package drama

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// 合成图：16:9，白底；每个色块中心取到的就是它的 HEX（代码画的，不会走样）。
func TestRenderMasterPalettePNG(t *testing.T) {
	out, _ := StyleLockFromText(styleLockOutput)
	raw, err := RenderMasterPalettePNG("都市冷调悬疑", out.Palette)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != MasterPaletteImageWidth || b.Dy() != MasterPaletteImageHeight {
		t.Fatalf("尺寸：%v", b)
	}
	if r, g, b, _ := img.At(10, 10).RGBA(); r>>8 != 0xFF || g>>8 != 0xFF || b>>8 != 0xFF {
		t.Fatal("背景应是白色")
	}
	// 第一行第一块是主角组的护士蓝 #5B7FA3：色块从 x=72+236、y=176 开始。
	if got := hexAt(img, 72+236+40, 176+40); got != "#5B7FA3" {
		t.Fatalf("第一块应是 #5B7FA3，取到 %s", got)
	}
	if dir := os.Getenv("PALETTE_IMAGE_OUT"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "master-palette.png"), raw, 0o644)
	}
	if path, problem := MasterPaletteFontNote(); path == "" {
		t.Logf("没有中文字体，只画了 HEX 和英文名：%s", problem)
	}
	if _, err := RenderMasterPalettePNG("x", MasterPalette{}); err == nil {
		t.Fatal("没有颜色时应报错")
	}
}

func hexAt(img image.Image, x, y int) string {
	r, g, b, _ := img.At(x, y).RGBA()
	const digits = "0123456789ABCDEF"
	out := []byte{'#'}
	for _, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		out = append(out, digits[v>>4], digits[v&15])
	}
	return string(out)
}
