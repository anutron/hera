// Package log is the placeholder for hera's structured logging helpers.
//
// In v1 the daemon logs via the stdlib log/slog package directly to
// stderr (see cmd/hera/start.go); this package has no exported API yet.
// File logging at ~/.hera/hera.log will land alongside the launchd
// install in a follow-up change.
package log
