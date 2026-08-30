// api/health.go — GET /api/health
package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	cobalt := os.Getenv("COBALT_API")
	if cobalt == "" {
		cobalt = "https://api.cobalt.tools"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        true,
		"service":   "saveit-web",
		"version":   "1.0.0",
		"time":      time.Now().UTC().Format(time.RFC3339),
		"cobaltApi": cobalt,
	})
}
