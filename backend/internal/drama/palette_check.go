package drama

import (
	"fmt"
	"image"
	"math"
	"strconv"
)

// 色卡取色校验（M7.2 第 4 阶段）：色卡出图后先按画面实际版式找色块——找出离背景色最远的一条横带，再沿这条带横向扫出
// 与背景差异大的连续区间，每个区间算一块、取中心一小片的平均色；找到的块数与色彩脚本不符时（色块之间没有缝、某块与背景太近）
// 退回把色带（找不到就用画面中部 80%）按色块数等分，并在 note 里写明。取到的颜色与色彩脚本的 HEX 算 CIEDE2000 色差。
// 只记警告，不拒绝图片：图像模型对 HEX 的还原本来就有偏差，人看了再决定要不要重出。

// PaletteCheckMaxDeltaE 是一块色差的警告阈值（ΔE2000）。
const PaletteCheckMaxDeltaE = 12.0

// paletteForegroundDistance 是像素算「色块」而不是背景的 sRGB 欧氏距离下限（JPEG 噪声在 10 以内，浅灰色块对白底约 30）。
const paletteForegroundDistance = 24.0

// PaletteCheckBlock 是一块色块的取色结果。
type PaletteCheckBlock struct {
	Hex     string  `json:"hex"`
	Sampled string  `json:"sampled"`
	DeltaE  float64 `json:"deltaE"`
}

// PaletteCheck 是一张色卡的取色校验（写进版本主图的 metadata.paletteCheck）。
type PaletteCheck struct {
	Blocks    []PaletteCheckBlock `json:"blocks"`
	Passed    bool                `json:"passed"`
	Threshold float64             `json:"threshold"`
	// Note 在 blocks 为空时是没法校验的原因（图片读不出、没有颜色）；blocks 不空时是取色方式的说明（没按色块定位、退回等分）。
	Note string `json:"note,omitempty"`
}

// CheckPaletteImage 按色块位置取色并与 hexes（色卡从左到右的颜色）比对；任一块 ΔE 超过阈值即 passed=false。
func CheckPaletteImage(img image.Image, hexes []string) PaletteCheck {
	check := PaletteCheck{Blocks: []PaletteCheckBlock{}, Threshold: PaletteCheckMaxDeltaE}
	if img == nil || len(hexes) == 0 {
		check.Note = "没有可比对的颜色"
		return check
	}
	b := img.Bounds()
	width, height := float64(b.Dx()), float64(b.Dy())
	if width < float64(len(hexes)) || height < 1 {
		check.Note = "图片太小，无法取色"
		return check
	}
	points, note := locatePaletteBlocks(img, len(hexes))
	check.Note = note
	check.Passed = true
	for i, hex := range hexes {
		p := points[i]
		r, g, bl := averageColor(img, p.x, p.y, p.radius)
		sampled := fmt.Sprintf("#%02X%02X%02X", r, g, bl)
		block := PaletteCheckBlock{Hex: hex, Sampled: sampled, DeltaE: -1}
		if want, ok := parseHexColor(hex); ok {
			block.DeltaE = math.Round(CIEDE2000(rgbToLab(want), rgbToLab([3]uint8{r, g, bl}))*100) / 100
			if block.DeltaE > PaletteCheckMaxDeltaE {
				check.Passed = false
			}
		} else {
			check.Passed = false
		}
		check.Blocks = append(check.Blocks, block)
	}
	return check
}

// paletteSamplePoint 是一块的取色中心和取色方块半径。
type paletteSamplePoint struct{ x, y, radius int }

// paletteSpan 是一段连续区间 [start, end)。
type paletteSpan struct{ start, end int }

// locatePaletteBlocks 找 n 块色块的取色点；块数对不上时退回等分，note 写明原因（块数对上时 note 为空）。
func locatePaletteBlocks(img image.Image, n int) ([]paletteSamplePoint, string) {
	b := img.Bounds()
	bg := paletteBackground(img)
	isForeground := func(x, y int) bool {
		r, g, bl, _ := img.At(x, y).RGBA()
		dr, dg, db := float64(r>>8)-bg[0], float64(g>>8)-bg[1], float64(bl>>8)-bg[2]
		return math.Sqrt(dr*dr+dg*dg+db*db) > paletteForegroundDistance
	}
	// 1. 每行前景占比最高、且连续最长的一段行是色带（标题、HEX 标注的前景占比都低得多）。
	rowStep := max(1, b.Dy()/400)
	colStep := max(1, b.Dx()/800)
	type row struct {
		y    int
		frac float64
	}
	rows := make([]row, 0, b.Dy()/rowStep+1)
	best := 0.0
	for y := b.Min.Y; y < b.Max.Y; y += rowStep {
		hit, total := 0, 0
		for x := b.Min.X; x < b.Max.X; x += colStep {
			total++
			if isForeground(x, y) {
				hit++
			}
		}
		frac := float64(hit) / float64(total)
		rows = append(rows, row{y, frac})
		best = math.Max(best, frac)
	}
	var band paletteSpan
	if best > 0 {
		var cur paletteSpan
		in := false
		for i, r := range rows {
			if r.frac >= best*0.6 {
				if !in {
					cur, in = paletteSpan{start: r.y}, true
				}
				cur.end = r.y + rowStep
			}
			if in && (r.frac < best*0.6 || i == len(rows)-1) {
				if cur.end-cur.start > band.end-band.start {
					band = cur
				}
				in = false
			}
		}
	}
	if band.end-band.start < 2 {
		return equalPalettePoints(b, n, paletteSpan{b.Min.X + b.Dx()/10, b.Max.X - b.Dx()/10}, b.Min.Y+b.Dy()/2, b.Dy()), "画面里找不到色带，按画面中部等分取色"
	}
	// 2. 在色带中间一半的行里，前景占多数的列算色块，连续的列成一块；窄于 2‰ 宽的缝（压缩噪声）并进来，过窄的区间丢掉。
	bandH := band.end - band.start
	y0, y1 := band.start+bandH/4, band.end-bandH/4
	ySamples := []int{}
	for y := y0; y < max(y1, y0+1); y += max(1, (y1-y0)/8) {
		ySamples = append(ySamples, y)
	}
	var spans []paletteSpan
	gapTolerance := max(2, b.Dx()/500)
	inSpan, gap := false, 0
	for x := b.Min.X; x < b.Max.X; x++ {
		hit := 0
		for _, y := range ySamples {
			if isForeground(x, y) {
				hit++
			}
		}
		if hit*2 > len(ySamples) {
			if inSpan {
				spans[len(spans)-1].end = x + 1
			} else {
				spans = append(spans, paletteSpan{x, x + 1})
			}
			inSpan, gap = true, 0
			continue
		}
		if inSpan {
			gap++
			if gap > gapTolerance {
				inSpan = false
			}
		}
	}
	minWidth := max(2, b.Dx()/(n*8))
	kept := spans[:0]
	for _, s := range spans {
		if s.end-s.start >= minWidth {
			kept = append(kept, s)
		}
	}
	cy := band.start + bandH/2
	if len(kept) == n {
		points := make([]paletteSamplePoint, n)
		for i, s := range kept {
			points[i] = paletteSamplePoint{x: (s.start + s.end) / 2, y: cy, radius: max(1, int(float64(min(s.end-s.start, bandH))*0.15))}
		}
		return points, ""
	}
	if len(kept) == 0 {
		return equalPalettePoints(b, n, paletteSpan{b.Min.X + b.Dx()/10, b.Max.X - b.Dx()/10}, cy, bandH), fmt.Sprintf("色带上找不到分开的色块（色彩脚本 %d 块），按画面中部等分取色", n)
	}
	strip := paletteSpan{kept[0].start, kept[len(kept)-1].end}
	return equalPalettePoints(b, n, strip, cy, bandH), fmt.Sprintf("色带上找到 %d 块、色彩脚本是 %d 块，按色带整体等分取色", len(kept), n)
}

// equalPalettePoints 把 [strip.start, strip.end) 等分成 n 块，取每块中心。
func equalPalettePoints(b image.Rectangle, n int, strip paletteSpan, cy int, bandH int) []paletteSamplePoint {
	span := float64(strip.end - strip.start)
	radius := max(1, int(math.Min(span/float64(n), float64(min(bandH, b.Dy())))*0.08))
	points := make([]paletteSamplePoint, n)
	for i := range points {
		points[i] = paletteSamplePoint{x: strip.start + int((float64(i)+0.5)*span/float64(n)), y: cy, radius: radius}
	}
	return points
}

// paletteBackground 是画面四条边一圈像素的平均色（色卡底色）。
func paletteBackground(img image.Image) [3]float64 {
	b := img.Bounds()
	var sum [3]float64
	count := 0.0
	add := func(x, y int) {
		r, g, bl, _ := img.At(x, y).RGBA()
		sum[0], sum[1], sum[2] = sum[0]+float64(r>>8), sum[1]+float64(g>>8), sum[2]+float64(bl>>8)
		count++
	}
	step := max(1, b.Dx()/200)
	for x := b.Min.X; x < b.Max.X; x += step {
		add(x, b.Min.Y)
		add(x, b.Max.Y-1)
	}
	step = max(1, b.Dy()/200)
	for y := b.Min.Y; y < b.Max.Y; y += step {
		add(b.Min.X, y)
		add(b.Max.X-1, y)
	}
	return [3]float64{sum[0] / count, sum[1] / count, sum[2] / count}
}

// averageColor 是 (cx, cy) 周围 radius 像素方块的平均 sRGB（越界的点跳过）。
func averageColor(img image.Image, cx, cy, radius int) (uint8, uint8, uint8) {
	b := img.Bounds()
	var sr, sg, sb, count uint64
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			if !(image.Point{X: x, Y: y}).In(b) {
				continue
			}
			r, g, bl, _ := img.At(x, y).RGBA()
			sr, sg, sb = sr+uint64(r>>8), sg+uint64(g>>8), sb+uint64(bl>>8)
			count++
		}
	}
	if count == 0 {
		return 0, 0, 0
	}
	return uint8((sr + count/2) / count), uint8((sg + count/2) / count), uint8((sb + count/2) / count)
}

// parseHexColor 读 #RRGGBB。
func parseHexColor(hex string) ([3]uint8, bool) {
	hex = NormalizeHexColor(hex)
	if !ValidHexColor(hex) {
		return [3]uint8{}, false
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return [3]uint8{}, false
	}
	return [3]uint8{uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
}

// Lab 是 CIE L*a*b*（D65 白点）。
type Lab struct{ L, A, B float64 }

// rgbToLab 把 sRGB 换成 CIE L*a*b*（D65）。
func rgbToLab(c [3]uint8) Lab {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	r, g, b := linear(c[0]), linear(c[1]), linear(c[2])
	x := (0.4124564*r + 0.3575761*g + 0.1804375*b) / 0.95047
	y := (0.2126729*r + 0.7151522*g + 0.0721750*b) / 1.0
	z := (0.0193339*r + 0.1191920*g + 0.9503041*b) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return Lab{L: 116*fy - 16, A: 500 * (fx - fy), B: 200 * (fy - fz)}
}

// CIEDE2000 是两个 Lab 颜色的 ΔE00（Sharma、Wu、Dalal 2005 的公式，kL = kC = kH = 1）。
func CIEDE2000(c1, c2 Lab) float64 {
	const deg = math.Pi / 180
	cab := (math.Hypot(c1.A, c1.B) + math.Hypot(c2.A, c2.B)) / 2
	cab7 := math.Pow(cab, 7)
	g := 0.5 * (1 - math.Sqrt(cab7/(cab7+math.Pow(25, 7))))
	a1, a2 := (1+g)*c1.A, (1+g)*c2.A
	cp1, cp2 := math.Hypot(a1, c1.B), math.Hypot(a2, c2.B)
	hue := func(a, b float64) float64 {
		if a == 0 && b == 0 {
			return 0
		}
		h := math.Atan2(b, a) / deg
		if h < 0 {
			h += 360
		}
		return h
	}
	hp1, hp2 := hue(a1, c1.B), hue(a2, c2.B)
	dL := c2.L - c1.L
	dC := cp2 - cp1
	var dh float64
	switch {
	case cp1*cp2 == 0:
		dh = 0
	case math.Abs(hp2-hp1) <= 180:
		dh = hp2 - hp1
	case hp2-hp1 > 180:
		dh = hp2 - hp1 - 360
	default:
		dh = hp2 - hp1 + 360
	}
	dH := 2 * math.Sqrt(cp1*cp2) * math.Sin(dh/2*deg)
	lp := (c1.L + c2.L) / 2
	cp := (cp1 + cp2) / 2
	var hp float64
	switch {
	case cp1*cp2 == 0:
		hp = hp1 + hp2
	case math.Abs(hp1-hp2) <= 180:
		hp = (hp1 + hp2) / 2
	case hp1+hp2 < 360:
		hp = (hp1 + hp2 + 360) / 2
	default:
		hp = (hp1 + hp2 - 360) / 2
	}
	t := 1 - 0.17*math.Cos((hp-30)*deg) + 0.24*math.Cos(2*hp*deg) + 0.32*math.Cos((3*hp+6)*deg) - 0.20*math.Cos((4*hp-63)*deg)
	dTheta := 30 * math.Exp(-math.Pow((hp-275)/25, 2))
	cp7 := math.Pow(cp, 7)
	rc := 2 * math.Sqrt(cp7/(cp7+math.Pow(25, 7)))
	sl := 1 + 0.015*math.Pow(lp-50, 2)/math.Sqrt(20+math.Pow(lp-50, 2))
	sc := 1 + 0.045*cp
	sh := 1 + 0.015*cp*t
	rt := -math.Sin(2*dTheta*deg) * rc
	l, c, h := dL/sl, dC/sc, dH/sh
	return math.Sqrt(l*l + c*c + h*h + rt*c*h)
}
