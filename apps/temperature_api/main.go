package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

// TemperatureResponse matches the structure expected by the smart_home service.
type TemperatureResponse struct {
	Value       float64   `json:"value"`
	Unit        string    `json:"unit"`
	Timestamp   time.Time `json:"timestamp"`
	Location    string    `json:"location"`
	Status      string    `json:"status"`
	SensorID    string    `json:"sensor_id"`
	SensorType  string    `json:"sensor_type"`
	Description string    `json:"description"`
}

// randomTemperature returns a random temperature between 15.0 and 30.0 °C.
func randomTemperature() float64 {
	raw := 15.0 + rand.Float64()*15.0
	return float64(int(raw*10)) / 10
}

func statusFor(v float64) string {
	if v < 18.0 || v > 28.0 {
		return "warning"
	}
	return "ok"
}

func writeJSON(w http.ResponseWriter, location, sensorID string) {
	v := randomTemperature()
	resp := TemperatureResponse{
		Value:       v,
		Unit:        "°C",
		Timestamp:   time.Now().UTC(),
		Location:    location,
		Status:      statusFor(v),
		SensorID:    sensorID,
		SensorType:  "temperature",
		Description: fmt.Sprintf("Temperature sensor reading for %s", location),
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("error encoding response: %v", err)
	}
}

func main() {
	mux := http.NewServeMux()

	// GET /temperature?location={location}
	mux.HandleFunc("/temperature", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		location := r.URL.Query().Get("location")
		if location == "" {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"location query parameter is required"}`, http.StatusBadRequest)
			return
		}
		log.Printf("GET /temperature?location=%s", location)
		writeJSON(w, location, "")
	})

	// GET /temperature/{id}
	mux.HandleFunc("/temperature/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		sensorID := strings.TrimPrefix(r.URL.Path, "/temperature/")
		if sensorID == "" {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"sensor id is required"}`, http.StatusBadRequest)
			return
		}
		location := r.URL.Query().Get("location")
		if location == "" {
			location = "unknown"
		}
		log.Printf("GET /temperature/%s", sensorID)
		writeJSON(w, location, sensorID)
	})

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	addr := ":" + port

	log.Printf("temperature-api listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
