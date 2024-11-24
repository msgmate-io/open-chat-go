package websocket

type Messages struct{}

type UserWentOnline struct {
	Type    string `json:"type"`
	Content struct {
		UserUUID string `json:"user_uuid"`
	} `json:"content"`
}

func (m *Messages) UserWentOnline(UserUUID string) UserWentOnline {
	return UserWentOnline{
		Type: "user_went_online",
		Content: struct {
			UserUUID string `json:"user_uuid"`
		}{
			UserUUID: UserUUID,
		},
	}
}
