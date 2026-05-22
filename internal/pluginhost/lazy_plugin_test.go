package pluginhost

import (
	"context"
	"encoding/json"
	"net"
	"net/rpc"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

type mockPlugin struct {
	papi.Plugin
	startCount atomic.Int64
	killed     atomic.Bool
	exited     chan struct{}
}

func (p *mockPlugin) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	return papi.Descriptor{
		PluginID: "test.lazy",
		Name:     "Lazy Test Plugin",
	}, nil
}

func (p *mockPlugin) Configure(ctx context.Context, config json.RawMessage) error {
	return nil
}

func (p *mockPlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func (p *mockPlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	return papi.HandleResult{}, nil
}

func (p *mockPlugin) Status(ctx context.Context) (string, error) {
	return "OK", nil
}

func (p *mockPlugin) Shutdown(ctx context.Context) error {
	return nil
}

func (p *mockPlugin) Kill() {
	p.killed.Store(true)
	close(p.exited)
}

func (p *mockPlugin) Exited() <-chan struct{} {
	return p.exited
}

func TestLazyPluginLifecycle(t *testing.T) {
	pm := papi.NewManager()
	host := New(pm, nil, nil, nil, nil, nil)

	mock := &mockPlugin{exited: make(chan struct{})}
	
	host.starter = func(ctx context.Context, exePath string) (*loadedCandidate, error) {
		mock.startCount.Add(1)
		mock.killed.Store(false)
		mock.exited = make(chan struct{})
		
		serverConn, clientConn := net.Pipe()
		
		srv := rpc.NewServer()
		if err := srv.RegisterName("Plugin", &transport.PluginRPCServer{Impl: mock}); err != nil {
			return nil, err
		}
		go srv.ServeConn(serverConn)

		rpcClient := transport.NewPluginRPCClient(rpc.NewClient(clientConn), nil)
		return &loadedCandidate{
			exePath: exePath,
			client:  mock,
			plugin:  rpcClient,
		}, nil
	}

	idleTimeout := 100 * time.Millisecond
	lazy := NewLazyPlugin(host, "dummy", papi.Descriptor{PluginID: "test.lazy"}, idleTimeout)

	// 1. Verify on-demand start
	ctx := context.Background()
	_, err := lazy.Invoke(ctx, "test", nil, "caller")
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}
	if mock.startCount.Load() != 1 {
		t.Errorf("Expected start count 1, got %d", mock.startCount.Load())
	}

	// 2. Verify idle stop
	time.Sleep(idleTimeout * 2)
	if !mock.killed.Load() {
		t.Error("Expected plugin to be killed after idle timeout")
	}

	// 3. Verify restart after stop
	_, err = lazy.Invoke(ctx, "test", nil, "caller")
	if err != nil {
		t.Fatalf("Invoke after restart failed: %v", err)
	}
	if mock.startCount.Load() != 2 {
		t.Errorf("Expected start count 2, got %d", mock.startCount.Load())
	}

	// 4. Verify concurrent requests only start one process
	time.Sleep(idleTimeout * 2)
	if !mock.killed.Load() {
		t.Error("Expected plugin to be killed after idle timeout (second time)")
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := lazy.Invoke(ctx, "test", nil, "caller")
			if err != nil {
				t.Errorf("Concurrent invoke failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if mock.startCount.Load() != 3 {
		t.Errorf("Expected start count 3, got %d", mock.startCount.Load())
	}

	// 5. Verify status reporting
	status, _ := lazy.Status(ctx)
	if status != "OK" {
		t.Errorf("Expected status OK, got %s", status)
	}

	time.Sleep(idleTimeout * 2)
	status, _ = lazy.Status(ctx)
	if status != "Sleeping" {
		t.Errorf("Expected status Sleeping, got %s", status)
	}
}
