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
	mainShaderTemplate = "shader_main.kage.tmpl"
	fxaaShaderTemplate = "fxaa.kage.tmpl"
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

// shaderPools recycles one packed scene per draw, including dense group IDs.
type shaderPools struct {
	circles     pool.SlicePool[float32]
	bridgeEnds  pool.SlicePool[float32]
	bridgeRadii pool.SlicePool[float32]
	groupColors pool.SlicePool[float32]
	uniforms    sync.Pool
}

func (p *shaderPools) init(capacity ShaderCapacity) {
	p.circles.Init(capacity.Circles * 4)
	p.bridgeEnds.Init(capacity.Bridges * 4)
	p.bridgeRadii.Init(capacity.Bridges * 4)
	p.groupColors.Init(capacity.Groups * 4)
	p.uniforms.New = func() any { return make(map[string]any) }
}

// MetaballShader renders metaballs and, when FxaaEnabled, post-processes them
// with FXAA via a single cached offscreen buffer. That buffer is mutable,
// lazily-(re)sized state, so an FxaaEnabled shader's Draw/DrawAt calls
// must not run concurrently on the same instance.
type MetaballShader struct {
	shader    *ebiten.Shader
	fxaa      *ebiten.Shader
	offscreen *ebiten.Image
	pools     shaderPools
	config    ShaderConfig
}

func NewMetaballShader(config ShaderConfig) (*MetaballShader, error) {
	if config.BorderThickness < 0 || math.IsNaN(float64(config.BorderThickness)) || math.IsInf(float64(config.BorderThickness), 0) {
		return nil, fmt.Errorf("invalid shader config: BorderThickness must be finite and nonnegative: %+v", config)
	}
	if config.Groups <= 0 || config.Circles <= 0 || config.Bridges < 0 || config.SmoothK <= 0 {
		return nil, fmt.Errorf("invalid shader config: %+v", config)
	}

	data := config

	if length := math.Hypot(float64(config.LightDirX), float64(config.LightDirY)); length > 0 {
		data.LightDirX = float32(float64(config.LightDirX) / length)
		data.LightDirY = float32(float64(config.LightDirY) / length)

		if config.EdgeThickness <= 0 {
			return nil, fmt.Errorf("invalid shader config: EdgeThickness must be positive when edge shading is enabled: %+v", config)
		}
	}

	var src bytes.Buffer
	if err := kageTemplates.ExecuteTemplate(&src, mainShaderTemplate, data); err != nil {
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

// packCircles stores (x, y, radius, group ID) for every circle.
func packCircles(out []float32, circles []Circle, group int) {
	for i, c := range circles {
		out[i*4], out[i*4+1], out[i*4+2], out[i*4+3] = c.X, c.Y, c.Radius, float32(group)
	}
}

// packBridges stores endpoints and (radiusA, radiusMiddle, radiusB, group ID).
// Bridge indices are local to their group, and are validated before packing.
func packBridges(ends, radii []float32, circles []Circle, bridges []Bridge, group int) {
	for i, b := range bridges {
		a, c := circles[b.A], circles[b.B]
		ends[i*4], ends[i*4+1], ends[i*4+2], ends[i*4+3] = a.X, a.Y, c.X, c.Y
		radii[i*4], radii[i*4+1], radii[i*4+2], radii[i*4+3] = a.Radius/2, b.MiddleRadius, c.Radius/2, float32(group)
	}
}

// validateGroups also protects the renderer's bridge filtering from bad indices.
func validateGroups(groups []Group) error {
	for i, g := range groups {
		for _, b := range g.Bridges {
			if b.A < 0 || b.A >= len(g.Circles) || b.B < 0 || b.B >= len(g.Circles) {
				return fmt.Errorf("group %d: invalid bridge indices: %d -> %d", i, b.A, b.B)
			}
		}
	}
	return nil
}

// Draw evaluates all groups in one metaball pass, followed by optional FXAA.
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
	if err := validateGroups(groups); err != nil {
		return err
	}
	capacity := CapacityForGroups(groups)
	if capacity.Groups > s.config.Groups || capacity.Circles > s.config.Circles || capacity.Bridges > s.config.Bridges {
		return fmt.Errorf("metaball counts exceed shader capacity: groups %d/%d, circles %d/%d, bridges %d/%d", capacity.Groups, s.config.Groups, capacity.Circles, s.config.Circles, capacity.Bridges, s.config.Bridges)
	}
	if capacity.Circles == 0 {
		return nil
	}
	// Map destination-local pixels back to the untranslated scene. Keep the
	// destination origin even when rendering into a zero-origin FXAA buffer.
	pixelOrigin := dst.Bounds().Min.Sub(offset)

	// FXAA filters the completed scene in an offscreen buffer.
	target := dst
	if s.fxaa != nil {
		w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
		target = s.offscreenFor(w, h)
		target.Clear()
	}

	s.drawScene(target, groups, capacity, xform, pixelOrigin)

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

// drawScene packs each primitive once and submits one full-scene draw call.
func (s *MetaballShader) drawScene(dst *ebiten.Image, groups []Group, capacity ShaderCapacity, xform UVTransform, pixelOrigin image.Point) {
	circles := s.pools.circles.Get()
	ends := s.pools.bridgeEnds.Get()
	radii := s.pools.bridgeRadii.Get()
	colors := s.pools.groupColors.Get()
	defer s.pools.circles.Put(circles)
	defer s.pools.bridgeEnds.Put(ends)
	defer s.pools.bridgeRadii.Put(radii)
	defer s.pools.groupColors.Put(colors)
	packGroups(*circles, *ends, *radii, *colors, groups)

	uniforms := s.pools.uniforms.Get().(map[string]any)
	defer s.pools.uniforms.Put(uniforms)
	uniforms["UvScale"] = xform.scale[:]
	uniforms["UvOffset"] = xform.offset[:]
	uniforms["PixelOrigin"] = []float32{float32(pixelOrigin.X), float32(pixelOrigin.Y)}
	uniforms["CircleCount"] = capacity.Circles
	uniforms["Circles"] = *circles
	uniforms["GroupCount"] = capacity.Groups
	uniforms["GroupColors"] = *colors
	if s.config.Bridges > 0 {
		uniforms["BridgeCount"] = capacity.Bridges
		uniforms["BridgeEnds"] = *ends
		uniforms["BridgeRadii"] = *radii
	}
	var transform ebiten.GeoM
	transform.Translate(float64(dst.Bounds().Min.X), float64(dst.Bounds().Min.Y))
	dst.DrawRectShader(dst.Bounds().Dx(), dst.Bounds().Dy(), s.shader, &ebiten.DrawRectShaderOptions{GeoM: transform, Uniforms: uniforms})
}

// Empty groups do not consume a uniform slot. Every group ID is dense and
// indexes the color array packed in the same traversal.
func packGroups(circles, ends, radii, colors []float32, groups []Group) {
	ci, bi, gi := 0, 0, 0
	for _, g := range groups {
		if len(g.Circles) == 0 {
			continue
		}
		packCircles(circles[ci*4:], g.Circles, gi)
		packBridges(ends[bi*4:], radii[bi*4:], g.Circles, g.Bridges, gi)
		colors[gi*4], colors[gi*4+1], colors[gi*4+2], colors[gi*4+3] = g.Color.R(), g.Color.G(), g.Color.B(), g.Color.A()
		ci += len(g.Circles)
		bi += len(g.Bridges)
		gi++
	}
}
