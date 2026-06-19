package hidapi

import (
	"context"
	"time"

	"github.com/bto-labs/mirajazz-go/internal/mirajazz"
)

// DefaultWatchInterval is the polling cadence used by [Watch] when the caller
// passes a non-positive interval.
const DefaultWatchInterval = time.Second

// Watch emits connect/disconnect events for devices matching any query until
// ctx is cancelled. It is the port of mirajazz::device::DeviceWatcher.
//
// async-hid delivers OS hot-plug events; hidapi exposes none, so this polls
// List at interval and diffs the result. The first poll seeds the known set and
// does NOT emit events for already-connected devices (matching the Rust
// watcher, whose stream "only watches new events"); use [List] to discover what
// is already attached.
//
// The returned channel is closed when ctx is done.
func Watch(ctx context.Context, queries []mirajazz.DeviceQuery, interval time.Duration) (<-chan mirajazz.LifecycleEvent, error) {
	if interval <= 0 {
		interval = DefaultWatchInterval
	}

	// Seed the known set before returning so initial devices are not reported.
	seed, err := List(queries)
	if err != nil {
		return nil, err
	}
	known := make(map[mirajazz.DeviceInfo]struct{}, len(seed))
	for _, info := range seed {
		known[info] = struct{}{}
	}

	events := make(chan mirajazz.LifecycleEvent)

	go func() {
		defer close(events)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			current, err := List(queries)
			if err != nil {
				// Transient enumeration failure: skip this tick.
				continue
			}

			seen := make(map[mirajazz.DeviceInfo]struct{}, len(current))
			for _, info := range current {
				seen[info] = struct{}{}
				if _, ok := known[info]; !ok {
					known[info] = struct{}{}
					if !send(ctx, events, mirajazz.LifecycleEvent{Kind: mirajazz.DeviceConnected, Info: info}) {
						return
					}
				}
			}

			for info := range known {
				if _, ok := seen[info]; !ok {
					delete(known, info)
					if !send(ctx, events, mirajazz.LifecycleEvent{Kind: mirajazz.DeviceDisconnected, Info: info}) {
						return
					}
				}
			}
		}
	}()

	return events, nil
}

// send delivers ev unless ctx is cancelled first; reports whether it succeeded.
func send(ctx context.Context, ch chan<- mirajazz.LifecycleEvent, ev mirajazz.LifecycleEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case ch <- ev:
		return true
	}
}
