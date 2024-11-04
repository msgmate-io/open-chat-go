package Views

import "net/http"

const page404 string = `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta http-equiv="X-UA-Compatible" content="IE=edge">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Login</title>
</head>
<body>
	<h1>404 Not Found</h1>
	<h2>Open Chat Go API</h2>
	<li>Try <a href="/api/schema/">/api/schema/</a></li>
	<li>Or Interactive <a href="/api/schema/swagger/">/api/schema/swagger/</a></li>
	<li>Or <a href="/user/self/">/user/self/</a></li>
	<li>Or login as a user <a href="/login">/login</a></li>
</body>
`

func Page404View(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/html")

	_, err := w.Write([]byte(page404))

	if err != nil {
		panic(err)
	}
}
