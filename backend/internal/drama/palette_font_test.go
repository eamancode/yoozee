package drama

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

// misalignedCollection 把一款单字体包成只有一款的 .ttc，每张表都放在奇数偏移上（与 wqy-zenhei.ttc 一样不按 4 字节对齐）。
func misalignedCollection(t *testing.T, ttf []byte) []byte {
	t.Helper()
	n := int(binary.BigEndian.Uint16(ttf[4:6]))
	const dirAt = 16
	dir := 12 + 16*n
	out := make([]byte, dirAt+dir)
	copy(out, "ttcf")
	binary.BigEndian.PutUint32(out[4:], 0x00010000)
	binary.BigEndian.PutUint32(out[8:], 1)
	binary.BigEndian.PutUint32(out[12:], dirAt)
	copy(out[dirAt:], ttf[:12])
	for i := 0; i < n; i++ {
		rec := ttf[12+16*i : 12+16*i+16]
		o, l := binary.BigEndian.Uint32(rec[8:12]), binary.BigEndian.Uint32(rec[12:16])
		if len(out)%4 != 1 {
			out = append(out, make([]byte, (5-len(out)%4)%4)...)
		}
		dst := out[dirAt+12+16*i:]
		copy(dst, rec[:8])
		binary.BigEndian.PutUint32(dst[8:12], uint32(len(out)))
		binary.BigEndian.PutUint32(dst[12:16], l)
		out = append(out, ttf[o:o+l]...)
	}
	return out
}

// M8 线上验收第 1 条：wqy-zenhei.ttc 的表没按 4 字节对齐，x/image 的 sfnt 报「invalid table offset」，色卡图只画了 HEX 和英文名。
// 字体集合逐款解析，表没对齐时抄成对齐的单字体再解析；没有中文字形的字体不当中文字体用。
func TestParseFontFileMisalignedCollection(t *testing.T) {
	raw := misalignedCollection(t, goregular.TTF)
	collection, err := opentype.ParseCollection(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collection.Font(0); err == nil || !strings.Contains(err.Error(), "invalid table offset") {
		t.Fatalf("构造的集合应复现 sfnt 的对齐报错：%v", err)
	}
	f, err := parseFontFile(raw, "Ag")
	if err != nil {
		t.Fatalf("表没对齐的字体集合应能读出：%v", err)
	}
	if !fontHasGlyphs(f, "Hello") {
		t.Fatal("读出的字体应带拉丁字形")
	}
	if _, err := parseFontFile(raw, cjkProbe); err == nil || !strings.Contains(err.Error(), "没有中文字形") {
		t.Fatalf("没有中文字形的字体不能当中文字体：%v", err)
	}
	if _, err := parseFontFile(goregular.TTF, "Ag"); err != nil {
		t.Fatalf("对齐的单字体照常读：%v", err)
	}
	if _, err := parseFontFile([]byte("ttcf\x00\x01\x00\x00\x00\x00\x00\x09"), cjkProbe); err == nil {
		t.Fatal("坏的集合头应报错")
	}
}

// 用真的中文字体（CANVAS_CJK_FONT 指定，否则按常见路径找；线上是 wqy-zenhei.ttc）合成主色卡：中文色名「暖杏」真的画上了
// （带中文字体和不带中文字体画出的图不一样）。机器上没有中文字体时跳过。
func TestRenderMasterPaletteChineseNames(t *testing.T) {
	candidates := cjkFontCandidates
	if custom := strings.TrimSpace(os.Getenv("CANVAS_CJK_FONT")); custom != "" {
		candidates = append([]string{custom}, candidates...)
	}
	var path string
	var raw []byte
	for _, c := range candidates {
		if b, err := os.ReadFile(c); err == nil {
			path, raw = c, b
			break
		}
	}
	if raw == nil {
		t.Skip("机器上没有中文字体")
	}
	cjk, err := parseFontFile(raw, cjkProbe)
	if err != nil {
		t.Fatalf("%s 读不出中文字体：%v", path, err)
	}
	base, err := loadPaletteFonts()
	if err != nil {
		t.Fatal(err)
	}
	with, without := *base, *base
	with.cjk, without.cjk = cjk, nil
	palette := MasterPalette{Groups: []MasterPaletteGroup{
		{Key: "protagonist", Name: "主角组", Colors: []PaletteColor{{NameZh: "暖杏", NameEn: "Warm Apricot", Hex: "#E8B98A"}, {NameZh: "旧藏青", NameEn: "Faded Navy", Hex: "#2B3447"}}},
		{Key: "environment", Name: "环境组", Colors: []PaletteColor{{NameZh: "雨夜乌黑", NameEn: "Rain Black", Hex: "#1A1D1F"}}},
	}}
	got, err := with.renderPalette("老街 · 主基调色卡", "MASTER PALETTE", "MASTER PALETTE", palette)
	if err != nil {
		t.Fatal(err)
	}
	latinOnly, err := without.renderPalette("老街 · 主基调色卡", "MASTER PALETTE", "MASTER PALETTE", palette)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, latinOnly) {
		t.Fatalf("%s：带中文字体画出的图应与只画英文的不同", path)
	}
	img, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	// 组名「主角组」画在左栏第一行（x 72～300，y 176～214）：有中文字体时这一块有深色字迹。
	if ink := darkPixels(img, image.Rect(72, 176, 300, 214)); ink < 50 {
		t.Fatalf("%s：组名区域只有 %d 个深色像素，中文没画上", path, ink)
	}
	if dir := os.Getenv("PALETTE_IMAGE_OUT"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "master-palette-cjk.png"), got, 0o644)
		_ = os.WriteFile(filepath.Join(dir, "master-palette-latin-only.png"), latinOnly, 0o644)
	}
}

func darkPixels(img image.Image, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if cr, cg, cb, _ := img.At(x, y).RGBA(); cr>>8 < 0x80 && cg>>8 < 0x80 && cb>>8 < 0x80 {
				n++
			}
		}
	}
	return n
}
