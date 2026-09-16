# softbody-demo

A rendering-independent 2D circle physics state with rectangular world bounds,
defaulting to `[0,1] x [0,1]`. Call `SetBounds` on the simulation goroutine to
resize the world. It immediately confines existing hard cores, updates the walls,
and rebuilds the hex grid on the next step. Bounds must fit the largest circle.

Each circle has:

- a hard `InnerRadius`
- a compressible `OuterRadius`
- mass, velocity, and one of three groups: red, green, blue

The outer shells use a nonlinear spring-damper response. The inner cores use
position correction plus a normal collision impulse, so the simulation does not
have to rely on an arbitrarily stiff penalty spring to prevent collapse.

Optional same-group attraction uses `Config.AttractionRange` as the maximum gap
between outer shells and `Config.AttractionStrength` as the force at contact.
The force fades quadratically to zero across that gap; shell and core collision
responses still prevent collapse. Both values must be positive to enable it.
The physics example uses a short range of `0.015` and strength of `0.3`.

Hold Space in the physics example to push nearby circles away from the mouse
pointer, regardless of group. `State.Repel(x, y)` uses `Config.ClickImpulse` and
`Config.ClickRadius`, with quadratic falloff to zero at the radius. Circles
exactly at the source stay unchanged because they have no outward direction.
Call it each tick for a sustained force, scaling `ClickImpulse` by the tick duration.

## Broad phase

Particles are assigned to the nearest center of a triangular lattice, producing
hexagonal Voronoi cells. The lattice rows are staggered. The neighbor stencil is
computed conservatively from the largest outer radius plus the attraction range,
so differently sized circles are not missed even when the configured grid spacing
is small.

The state is counting-sorted into cell order every substep. That makes all
particles belonging to a cell contiguous in the SoA arrays, which is useful for
both cache locality and SIMD.

## Parallelism

A cell job writes only to particles owned by that cell and only reads neighboring
cells. Therefore cell jobs can run concurrently without atomics or per-cell
locks. Pair interactions are intentionally evaluated from both directions:
A computes the effect of B on A, and B independently computes the effect of A on
B. This trades some arithmetic for race-free parallelism and SIMD-friendly spans.

## SIMD

With Go 1.27:

```sh
GOEXPERIMENT=simd go test ./...
```

`kernel_simd.go` SIMD-accelerates:

- Cartesian -> fractional axial hex coordinates
- circle-vs-neighbor-span collision math
- integration and boundary clamping

Without `GOEXPERIMENT=simd`, `kernel_scalar.go` is selected automatically.

## Example

```go
cfg := softbody.DefaultConfig()
cfg.Workers = 8
cfg.Substeps = 2

world := softbody.New(cfg)

_, err := world.AddCircle(softbody.CircleSpec{
    X:           0.25,
    Y:           0.5,
    InnerRadius: 0.018,
    OuterRadius: 0.028,
    Mass:        1,
    Group:       softbody.Red,
})
if err != nil {
    panic(err)
}

// Left attracts red, right attracts blue, middle attracts green.
world.RegisterClick(softbody.MouseLeft, 0.5, 0.5)

world.Step(1.0 / 60.0)

circles := world.Snapshot(nil)
_ = circles
```

A registered click is consumed by the next `Step` and acts as an instantaneous
attraction impulse. Call `RegisterClick` repeatedly if later input handling wants
"held button" behavior.
Set `Config.ClickRadius` to positive infinity for attraction across the entire
world without distance falloff.

`RegisterClick` is safe concurrently with `Step`. Other state-mutating APIs are
intended to be called from the simulation/update goroutine.
