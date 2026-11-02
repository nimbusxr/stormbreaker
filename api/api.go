// Package api holds stormbreaker's HTTP contracts. The orchestrator serves its document at
// /openapi.yaml and /openapi.json, and its tests hold every operation to it.
package api

import _ "embed"

// Orchestrator is the orchestrator's OpenAPI 3.1 document.
//
//go:embed orchestrator.openapi.yaml
var Orchestrator []byte
