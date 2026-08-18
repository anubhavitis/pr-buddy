package host

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type completeRequest struct {
	Backend  string `json:"backend"`
	Prompt   string `json:"prompt"`
	MLXURL   string `json:"mlx_url,omitempty"`
	MLXModel string `json:"mlx_model,omitempty"`
}

type completeResponse struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

func Handler(c *Completer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/complete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req completeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, completeResponse{Error: "invalid json"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		text, err := c.Complete(ctx, Request{
			Backend:  Backend(req.Backend),
			Prompt:   req.Prompt,
			MLXURL:   req.MLXURL,
			MLXModel: req.MLXModel,
		})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, completeResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, completeResponse{OK: true, Text: text})
	})
	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || origin == "null" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
