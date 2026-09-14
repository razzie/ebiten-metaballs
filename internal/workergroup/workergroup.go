package workergroup

import (
	"errors"
	"sync"
)

type Job struct {
	wg      *sync.WaitGroup
	sem     chan struct{}
	errChan chan<- error
}

func (j Job) Start() {
	j.sem <- struct{}{}
}

func (j Job) Fork() *Job {
	j.wg.Add(1)
	return &Job{
		wg:      j.wg,
		sem:     j.sem,
		errChan: j.errChan,
	}
}

func (j Job) Done(err *error) {
	if err != nil && *err != nil && j.errChan != nil {
		j.errChan <- *err
	}
	j.wg.Done()
	<-j.sem
}

type WorkerGroup interface {
	TakeJob() *Job
	Wait() error
}

type workerGroup struct {
	wg       sync.WaitGroup
	sem      chan struct{}
	errChan  chan error
	finalErr chan error
}

func New(maxWorkers int) WorkerGroup {
	wg := &workerGroup{
		sem:      make(chan struct{}, maxWorkers),
		errChan:  make(chan error),
		finalErr: make(chan error, 1),
	}
	go func() {
		var finalErr error
		for err := range wg.errChan {
			if finalErr == nil {
				finalErr = err
			} else {
				finalErr = errors.Join(finalErr, err)
			}
		}
		wg.finalErr <- finalErr
	}()
	return wg
}

func (wg *workerGroup) TakeJob() *Job {
	wg.wg.Add(1)
	return &Job{
		wg:      &wg.wg,
		sem:     wg.sem,
		errChan: wg.errChan,
	}
}

func (wg *workerGroup) Wait() error {
	wg.wg.Wait()
	close(wg.errChan)
	return <-wg.finalErr
}
