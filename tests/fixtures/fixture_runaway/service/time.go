package service

import "time"

// GetTime returns the current UTC time.
func GetTime() time.Time {
	return time.Now().UTC()
}
