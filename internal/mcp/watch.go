package mcp

import (
	"context"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// WatchStore tells subscribed clients when the store changed underneath them
// (the CLI in a terminal, a hook, another MCP client), so they can refresh
// instead of polling every view. It checks SQLite's data_version, which changes
// whenever another connection commits, every interval, and sends
// resources/updated for acline://events. It returns when ctx is done.
// `acline mcp serve` runs it; tests and other embedders need not.
func WatchStore(ctx context.Context, s *sdkmcp.Server, st *store.Store, interval time.Duration) {
	last, err := dataVersion(st)
	if err != nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		v, err := dataVersion(st)
		if err != nil || v == last {
			continue
		}
		last = v
		_ = s.ResourceUpdated(ctx, &sdkmcp.ResourceUpdatedNotificationParams{URI: eventsResourceURI})
	}
}

func dataVersion(st *store.Store) (int64, error) {
	var v int64
	err := st.DB.QueryRow(`PRAGMA data_version`).Scan(&v)
	return v, err
}
