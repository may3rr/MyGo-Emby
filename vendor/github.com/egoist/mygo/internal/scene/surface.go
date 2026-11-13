package scene

import "math"

// A Surface is a picture that a system decoder left where it decoded it,
// a frame of video, which renderers sample where it is rather than upload
// as RGBA (Image.Surface). The image showing it keeps W and H, and its
// version changes with each frame; its Pixels, which the CPU renderer
// draws and blurs read, come from its Restore, which converts the frame.
type Surface struct {
	// Format is how the pixels are laid out: SurfaceNV12's luma, then
	// chroma at half the size both ways, Cb and Cr interleaved, which
	// renderers convert to RGB with Matrix as they sample them; or
	// SurfaceBGRA, opaque or premultiplied as Image's pixels are.
	Format SurfaceFormat
	Matrix YUVMatrix
	// Planes are the frame in memory, for renderers to upload: the luma
	// and the chroma of NV12, or BGRA's one. Nil for a frame only on the
	// GPU, as Native has it.
	Planes [2]Plane
	// Native is the frame on the GPU, as the platform's renderer takes it:
	// a CVPixelBufferRef on macOS, which Metal wraps as textures; on
	// Windows the NT handle of a shared Direct3D 11 texture in BGRA with a
	// keyed mutex, which Direct3D opens on its device once (NativeID) and
	// acquires with key 0 around the frames reading it, as the producer
	// does around writing it; in browsers the number of the video element
	// playing it (media.AddElement), which WebGL uploads from. 0 for
	// none. The producer keeps it valid
	// while the image shows the surface; renderers keep what they made of
	// it until the GPU read it.
	Native uintptr
	// NativeID identifies Native's texture among all those the producers
	// of an app ever share, which renderers open once: a handle's number
	// may name another texture once closed.
	NativeID uint64
}

// SurfaceFormat is the pixel format of a Surface.
type SurfaceFormat uint8

const (
	// SurfaceNV12 is 8-bit YCbCr 4:2:0: a plane of luma, a byte a pixel,
	// then a plane of chroma half as wide and high, Cb and Cr a byte each.
	SurfaceNV12 SurfaceFormat = iota
	// SurfaceBGRA is a plane of 8-bit blue, green, red and alpha.
	SurfaceBGRA
)

// Plane is a plane of a Surface in memory: rows of Stride bytes.
type Plane struct {
	Pix    []byte
	Stride int
}

// YUVMatrix is how a Surface's YCbCr converts to RGB: the coefficients of
// a standard, and whether the samples take their full range or the
// limited (video) range, 16 to 235 for luma and 16 to 240 for chroma.
type YUVMatrix uint8

const (
	BT601 YUVMatrix = iota
	BT709
	BT2020
	// FullRange is added to a matrix for samples of the full range.
	FullRange YUVMatrix = 3
	// BT2020PQ is HDR10: BT.2020 primaries in the perceptual quantizer
	// (SMPTE ST 2084) transfer, in the video range. BT2020HLG is the same
	// primaries in the hybrid log-gamma transfer. Renderers tone map both
	// to the display, which shows SDR: see toneMap.
	BT2020PQ  YUVMatrix = 6
	BT2020HLG YUVMatrix = 7
)

// yuvCoeffs are Kr and Kb of each standard.
var yuvCoeffs = [3][2]float32{{0.299, 0.114}, {0.2126, 0.0722}, {0.2627, 0.0593}}

// YUVToRGB converts samples y, cb and cr, 0 to 1, to RGB, 0 to 1 and
// clamped, as the renderers' shaders do (yuvToRGB in each). HDR matrices
// tone map their linear light to the SDR white of the display, so that a
// film in HDR shows its colors and its brightness as a player does.
func (m YUVMatrix) YUVToRGB(y, cb, cr float32) (r, g, b float32) {
	if m == BT2020PQ || m == BT2020HLG {
		// BT.2020, video range, to linear light in nits / 100: the white
		// of SDR is 100 nits.
		y = (y - 16.0/255) * (255.0 / 219)
		cb = (cb - 128.0/255) * (255.0 / 224)
		cr = (cr - 128.0/255) * (255.0 / 224)
		kr, kb := yuvCoeffs[BT2020][0], yuvCoeffs[BT2020][1]
		r = y + 2*(1-kr)*cr
		b = y + 2*(1-kb)*cb
		g = (y - kr*r - kb*b) / (1 - kr - kb)
		var lin [3]float32
		for i, v := range [3]float32{r, g, b} {
			v = clamp01(v)
			if m == BT2020HLG {
				lin[i] = hlgToNits(v)
			} else {
				lin[i] = pqToNits(v)
			}
		}
		return toneMapRGB(lin[0], lin[1], lin[2])
	}
	std, full := m%FullRange, m >= FullRange
	if std > BT2020 {
		std = BT709
	}
	kr, kb := yuvCoeffs[std][0], yuvCoeffs[std][1]
	if full {
		cb -= 128.0 / 255
		cr -= 128.0 / 255
	} else {
		y = (y - 16.0/255) * (255.0 / 219)
		cb = (cb - 128.0/255) * (255.0 / 224)
		cr = (cr - 128.0/255) * (255.0 / 224)
	}
	r = y + 2*(1-kr)*cr
	b = y + 2*(1-kb)*cb
	g = (y - kr*r - kb*b) / (1 - kr - kb)
	return clamp01(r), clamp01(g), clamp01(b)
}

func clamp01(v float32) float32 { return min(max(v, 0), 1) }

// RGBA converts the surface's planes in memory into premultiplied RGBA,
// 4×W×H bytes, into dst when it has room, as the shaders sample them:
// each pixel's chroma bilinear between the chroma samples around it
// (centered between luma's, as MPEG-1 and JPEG place them). It returns
// nil without planes.
func (s *Surface) RGBA(dst []byte, w, h int) []byte {
	if s.Planes[0].Pix == nil || w <= 0 || h <= 0 {
		return nil
	}
	n := 4 * w * h
	if cap(dst) < n {
		dst = make([]byte, n)
	}
	dst = dst[:n]
	switch s.Format {
	case SurfaceBGRA:
		p := s.Planes[0]
		for y := range h {
			src, out := p.Pix[y*p.Stride:][:4*w], dst[4*w*y:][:4*w]
			for x := 0; x < 4*w; x += 4 {
				out[x], out[x+1], out[x+2], out[x+3] = src[x+2], src[x+1], src[x], src[x+3]
			}
		}
	case SurfaceNV12:
		lp, cp := s.Planes[0], s.Planes[1]
		cw, ch := (w+1)/2, (h+1)/2
		chroma := func(x, y int, k int) float32 {
			x, y = min(max(x, 0), cw-1), min(max(y, 0), ch-1)
			return float32(cp.Pix[y*cp.Stride+2*x+k]) / 255
		}
		for y := range h {
			// The chroma sample's center is at (2i+1)/2 in luma pixels.
			fy := (float32(y)+0.5)/2 - 0.5
			y0 := floor(fy)
			ty := fy - float32(y0)
			for x := range w {
				fx := (float32(x)+0.5)/2 - 0.5
				x0 := floor(fx)
				tx := fx - float32(x0)
				var c [2]float32
				for k := range 2 {
					a := chroma(x0, y0, k)*(1-tx) + chroma(x0+1, y0, k)*tx
					b := chroma(x0, y0+1, k)*(1-tx) + chroma(x0+1, y0+1, k)*tx
					c[k] = a*(1-ty) + b*ty
				}
				r, g, b := s.Matrix.YUVToRGB(float32(lp.Pix[y*lp.Stride+x])/255, c[0], c[1])
				o := dst[4*(w*y+x):]
				o[0], o[1], o[2], o[3] = to8(r), to8(g), to8(b), 255
			}
		}
	}
	return dst
}

func floor(v float32) int {
	i := int(v)
	if float32(i) > v {
		i--
	}
	return i
}

func to8(v float32) byte { return byte(v*255 + 0.5) }

// The reference white of SDR, and the brightest light a film may show
// before it is tone mapped: 100 and 1000 nits, the units of pqToNits.
const (
	sdrWhite = 100.0
	hdrPeak  = 1000.0
)

// pqToNits converts a sample in the perceptual quantizer (SMPTE ST 2084)
// transfer to linear light in nits: 0 to 10000.
func pqToNits(e float32) float32 {
	const m1, m2 = 0.1593017578125, 78.84375
	const c1, c2, c3 = 0.8359375, 18.8515625, 18.6875
	e = clamp01(e)
	p := float32(math.Pow(float64(e), 1.0/m2))
	num := max(p-c1, 0)
	den := c2 - c3*p
	if den <= 0 {
		return 0
	}
	return float32(math.Pow(float64(num/den), 1.0/m1)) * 10000
}

// hlgToNits converts a sample in the hybrid log-gamma transfer to linear
// light in nits, the 1000-nit display its reference white names.
func hlgToNits(e float32) float32 {
	const a, b, c = 0.17883277, 0.28466892, 0.55991073
	e = clamp01(e)
	var l float32
	if e <= 0.5 {
		l = e * e / 3
	} else {
		l = (float32(math.Exp(float64((e-c)/a))) + b) / 12
	}
	return 1000 * float32(math.Pow(float64(l), 1.2)) // the OOTF, gamma 1.2
}

// toneMap compresses linear light, in nits, into the 0 to 1 the display
// shows as its white: no light below 80 nits changes, and the rest rolls
// off to the peak, so that a film in HDR keeps its shadows and its look.
func toneMap(nits float32) float32 {
	l := max(nits, 0) / sdrWhite // in SDR whites
	const knee = 0.8
	if l <= knee {
		return l
	}
	scale := float32((hdrPeak/sdrWhite - knee) / 3.0)
	return knee + (1-knee)*(1-float32(math.Exp(float64(-(l-knee)/scale))))
}

// toneMapRGB tone maps linear light, in nits, keeping the hue of the
// picture: its luminance is compressed and its colors scale with it.
// It returns sRGB samples, 0 to 1, as the rest of YUVToRGB does.
func toneMapRGB(r, g, b float32) (float32, float32, float32) {
	y := 0.2627*r + 0.6780*g + 0.0593*b
	t := toneMap(y)
	s := float32(0)
	if y > 1e-6 {
		s = t / y
	}
	return srgbEncode(clamp01(r * s)), srgbEncode(clamp01(g * s)), srgbEncode(clamp01(b * s))
}

// srgbEncode converts linear light, 0 to 1, to an sRGB sample.
func srgbEncode(v float32) float32 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*float32(math.Pow(float64(v), 1.0/2.4)) - 0.055
}
