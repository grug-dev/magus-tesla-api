// Command monthly-capacity computes one calendar month's effective pack capacity, for
// one vehicle or for every vehicle, on request. Two optional flags:
//
//	-period string     calendar month, "YYYY-MM" (default: the previous month)
//	-tesla-id int64     one vehicle's Tesla id (default: every vehicle with a usable record)
//
// This tool is run by hand, against a database an operator can reach directly. It is not
// built into the platform's deployed container image -- a manual, occasional tool does not
// need a standing place in the deploy image.
//
//	go run ./cmd/monthly-capacity                            # previous month, every vehicle
//	go run ./cmd/monthly-capacity -period 2026-08             # one month, every vehicle
//	go run ./cmd/monthly-capacity -tesla-id 123               # previous month, one vehicle
//	go run ./cmd/monthly-capacity -period 2026-08 -tesla-id 123
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/config"
)

func main() {
	periodFlag := flag.String("period", "", "calendar month to measure, YYYY-MM (default: the previous month)")
	teslaIDFlag := flag.Int64("tesla-id", 0, "one vehicle's Tesla id (default: every vehicle with a usable record)")
	flag.Parse()

	// flag.Visit only walks flags a caller actually set, so an explicit
	// "-tesla-id 0" is told apart from the flag being left out entirely.
	var teslaIDWasSet bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "tesla-id" {
			teslaIDWasSet = true
		}
	})
	teslaID := teslaIDPointer(*teslaIDFlag, teslaIDWasSet)

	period, err := resolvePeriod(*periodFlag, clock.Now(), clock.Zone())
	if err != nil {
		log.Fatalf("invalid -period %q: %v", *periodFlag, err)
	}

	dbURL, err := config.LoadDatabase()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("db pool: %v", err)
	}
	defer pool.Close()

	report, err := charging.NewMonthlyCapacityCalculator(pool).Calculate(ctx, period, teslaID)
	if err != nil {
		log.Fatalf("calculate: %v", err)
	}

	// One report format, the same one internal/app's own nightly log line uses for
	// the same report -- a single vocabulary for what a MonthlyCapacityReport says.
	fmt.Printf("monthly capacity: period %s: %d vehicle(s) found, %d measured, %d thin\n",
		report.Period.Format("2006-01"), report.VehiclesFound, report.Measured, report.Thin)
}
