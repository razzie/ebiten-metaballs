package workergroup

import (
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
)

func TestWaitWithoutJobs(t *testing.T) {
	if err := New(1).Wait(); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
}

func TestWaitIncludesUnstartedJobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wg := New(1)
		job := wg.TakeJob()
		result := make(chan error, 1)
		go func() { result <- wg.Wait() }()

		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("Wait() returned before the job started: %v", err)
		default:
		}

		job.Start()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("Wait() returned before Done(): %v", err)
		default:
		}

		job.Done(nil)
		if err := <-result; err != nil {
			t.Fatalf("Wait() = %v, want nil", err)
		}
	})
}

func TestWorkerLimit(t *testing.T) {
	for _, limit := range []int{1, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				wg := New(limit)
				started := make(chan struct{}, limit+1)
				release := make(chan struct{})
				for i := 0; i < limit+1; i++ {
					job := wg.TakeJob()
					go func() {
						job.Start()
						defer job.Done(nil)
						started <- struct{}{}
						<-release
					}()
				}

				synctest.Wait()
				if got := len(started); got != limit {
					t.Fatalf("started jobs = %d, want %d", got, limit)
				}
				for i := 0; i < limit; i++ {
					<-started
				}

				// Completing one job must let the queued job start.
				release <- struct{}{}
				synctest.Wait()
				if got := len(started); got != 1 {
					t.Fatalf("newly started jobs = %d, want 1", got)
				}
				close(release)
				if err := wg.Wait(); err != nil {
					t.Fatalf("Wait() = %v, want nil", err)
				}
			})
		})
	}
}

func TestWaitIncludesForkedJobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wg := New(1)
		parent := wg.TakeJob()
		parent.Start()
		result := make(chan error, 1)
		go func() { result <- wg.Wait() }()
		synctest.Wait()

		// Jobs can add children while Wait is already blocked.
		child := parent.Fork()
		sibling := parent.Fork()
		parent.Done(nil)
		child.Start()
		grandchild := child.Fork()
		child.Done(nil)

		for _, job := range []*Job{sibling, grandchild} {
			synctest.Wait()
			select {
			case err := <-result:
				t.Fatalf("Wait() returned with an unfinished forked job: %v", err)
			default:
			}
			job.Start()
			job.Done(nil)
		}
		if err := <-result; err != nil {
			t.Fatalf("Wait() = %v, want nil", err)
		}
	})
}

func TestForkSharesWorkerLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wg := New(1)
		parent := wg.TakeJob()
		parent.Start()
		child := parent.Fork()
		started := make(chan struct{})
		release := make(chan struct{})
		go func() {
			child.Start()
			defer child.Done(nil)
			close(started)
			<-release
		}()

		synctest.Wait()
		select {
		case <-started:
			t.Fatal("forked job started while parent occupied the only worker slot")
		default:
		}

		parent.Done(nil)
		<-started
		close(release)
		if err := wg.Wait(); err != nil {
			t.Fatalf("Wait() = %v, want nil", err)
		}
	})
}

func TestForkReportsErrors(t *testing.T) {
	wg := New(3)
	parent := wg.TakeJob()
	child := parent.Fork()
	grandchild := child.Fork()
	want := []error{
		errors.New("parent failure"),
		errors.New("child failure"),
		errors.New("grandchild failure"),
	}
	for i, job := range []*Job{parent, child, grandchild} {
		go func() {
			job.Start()
			var err error
			defer job.Done(&err)
			err = want[i]
		}()
	}
	got := wg.Wait()
	for _, err := range want {
		if !errors.Is(got, err) {
			t.Errorf("Wait() = %v, want to include %v", got, err)
		}
	}
}

func TestWaitErrors(t *testing.T) {
	first := errors.New("first failure")
	second := errors.New("second failure")
	for _, test := range []struct {
		name string
		errs []error
	}{
		{name: "successful jobs", errs: []error{nil, nil}},
		{name: "single error", errs: []error{first}},
		{name: "multiple errors", errs: []error{first, second}},
		{name: "errors mixed with nil", errs: []error{nil, first, nil, second, nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			wg := New(2)
			for _, err := range test.errs {
				job := wg.TakeJob()
				go func() {
					job.Start()
					var jobErr error
					defer job.Done(&jobErr)
					// Deferred Done must read the value assigned after defer.
					jobErr = err
				}()
			}
			got := wg.Wait()
			wantError := false
			for _, err := range test.errs {
				if err != nil {
					wantError = true
					if !errors.Is(got, err) {
						t.Errorf("Wait() = %v, want to include %v", got, err)
					}
				}
			}
			if !wantError && got != nil {
				t.Fatalf("Wait() = %v, want nil", got)
			}
		})
	}
}
