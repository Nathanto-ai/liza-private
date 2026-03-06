package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// BUG: GetUser does not validate id<=0 and returns 200 with empty name
// instead of 400 for invalid IDs. Also returns 200 for missing users
// instead of 404.

var users = map[int]string{
	123: "Alice",
	456: "Bob",
}

type UserResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func GetUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idStr := r.URL.Query().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		// BUG: returns 200 instead of 400
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid user ID"})
		return
	}

	// BUG: missing validation for id <= 0
	name, ok := users[id]
	if !ok {
		// BUG: returns 200 instead of 404
		json.NewEncoder(w).Encode(ErrorResponse{Error: "user not found"})
		return
	}

	json.NewEncoder(w).Encode(UserResponse{ID: id, Name: name})
}
