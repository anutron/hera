// Package idle tracks per-task idle state from argus's session.* event stream.
//
// A task is considered idle for injection purposes only when its most recent
// session event is session.idle AND that event was emitted at least
// `debounce` ago. Default debounce is 2 seconds – tunable when argus
// clarifies session.idle semantics.
//
// See openspec/changes/hera-v1/design.md decision D10.
package idle
