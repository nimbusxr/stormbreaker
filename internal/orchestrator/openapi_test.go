package orchestrator

import (
	"slices"
	"strings"
	"testing"

	validator "github.com/pb33f/libopenapi-validator"
)

// TestTheDocumentIsValid holds the orchestrator's OpenAPI document to OpenAPI 3.1.
func TestTheDocumentIsValid(t *testing.T) {
	doc, err := specDoc()
	if err != nil {
		t.Fatal(err)
	}
	v, errs := validator.NewValidator(doc)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if ok, errs := v.ValidateDocument(); !ok {
		for _, err := range errs {
			t.Error(err)
		}
	}
}

// TestEveryRouteIsDocumented keeps the document and the API from drifting apart: every route the
// orchestrator serves is an operation in the document, and every operation in the document is
// served.
func TestEveryRouteIsDocumented(t *testing.T) {
	doc, err := specDoc()
	if err != nil {
		t.Fatal(err)
	}
	model, err := doc.BuildV3Model()
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for path, item := range model.Model.Paths.PathItems.FromOldest() {
		for method := range item.GetOperations().FromOldest() {
			documented = append(documented, strings.ToUpper(method)+" "+path)
		}
	}
	var served []string
	for _, rt := range routes {
		served = append(served, rt.method+" "+rt.path)
	}
	slices.Sort(documented)
	slices.Sort(served)
	for _, op := range served {
		if !slices.Contains(documented, op) {
			t.Errorf("%s is served but not in api/orchestrator.openapi.yaml", op)
		}
	}
	for _, op := range documented {
		if !slices.Contains(served, op) {
			t.Errorf("%s is in api/orchestrator.openapi.yaml but not served", op)
		}
	}
}
