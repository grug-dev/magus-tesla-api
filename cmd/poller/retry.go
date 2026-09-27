// retry.go adapts the analytics read port to the shape internal/app's retry
// schedule asks for. The two modules never import each other, so the
// composition root is the one place that joins them.
package main

import (
	"context"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/app"
)

// notDoneFromAnalytics satisfies app.NotDoneVehicles. A vehicle is not done
// for a date when analytics has no vehicle_metrics row for it on that date.
type notDoneFromAnalytics struct {
	reader analytics.UnfinishedReader
}

var _ app.NotDoneVehicles = notDoneFromAnalytics{}

func (n notDoneFromAnalytics) NotDone(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error) {
	return n.reader.UnfinishedForDate(ctx, teslaIDs, date)
}
