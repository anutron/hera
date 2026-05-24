// Package log provides hera's structured logging helpers.
//
// Output goes to both stderr (for `hera start --foreground`) and the
// rolling log file at ~/.hera/hera.log. Keys are colon-separated
// key=value pairs for grep-friendliness; no JSON shipping in v1.
package log
