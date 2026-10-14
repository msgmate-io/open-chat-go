package externalintegrations

// SourceRepository describes where a compiled integration's Go source lives so
// the /integrations overview can deep-link models, API routes and functions
// back to their implementation. The map is populated at build time by the
// generated `sources_gen.go` (produced by `openchat-integrations resolve`); it
// stays empty when the generator has not run.
type SourceRepository struct {
	ID         string
	Repository string
	Ref        string
}

// SourceRepositories maps integration name to its source repository. It is
// mutated by the generated sources_gen.go `init` functions.
var SourceRepositories = map[string]SourceRepository{}
