package softbody

import (
	"fmt"
	"math"
	"testing"
)

func TestGravityFreeFlight(t *testing.T) {
	for _, gravity := range []Point{{}, {0, 1}, {-.5, .75}} {
		for _, substeps := range []int{1, 4} {
			for _, workers := range []int{1, 4} {
				t.Run(fmt.Sprintf("gravity=%v/substeps=%d/workers=%d", gravity, substeps, workers), func(t *testing.T) {
					cfg := DefaultConfig()
					cfg.GravityX, cfg.GravityY = gravity.X, gravity.Y
					cfg.Substeps, cfg.Workers, cfg.LinearDamping = substeps, workers, -1
					s := New(cfg)
					// Full SIMD vectors and a tail, unequal masses and groups,
					// and reverse spatial order to exercise grid sorting.
					const count = 65
					if err := s.SetBounds(Bounds{MaxX: 2 * (count + 1), MaxY: 2}); err != nil {
						t.Fatal(err)
					}
					masses := [...]float32{.25, 1, 4}
					for i := range count {
						_, err := s.AddCircle(CircleSpec{
							X: float32(2 * (count - i)), Y: 1, VX: .25, VY: -.125,
							InnerRadius: .1, OuterRadius: .1,
							Mass: masses[i%len(masses)], Group: Group(i % 3),
						})
						if err != nil {
							t.Fatal(err)
						}
					}
					const dt = float32(.25)
					before := bridgeCircles(s)
					s.Step(0)
					s.Step(-dt)
					for i, c := range bridgeCircles(s) {
						if c != before[i] {
							t.Fatal("nonpositive step applied gravity")
						}
					}
					s.Step(dt)
					s.Step(dt)
					const elapsed = float64(2 * dt)
					// Semi-implicit Euler uses the updated velocity for movement.
					displacement := elapsed * (elapsed + float64(dt)/float64(substeps)) / 2
					for i, c := range bridgeCircles(s) {
						nearBridge(t, "VX", float64(c.VX), .25+float64(gravity.X)*elapsed)
						nearBridge(t, "VY", float64(c.VY), -.125+float64(gravity.Y)*elapsed)
						nearBridge(t, "X", float64(c.X), float64(before[i].X)+.25*elapsed+float64(gravity.X)*displacement)
						nearBridge(t, "Y", float64(c.Y), 1-.125*elapsed+float64(gravity.Y)*displacement)
					}
				})
			}
		}
	}
}

func TestGravityWithMassDependentDamping(t *testing.T) {
	for _, substeps := range []int{1, 4} {
		t.Run(fmt.Sprint(substeps), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.GravityX, cfg.GravityY = -.5, 1
			cfg.Substeps = substeps
			cfg.LinearDamping, cfg.LinearDampingMassFactor = 2, .5
			s := New(cfg)
			masses := [...]float32{.25, 1, 4}
			for i, mass := range masses {
				if _, err := s.AddCircle(CircleSpec{X: .2 + float32(i)*.3, Y: .5,
					InnerRadius: .01, OuterRadius: .02, Mass: mass}); err != nil {
					t.Fatal(err)
				}
			}
			const dt = float32(.25)
			s.Step(dt)
			for i, c := range bridgeCircles(s) {
				rate := float64(cfg.LinearDamping * (1 + cfg.LinearDampingMassFactor*masses[i]))
				decay := math.Pow(1/(1+rate*float64(dt)/float64(substeps)), float64(substeps))
				nearBridge(t, "VX", float64(c.VX), float64(cfg.GravityX)/rate*(1-decay))
				nearBridge(t, "VY", float64(c.VY), float64(cfg.GravityY)/rate*(1-decay))
			}
		})
	}
}

func TestGravityIgnoresKinematicCirclesUntilReleased(t *testing.T) {
	s := dragWorld()
	s.cfg.GravityX, s.cfg.GravityY = -.5, 1
	anchor := addDragCircle(t, s, .25, .25, 4, 0)
	held := addDragCircle(t, s, .75, .25, 2, 1)
	if err := s.AnchorCircle(anchor, .25, .25); err != nil {
		t.Fatal(err)
	}
	if ids := s.Drag(DragSpec{X: .75, Y: .25}); len(ids) != 1 || ids[0] != held {
		t.Fatalf("drag selected %v, want circle %d", ids, held)
	}
	s.Carry(.75, .5)
	const dt = float32(.125)
	s.Step(dt)
	circles := bridgeCircles(s)
	if c := circles[0]; c.X != .25 || c.Y != .25 || c.VX != 0 || c.VY != 0 {
		t.Fatalf("gravity moved anchor: %+v", c)
	}
	if c := circles[1]; c.X != .75 || c.Y != .5 || c.VX != 0 || c.VY != 2 {
		t.Fatalf("gravity changed drag movement: %+v", c)
	}
	if !s.ReleaseCircle(anchor) {
		t.Fatal("failed to release anchor")
	}
	s.Drop()
	s.Step(dt)
	for _, c := range bridgeCircles(s) {
		nearBridge(t, "released VX", float64(c.VX), float64(s.cfg.GravityX*dt))
		nearBridge(t, "released VY", float64(c.VY), float64(s.cfg.GravityY*dt))
	}
}

func TestGravityAddsToBridgeForces(t *testing.T) {
	s, left, right := bridgeWorld(t, .5, 1)
	s.cfg.GravityX, s.cfg.GravityY = -.5, 1
	addTestBridge(t, s, BridgeSpec{A: left, B: right, MaxDistance: .25, AttractForce: 2})
	s.Step(.1)
	for _, c := range s.Snapshot(nil) {
		wantVX := float64(0) // Gravity cancels the bridge acceleration at mass 1.
		if c.ID == right {
			wantVX = -.075 // Gravity and bridge attraction add at mass 2.
		}
		nearBridge(t, "VX", float64(c.VX), wantVX)
		nearBridge(t, "VY", float64(c.VY), .1)
	}
}
