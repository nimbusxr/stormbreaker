// Package scale holds stormbreaker's scale tests: what each part does at the size a load run
// reaches (100,000 records a second for ten minutes: 60 million identities). They take minutes
// and gigabytes, so they build only with the scale tag:
//
//	go test -tags scale -run Footprint -timeout 60m -v ./internal/scale
//	STORMBREAKER_SCALE_IDENTITIES=1000000 go test -tags scale -v ./internal/scale   # smaller
package scale
