package ingestion

import "context"

type SyncResult struct {
	Ingested int `json:"ingested"`
	Skipped  int `json:"skipped"`
	Errors   int `json:"errors"`
}

type Syncer interface {
	Name() string
	Sync(ctx context.Context) (*SyncResult, error)
}
