// Package integrationhost wires the core's concrete services into the stable
// integrationinterface.Host capability surface. Integrations consume the Host
// (via integrationinterface.RequireHost) instead of importing backend/...
// packages directly, which keeps their compile inputs stable and independent of
// core internals.
//
// The adapter is intentionally thin: it does not cache the runtime config (the
// values are resolved on every call, like the runtimecfg package itself) and it
// hands out the concrete *gorm.DB behind an opaque handle so this package, not
// the interface module, owns the ORM dependency.
package integrationhost

import (
	"log"
	"net/http"

	"backend/database"
	"backend/runtimecfg"
	"backend/server/util"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

// Install makes the core adapter the process-wide Host. Call it once at server
// startup, before any integration bootstrap hook or HTTP handler runs. It also
// wires any integration-declared chat backends into the core registries.
func Install() {
	integrationinterface.SetHost(host{})
	installChatBackends()
}

type host struct{}

func (host) Version() int                         { return integrationinterface.HostVersion }
func (host) Config() integrationinterface.Config  { return config{} }
func (host) Logger() integrationinterface.Logger  { return stdLogger{} }
func (host) HTTP() integrationinterface.HTTP      { return httpAdapter{} }
func (host) Storage() integrationinterface.Storage { return storage{db: database.GetGlobalDB()} }
func (host) Chat() integrationinterface.Chat      { return chatAdapter{} }
func (host) Workspace() integrationinterface.Workspace {
	return workspaceAdapter{}
}
func (host) Web() integrationinterface.Web   { return webAdapter{} }
func (host) Tokens() integrationinterface.Tokens { return tokensAdapter{} }
func (host) Tools() integrationinterface.Tools   { return toolsAdapter{} }

// config adapts the resolved runtime configuration (backend/runtimecfg) to the
// stable Config surface.
type config struct{}

func (config) All() map[string]integrationinterface.Value {
	values := runtimecfg.GetAll()
	out := make(map[string]integrationinterface.Value, len(values))
	for key, value := range values {
		out[key] = integrationinterface.Value{Value: value.Value, Sensitive: value.Sensitive}
	}
	return out
}

func (config) Get(key string) (integrationinterface.Value, bool) {
	value, ok := runtimecfg.GetAll()[key]
	if !ok {
		return integrationinterface.Value{}, false
	}
	return integrationinterface.Value{Value: value.Value, Sensitive: value.Sensitive}, true
}

// stdLogger routes integration logs through the standard library logger. It is
// the single seam where a richer core logger can be swapped in later.
type stdLogger struct{}

func (stdLogger) Debugf(format string, args ...interface{}) {
	log.Printf("[integration][debug] "+format, args...)
}

func (stdLogger) Infof(format string, args ...interface{}) {
	log.Printf("[integration] "+format, args...)
}

func (stdLogger) Warnf(format string, args ...interface{}) {
	log.Printf("[integration][warn] "+format, args...)
}

func (stdLogger) Errorf(format string, args ...interface{}) {
	log.Printf("[integration][error] "+format, args...)
}

// storage wraps the concrete persistence handle. Handle returns the raw
// *gorm.DB; integrations type-assert it before passing it to their ORM helpers.
type storage struct {
	db *gorm.DB
}

func (s storage) Handle() interface{} { return s.db }

func (s storage) Global() interface{} { return database.GetGlobalDB() }

// user wraps the core user model behind the identity-only User surface.
type user struct {
	u *database.User
}

func (u user) ID() uint         { return u.u.ID }
func (u user) Username() string { return u.u.Username }
func (u user) Email() string    { return u.u.Email }
func (u user) IsAdmin() bool    { return u.u.IsAdmin }

// httpAdapter resolves request-scoped services from the core request context.
type httpAdapter struct{}

func (httpAdapter) DB(r *http.Request) (integrationinterface.Storage, error) {
	db, err := util.GetDB(r)
	if err != nil {
		return nil, err
	}
	return storage{db: db}, nil
}

func (httpAdapter) DBAndUser(r *http.Request) (integrationinterface.Storage, integrationinterface.User, error) {
	db, coreUser, err := util.GetDBAndUser(r)
	if err != nil {
		return nil, nil, err
	}
	return storage{db: db}, user{u: coreUser}, nil
}

func (httpAdapter) OptionalUser(r *http.Request) integrationinterface.User {
	coreUser := util.GetOptionalUser(r)
	if coreUser == nil {
		return nil
	}
	return user{u: coreUser}
}
