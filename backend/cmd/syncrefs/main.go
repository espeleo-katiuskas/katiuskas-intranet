// Command syncrefs tells the catalogue of places (when it is a project of its own, CATALOGUE_URL) of every
// link of an activity to a place, as references. The API does it as activities change; this is for what
// that missed (the catalogue was down) and for the first time, when the club's data is copied to a
// catalogue that does not know yet which places it uses. It is safe to run any number of times.
//
// It needs DATABASE_URL, CATALOGUE_URL, CATALOGUE_PRIVATE_KEY and, if it is not the usual, CATALOGUE_ISSUER.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/catalogueclient"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/config"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/logging"
)

func main() {
	slog.SetDefault(logging.New())
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if cfg.CatalogueURL == "" {
		fmt.Fprintln(os.Stderr, "CATALOGUE_URL is not set: the catalogue is part of the API, there is nothing to tell")
		os.Exit(1)
	}
	client, err := catalogueclient.New(cfg.CatalogueURL, cfg.CatalogueIssuer, cfg.CataloguePrivateKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to database: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	service := activities.NewService(activities.NewRepository(pool), nil, nil, client).WithPlaceReferences(client)
	// Nobody is signed in: the club acts, as a member of its group.
	ctx = catalogueclient.WithPrincipal(ctx, catalogueclient.SystemPrincipal(access.ClubGroup))
	n, err := service.SyncReferences(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "told the catalogue of %d links, then: %v\n", n, err)
		os.Exit(1)
	}
	fmt.Printf("told the catalogue of %d links of activities to places\n", n)
}
