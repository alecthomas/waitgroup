package waitgroup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
)

func TestDoneWithInvalidID(t *testing.T) {
	g := Group{}
	err := g.Done(ID{1})
	assert.True(t, errors.Is(err, ErrUnknownID), "expected ErrUnknownID but got %v", err)
}

func TestAddDone(t *testing.T) {
	g := Group{}
	id := g.Add()
	err := g.Done(id)
	assert.NoError(t, err)
	err = g.Wait(context.Background())
	assert.NoError(t, err)
}

func TestWaitTimeout(t *testing.T) {
	g := Group{}
	g.Go(func(ctx context.Context) error {
		time.Sleep(time.Second * 5)
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := g.Wait(ctx)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected DeadlineExceeded but got %v", err)
}

func TestWaitError(t *testing.T) {
	var ErrTask = errors.New("task")
	g := Group{}
	g.Go(func(ctx context.Context) error {
		return ErrTask
	})
	err := g.Wait(context.Background())
	assert.True(t, errors.Is(err, ErrTask), "expected ErrTask but got %v", err)
	err = g.Wait(context.Background())
	assert.True(t, errors.Is(err, ErrTask), "expected ErrTask but got %v", err)
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := WithContext(ctx)
	g.Go(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})
	cancel()
	err := g.Wait(context.Background())
	assert.NoError(t, err)
}

func TestLink(t *testing.T) {
	g := &Group{}
	sub := g.Sub()

	g.Link(sub)

	id := sub.Add()

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
	defer cancel()

	err := g.Wait(ctx)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), true, "expected DeadlineExceeded but got %v", err)

	err = sub.Done(id)
	assert.NoError(t, err)

	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond*500)
	defer cancel()

	err = g.Wait(ctx)
	assert.NoError(t, err)
}

func FuzzGroup(f *testing.F) {
	// tasks, goroutines, sub-groups
	f.Add(10, 10, 10)
	f.Fuzz(func(t *testing.T, tasks, goroutines, subgroups int) {
		if tasks == 0 || goroutines == 0 || subgroups == 0 {
			return
		}
		if tasks < 0 {
			tasks = -tasks
		}
		if goroutines < 0 {
			goroutines = -goroutines
		}
		if subgroups < 0 {
			subgroups = -subgroups
		}

		errch := make(chan error, tasks)
		g := &Group{}

		for i := 0; i < subgroups; i++ {
			sub := g.Sub()
			runTasksInGroup(errch, sub, tasks, goroutines)
		}

		runTasksInGroup(errch, g, tasks, goroutines)

		err := g.Wait(context.Background())
		close(errch)
		assert.NoError(t, err)
		assert.NoError(t, <-errch)
	})
}

func runTasksInGroup(errch chan error, g *Group, tasks, goroutines int) {
	started := make([]ID, tasks)
	for i := 0; i < tasks; i++ {
		started[i] = g.Add()
	}
	for i := 0; i < goroutines; i++ {
		g.Go(func(ctx context.Context) error {
			time.Sleep(time.Millisecond * 10)
			return nil
		})
	}
	go func() {
		for _, id := range started {
			err := g.Done(id)
			if err != nil {
				errch <- err
			}
		}
	}()
}
