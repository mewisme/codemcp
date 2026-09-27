// Package product owns remote, privacy-bounded CodeMCP product telemetry.
//
// It is intentionally separate from the richer local telemetry package. Product
// telemetry must be constructed from narrow safe inputs rather than serializing
// local logger, activity, approval, tool, or background event payloads.
package product
