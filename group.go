// Package waitgroup provides a simple and safe way to wait for a group of tasks
// to complete.
//
// It combines sync.WaitGroup and errgroup.Group, but with a few differences:
//
// 1. Add/Done pairs must balance exactly, and are tracked by an ID.
// 2. It supports linking to other groups, to create trees of tasks.
// 3. Task functions are passed a context.
//
// It is safe to use from multiple goroutines.
package waitgroup

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrUnknownID is returned by Done() if the ID does not have a corresponding Add().
var ErrUnknownID = fmt.Errorf("waitgroup: unknown ID")

// A Link that can be waited on.
type Link interface {
	Wait(context.Context) error
}

// An ID is an opaque identifier that uniquely identifies a task in a Group.
type ID struct{ uint64 }

// IDs is a concurrent-safe set of IDs.
//
// The zero value is usable. It is not safe to copy.
type IDs struct {
	_   noCopy
	ids sync.Map
}

// IDsFromSlice creates an IDs from a slice.
func IDsFromSlice(ids ...ID) *IDs {
	var i IDs
	for _, id := range ids {
		i.Add(id)
	}
	return &i
}

// Add an ID to the set.
func (i *IDs) Add(id ID) { i.ids.Store(id, true) }

// Remove an ID from the set and return true if it was present.
func (i *IDs) Remove(id ID) bool { _, ok := i.ids.LoadAndDelete(id); return ok }

// Range over the IDs in the set.
func (i *IDs) Range(f func(ID) bool) {
	i.ids.Range(func(k, _ interface{}) bool { return f(k.(ID)) })
}

// Group is a collection of tasks that can be waited on.
//
// The zero value is ready to use, but a context can be provided by calling
// WithContext.
type Group struct {
	_ noCopy

	id uint64

	ctx context.Context // nolint

	wg sync.WaitGroup

	linkedMu sync.Mutex
	linked   []Link

	errOnce sync.Once
	err     error

	tracked sync.Map // Tracked IDs
}

// WithContext creates a new Group with a context.
func WithContext(ctx context.Context) *Group {
	return &Group{ctx: ctx}
}

// Add a task to the wait group and return an ID that can be used to mark it as done.
func (g *Group) Add() ID {
	g.wg.Add(1)
	id := ID{atomic.AddUint64(&g.id, 1)}
	g.tracked.Store(id, true)
	return id
}

// Link this Group to another task's completion.
//
// Links are waited on in the order they are added, after the Group's own tasks.
// A link can be added prior to any tasks being added to it.
func (g *Group) Link(other Link) {
	g.linkedMu.Lock()
	g.linked = append(g.linked, other)
	g.linkedMu.Unlock()
}

// Sub creates a linked sub-group.
//
// The sub-group inherits the parent's context.
func (g *Group) Sub() *Group {
	sub := &Group{ctx: g.ctx}
	g.Link(sub)
	return sub
}

// Go runs a function in a goroutine, incrementing and decrementing the wait group on start and finish.
func (g *Group) Go(f func(ctx context.Context) error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ctx := g.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		err := f(ctx)
		if err != nil {
			g.setError(err)
		}
	}()
}

// Done marks a previously added task as done or errors if the ID is unknown or already complete.
func (g *Group) Done(id ID) error {
	_, ok := g.tracked.LoadAndDelete(id)
	if !ok {
		return ErrUnknownID
	}
	g.wg.Done()
	return nil
}

// Wait for all tasks to complete or ctx to be done.
func (g *Group) Wait(ctx context.Context) error {
	wait := make(chan struct{})
	go func() {
		g.wg.Wait()
		g.linkedMu.Lock()
		linked := make([]Link, len(g.linked))
		copy(linked, g.linked)
		g.linkedMu.Unlock()
		for _, l := range linked {
			err := l.Wait(ctx)
			// Don't propagate context errors.
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				g.setError(err)
			}
		}
		close(wait)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()

	case <-wait:
		return g.err
	}
}

func (g *Group) setError(err error) {
	g.errOnce.Do(func() { g.err = err })
}

type noCopy struct{}

// Lock is a no-op used by -copylocks checker from `go vet`.
func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
