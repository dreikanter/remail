package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

type errorBody struct {
	Message string `json:"message"`
}

// errorEnvelope is the single failure shape every command emits in JSON mode,
// so a caller can branch on one key regardless of which command ran.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Nothing useful remains if encoding the result fails, and the process is
	// already on its way out.
	_ = enc.Encode(v)
}

func emitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
