package metaballs

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/razzie/ebiten-metaballs/internal/pool"
)

//go:embed *.kage.tmpl
var kageFS embed.FS

var kageTemplates = template.Must(template.New("").Funcs(template.FuncMap{
	"float":        formatKageFloat,
	"floatDefault": formatKageFloatDefault,
	"vec2":         formatKageVec2,
}).ParseFS(kageFS, "*.kage.tmpl"))

const (
	basicShaderTemplate = "shader_basic.kage.tmpl"
	edgeShaderTemplate  = "shader_edge.kage.tmpl"
	fxaaShaderTemplate  = "fxaa.kage.tmpl"
)

// formatKageFloat renders f as a Kage float literal, which requires a decimal point.
func formatKageFloat(f float32) string {
	s := strconv.FormatFloat(float64(f), 'f', -1, 32)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

func formatKageFloatDefault(f float32, d float64) string {
	if f == 0 {
		return formatKageFloat(float32(d))
	}
	return formatKageFloat(f)
}

// formatKageVec2 renders x, y as a Kage vec2(...) literal.
func formatKageVec2(x, y float32) string {
	return fmt.Sprintf("vec2(%s, %s)", formatKageFloat(x), formatKageFloat(y))
}

// shaderPools recycles the packed-uniform float32 buffers a MetaballShader
// builds every drawPass, sized once from the shader's fixed ShaderCapacity.
type shaderPools struct {
	mainCircles      pool.SlicePool[float32]
	otherCircles     pool.SlicePool[float32]
	mainBridgeEnds   pool.SlicePool[float32]
	mainBridgeRadii  pool.SlicePool[float32]
	otherBridgeEnds  pool.SlicePool[float32]
	otherBridgeRadii pool.SlicePool[float32]
	uniforms         sync.Pool // map[string]any
}

func (p *shaderPools) init(capacity ShaderCapacity) {
	p.mainCircles.Init(capacity.MainCircles * 4)
	p.otherCircles.Init(capacity.OtherCircles * 4)
	p.mainBridgeEnds.Init(capacity.MainBridges * 4)
	p.mainBridgeRadii.Init(capacity.MainBridges * 3)
	p.otherBridgeEnds.Init(capacity.OtherBridges * 4)
	p.otherBridgeRadii.Init(capacity.OtherBridges * 3)
	p.uniforms.New = func() any {
		return make(map[string]any)
	}
}

// MetaballShader renders metaballs and, when FxaaEnabled, post-processes them
// with FXAA via a single cached offscreen buffer. That buffer is mutable,
// lazily-(re)sized state, so an FxaaEnabled shader's Draw*/DrawScaledAt calls
// must not run concurrently on the same instance.
type MetaballShader struct {
	shader    *ebiten.Shader
	fxaa      *ebiten.Shader
	offscreen *ebiten.Image
	pools     shaderPools
	config    ShaderConfig
}

func NewMetaballShader(config ShaderConfig) (*MetaballShader, error) {
	if config.MainCircles <= 0 || config.MainBridges < 0 ||
		config.OtherCircles < 0 || config.OtherBridges < 0 || config.SmoothK <= 0 {
		return nil, fmt.Errorf("invalid shader config: %+v", config)
	}

	edgeShadingEnabled := false
	data := config

	if length := math.Hypot(float64(config.LightDirX), float64(config.LightDirY)); length > 0 {
		edgeShadingEnabled = true
		data.LightDirX = float32(float64(config.LightDirX) / length)
		data.LightDirY = float32(float64(config.LightDirY) / length)

		if config.EdgeThickness <= 0 {
			return nil, fmt.Errorf("invalid shader config: EdgeThickness must be positive when edge shading is enabled: %+v", config)
		}
	}

	templateName := basicShaderTemplate
	if edgeShadingEnabled {
		templateName = edgeShaderTemplate
	}

	var src bytes.Buffer
	if err := kageTemplates.ExecuteTemplate(&src, templateName, data); err != nil {
		return nil, fmt.Errorf("generate metaball shader source: %w", err)
	}

	shader, err := ebiten.NewShader(src.Bytes())
	if err != nil {
		return nil, fmt.Errorf("compile metaball shader: %w", err)
	}

	ms := &MetaballShader{shader: shader, config: config}
	ms.pools.init(config.ShaderCapacity)

	if config.FxaaEnabled && !config.rendererFxaaEnabled {
		var fxaaSrc bytes.Buffer
		if err := kageTemplates.ExecuteTemplate(&fxaaSrc, fxaaShaderTemplate, config); err != nil {
			return nil, fmt.Errorf("generate fxaa shader source: %w", err)
		}

		fxaaShader, err := ebiten.NewShader(fxaaSrc.Bytes())
		if err != nil {
			return nil, fmt.Errorf("compile fxaa shader: %w", err)
		}
		ms.fxaa = fxaaShader
	}

	return ms, nil
}

// offscreenFor returns s.offscreen resized (recreated) to (w, h) if needed.
func (s *MetaballShader) offscreenFor(w, h int) *ebiten.Image {
	if s.offscreen != nil && s.offscreen.Bounds().Dx() == w && s.offscreen.Bounds().Dy() == h {
		return s.offscreen
	}
	if s.offscreen != nil {
		s.offscreen.Deallocate()
	}
	s.offscreen = ebiten.NewImage(w, h)
	return s.offscreen
}

// packCircles packs circles into vec4(x, y, radius, unused) entries using a
// pooled buffer, padded with zero entries up to p's default length so the
// uniform array length matches the shader. release must be called once the
// returned slice is no longer needed.
func packCircles(out *[]float32, circles []Circle) {
	for i, c := range circles {
		(*out)[i*4], (*out)[i*4+1], (*out)[i*4+2] = c.X, c.Y, c.Radius
	}
}

// packBridges packs bridges into vec4(a.x, a.y, b.x, b.y) ends and
// vec3(radiusA, radiusMiddle, radiusB) radii, using pooled buffers padded up
// to endsPool/radiiPool's default length. release is non-nil (and must
// still be called) even when err is returned.
func packBridges(ends, radii *[]float32, circles []Circle, bridges []Bridge) (err error) {
	for i, br := range bridges {
		if br.A < 0 || br.A >= len(circles) ||
			br.B < 0 || br.B >= len(circles) {
			return fmt.Errorf("invalid bridge indices: %d -> %d", br.A, br.B)
		}

		a := circles[br.A]
		b := circles[br.B]

		(*ends)[i*4], (*ends)[i*4+1], (*ends)[i*4+2], (*ends)[i*4+3] = a.X, a.Y, b.X, b.Y
		(*radii)[i*3], (*radii)[i*3+1], (*radii)[i*3+2] = a.Radius/2, br.MiddleRadius, b.Radius/2
	}

	return nil
}

// Draw renders each group in its own pass, using the remaining groups
// combined as the "other" field to drive the squeezing effect.
func (s *MetaballShader) Draw(dst *ebiten.Image, groups []Group, xform UVTransform) error {
	return s.DrawAt(dst, groups, xform, image.Point{})
}

// DrawAt is like Draw, but translates the scene by offset pixels in dst's
// coordinate system: positive X moves right and positive Y moves down.
// A sub-image clips the scene to its bounds without changing its origin.
func (s *MetaballShader) DrawAt(dst *ebiten.Image, groups []Group, xform UVTransform, offset image.Point) error {
	if xform.scale[0] <= 0 || xform.scale[1] <= 0 {
		return fmt.Errorf("uv scale must be positive: %v", xform.scale)
	}
	// Map destination-local pixels back to the untranslated scene. Keep the
	// destination origin even when rendering into a zero-origin FXAA buffer.
	pixelOrigin := dst.Bounds().Min.Sub(offset)

	// FXAA needs the fully-composited image, so all group passes render into
	// an offscreen buffer first and only the final antialiased result reaches dst.
	target := dst
	if s.fxaa != nil {
		w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
		target = s.offscreenFor(w, h)
		target.Clear()
	}

	for i, g := range groups {
		other := combineGroups(groups, i)

		if err := s.drawPass(target, g, other, g.Color, xform, pixelOrigin); err != nil {
			return err
		}
	}

	if s.fxaa != nil {
		var transform ebiten.GeoM
		transform.Translate(float64(dst.Bounds().Min.X), float64(dst.Bounds().Min.Y))
		w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
		dst.DrawRectShader(w, h, s.fxaa, &ebiten.DrawRectShaderOptions{
			GeoM:   transform,
			Images: [4]*ebiten.Image{target},
		})
	}

	return nil
}

func (s *MetaballShader) drawPass(
	dst *ebiten.Image,
	main Group,
	other Group,
	color ebiten.ColorScale,
	xform UVTransform,
	pixelOrigin image.Point,
) error {
	if len(main.Circles) > s.config.MainCircles ||
		len(main.Bridges) > s.config.MainBridges ||
		len(other.Circles) > s.config.OtherCircles ||
		len(other.Bridges) > s.config.OtherBridges {
		return fmt.Errorf(
			"metaball counts exceed shader capacity: main circles %d/%d, main bridges %d/%d, other circles %d/%d, other bridges %d/%d",
			len(main.Circles), s.config.MainCircles,
			len(main.Bridges), s.config.MainBridges,
			len(other.Circles), s.config.OtherCircles,
			len(other.Bridges), s.config.OtherBridges,
		)
	}

	uniforms := s.pools.uniforms.Get().(map[string]any)
	defer s.pools.uniforms.Put(uniforms)
	uniforms["UvScale"] = xform.scale[:]
	uniforms["UvOffset"] = xform.offset[:]
	uniforms["PixelOrigin"] = []float32{float32(pixelOrigin.X), float32(pixelOrigin.Y)}
	uniforms["MainColor"] = []float32{color.R(), color.G(), color.B(), color.A()}

	mainCircles := s.pools.mainCircles.Get()
	packCircles(mainCircles, main.Circles)
	defer s.pools.mainCircles.Put(mainCircles)
	uniforms["MainCircleCount"] = len(main.Circles)
	uniforms["MainCircles"] = *mainCircles

	if s.config.MainBridges > 0 {
		mainEnds := s.pools.mainBridgeEnds.Get()
		mainRadii := s.pools.mainBridgeRadii.Get()
		err := packBridges(mainEnds, mainRadii, main.Circles, main.Bridges)
		defer s.pools.mainBridgeEnds.Put(mainEnds)
		defer s.pools.mainBridgeRadii.Put(mainRadii)
		if err != nil {
			return err
		}
		uniforms["MainBridgeCount"] = len(main.Bridges)
		uniforms["MainBridgeEnds"] = *mainEnds
		uniforms["MainBridgeRadii"] = *mainRadii
	}

	if s.config.OtherCircles > 0 {
		otherCirclesBuf := s.pools.otherCircles.Get()
		packCircles(otherCirclesBuf, other.Circles)
		defer s.pools.otherCircles.Put(otherCirclesBuf)
		uniforms["OtherCircleCount"] = len(other.Circles)
		uniforms["OtherCircles"] = *otherCirclesBuf
	}

	if s.config.OtherBridges > 0 {
		otherEnds := s.pools.otherBridgeEnds.Get()
		otherRadii := s.pools.otherBridgeRadii.Get()
		err := packBridges(otherEnds, otherRadii, other.Circles, other.Bridges)
		defer s.pools.otherBridgeEnds.Put(otherEnds)
		defer s.pools.otherBridgeRadii.Put(otherRadii)
		if err != nil {
			return err
		}
		uniforms["OtherBridgeCount"] = len(other.Bridges)
		uniforms["OtherBridgeEnds"] = *otherEnds
		uniforms["OtherBridgeRadii"] = *otherRadii
	}

	var transform ebiten.GeoM
	transform.Translate(float64(dst.Bounds().Min.X), float64(dst.Bounds().Min.Y))
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	dst.DrawRectShader(w, h, s.shader, &ebiten.DrawRectShaderOptions{
		GeoM:     transform,
		Uniforms: uniforms,
	})

	return nil
}
