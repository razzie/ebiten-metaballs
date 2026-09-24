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
global velocity damping, including the mass contribution. Per-bridge damping
is configured separately.

Optional same-group attraction uses `Config.AttractionRange` as the maximum gap
between outer shells and `Config.AttractionStrength` as the force at contact.
The force fades quadratically to zero across that gap; shell and core collision
responses still prevent collapse. Both values must be positive to enable it.

## Bridges

`State.AddBridge` connects two circles using the stable IDs returned by
`AddCircle`. Each bridge has its own distance limits. Enable `ConstrainDistance`
for a chain with firm length limits, or leave it false for spring connections:

```go
bridgeID, err := world.AddBridge(softbody.BridgeSpec{
    A: circleAID, B: circleBID,
    MinDistance: 0.08,
    MaxDistance: 0.12,
    BreakDistance: 0.25,
    ConstrainDistance: true,
    Damping: 2.5,
})
if err != nil {
    panic(err)
}
_ = bridgeID // Pass to world.RemoveBridge to disconnect manually.
```

Distances are measured between circle centers in world units.

### Distance constraints

With `ConstrainDistance: true`, each substep predicts movement, then repeatedly
corrects bridge lengths into `[MinDistance, MaxDistance]`. Corrections are shared
in proportion to inverse mass: a mass-1 circle moves four times as far as a mass-4
circle. The velocity changes from these corrections carry the response into the
next substep. Links are solved sequentially, alternating traversal direction on
each pass, so corrections propagate along a chain within the same substep.

Equal minimum and maximum distances make a fixed-length link. A zero minimum
makes a rope that can go slack. `AttractForce` and `RepelForce` are ignored in
constraint mode; both limits remain active even when those fields are zero.
Constraints do not resist bending or give the bridge collision geometry.

`Config.BridgeIterations` defaults to 8 passes per substep. Increase it for longer
chains or tighter length tolerances; increase `Substeps` for fast motion. A finite
number of passes can leave some distance error, especially in long chains or
crowded contacts. This is a hard distance solver, not a compliant spring solver.

Hard-core contacts and wall confinement are interleaved with the bridge passes.
The collision grid and stable-ID lookup are refreshed after bridge movement, so
corrections can create new contacts without leaving stale endpoint indices.
Shell forces and external impulses are not reapplied during these passes.
Collision corrections take precedence at the end of a pass; conflicting limits
(such as a maximum distance smaller than the combined core radii) cannot all be
satisfied. More iterations cost additional bridge, grid, and contact work.

### Damping

`BridgeSpec.Damping` is an axial damping coefficient in force per unit speed.
It works in both modes and also inside the slack interval. Positive values resist
endpoints approaching or separating, but do not resist shared translation or
instantaneous tangential motion. Zero disables damping. Unlike global
`LinearDamping`, it preserves the pair's total linear momentum.

Damping uses an implicit velocity solve after the position corrections. For an
isolated bridge, relative axial speed is divided by
`1 + Damping * dt * (1/massA + 1/massB)` per substep. Accumulated impulses keep
extra solver passes from multiplying the damping strength; extra passes improve
convergence when bridges share circles. The bridges example uses `Damping: 2.5`.

### Spring connections

With `ConstrainDistance: false` (the default), existing spring behavior is
preserved. Below
`MinDistance`, the bridge repels with magnitude `RepelForce * (MinDistance - distance)`;
above `MaxDistance`, it attracts with magnitude `AttractForce * (distance - MaxDistance)`.
The force fields specify stiffness (force per world unit): doubling the stretch
or compression outside the range doubles the force. Inside the inclusive range
it applies no spring force, so force grows continuously from zero at either limit.
Optional bridge damping still applies.
Forces are equal and opposite and divided
by each circle's mass to obtain acceleration. Zero disables the corresponding
force. Multiple bridges add their forces, including bridges sharing endpoints.
Coincident centers repel along a deterministic horizontal direction.

These fields previously specified constant forces. To match an old force at a
chosen stretch or compression, divide that force by the distance outside the
range to obtain the new stiffness.

### Lifecycle and collisions

A positive `BreakDistance` permanently removes the bridge when exceeded, before
applying any bridge force in that substep. Constraint or damped bridges also
check predicted positions before corrections or damping, so a correction cannot
hide a break. Transient positions during solver passes do not trigger breaks.
Zero makes a bridge unbreakable. All limits, forces, and damping coefficients
must be finite and nonnegative, `MinDistance <= MaxDistance`, and a
nonzero `BreakDistance` must be at least `MaxDistance`. Endpoints must be distinct
existing circles; their groups and separation do not restrict bridge creation.

`State.BridgeSnapshot(dst)` copies active bridges and their stable bridge IDs,
omitting broken or manually removed bridges. Bridge IDs are separate from circle
IDs. `RemoveBridge(id)` reports whether an active bridge was removed. These APIs
must run on the simulation goroutine, like `AddCircle` and `Snapshot`.

Bridges are solved every substep, independent of the collision grid's neighbor
range. They have no collision geometry and do not interact with other bridges,
circles along their length, or walls. Endpoint circles retain their ordinary
collisions. Rendering bridges is the caller's responsibility.

## Drag and drop

Use three separate calls on the simulation goroutine, with pointer positions in
world coordinates:

```go
// Left button pressed: hit-test once and grab the nearest circle.
ids := world.Drag(softbody.DragSpec{X: x, Y: y, MaxCircles: 1})
_ = ids // Stable IDs of the selected circles, nearest first.

// While held: update the target; Step performs the movement.
world.Carry(x, y)
world.Step(1.0 / 60.0)

// Left button released: apply the latest position and release at rest.
world.Carry(x, y)
world.Drop()
```

`Drag` tests outer disks, preserves the pointer-to-center grab offsets, and
replaces any existing selection. `MaxCircles <= 0` grabs all hits; a positive
value limits the selection. Ties use stable IDs, so grid sorting does not affect
selection. Optional `Groups` filters the hits; empty means all groups. A miss
leaves nothing selected. Nonfinite pointer coordinates are ignored.

`Carry` never hit-tests again. The selected IDs survive grid reordering, and
new circles under the pointer are not added. Motion to the latest target is
spread over the next positive step's substeps. Each core is clamped to the world
bounds, including after a resize; individual clamping can change the spacing of
a multiple-circle selection at a wall. `Carry` and `Drop` are harmless without a
selection. `Drop` restores original masses and clears velocity (no throwing),
including when called before the next step.

Held circles behave as moving anchors: collisions, forces, and bridge solvers
cannot displace them. Free circles still collide with them, and connected
circles follow through spring forces or distance constraints. Bridge damping
uses the anchor's movement velocity. Bridges do not automatically select their
other endpoints. Break distances remain active and are checked before constraint
corrections; fast pointer motion can break a breakable bridge. If both endpoints
are held, their prescribed positions take precedence over bridge limits and
mutual core separation. Unbreakable bridges remain attached even when those
limits cannot be satisfied. As usual, substeps are discrete, so very fast pointer
motion can pass through circles between collision checks.

The bridges example uses left drag for one circle, Shift+left drag for all hits,
and right click for repulsion.

## External impulses

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
Bridge forces are accumulated serially after cell jobs finish. Constraint,
contact-projection, and damping passes also run serially, so bridges sharing
endpoints do not race with each other or with collisions.

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
