package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// IconState 决定图标的样子：三个（或更多）运行槽，忙的实心、空的描边；
// 有事等你处理时右上角加一个圆点；暂停时整体变淡。
type IconState struct {
	Slots   int
	Busy    int
	Waiting bool
	Paused  bool
}

var (
	inkLight = color.NRGBA{0x1d, 0x1b, 0x17, 0xff}
	paper    = color.NRGBA{0xec, 0xe7, 0xda, 0xff}
	accent   = color.NRGBA{0xd6, 0x40, 0x1f, 0xff}
)

// Icon 画出 size×size 的 PNG。template 为 true 时只用黑色和透明度（macOS 会按菜单栏明暗自动着色）；
// 否则画成深色圆角底板加浅色运行槽，深浅任务栏都看得清。
func Icon(st IconState, size int, template bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	slots := min(max(st.Slots, 1), 5)
	ink := paper
	if template {
		ink = color.NRGBA{0, 0, 0, 0xff}
	}
	dot := accent
	if template {
		dot = ink
	}

	// 运行槽：竖条，占图标宽度的 80%
	pad, gap, top, bottom := s*0.08, s*0.08, s*0.32, s*0.92
	if !template {
		pad, gap, top, bottom = s*0.22, s*0.07, s*0.28, s*0.76
	}
	w := (s - 2*pad - gap*float64(slots-1)) / float64(slots)
	stroke := math.Max(1.2, s*0.06)
	radius := math.Min(w*0.35, s*0.08)
	alpha := 1.0
	if st.Paused {
		alpha = 0.45
	}

	type shape func(x, y float64) float64 // 覆盖度 0..1
	var shapes []struct {
		f shape
		c color.NRGBA
	}
	if !template {
		inset := s * 0.04
		bg := func(x, y float64) float64 {
			return clamp01(0.5 - roundRectDist(x, y, inset, inset, s-inset, s-inset, s*0.22))
		}
		shapes = append(shapes, struct {
			f shape
			c color.NRGBA
		}{bg, inkLight})
	}
	for i := 0; i < slots; i++ {
		x0 := pad + float64(i)*(w+gap)
		x1 := x0 + w
		filled := i < st.Busy
		f := func(x, y float64) float64 {
			d := roundRectDist(x, y, x0, top, x1, bottom, radius)
			if filled {
				return clamp01(0.5 - d)
			}
			return clamp01(0.5-d) * clamp01(0.5+d+stroke) // 只要边框
		}
		shapes = append(shapes, struct {
			f shape
			c color.NRGBA
		}{f, withAlpha(ink, alpha)})
	}
	if st.Waiting {
		cx, cy, r, ring := s*0.80, s*0.20, s*0.17, s*0.08
		if template {
			cx, cy, r, ring = s*0.86, s*0.13, s*0.12, s*0.05
		}
		// 先挖一圈透明，让圆点和运行槽分开
		cut := func(x, y float64) float64 { return clamp01(r + ring - math.Hypot(x-cx, y-cy) + 0.5) }
		dotf := func(x, y float64) float64 { return clamp01(r - math.Hypot(x-cx, y-cy) + 0.5) }
		shapes = append(shapes, struct {
			f shape
			c color.NRGBA
		}{cut, color.NRGBA{}}, struct {
			f shape
			c color.NRGBA
		}{dotf, dot})
	}

	const ss = 4 // 每像素 4×4 采样做抗锯齿
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := float64(px) + (float64(sx)+0.5)/ss
					y := float64(py) + (float64(sy)+0.5)/ss
					var cr, cg, cb, ca float64
					for _, sh := range shapes {
						cov := sh.f(x, y)
						if cov <= 0 {
							continue
						}
						if sh.c.A == 0 { // 挖空
							ca *= 1 - cov
							cr *= 1 - cov
							cg *= 1 - cov
							cb *= 1 - cov
							continue
						}
						sa := cov * float64(sh.c.A) / 255
						cr = float64(sh.c.R)*sa + cr*(1-sa)
						cg = float64(sh.c.G)*sa + cg*(1-sa)
						cb = float64(sh.c.B)*sa + cb*(1-sa)
						ca = sa + ca*(1-sa)
					}
					r, g, b, a = r+cr, g+cg, b+cb, a+ca
				}
			}
			n := float64(ss * ss)
			if a > 0 {
				img.SetNRGBA(px, py, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(math.Round(a / n * 255))})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// ICO 把 PNG 包成 Windows 托盘需要的 .ico（Vista 起支持内嵌 PNG）。
func ICO(pngData []byte, size int) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&b, le, [3]uint16{0, 1, 1}) // 保留、类型 1=图标、1 张
	dim := byte(size)
	if size >= 256 {
		dim = 0
	}
	b.Write([]byte{dim, dim, 0, 0})
	_ = binary.Write(&b, le, [2]uint16{1, 32}) // 色彩平面、位深
	_ = binary.Write(&b, le, [2]uint32{uint32(len(pngData)), 22})
	b.Write(pngData)
	return b.Bytes()
}

func roundRectDist(x, y, x0, y0, x1, y1, r float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-r, (y1-y0)/2-r
	dx, dy := math.Abs(x-cx)-hx, math.Abs(y-cy)-hy
	out := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	in := math.Min(math.Max(dx, dy), 0)
	return out + in - r
}

func withAlpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(float64(c.A) * a)
	return c
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }
