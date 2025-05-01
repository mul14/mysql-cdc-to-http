package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

func logRequest(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Format(time.RFC3339)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[%s] Error reading body: %v\n", now, err)
		http.Error(w, "Can't read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Log the method, path, headers, and body
	log.Printf("[%s] %s %s\n", now, r.Method, r.URL.Path)
	for k, v := range r.Header {
		log.Printf("[%s] Header: %s: %s\n", now, k, v)
	}
	log.Printf("[%s] Body: %s\n", now, string(body))

	// Respond
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func main() {
	log.Println("Starting HTTP log server on :8080")

	http.HandleFunc("/", logRequest)

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
