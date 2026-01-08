package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

type storageLocation struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Type     string  `json:"type"`
	ParentID *string `json:"parent_id"`
}

type storageLocationsResponse struct {
	Locations []storageLocation `json:"locations"`
}

func storageLocationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !authorizeInternalAPI(w, r) {
		return
	}

	parentID := strings.TrimSpace(r.URL.Query().Get("parent_id"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))

	locations := filterStorageLocations(seedStorageLocations(), parentID, query)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(storageLocationsResponse{Locations: locations})
}

func authorizeInternalAPI(w http.ResponseWriter, r *http.Request) bool {
	token := strings.TrimSpace(os.Getenv("ATOM_VALENCE_INTERNAL_TOKEN"))
	if token == "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("Authorization")) == "Bearer "+token {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func seedStorageLocations() []storageLocation {
	building := "loc_building_a"
	room := "loc_room_101"
	rangeID := "loc_range_a"

	return []storageLocation{
		{ID: building, Label: "Building A", Type: "building", ParentID: nil},
		{ID: room, Label: "Room 101", Type: "room", ParentID: &building},
		{ID: rangeID, Label: "Range A", Type: "range", ParentID: &room},
		{ID: "loc_shelf_3", Label: "Shelf 3", Type: "shelf", ParentID: &rangeID},
		{ID: "loc_shelf_4", Label: "Shelf 4", Type: "shelf", ParentID: &rangeID},
	}
}

func filterStorageLocations(locations []storageLocation, parentID, query string) []storageLocation {
	if parentID == "" && query == "" {
		return locations
	}

	filtered := make([]storageLocation, 0, len(locations))
	for _, location := range locations {
		if parentID != "" {
			if location.ParentID == nil || *location.ParentID != parentID {
				continue
			}
		}
		if query != "" {
			if !strings.Contains(strings.ToLower(location.Label), query) {
				continue
			}
		}
		filtered = append(filtered, location)
	}
	return filtered
}
