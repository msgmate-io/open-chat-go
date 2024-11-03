package Api

import (
	"backend/Models"
	"encoding/json"
	"fmt"
	"github.com/go-chi/jwtauth"
	"net/http"
	"strconv"
)

func RetrieveAuthenticatedUserId(r *http.Request) (Models.User, bool, error) {
	_, claims, _ := jwtauth.FromContext(r.Context())
	userId, err := strconv.ParseInt(fmt.Sprintf("%v", claims["user_id"]), 10, 64)

	fmt.Printf("DEBUG: claims is %v\n", claims["user_id"])
	fmt.Printf("DEBUG: user_id is %v\n", userId)

	if err != nil {
		return Models.User{}, false, err
	}

	userObj := Models.User{}
	response := Models.DB.First(&userObj, userId)
	if response.Error != nil {
		return Models.User{}, false, response.Error
	}

	return userObj, true, nil
}

// UserSelfHandler godoc
//
//	@Summary		Show a bottle
//	@Router			/api/user/self/ [get]
func UserSelfHandler(w http.ResponseWriter, r *http.Request) {

	user, ok, err := RetrieveAuthenticatedUserId(r)

	if !ok {
		w.WriteHeader(http.StatusForbidden)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Forbidden"))
		return
	}

	w.WriteHeader(http.StatusOK)

	w.Header().Set("Content-Type", "text/plain")

	_, err = w.Write([]byte(fmt.Sprintf("protected area. hi %v %d", user, user.ID)))

	if err != nil {
		fmt.Println("Error writing response:", err)
	}
}

type UserLoginRequest struct {
	Username string
	Password string
}

// UserLoginHandler godoc
//
//		@Summary		Login a user
//	 	@Description	Login a user
//		@Accept			json
//		@Produce		json
//		@Param 			username body string true "username"
//		@Param 			password body string true "password"
//		@Router			/api/user/login/ [post]
func UserLoginHandler(w http.ResponseWriter, r *http.Request) {
	var request UserLoginRequest
	err := json.NewDecoder(r.Body).Decode(&request)

	if err != nil {
		fmt.Println("Error decoding request:", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	fmt.Printf("DEBUG: request is %v\n", request)
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("ok"))
}
