//go:build linux

package owner

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestThreeClientsSerializeMetadataAndShareOneProcessOwner(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "sleep", Name: "Sleep", Executable: "/usr/bin/sleep"}}
	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := Start(ctx, paths, cfg)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- runtime.Serve(ctx) }()
	socket, err := ipc.SocketPath(paths)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	clients := [3]*ipc.Client{ipc.NewClient(socket), ipc.NewClient(socket), ipc.NewClient(socket)}
	for i, c := range clients {
		if err := c.Ping(context.Background()); err != nil {
			cancel()
			t.Fatalf("client %d handshake: %v", i, err)
		}
	}
	created, err := clients[0].Create(context.Background(), app.CreateRequest{Agent: "sleep", WorkspacePath: t.TempDir(), ExtraArgs: []string{"30"}})
	if err != nil {
		cancel()
		t.Fatalf("create: %v", err)
	}
	id := string(created.Session.ID)
	defer func() {
		_, _ = runtime.Client().Kill(context.Background(), app.KillRequest{Ref: id, Signal: session.SignalKill})
	}()
	listed, err := clients[1].List(context.Background(), app.ListRequest{})
	if err != nil {
		cancel()
		t.Fatalf("client B list: %v", err)
	}
	if listed.Snapshot.Authority != session.AuthorityLive || len(listed.Snapshot.Sessions) != 1 {
		cancel()
		t.Fatalf("client B snapshot authority/rows = %q/%d", listed.Snapshot.Authority, len(listed.Snapshot.Sessions))
	}
	inspected, err := clients[2].Get(context.Background(), app.GetRequest{Ref: id})
	if err != nil {
		cancel()
		t.Fatalf("client C inspect: %v", err)
	}
	if inspected.Session.ID != created.Session.ID || !inspected.Session.HasIdentity {
		cancel()
		t.Fatalf("client C inspect = %+v", inspected.Session)
	}
	var wg sync.WaitGroup
	results := make(chan app.RenameResult, 3)
	errs := make(chan error, 3)
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *ipc.Client) {
			defer wg.Done()
			result, err := c.Rename(context.Background(), app.RenameRequest{Ref: id, Name: fmt.Sprintf("client-%d", i)})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(i, c)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		cancel()
		t.Fatalf("parallel rename: %v", err)
	}
	revisions := map[uint64]bool{}
	for result := range results {
		revisions[result.Revision] = true
	}
	for revision := uint64(2); revision <= 4; revision++ {
		if !revisions[revision] {
			cancel()
			t.Fatalf("parallel mutation revisions = %v, missing %d", revisions, revision)
		}
	}
	final, err := clients[2].List(context.Background(), app.ListRequest{})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if final.Snapshot.Revision != 4 {
		cancel()
		t.Fatalf("revision after three concurrent writers = %d, want 4", final.Snapshot.Revision)
	}
	stopped, err := clients[1].Stop(context.Background(), app.StopRequest{Ref: id, Grace: time.Second})
	if err != nil {
		cancel()
		t.Fatalf("graceful stop: %v", err)
	}
	if !stopped.Stopped {
		cancel()
		t.Fatalf("stop result = %+v", stopped)
	}
	restarted, err := clients[0].Restart(context.Background(), app.RestartRequest{Ref: id})
	if err != nil {
		cancel()
		t.Fatalf("restart: %v", err)
	}
	if restarted.Session.Generation != 2 {
		cancel()
		t.Fatalf("restart generation = %d, want 2", restarted.Session.Generation)
	}
	killed, err := clients[2].Kill(context.Background(), app.KillRequest{Ref: id, Signal: session.SignalKill})
	if err != nil {
		cancel()
		t.Fatalf("kill: %v", err)
	}
	if killed.Signal != session.SignalKill {
		cancel()
		t.Fatalf("kill signal = %q", killed.Signal)
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not stop after clients disconnected")
	}
}
