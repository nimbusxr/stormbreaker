package orchestrator

// Who may do what (ADR 0019). A principal is a token, kept as its SHA-256, and its grants: admin
// for everything; suites, the groups whose runs it may open and act as the suite of; dispatchers,
// the dispatchers it may register, join to groups and report for. Reads are every principal's; a
// read-only principal, such as the UI's (ADR 0026), may only read.

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// Principal is a credential and what it may do.
type Principal struct {
	Name string `yaml:"name"`
	// TokenSHA256 is the hex SHA-256 of the principal's bearer token: the token itself is never
	// kept.
	TokenSHA256 string `yaml:"token-sha256"`
	// Admin may do everything, groups included.
	Admin bool `yaml:"admin"`
	// Suites are the groups whose runs it may open, and act as the suite of.
	Suites []string `yaml:"suites"`
	// Dispatchers are the dispatchers it may register, join to groups and report for.
	Dispatchers []string `yaml:"dispatchers"`
	// ReadOnly says it may only read, as the UI does: it has no grants, on purpose.
	ReadOnly bool `yaml:"read-only"`
}

// LoadPrincipals reads a principals file:
//
//	principals:
//	  - name: platform
//	    token-sha256: <hex SHA-256 of its token>
//	    admin: true
//	  - name: delivery-ci
//	    token-sha256: ...
//	    suites: [delivery]
//	  - name: parcels-team
//	    token-sha256: ...
//	    dispatchers: [parcels]
//	  - name: ui
//	    token-sha256: ...
//	    read-only: true
func LoadPrincipals(path string) ([]Principal, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Principals []Principal `yaml:"principals"`
	}
	if err := yaml.UnmarshalWithOptions(b, &f, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for i, p := range f.Principals {
		h, err := hex.DecodeString(p.TokenSHA256)
		switch {
		case strings.TrimSpace(p.Name) == "":
			return nil, fmt.Errorf("%s: principal %d has no name", path, i+1)
		case err != nil || len(h) != sha256.Size:
			return nil, fmt.Errorf("%s: principal %s's token-sha256 is not a SHA-256 in hex (sha256sum of the token)", path, p.Name)
		case seen[p.TokenSHA256]:
			return nil, fmt.Errorf("%s: two principals share a token", path)
		case p.ReadOnly && (p.Admin || len(p.Suites) > 0 || len(p.Dispatchers) > 0):
			return nil, fmt.Errorf("%s: principal %s is read-only and has grants: it is one or the other", path, p.Name)
		case !p.ReadOnly && !p.Admin && len(p.Suites) == 0 && len(p.Dispatchers) == 0:
			return nil, fmt.Errorf("%s: principal %s may do nothing: give it admin, suites or dispatchers, or say it is read-only", path, p.Name)
		}
		seen[p.TokenSHA256] = true
	}
	return f.Principals, nil
}

// TokenHash is what a principals file keeps of a token.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type principalKey struct{}

// principal is the request's principal, or none: then the request is refused 401. With neither a
// token nor principals configured, everyone is an admin, as the orchestrator says in its log.
func (o *Orchestrator) principal(r *http.Request) (*Principal, bool) {
	if o.cfg.Token == "" && len(o.cfg.Principals) == 0 {
		return &Principal{Name: "anyone", Admin: true}, true
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || got == "" {
		return nil, false
	}
	if o.cfg.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(o.cfg.Token)) == 1 {
		return &Principal{Name: "the shared token", Admin: true}, true
	}
	want := TokenHash(got)
	for i := range o.cfg.Principals {
		if subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(o.cfg.Principals[i].TokenSHA256))) == 1 {
			return &o.cfg.Principals[i], true
		}
	}
	return nil, false
}

func forbidden(p *Principal, format string, args ...any) error {
	return refuse(http.StatusForbidden, "%s may not %s", p.Name, fmt.Sprintf(format, args...))
}

// mayCreateGroup is an admin's, or a suite's, for one of its own groups.
func mayCreateGroup(p *Principal, group string) error {
	if p.Admin || slices.Contains(p.Suites, group) {
		return nil
	}
	return forbidden(p, "create group %s", group)
}

// mayDispatch is for one of the principal's dispatchers: registering it, joining it to a group,
// reporting for it.
func mayDispatch(p *Principal, name string) error {
	if p.Admin || slices.Contains(p.Dispatchers, name) {
		return nil
	}
	return forbidden(p, "act as dispatcher %s", name)
}

// mayOpen is for a group the principal is a suite of.
func mayOpen(p *Principal, group string) error {
	if p.Admin || slices.Contains(p.Suites, group) {
		return nil
	}
	return forbidden(p, "open runs in group %s", group)
}

// mayRunAsSuite is for a run of a group the principal is a suite of.
func (o *Orchestrator) mayRunAsSuite(ctx context.Context, p *Principal, runID string) error {
	if p.Admin {
		return nil
	}
	r, err := o.runRow(ctx, o.ro, runID)
	if err != nil {
		return err
	}
	if slices.Contains(p.Suites, r.group) {
		return nil
	}
	return forbidden(p, "act as the suite of run %s, in group %s", runID, r.group)
}

// mayReportAs is for a footprint report: as the suite of the run, or as one of its dispatchers.
func (o *Orchestrator) mayReportAs(ctx context.Context, p *Principal, runID, name string) error {
	if name == protocol.Suite {
		return o.mayRunAsSuite(ctx, p, runID)
	}
	return mayDispatch(p, name)
}
