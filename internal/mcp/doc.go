// Package mcp owns ludwig's MCP tool surface.
//
// On daemon startup it registers five tools with argus
// (ludwig_join, ludwig_send, ludwig_inbox, ludwig_mark_read, ludwig_status)
// and hosts an HTTP listener on 127.0.0.1:7744 (default) for argus to POST
// callbacks into. A randomly-generated shared secret is included in the
// auth_header at registration time; the callback listener constant-time
// compares incoming requests against it.
//
// Tool registrations re-POST every 5 minutes to stay within argus's
// 10-minute idle sweep window.
package mcp
