package service

import (
	"context"
	"time"
)

// UsageFlowRow aggregates usage for one user, model and account.
type UsageFlowRow struct {
	UserID      int64
	UserName    string
	Model       string
	AccountID   int64
	AccountName string
	Requests    int64
	CostUSD     float64
	TotalTokens int64
	InputTokens int64
	CacheTokens int64
}

// UsageFlowReader reads aggregated usage for the bot's flow image.
type UsageFlowReader interface {
	ListUsageFlow(ctx context.Context, since time.Time) ([]UsageFlowRow, error)
}
