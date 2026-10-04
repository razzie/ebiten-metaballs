# Scrolling worlds

Scrolling uses one `State`, a movable world rectangle, and a rendering camera.
Boundary removal, circle/polygon translation, and origin shifting are implemented.
Content is added with the existing circle, polygon, and bridge methods; extending
bounds does not generate content or restore removed entities.

## Changing the active rectangle

```go
cfg := softbody.DefaultConfig()
cfg.BoundaryMode = softbody.BoundaryRemove
world := softbody.New(cfg)

// Retain a margin around the visible region.
err := world.SetBounds(softbody.Bounds{
    MinX: cameraX - margin,
    MaxX: cameraX + viewWidth + margin,
    MinY: levelMinY,
    MaxY: levelMaxY,
})
// Handle err, then add content in newly exposed space before Step.
```

`Bounds()` returns the current rectangle. `SetBounds` extends, shrinks, or moves
it without translating surviving entities. Bounds must be finite with finite
positive dimensions. A separate append/chunk-import API is unnecessary.

The rendering camera is independent. `UVTransform.SetOffset` controls which
world coordinates map to the screen; `ScreenToUV` converts pointer input using
the same transform. For `world = screen * scale + offset`, increasing the offset
scrolls toward increasing world coordinates. Physics and rendering must use the
same units. The water example animates rendering circles without `softbody`.

## Boundary policy

Set `Config.BoundaryMode` when creating the world, or call
`SetBoundaryMode(mode)` on the simulation goroutine. Unknown constructor modes
fall back to walls; the setter rejects unknown modes atomically.

| Behavior | BoundaryWalls (default) | BoundaryRemove |
| --- | --- | --- |
| Circle approaches an edge | Shell response and core confinement | No boundary force, clamp, or bounce |
| Circle center leaves the rectangle | Core remains confined | Remove circle and incident bridges |
| Bounds shrink or move | Immediately confine cores | Immediately remove outside circles and polygon boxes |
| Bounds expand | Keep existing coordinates | Keep existing coordinates |
| Drag target is outside | Clamp core to walls | Allow exits; clean up deleted selections |
| Polygon is wholly outside | Retain it | Remove it |
| Polygon partially overlaps | Retain it | Retain it without clipping |

Anchored circles keep their positions when walls change. Removal mode still
deletes outside anchors and their bridges. See [anchoring](README.md#anchoring).

Circle removal uses centers strictly outside the closed rectangle. Exact-edge
centers survive, even if a core or shell extends beyond the edge. This keeps
surviving centers within grid coverage. Polygon removal uses conservative AABB
intersection: disjoint boxes are removed; touching or overlapping boxes survive.
An enclosing polygon is retained even if all its vertices are outside.

Removal deletes incident bridges regardless of `BreakDistance`, without an
additional breaking impulse. Surviving circles keep their IDs; deleted IDs are
never reused. Deleted held circles leave the selection without releasing its
surviving members. Snapshots contain only surviving entities.

Removal happens at safe discrete checkpoints after prescribed drag movement,
after integration and polygon sweeps, and after complete bridge constraint and
contact-projection sweeps. Workers and pair loops finish before particle storage
is compacted. An escaped integrated endpoint is deleted before a bridge can
pull it back. This is not continuous detection of every path crossing or every
transient position inside a solver sweep.

`SetBounds`, switching to removal, translations, polygon penetration recovery,
and `Drop` applying a pending target also perform the relevant immediate cleanup.
Outside additions are rejected before allocating IDs: circles need an in-bounds
center (including after initial polygon recovery), and polygon boxes need to
intersect bounds. Wall mode requires the largest outer diameter to fit the
rectangle; removal mode does not. Switching to walls validates fit before
confinement and leaves state unchanged on error.

## Translation and origin shifting

| Method | Displacement | Other behavior |
| --- | --- | --- |
| `TranslateCircles(dx, dy)` | Add to unanchored circles and active drag target | Bounds, polygons, and queued impulses stay fixed |
| `TranslatePolygons(dx, dy)` | Add to every polygon vertex and refresh boxes | Recover unanchored circle penetrations immediately |
| `ShiftOrigin(dx, dy)` | Subtract from every world-coordinate quantity | Preserve IDs, velocities, masses, radii, and bridge limits |

Circle translation includes held circles and preserves velocities. Polygon-core
intersections and cores outside closed walls are rejected atomically. In removal
mode, centers translated outside are removed with their bridges. This is a
teleport, not a sweep against terrain between the initial and final positions.

Polygon translation preserves IDs and revalidates geometry after float32
rounding. It immediately projects unanchored penetrations, as `AddPolygon` does;
that can move circles or bounce their velocities. Removal mode discards wholly
outside polygon boxes before recovery and removes circles pushed outside.
Bounds, drag targets, and queued impulse sources remain fixed. The operation
repositions static obstacles; moving platforms would additionally need velocity
and continuous collision handling for the moving obstacle itself.

Origin shifting transforms circles, polygons and their boxes, bounds, the active
drag target, and pending radial-impulse sources together. It does not project,
confine, or remove bodies. Invalid/nonfinite results, collapsed bounds, and
invalid polygon geometry are rejected before any mutation. The grid rebuilds
before the next solve. Float32 rounding and grid ordering can prevent
bit-identical subsequent trajectories.

All mutations run on the simulation goroutine. Origin shifting locks the impulse
queue while validating and shifting it, so concurrent queue writes remain safe.
The application must synchronize the coordinate-frame change with its input
producers: a mutex cannot infer whether a new source supplied after rebasing
still uses the old frame. Shift any application-owned camera and cached world
positions too, and keep accumulated global offsets in float64 or integer level
coordinates. Rebase occasionally, rather than using this as every camera update.

## Grid and storage

The grid supports negative and offset coordinates, and rebuilds before each
substep and each bridge-contact pass. Its neighbor stencil is conservative for
shells and optional attraction. Circle sorting and removal maintain a live-ID
map, so bridges and selections continue to refer to the same surviving circles.
Removal recomputes the largest live radius and invalidates geometry if it changes.

The grid still allocates `qCount*rCount` cells, clears all counts, and scans all
cells during each counting sort. Active-cell jobs reduce contact work, but not
empty-cell costs. Polygon shell-candidate rebuilds test every polygon against
every cell; polygon sweeps/core recovery scan loaded polygons with AABB rejection.

The initial investigation measured these allocations with a temporary Go test
overlay, spacing 0.1, and one circle of outer radius 0.02:

| Bounds size | Allocated cells | Occupied cells |
| --- | ---: | ---: |
| 1 × 1 | 391 | 1 |
| 10 × 1 | 1,921 | 1 |
| 1 × 10 | 9,075 | 1 |
| 100 × 1 | 17,221 | 1 |
| 1 × 100 | 690,200 | 1 |

These are allocation counts, not performance benchmarks. Vertical growth is
particularly expensive because rectangular axial allocation widens its q range
as its r range grows. Moving a bounded rectangle and removing old entities keeps
the problem bounded; continually extending it does not.

Bounds changes currently allocate fresh grid arrays on the next step. Array
reuse, separate polygon-candidate invalidation, sparse grid blocks, and polygon
spatial indexing remain possible measured optimizations. They are not required
by the implemented API.

## Validation

Tests cover all four exit directions, exact-edge survival, polygon integration,
mode changes, polygon retention/candidate compaction, bridge and partial drag
cleanup, constraint/contact-created exits, sparse ID churn, full evacuation and
repopulation, radius recomputation, and scalar/SIMD integration without walls.
Translation tests cover held bodies and pending targets, moved collision
geometry, rebased simulations with bridges/polygons/impulses, atomic failures,
and concurrent impulse queuing during origin shifts.

Run `go test ./...`, `GOEXPERIMENT=simd go test ./...`, and
`go test -race ./softbody` to verify these paths.
