package drama

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
)

// Sharma、Wu、Dalal（2005）给出的 CIEDE2000 测试数据（节选，含色相跨 0° 和低彩度的情形）。
func TestCIEDE2000MatchesSharmaData(t *testing.T) {
	cases := []struct {
		a, b Lab
		want float64
	}{
		{Lab{50, 2.6772, -79.7751}, Lab{50, 0, -82.7485}, 2.0425},
		{Lab{50, 3.1571, -77.2803}, Lab{50, 0, -82.7485}, 2.8615},
		{Lab{50, 0, 0}, Lab{50, -1, 2}, 2.3669},
		{Lab{50, 2.5, 0}, Lab{61, -5, 29}, 22.8977},
		{Lab{50, 2.49, -0.001}, Lab{50, -2.49, 0.0011}, 7.2195},
		{Lab{60.2574, -34.0099, 36.2677}, Lab{60.4626, -34.1751, 39.4387}, 1.2644},
		{Lab{2.0776, 0.0795, -1.135}, Lab{0.9033, -0.0636, -0.5514}, 0.9082},
	}
	for _, c := range cases {
		got := CIEDE2000(c.a, c.b)
		if math.Abs(got-c.want) > 1e-4 {
			t.Errorf("ΔE00(%v, %v) = %.4f，应为 %.4f", c.a, c.b, got, c.want)
		}
		if back := CIEDE2000(c.b, c.a); math.Abs(back-got) > 1e-9 {
			t.Errorf("ΔE00 应对称：%.6f / %.6f", got, back)
		}
	}
	if d := CIEDE2000(Lab{40, 10, -20}, Lab{40, 10, -20}); d != 0 {
		t.Fatalf("同色 ΔE 应为 0，得到 %v", d)
	}
}

func TestRGBToLab(t *testing.T) {
	white := rgbToLab([3]uint8{255, 255, 255})
	if math.Abs(white.L-100) > 0.01 || math.Abs(white.A) > 0.01 || math.Abs(white.B) > 0.01 {
		t.Fatalf("白色应是 L=100、a=b=0：%+v", white)
	}
	red := rgbToLab([3]uint8{255, 0, 0})
	if math.Abs(red.L-53.24) > 0.05 || math.Abs(red.A-80.09) > 0.1 || math.Abs(red.B-67.20) > 0.1 {
		t.Fatalf("纯红的 Lab 不对：%+v", red)
	}
}

// paletteCard 按 CheckPaletteImage 的取色位置画一张色卡：白底、中部 80% 等分成 n 块。
func paletteCard(w, h int, hexes []string) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	left, span := float64(w)*0.1, float64(w)*0.8
	block := span / float64(len(hexes))
	for i, hex := range hexes {
		c, _ := parseHexColor(hex)
		x0, x1 := int(left+float64(i)*block+block*0.06), int(left+float64(i+1)*block-block*0.06)
		y0, y1 := h/2-int(block/2), h/2+int(block/2)
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.Set(x, y, color.RGBA{c[0], c[1], c[2], 255})
			}
		}
	}
	return img
}

func TestCheckPaletteImage(t *testing.T) {
	hexes := []string{"#1A1518", "#6B1F22", "#D9B89C", "#5C544A", "#8A95A0"}
	good := CheckPaletteImage(paletteCard(1600, 900, hexes), hexes)
	if !good.Passed || len(good.Blocks) != len(hexes) || good.Threshold != PaletteCheckMaxDeltaE {
		t.Fatalf("按 HEX 画的色卡应通过：%+v", good)
	}
	for _, b := range good.Blocks {
		if b.DeltaE > 0.5 || b.Sampled != b.Hex {
			t.Fatalf("按 HEX 画的色块取色应一致：%+v", b)
		}
	}
	drawn := append([]string{}, hexes...)
	drawn[2] = "#2E7D32"
	bad := CheckPaletteImage(paletteCard(1600, 900, drawn), hexes)
	if bad.Passed {
		t.Fatalf("画错一块应不通过：%+v", bad)
	}
	for i, b := range bad.Blocks {
		if (i == 2) != (b.DeltaE > PaletteCheckMaxDeltaE) {
			t.Fatalf("只有第 3 块该超阈值：%+v", bad.Blocks)
		}
	}
	if empty := CheckPaletteImage(paletteCard(1600, 900, hexes), nil); empty.Passed || empty.Note == "" {
		t.Fatalf("没有颜色时不能算通过：%+v", empty)
	}
}

// seamPaletteCard 仿线上色卡版式：近白底、上方一行标题、色块横跨 5.75%–94.35% 宽、块间白缝、块下方 HEX 标注；按 PNG 编码再读回。
func seamPaletteCard(t *testing.T, w, h int, hexes []string) image.Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.Set(x, y, c)
			}
		}
	}
	fill(0, 0, w, h, color.RGBA{252, 252, 252, 255})
	fill(w*32/100, h*11/100, w*68/100, h*13/100, color.RGBA{40, 40, 40, 255})
	left, right := float64(w)*0.0575, float64(w)*0.9435
	pitch := (right - left) / float64(len(hexes))
	for i, hex := range hexes {
		c, _ := parseHexColor(hex)
		x0, x1 := int(left+float64(i)*pitch), int(left+float64(i+1)*pitch-pitch*0.18)
		fill(x0, h*34/100, x1, h*62/100, color.RGBA{c[0], c[1], c[2], 255})
		fill(x0, h*64/100, x0+(x1-x0)*3/4, h*66/100, color.RGBA{30, 30, 30, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

// 13 色带白缝：按中部 80% 等分会落进白缝（线上 11 色卡误报 ΔE 31/48），按色块定位后每块都应取准。
func TestCheckPaletteImageLocatesSeamBlocks(t *testing.T) {
	hexes := []string{"#DCE6EA", "#6E7276", "#C4BFAF", "#9FB8C6", "#23262A", "#C79A4A", "#15171A", "#2C2E31", "#9AA3A9", "#8E3A2E", "#A7AEB2", "#5C544A", "#E4D2B0"}
	img := seamPaletteCard(t, 2816, 1584, hexes)
	check := CheckPaletteImage(img, hexes)
	if !check.Passed || check.Note != "" || len(check.Blocks) != len(hexes) {
		t.Fatalf("13 色带白缝的色卡应按色块取色并通过：%+v", check)
	}
	for _, b := range check.Blocks {
		if b.DeltaE >= 3 {
			t.Fatalf("每块 ΔE 应 < 3：%+v", check.Blocks)
		}
	}
	drawn := append([]string{}, hexes...)
	drawn[9] = "#2E7D32"
	bad := CheckPaletteImage(seamPaletteCard(t, 2816, 1584, drawn), hexes)
	for i, b := range bad.Blocks {
		if (i == 9) != (b.DeltaE > PaletteCheckMaxDeltaE) {
			t.Fatalf("只有第 10 块该超阈值：%+v", bad.Blocks)
		}
	}
}

// 色块之间没有缝时块数对不上：退回把整条色带等分，note 写明，仍能取准。
func TestCheckPaletteImageFallsBackWhenBlocksTouch(t *testing.T) {
	hexes := []string{"#1A1518", "#6B1F22", "#D9B89C", "#5C544A", "#8A95A0"}
	w, h := 1600, 900
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	left, pitch := 200, 240
	for i, hex := range hexes {
		c, _ := parseHexColor(hex)
		for y := 300; y < 600; y++ {
			for x := left + i*pitch; x < left+(i+1)*pitch; x++ {
				img.Set(x, y, color.RGBA{c[0], c[1], c[2], 255})
			}
		}
	}
	check := CheckPaletteImage(img, hexes)
	if !check.Passed || !strings.Contains(check.Note, "等分") {
		t.Fatalf("色块相连时应按色带等分取色并写 note：%+v", check)
	}
	for _, b := range check.Blocks {
		if b.DeltaE > 0.5 {
			t.Fatalf("按色带等分仍应取准：%+v", check.Blocks)
		}
	}
}
