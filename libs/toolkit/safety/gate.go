package safety

import (
	"encoding/json"

	"emperror.dev/errors"
)

// GateParams represents the DryRun and Confirmed fields that every write tool
// params struct must include. The middleware extracts these from the raw JSON
// arguments before passing them to the tool.
type GateParams struct {
	DryRun    bool `json:"dryRun"`
	Confirmed bool `json:"confirmed"`
}

// ExtractGateParams extracts DryRun and Confirmed from a raw JSON params string.
// Unknown fields are ignored; missing boolean fields default to false.
func ExtractGateParams(rawJSON string) (GateParams, error) {
	var gp GateParams
	if err := json.Unmarshal([]byte(rawJSON), &gp); err != nil {
		return GateParams{}, errors.Wrap(err, "failed to parse gate params")
	}
	return gp, nil
}

// ShouldGate checks whether a tool call must be gated (require dry-run/confirmed flow).
//
// Rules:
//   - Read-only tools (not in writeTools) always pass — no gate required.
//   - Write tools with DryRun=true always pass — dry-run is always allowed.
//   - Write tools with Confirmed=true pass — user has approved the dry-run.
//   - Write tools with neither DryRun nor Confirmed are rejected.
//
// Returns nil if the call is allowed, or an error explaining the required flow.
func ShouldGate(toolName string, writeTools map[string]bool, gp GateParams) error {
	if !writeTools[toolName] {
		return nil // read-only tool, no gate required
	}
	if gp.DryRun {
		return nil // dry-run is always allowed for write tools
	}
	if !gp.Confirmed {
		return ErrGateRequired
	}
	return nil
}

// ErrGateRequired is returned when a write tool is called without dry-run or confirmation.
var ErrGateRequired = errors.New(
	"SAFETY GATE: This is a write operation. You must first call this tool with dryRun=true, show the result to the user, and then re-call with confirmed=true after user approval.",
)
