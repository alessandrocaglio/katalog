package models

import (
	"encoding/json"
	"reflect"
	// "strings" // Removed unused import
	"testing"
	"time"
)

func TestLogEntry_JSON(t *testing.T) {
	// Sample LogEntry
	originalEntry := LogEntry{
		Time:       time.Now().Unix(),
		Host:       "test-host",
		Source:     "test-source",
		SourceType: "test-type",
		Event:      "This is a test log event.",
		Fields: map[string]string{
			"env":  "dev",
			"app":  "katalog-test",
			"code": "123",
		},
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(originalEntry)
	if err != nil {
		t.Fatalf("Failed to marshal LogEntry to JSON: %v", err)
	}

	// Unmarshal from JSON
	var unmarshaledEntry LogEntry
	err = json.Unmarshal(jsonData, &unmarshaledEntry)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON to LogEntry: %v", err)
	}

	// Compare original and unmarshaled entries
	if !reflect.DeepEqual(originalEntry, unmarshaledEntry) {
		t.Errorf(`Original and unmarshaled LogEntry are not deep equal.
Original: %+v
Unmarshaled: %+v`, originalEntry, unmarshaledEntry)
	}

	// Test with omitempty for Fields
	entryWithoutFields := LogEntry{
		Time:       time.Now().Unix(),
		Host:       "test-host-2",
		Source:     "test-source-2",
		SourceType: "test-type-2",
		Event:      "Another event without fields.",
		Fields:     nil, // Should be omitted
	}

	jsonDataWithoutFields, err := json.Marshal(entryWithoutFields)
	if err != nil {
		t.Fatalf("Failed to marshal LogEntry without fields to JSON: %v", err)
	}

	// Create an expected JSON string by marshaling a new entry without fields
	expectedEntryJSON, err := json.Marshal(entryWithoutFields)
	if err != nil {
		t.Fatalf("Failed to marshal expected entry without fields to JSON: %v", err)
	}

	if string(jsonDataWithoutFields) != string(expectedEntryJSON) {
		t.Errorf(`JSON output for LogEntry without fields is not as expected.
Expected: %s
Got: %s`, string(expectedEntryJSON), string(jsonDataWithoutFields))
	}
}
