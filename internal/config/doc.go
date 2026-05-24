// Package config loads ludwig's runtime configuration. Defaults are baked in
// for v1; future versions will add a ~/.ludwig/config.toml override surface.
//
// LoadToken reads the scope token from ~/.ludwig/api-token. Missing or empty
// token files abort startup with an instructional error message – see the
// "Scope token loaded from filesystem" requirement in the spec.
package config
