// Package mcp owns hera's MCP tool surface.
//
// On daemon startup it registers six tools with argus
// (hera_new_orchestrator, hera_join, hera_send, hera_inbox, hera_mark_read,
// hera_status) and hosts an HTTP listener on 127.0.0.1:7744 (default) for
// argus to POST callbacks into. A randomly-generated shared secret is
// included in the auth_header at registration time; the callback listener
// constant-time compares incoming requests against it.
//
// Tool registrations re-POST every 5 minutes to stay within argus's
// 10-minute idle sweep window.
package mcp
