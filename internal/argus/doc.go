// Package argus is a typed HTTP client for argus's REST API on 127.0.0.1:7743.
//
// Every request carries Authorization: Bearer <scope-token> and
// X-Argus-Plugin-Version: 1. The client also implements the SSE event-stream
// subscription and PTY-output stream that hera consumes.
//
// Contract reference: ~/Development/Personal/argus/docs/plugins.md.
package argus
