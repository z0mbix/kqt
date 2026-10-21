package kube

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type namespaceWatchUpdate struct {
	namespace string
	update    EventUpdate
	done      bool
	err       error
}

type namespaceWatchState struct {
	state, message  string
	settled, synced bool
	err             error
}

// Each namespace keeps its own resource version and reset boundary. Callbacks
// are serialized here, and a denied namespace never stops healthy watches.
func followNamespaceScope(parent context.Context, q Query, emit func(EventUpdate), follow func(context.Context, Query, func(EventUpdate)) error) error {
	if !q.Resource.Namespaced {
		q.Namespace = ""
		return follow(parent, q, emit)
	}
	names, err := NamespaceNames(q.Namespace)
	if err != nil {
		return err
	}
	if len(names) < 2 {
		q.Namespace = strings.Join(names, ",")
		return follow(parent, q, emit)
	}
	ctx, cancel := context.WithCancel(parent)
	updates := make(chan namespaceWatchUpdate, 64)
	var tasks sync.WaitGroup
	defer func() { cancel(); tasks.Wait() }()
	states := map[string]*namespaceWatchState{}
	for _, name := range names {
		states[name] = &namespaceWatchState{}
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			query := q
			query.Namespace = name
			send := func(value namespaceWatchUpdate) {
				select {
				case updates <- value:
				case <-ctx.Done():
				}
			}
			err := follow(ctx, query, func(update EventUpdate) {
				send(namespaceWatchUpdate{namespace: name, update: update})
			})
			send(namespaceWatchUpdate{namespace: name, done: true, err: err})
		}()
	}
	remaining := len(names)
	for remaining > 0 {
		select {
		case <-ctx.Done():
			return nil
		case value := <-updates:
			if ctx.Err() != nil {
				return nil
			}
			state := states[value.namespace]
			update := value.update
			if value.done {
				remaining--
				state.settled, state.err = true, value.err
				if state.err == nil {
					state.err = fmt.Errorf("watch ended")
				}
			} else {
				if update.State != "" {
					state.state, state.message = update.State, update.Message
				}
				if update.Synced {
					state.settled, state.synced = true, true
				}
				if update.Type == "" || update.Object.GetNamespace() == value.namespace {
					forward := update
					forward.Namespace, forward.State, forward.Message = value.namespace, "", ""
					emit(forward)
				}
			}
			if update.State == "" && !update.Synced && !value.done {
				continue
			}
			failures := &NamespaceErrors{Total: len(names), Failures: map[string]error{}}
			live, settled := 0, true
			for name, status := range states {
				settled = settled && status.settled
				if status.err != nil {
					failures.Failures[name] = status.err
				} else if status.message != "" {
					failures.Failures[name] = fmt.Errorf("%s", status.message)
				} else if status.synced {
					failures.Available++
				}
				if status.err == nil && status.state == "live" {
					live++
				}
			}
			status := EventUpdate{ScopeStatus: true, Synced: settled}
			switch {
			case len(failures.Failures) > 0:
				status.State, status.Message = "partial", failures.Error()
			case live == len(names):
				status.State = "live"
			default:
				status.State = fmt.Sprintf("connecting %d/%d namespaces", live, len(names))
			}
			emit(status)
			if remaining == 0 {
				return failures
			}
		}
	}
	return nil
}
