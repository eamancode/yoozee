package drama

import (
	"fmt"
	"image"
	"math"
	"strconv"
)

// 色卡取色校验（M7.2 第 4 阶段）：色卡出图后按色块数把画面中部 80% 的宽度等分，取每块中心一小片的平均色，
// 与色彩脚本的 HEX 算 CIEDE2000 色差。只记警告，不拒绝图片：图像模型对 HEX 的还原本来就有偏差，人看了再决定要不要重出。

// PaletteCheckMaxDeltaE 是一块色差的警告阈值（ΔE2000）。
const PaletteCheckMaxDeltaE = 12.0

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
	// Note 是没法校验时的原因（图片读不出、没有颜色）。
	Note string `json:"note,omitempty"`
}

// CheckPaletteImage 按色块数等分取色并与 hexes（色卡从左到右的颜色）比对；任一块 ΔE 超过阈值即 passed=false。
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
	n := float64(len(hexes))
	left, span := b.Min.X+int(width*0.1), width*0.8
	cy := b.Min.Y + int(height*0.5)
	radius := max(1, int(math.Min(span/n, height)*0.08))
	check.Passed = true
	for i, hex := range hexes {
		cx := left + int((float64(i)+0.5)*span/n)
		r, g, bl := averageColor(img, cx, cy, radius)
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
