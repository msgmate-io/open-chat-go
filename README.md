## open-chat-go (mvp)

Rewrite of the [msgmate open-chat spec, but in go](https://beta.msgmate.io/api/schema/swagger-ui/).
Aim is portability and performance to enable planned p2p features.

### docker dev

auto-reload go-backend and auto-rebuild api docs

```bash
docker compose -f compose-dev.yaml up --build
```

### test

```bash
cd backend
go test -v ./...
go test -v ./... -run "^SomeTest_Func$" 
```

### build

```bash
go build -ldflags "-s -w"
```

### third party packages

- `github.com/urfave/cli/v3` for cli
- `github.com/rs/cors` for cors
- `gorm.io/gorm + drivers` as orm for sqlite + psql and convenience
- `golang.org/x/crypto` password hashing

Development only packages

- `github.com/swaggo/swag/v2/cmd/swag@latest` generating swagger docs from comments
- `github.com/githubnemo/CompileDaemon` hot reloads