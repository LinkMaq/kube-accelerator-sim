// Package memory provides the deterministic Cluster Simulation Inventory
// source Adapter used by Module tests and local fixtures.
package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory"
)

type Adapter struct {
	mu      sync.Mutex
	opened  bool
	updates chan inventory.Observation
}

func New() *Adapter {
	return &Adapter{updates: make(chan inventory.Observation, 1)}
}

func (adapter *Adapter) Open(
	_ context.Context,
	selection cluster.TargetSelection,
) (inventory.SourceStream, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.opened {
		return nil, fmt.Errorf("memory inventory source supports one stream")
	}
	adapter.opened = true
	return &stream{
		target:  inventory.Target{ContextName: selection.ContextName},
		updates: adapter.updates,
		done:    make(chan struct{}),
	}, nil
}

func (adapter *Adapter) Publish(observation inventory.Observation) {
	select {
	case adapter.updates <- observation:
		return
	default:
	}
	select {
	case <-adapter.updates:
	default:
	}
	adapter.updates <- observation
}

type stream struct {
	target  inventory.Target
	updates chan inventory.Observation
	done    chan struct{}
	once    sync.Once
}

func (stream *stream) Target() inventory.Target { return stream.target }

func (stream *stream) Next(ctx context.Context) (inventory.Observation, error) {
	select {
	case observation := <-stream.updates:
		return observation, nil
	case <-stream.done:
		return inventory.Observation{}, fmt.Errorf("memory inventory stream is closed")
	case <-ctx.Done():
		return inventory.Observation{}, ctx.Err()
	}
}

func (stream *stream) Close() error {
	stream.once.Do(func() { close(stream.done) })
	return nil
}
