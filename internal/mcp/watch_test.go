package mcp

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// connectWithClientOptions is connectedTestServer with client-side handlers.
func connectWithClientOptions(t *testing.T, opts *sdkmcp.ClientOptions) (*sdkmcp.Server, *sdkmcp.ClientSession, *store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Actor = store.Actor{Type: "human", ID: "tester"}
	t.Cleanup(func() { st.Close() })
	server := NewServer(st)
	ctx := context.Background()
	t1, t2 := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, opts).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return server, cs, st, path
}

// A long check_run gave the client no sign of life, so its timeout fired.
func TestCheckRunReportsProgress(t *testing.T) {
	prev := progressEvery
	progressEvery = 20 * time.Millisecond
	t.Cleanup(func() { progressEvery = prev })
	var got atomic.Int32
	_, cs, st, _ := connectWithClientOptions(t, &sdkmcp.ClientOptions{
		ProgressNotificationHandler: func(context.Context, *sdkmcp.ProgressNotificationClientRequest) { got.Add(1) },
	})
	pid, _ := st.AddProject("p", t.TempDir(), "")
	if err := st.SetCheckRunner(pid, "test", "sleep 0.3", ""); err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &pid})
	params := &sdkmcp.CallToolParams{Name: "acline_check_run", Arguments: map[string]any{"task_id": id, "kind": "test"}}
	params.SetProgressToken("tok")
	res, err := cs.CallTool(context.Background(), params)
	if err != nil || res.IsError {
		t.Fatalf("check_run: %v %+v", err, res)
	}
	if got.Load() == 0 {
		t.Fatal("no progress notification during a 300ms run")
	}
}

// Views could only poll; a subscriber now hears that another process
// changed the store.
func TestWatchStoreNotifiesSubscribersOfOutsideChanges(t *testing.T) {
	updated := make(chan string, 4)
	server, cs, _, path := connectWithClientOptions(t, &sdkmcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *sdkmcp.ResourceUpdatedNotificationRequest) {
			updated <- req.Params.URI
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := cs.Subscribe(ctx, &sdkmcp.SubscribeParams{URI: eventsResourceURI}); err != nil {
		t.Fatal(err)
	}
	go WatchStore(ctx, server, watcherStore(t, path), 20*time.Millisecond)

	other, err := store.Open(path) // another process, e.g. the CLI in a terminal
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.Actor = store.Actor{Type: "human", ID: "cli"}
	if _, err := other.AddTask("from the terminal", "", "normal", store.TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	select {
	case uri := <-updated:
		if uri != eventsResourceURI {
			t.Fatalf("updated %q", uri)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no resources/updated after another connection wrote")
	}
}

// watcherStore opens a second connection to the store for the watcher, like
// another process: data_version only moves for commits made on other connections.
func watcherStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
