package drama

import (
	"image"
	"image/color"
	"math"
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
