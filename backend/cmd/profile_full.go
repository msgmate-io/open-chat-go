//go:build !coreonly

package cmd

// BuildProfile is the integration profile this binary was compiled for. Any
// build that is not the public `core-only` profile is reported as `full`.
const BuildProfile = "full"
