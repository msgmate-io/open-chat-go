package admin

import (
	"backend/server/util"
	"net/http"
)

func AsynqUIHandler(uiHandler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if uiHandler == nil {
			http.Error(w, "Asynq UI is not configured", http.StatusServiceUnavailable)
			return
		}

		_, user, err := util.GetDBAndUser(r)
		if err != nil {
			http.Error(w, "Unable to get database or user", http.StatusBadRequest)
			return
		}
		if !user.IsAdmin {
			http.Error(w, "User is not an admin", http.StatusForbidden)
			return
		}

		inspector, err := util.GetAsynqInspector(r)
		if err != nil {
			http.Error(w, "Asynq inspector unavailable", http.StatusServiceUnavailable)
			return
		}
		if _, err := inspector.Queues(); err != nil {
			http.Error(w, "Asynq UI unavailable: Redis not reachable", http.StatusServiceUnavailable)
			return
		}

		uiHandler.ServeHTTP(w, r)
	}
}
