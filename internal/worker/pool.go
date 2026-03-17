package worker

import (
	"context"
	"sync"
)

type Pool struct {
	size    int
	tasks   chan func()
	wg      sync.WaitGroup
}

func NewPool(size int) *Pool {
	return &Pool{
		size:  size,
		tasks: make(chan func(), size*2),
	}
}

func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.size; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case task, ok := <-p.tasks:
					if !ok {
						return
					}
					task()
				}
			}
		}()
	}
}

func (p *Pool) Submit(task func()) {
	p.tasks <- task
}

func (p *Pool) Stop() {
	close(p.tasks)
	p.wg.Wait()
}
