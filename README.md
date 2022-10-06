# Like sync.WaitGroup and errgroup.Group had a baby

[![PkgGoDev](https://pkg.go.dev/badge/github.com/alecthomas/waitgroup)](https://pkg.go.dev/github.com/alecthomas/waitgroup) [![CI](https://github.com/alecthomas/assert/actions/workflows/ci.yml/badge.svg)](https://github.com/alecthomas/assert/actions/workflows/ci.yml) [![Go Report Card](https://goreportcard.com/badge/github.com/alecthomas/waitgroup)](https://goreportcard.com/report/github.com/alecthomas/waitgroup) [![Slack chat](https://img.shields.io/static/v1?logo=slack&style=flat&label=slack&color=green&message=gophers)](https://gophers.slack.com/messages/CN9DS8YF3)

## Motivation

A pattern I encounter fairly frequently with long-running services is the
following lifecycle:

```go
// Start the service in the background, or return any startup errors.
Start() error
// Wait for the service to complete and return any errors.
Wait() error
// Close stops the service and returns any errors.
Close() error
```

The `Start()` function usually creates some number of background tasks,
listeners, etc. Then at shutdown it will need to wait for them, in addition to
performing other synchronous shutdown tasks such as closing connections or
cleaning up resources. A combination of `sync.WaitGroup` and `errgroup.Group`
can solve this, but there are a couple of problems:

1. There is no safe way to balance `Add()` and `Done()` calls when they are in
    separate methods, `Start()` and `Close()` in this case.
2. `errgroup.Group` does not support explicit `Add()` and `Done()` calls, so a
   separate and unrelated `sync.WaitGroup` is required.

## Usage

Here's a full example illustrating how this is typically used (not tested):

```go

// ShutdownDeadline is the maximum time to wait for the service to shutdown.
const ShutdownDeadline = time.Second * 60

type Service struct {
    wg    *waitgroup.Group // Wait for all aspects of the service to stop.
    conns *waitgroup.Group // Connection draining group.

    cancel func()          // Call to forcibly terminate background tasks.

    l net.Listener
    listenTask waitgroup.ID // waitgroup ID associated with stopping the listener.
}

func (s *Service) Start(addr string) error {
    // Create a context for the waitgroups, along with a cancellation function
    // that we will use to tell the server to shut down.
    ctx, cancel := context.WithCancel(context.Background())
    s.cancel = cancel

    s.wg = waitgroup.WithContext(ctx)
    s.conns = waitgroup.WithContext(ctx)

    s.wg.Link(s.conns) // Outer group waits for connection draining group.

    l, err := net.Listen("tcp", addr)
    if err != nil {
        return err
    }
    s.l = l
    // Record a task ID for the listener, decremented when the listener is stopped.
    s.listenTask = s.wg.Add()

    // Start a task for the accept loop (not implemented here). The accept loop
    // tracks each new connection in the conns waitgroup.
    s.wg.Go(s.accept)

    return nil
}

func (s *Service) Close() error {
    ctx, cancel := context.WithTimeout(context.Background(), ShutdownDeadline)
    defer cancel()

    // Tell the waitgroups to shutdown.
    s.cancel()

    merr := multierror.Errors{}

    // First, wait for connection drain to complete.
    err := s.conns.Wait(ctx)
    if err != nil {
        merr.Add(err)
    }

    // Close the listener and mark the task as done.
    err = s.l.Close()
    if err != nil {
        merr.Add(err)
    }
    s.listenTask.Done(s.listenTask)

    // Finally, wait for the outer group to complete, at which point everything will be cleaned up.
    err = s.wg.Wait(ctx)
    if err != nil {
        merr.Add(err)
    }

    return merr
}

func (s *Service) Wait() error {
    return s.wg.Wait()
}
```