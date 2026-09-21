# softbody

A rendering-independent 2D circle physics state with rectangular world bounds,
defaulting to `[0,1] x [0,1]`. Call `SetBounds` on the simulation goroutine to
resize the world. It immediately confines existing hard cores, updates the walls,
and rebuilds the hex grid on the next step. Bounds must fit the largest circle.

Each circle has:

- a hard `InnerRadius`
- a compressible `OuterRadius`
- mass, velocity, and an application-defined `Group` identifier

The outer shells use a nonlinear spring-damper response. The inner cores use
position correction plus a normal collision impulse, so the simulation does not
have to rely on an arbitrarily stiff penalty spring to prevent collapse.

`Config.LinearDamping` sets the base velocity damping rate.
`Config.LinearDampingMassFactor` defaults to zero, giving every circle the same
damping. Positive values increase the rate with mass:
`rate = LinearDamping * (1 + LinearDampingMassFactor * mass)`.
Each substep multiplies velocity by `1 / (1 + rate * dt)`. For example, a factor
of `1` gives masses `0.25`, `1`, and `4` rates of `1.25`, `2`, and `5` times the
base rate. To make larger circles damp more, assign larger masses, as the physics
example does with mass proportional to area. Negative factors are clamped to
zero. A zero `LinearDamping` selects the default rate; a negative value disables
all velocity damping, including the mass contribution.

Optional same-group attraction uses `Config.AttractionRange` as the maximum gap
between outer shells and `Config.AttractionStrength` as the force at contact.
The force fades quadratically to zero across that gap; shell and core collision
responses still prevent collapse. Both values must be positive to enable it.

External attraction and repulsion use `State.QueueRadialImpulse`. Each impulse
specifies a source in world coordinates, radius, signed strength, and optional
list of target groups. Positive strength pushes outward; negative strength pulls
inward. Empty groups select every circle, including group zero. Strength is
momentum, divided by circle mass to obtain the velocity change, with quadratic
falloff to zero at the radius. A positive infinite radius disables falloff.
Circles at the source stay unchanged because they have no radial direction.
Sources may lie outside the world bounds and are not clamped when bounds change.

Impulses are consumed once by the next `Step` with a positive timestep and at
least one circle, regardless of the number of substeps. Multiple impulses add
together. For a sustained force, queue an impulse each tick with strength equal
to force times tick duration. Nonpositive or NaN radii, nonfinite coordinates or
strengths, and zero strengths are ignored.

Colors, input bindings, and cursor interaction settings belong to the caller;
the physics example defines these in `examples/physics/main.go`.

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
    Group:       42,
})
if err != nil {
    panic(err)
}

// Pull group 42 toward a point on the next step.
world.QueueRadialImpulse(softbody.RadialImpulse{
    X: 0.5, Y: 0.5, Radius: 0.5, Strength: -0.35,
    Groups: []softbody.Group{42},
})

world.Step(1.0 / 60.0)

circles := world.Snapshot(nil)
_ = circles
```

`QueueRadialImpulse` is safe concurrently with `Step` and copies the group list
before returning. Other state APIs, including `Snapshot`, must not run
concurrently with simulation mutations.

This API replaces `RegisterClick` and `Repel`. The former `Config.ClickRadius`
and `Config.ClickImpulse` settings are now supplied per impulse as `Radius` and
`Strength`. `Group` accepts any `uint32` value; there are no predefined colors
or mouse buttons.
