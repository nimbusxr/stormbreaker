// Command parcels-dispatcher is the parcels team's dispatcher, built with the dispatcher-sdk.
// There is one per region, each answering for its region's store.
//
// Prepare: for every parcel the run's suite will register (the identities claimed at the parcels
// service), it upserts the parcel in its region through the service's admin door, so later
// requests that refer to it find it, and declares it in its region's store. Cleanup: it deletes
// what it declared there and has not yet answered for, checks each is gone, and reports them
// removed and verified. What it has answered for is in the footprint, so a restart mid-cleanup
// neither deletes nor reports anything twice.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nimbusxr/stormbreaker/dispatcher/sdk"
)

// store is where the parcels service keeps parcels; the region is where in it.
const store = "parcels-mem"

var client = &http.Client{Timeout: 5 * time.Second}

func admin(ctx context.Context, method, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func main() {
	base := strings.TrimRight(os.Getenv("PARCELS_ADMIN_URL"), "/")
	// The service the suite sends to, as the suite names it (its store in the footprint).
	front := os.Getenv("PARCELS_SERVICE")
	if front == "" {
		front = "parcels"
	}
	// Each region's instance answers for its own region's store, and only that.
	region := os.Getenv("STORMBREAKER_INSTANCE")
	if region == "" {
		region = "local"
	}
	sdk.Main(sdk.Hooks{
		Prepare: func(ctx context.Context, run *sdk.Run, declare func(sdk.Entry) error) error {
			// Only the suite's parcels, at the service it sends to: the round reads nothing else,
			// and is not run again until the suite declares more there.
			for c, err := range run.Footprint(ctx, sdk.From(sdk.Suite), sdk.At(front, "")) {
				if err != nil {
					return err
				}
				code, err := admin(ctx, http.MethodPut, base+"/parcels/"+c.Identity)
				if err != nil {
					return fmt.Errorf("prepare parcel %s through the admin door: %w", c.Identity, err)
				}
				if code/100 != 2 {
					return fmt.Errorf("prepare parcel %s through the admin door: it answered %d", c.Identity, code)
				}
				if err := declare(sdk.Entry{Store: store, Locator: region, Identities: []string{c.Identity}}); err != nil {
					return err
				}
			}
			return nil
		},
		Cleanup: func(ctx context.Context, run *sdk.Run, remove func(sdk.Removal) error) (bool, error) {
			for ref, err := range run.Unanswered(ctx, store, region) {
				if err != nil {
					return false, err
				}
				if _, err := admin(ctx, http.MethodDelete, base+"/parcels/"+ref); err != nil {
					return false, err
				}
				// Removed is only what is checked gone.
				code, err := admin(ctx, http.MethodGet, base+"/parcels/"+ref)
				if err != nil {
					return false, fmt.Errorf("check parcel %s is gone: %w", ref, err)
				}
				if code != http.StatusNotFound {
					return false, fmt.Errorf("parcel %s is still there after its delete (%d)", ref, code)
				}
				if err := remove(sdk.Removal{Store: store, Locator: region, Identities: []string{ref}}); err != nil {
					return false, err
				}
			}
			return true, nil
		},
	})
}
