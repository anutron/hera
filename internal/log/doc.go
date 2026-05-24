// Package log provides ludwig's structured logging helpers.
//
// Output goes to both stderr (for `ludwig start --foreground`) and the
// rolling log file at ~/.ludwig/ludwig.log. Keys are colon-separated
// key=value pairs for grep-friendliness; no JSON shipping in v1.
package log
