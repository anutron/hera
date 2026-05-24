// Package daemon wires together every other internal package into a
// runnable ludwig process.
//
// Run(ctx, cfg) opens the DB, loads the scope token, builds the argus
// client, starts the event subscriber, starts the idle tracker, brings
// up the MCP server, and registers all five tools with argus. On
// SIGINT/SIGTERM it tears the same stack down in reverse.
package daemon
