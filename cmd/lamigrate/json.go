package main

// JSONOutput is the versioned schema for --json output.
// The schema is experimental (version 1) and may change.
type JSONOutput struct {
	Version int         `json:"version"`
	Command string      `json:"command"`
	Data    interface{} `json:"data"`
	Error   *JSONError  `json:"error,omitempty"`
}

// JSONError is the error object in JSON output.
type JSONError struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

const jsonSchemaVersion = 1
