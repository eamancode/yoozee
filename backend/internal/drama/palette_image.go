package drama

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// 色卡图（M8 卡 A 主基调色卡、卡 B 场次色卡）：不调图片模型，由代码按色值画一张 16:9 的 PNG——白底，顶部标题，每组一行：组名在左，右边一排色块，
// 每块下面写 HEX、中文名、英文名（与《色卡提示词卡》的版式一致：色块 + HEX + 色名）。拉丁字母用 Go 自带字体（gofont，BSD 许可，
// 随 golang.org/x/image 编进二进制）；中文用系统里的 CJK 字体（CANVAS_CJK_FONT 指定，否则按常见路径找：Linux 文泉驿 / Noto CJK、
// Windows 微软雅黑 / 黑体、macOS 苹方；.ttc 字体集合取带中文字形的那一款）。找不到中文字体时只画 HEX 和英文名，中文名不画
// （MasterPaletteFontNote 说明原因，风格锁定 / 场次色卡 / 重新合成的结果带着它给页面提示）。

const (
	MasterPaletteImageWidth  = 1920
	MasterPaletteImageHeight = 1080
)

// cjkFontCandidates 是找中文字体的常见路径（按顺序取第一个能读出的）。
var cjkFontCandidates = []string{
	"/usr/share/fonts/wenquanyi/wqy-zenhei/wqy-zenhei.ttc",
	"/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
	"/usr/share/fonts/wenquanyi/wqy-microhei/wqy-microhei.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
	"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
	`C:\Windows\Fonts\msyh.ttc`,
	`C:\Windows\Fonts\simhei.ttf`,
	`C:\Windows\Fonts\Deng.ttf`,
	`C:\Windows\Fonts\simsun.ttc`,
	"/System/Library/Fonts/PingFang.ttc",
	"/System/Library/Fonts/STHeiti Medium.ttc",
	"/System/Library/Fonts/Hiragino Sans GB.ttc",
}

type paletteFontSet struct {
	regular, bold, mono *opentype.Font
	cjk                 *opentype.Font
	cjkPath             string
	cjkErr              string
}

var (
	paletteFontsOnce sync.Once
	paletteFonts     paletteFontSet
	paletteFontsErr  error
)

func loadPaletteFonts() (*paletteFontSet, error) {
	paletteFontsOnce.Do(func() {
		var err error
		if paletteFonts.regular, err = opentype.Parse(goregular.TTF); err != nil {
			paletteFontsErr = err
			return
		}
		if paletteFonts.bold, err = opentype.Parse(gobold.TTF); err != nil {
			paletteFontsErr = err
			return
		}
		if paletteFonts.mono, err = opentype.Parse(gomono.TTF); err != nil {
			paletteFontsErr = err
			return
		}
		candidates := cjkFontCandidates
		if custom := strings.TrimSpace(os.Getenv("CANVAS_CJK_FONT")); custom != "" {
			candidates = append([]string{custom}, candidates...)
		}
		var problems []string
		for _, path := range candidates {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			f, err := parseFontFile(raw, cjkProbe)
			if err != nil {
				problems = append(problems, path+"："+err.Error())
				continue
			}
			paletteFonts.cjk, paletteFonts.cjkPath = f, path
			return
		}
		paletteFonts.cjkErr = "没有找到可用的中文字体（可用 CANVAS_CJK_FONT 指定 .ttf / .ttc 路径）"
		if len(problems) > 0 {
			paletteFonts.cjkErr += "；读不出：" + strings.Join(problems, "；")
		}
	})
	return &paletteFonts, paletteFontsErr
}

// cjkProbe 是挑中文字体的试字：一款字体这几个字都有字形才算能画中文色名。
const cjkProbe = "暖杏色中"

// parseFontFile 读单个字体或字体集合（.ttc / .otc）：逐款试，取 probe 里每个字都有字形的第一款。
// golang.org/x/image 的 sfnt 要求每张表从 4 字节对齐的位置开始，文泉驿正黑（Alpine font-wqy-zenhei 的 wqy-zenhei.ttc）三款
// 几乎每张表都没对齐，直接解析报「sfnt: invalid table offset」；读不出时把这一款的表抄成一份对齐的单字体再解析（只改表的位置，不改内容）。
func parseFontFile(raw []byte, probe string) (*opentype.Font, error) {
	offsets, err := sfntFontOffsets(raw)
	if err != nil {
		return nil, err
	}
	var firstErr error
	parsed := 0
	for _, offset := range offsets {
		f, err := parseSFNTAt(raw, offset)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		parsed++
		if fontHasGlyphs(f, probe) {
			return f, nil
		}
	}
	if parsed > 0 {
		return nil, fmt.Errorf("字体里没有中文字形（试了 %d 款）", parsed)
	}
	return nil, firstErr
}

// sfntFontOffsets 是文件里每一款字体的表目录位置：单字体是 0，字体集合按 TTC 头里的偏移。
func sfntFontOffsets(raw []byte) ([]uint32, error) {
	if len(raw) < 12 {
		return nil, fmt.Errorf("不是字体文件")
	}
	if string(raw[:4]) != "ttcf" {
		return []uint32{0}, nil
	}
	n := binary.BigEndian.Uint32(raw[8:12])
	if n == 0 || n > 64 || 12+4*int(n) > len(raw) {
		return nil, fmt.Errorf("字体集合头不对（%d 款）", n)
	}
	offsets := make([]uint32, n)
	for i := range offsets {
		offsets[i] = binary.BigEndian.Uint32(raw[12+4*i:])
	}
	return offsets, nil
}

// parseSFNTAt 解析 offset 处的一款字体：先原样解析，表没对齐时抄成对齐的单字体再解析。
func parseSFNTAt(raw []byte, offset uint32) (*opentype.Font, error) {
	if offset == 0 {
		if f, err := opentype.Parse(raw); err == nil {
			return f, nil
		}
	}
	aligned, err := alignedSFNT(raw, offset)
	if err != nil {
		return nil, err
	}
	return opentype.Parse(aligned)
}

// alignedSFNT 把 offset 处这一款字体的表目录和表按顺序抄进一份新的单字体，每张表从 4 字节对齐处开始（表的偏移在 TTC 里也是从文件头算）。
func alignedSFNT(raw []byte, offset uint32) ([]byte, error) {
	if uint64(offset)+12 > uint64(len(raw)) {
		return nil, fmt.Errorf("字体表目录越界")
	}
	head := raw[offset:]
	n := int(binary.BigEndian.Uint16(head[4:6]))
	dir := 12 + 16*n
	if len(head) < dir {
		return nil, fmt.Errorf("字体表目录越界")
	}
	size := dir
	for i := 0; i < n; i++ {
		rec := head[12+16*i:]
		o, l := binary.BigEndian.Uint32(rec[8:12]), binary.BigEndian.Uint32(rec[12:16])
		if uint64(o)+uint64(l) > uint64(len(raw)) {
			return nil, fmt.Errorf("字体表 %q 越界", rec[:4])
		}
		size += (int(l) + 3) &^ 3
	}
	out := make([]byte, size)
	copy(out, head[:12])
	pos := dir
	for i := 0; i < n; i++ {
		rec, dst := head[12+16*i:12+16*i+16], out[12+16*i:12+16*i+16]
		o, l := binary.BigEndian.Uint32(rec[8:12]), binary.BigEndian.Uint32(rec[12:16])
		copy(dst, rec[:8])
		binary.BigEndian.PutUint32(dst[8:12], uint32(pos))
		binary.BigEndian.PutUint32(dst[12:16], l)
		copy(out[pos:], raw[o:o+l])
		pos += (int(l) + 3) &^ 3
	}
	return out, nil
}

// fontHasGlyphs 是 probe 里每个字在字体里都有字形。
func fontHasGlyphs(f *opentype.Font, probe string) bool {
	var buf sfnt.Buffer
	for _, r := range probe {
		if i, err := f.GlyphIndex(&buf, r); err != nil || i == 0 {
			return false
		}
	}
	return true
}

// MasterPaletteFontNote 是合成图用的中文字体：找到时返回路径，找不到时返回原因（只画 HEX 和英文名）。
func MasterPaletteFontNote() (path string, problem string) {
	fonts, err := loadPaletteFonts()
	if err != nil {
		return "", err.Error()
	}
	return fonts.cjkPath, fonts.cjkErr
}

// paletteText 是一种字号、字重的文字：拉丁字符用 gofont，其余用中文字体（没有时跳过）。
type paletteText struct {
	latin font.Face
	cjk   font.Face
}

func (s *paletteFontSet) text(size float64, bold bool, mono bool) (paletteText, error) {
	base := s.regular
	if bold {
		base = s.bold
	}
	if mono {
		base = s.mono
	}
	latin, err := opentype.NewFace(base, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return paletteText{}, err
	}
	out := paletteText{latin: latin}
	if s.cjk != nil {
		if out.cjk, err = opentype.NewFace(s.cjk, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}); err != nil {
			return paletteText{}, err
		}
	}
	return out, nil
}

// face 是一个字符用哪种字体；返回 nil 表示画不了（没有中文字体）。
func (t paletteText) face(r rune) font.Face {
	if r < 0x2E80 && (r <= unicode.MaxLatin1 || unicode.Is(unicode.Latin, r) || unicode.IsPunct(r) || unicode.IsSymbol(r)) {
		if _, ok := t.latin.GlyphAdvance(r); ok {
			return t.latin
		}
	}
	if t.cjk != nil {
		return t.cjk
	}
	if _, ok := t.latin.GlyphAdvance(r); ok {
		return t.latin
	}
	return nil
}

func (t paletteText) width(s string) fixed.Int26_6 {
	var w fixed.Int26_6
	for _, r := range s {
		if f := t.face(r); f != nil {
			adv, _ := f.GlyphAdvance(r)
			w += adv
		}
	}
	return w
}

// fit 截到 maxWidth 像素以内，截了加「…」。
func (t paletteText) fit(s string, maxWidth int) string {
	limit := fixed.I(maxWidth)
	if t.width(s) <= limit {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := strings.TrimSpace(string(runes)) + "…"
		if t.width(candidate) <= limit {
			return candidate
		}
	}
	return ""
}

// draw 从 (x, baseline) 开始画一行字。
func (t paletteText) draw(dst draw.Image, x, baseline int, s string, c color.Color) {
	dot := fixed.P(x, baseline)
	for _, r := range s {
		f := t.face(r)
		if f == nil {
			continue
		}
		d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: f, Dot: dot}
		d.DrawString(string(r))
		dot = d.Dot
	}
}

func (t paletteText) hasCJK() bool { return t.cjk != nil }

var (
	paletteInk    = color.RGBA{0x1F, 0x1F, 0x1F, 0xFF}
	paletteMuted  = color.RGBA{0x7A, 0x7A, 0x7A, 0xFF}
	paletteRule   = color.RGBA{0xE2, 0xE2, 0xE2, 0xFF}
	paletteBorder = color.RGBA{0xD4, 0xD4, 0xD4, 0xFF}
)

// RenderMasterPalettePNG 画主基调色卡：title 是标题（风格名或项目名）。最多画 StyleLockMaxColors 个颜色；HEX 不合法的颜色跳过。
func RenderMasterPalettePNG(title string, p MasterPalette) ([]byte, error) {
	heading := strings.TrimSpace(strings.TrimSpace(title) + " · 主基调色卡")
	if strings.HasPrefix(heading, "·") {
		heading = "主基调色卡"
	}
	return renderPalettePNG(heading, "MASTER PALETTE", "MASTER PALETTE", p)
}

// RenderScenePalettePNG 画一场的场次色卡：标题「E01S02 · 内·电梯口·夜」，颜色按主色卡的分组一组一行（没写分组的归「其他」），
// 与主基调色卡同一版式。
func RenderScenePalettePNG(e ColorScriptEntry) ([]byte, error) {
	sub := "SCENE PALETTE"
	if e.InheritsFrom != "" {
		sub += " · FROM " + e.InheritsFrom
	}
	return renderPalettePNG(PaletteHeading(e), e.Code(), sub, ScenePaletteGroups(e))
}

// renderPalettePNG 画一张分组色卡：heading 是标题（没有中文字体时用 latinHeading），sub 是右上角的英文小标题（后面接色数）。
func renderPalettePNG(heading string, latinHeading string, sub string, p MasterPalette) ([]byte, error) {
	fonts, err := loadPaletteFonts()
	if err != nil {
		return nil, err
	}
	return fonts.renderPalette(heading, latinHeading, sub, p)
}

func (fonts *paletteFontSet) renderPalette(heading string, latinHeading string, sub string, p MasterPalette) ([]byte, error) {
	p = NormalizeMasterPalette(p)
	type swatch struct {
		c   color.RGBA
		col PaletteColor
	}
	type row struct {
		def      MasterPaletteGroupDef
		swatches []swatch
	}
	var rows []row
	total := 0
	for _, g := range p.Groups {
		r := row{def: MasterPaletteGroupDefOf(g)}
		for _, c := range g.Colors {
			rgb, ok := hexRGBA(c.Hex)
			if !ok || total >= StyleLockMaxColors {
				continue
			}
			r.swatches = append(r.swatches, swatch{c: rgb, col: c})
			total++
		}
		if len(r.swatches) > 0 {
			rows = append(rows, r)
		}
	}
	if total == 0 {
		return nil, fmt.Errorf("色卡没有能画的颜色")
	}
	titleFace, err := fonts.text(44, true, false)
	if err != nil {
		return nil, err
	}
	subFace, _ := fonts.text(22, false, true)
	groupFace, _ := fonts.text(30, true, false)
	groupEnFace, _ := fonts.text(17, false, true)
	hexFace, _ := fonts.text(22, true, true)
	nameFace, _ := fonts.text(21, false, false)
	enFace, _ := fonts.text(16, false, false)

	img := image.NewRGBA(image.Rect(0, 0, MasterPaletteImageWidth, MasterPaletteImageHeight))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	const margin = 72
	right := MasterPaletteImageWidth - margin

	if !titleFace.hasCJK() {
		heading = latinHeading
	}
	sub = fmt.Sprintf("%s · %d COLORS", sub, total)
	subW := subFace.width(sub).Ceil()
	titleFace.draw(img, margin, 112, titleFace.fit(heading, right-margin-subW-40), paletteInk)
	subFace.draw(img, right-subW, 112, sub, paletteMuted)
	draw.Draw(img, image.Rect(margin, 146, right, 148), image.NewUniform(paletteRule), image.Point{}, draw.Src)

	const top, bottom = 176, MasterPaletteImageHeight - 56
	rowH := min(280, (bottom-top)/len(rows))
	const labelW, gap = 236, 16
	swatchLeft := margin + labelW
	for i, r := range rows {
		y := top + i*rowH
		name := r.def.Name
		if !groupFace.hasCJK() {
			name = ""
		}
		groupFace.draw(img, margin, y+34, groupFace.fit(name, labelW-20), paletteInk)
		groupEnFace.draw(img, margin, y+62, groupEnFace.fit(r.def.En, labelW-20), paletteMuted)
		count := fmt.Sprintf("%d COLORS", len(r.swatches))
		if len(r.swatches) == 1 {
			count = "1 COLOR"
		}
		groupEnFace.draw(img, margin, y+86, count, paletteMuted)
		n := len(r.swatches)
		w := min(220, (right-swatchLeft-(n-1)*gap)/n)
		textBlock := 84
		h := max(48, rowH-textBlock-22)
		for j, s := range r.swatches {
			x := swatchLeft + j*(w+gap)
			box := image.Rect(x, y, x+w, y+h)
			draw.Draw(img, box, image.NewUniform(paletteBorder), image.Point{}, draw.Src)
			draw.Draw(img, box.Inset(1), image.NewUniform(s.c), image.Point{}, draw.Src)
			base := y + h
			hexFace.draw(img, x, base+28, hexFace.fit(s.col.Hex, w), paletteInk)
			if nameFace.hasCJK() && s.col.NameZh != "" {
				nameFace.draw(img, x, base+54, nameFace.fit(s.col.NameZh, w), paletteInk)
			}
			enFace.draw(img, x, base+76, enFace.fit(s.col.NameEn, w), paletteMuted)
		}
		if i < len(rows)-1 {
			lineY := y + rowH - 10
			draw.Draw(img, image.Rect(margin, lineY, right, lineY+1), image.NewUniform(paletteRule), image.Point{}, draw.Src)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// hexRGBA 读 #RRGGBB。
func hexRGBA(hex string) (color.RGBA, bool) {
	hex = NormalizeHexColor(hex)
	if !ValidHexColor(hex) || utf8.RuneCountInString(hex) != 7 {
		return color.RGBA{}, false
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "#%02X%02X%02X", &r, &g, &b); err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{r, g, b, 0xFF}, true
}
