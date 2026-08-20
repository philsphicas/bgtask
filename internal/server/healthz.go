package server

import "net/http"

// handleHealthz always responds 200 OK, regardless of which exposures are
// configured, so external liveness checks never depend on REST/MCP mount
// state or auth configuration.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
