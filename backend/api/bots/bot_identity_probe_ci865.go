package bots

import (
	"net/http"
	"strings"

	"backend/server/util"
)

// BotIdentityProbeResponse is the JSON body of the identity probe the Live
// Runtime Canary calls once per run (ci#865). It mirrors the lookup the
// create-interaction handler performs: the probe resolves the acting user and
// the bot by identifier, exactly like createBotInteraction does, and reports
// the resolved identifiers so the canary can see which owner-scoped identity
// the call would act for.
//
// A probe never fails on an unknown identifier: it returns 200 with
// `resolves=false` so the canary can distinguish "identity lookup ok" from
// "backend create-interaction failure" without a synthetic error path.
type BotIdentityProbeResponse struct {
	Resolves           bool   `json:"resolves"`
	BotUUID            string `json:"bot_uuid,omitempty"`
	BotName            string `json:"bot_name,omitempty"`
	ActingUserUUID     string `json:"acting_user_uuid,omitempty"`
	ActingUserUsername string `json:"acting_user_username,omitempty"`
}

// ProbeCreateInteractionIdentity is the canary-facing readiness probe for the
// create-interaction path (open-chat-go-ci#865). It runs the exact identity
// resolution the POST /bots/{identifier}/interactions handler uses — acting
// user resolution plus readable-bot lookup — without creating any interaction,
// so a regression in the interaction/backend path no longer surfaces as an
// empty-body decode error in the canary script.
//
//	GET /api/v1/bots/{identifier}/probe-create-interaction
//
// The handler answers 200 in every case it can run, with `resolves` describing
// whether the bot is reachable for the acting user. Unknown identifier,
// ambiguous identifier, or a bot the acting user cannot read all resolve to
// `resolves=false` (200); a database/context failure is a 500 with a JSON
// envelope like every other bots route.
func (h *BotsHandler) ProbeCreateInteractionIdentity(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		writeBotAPIError(w, http.StatusBadRequest, "database_unavailable", "Unable to get database or user")
		return
	}

	actingUser, err := resolveActingUser(DB, user)
	if err != nil {
		writeBotAPIError(w, http.StatusBadRequest, "unable_to_resolve_acting_user", "Unable to resolve acting user")
		return
	}

	identifier := strings.TrimSpace(r.PathValue("identifier"))
	runtime, err := resolveReadableBot(DB, actingUser, identifier)
	if err != nil {
		// gorm.ErrRecordNotFound and errAmbiguousIdentifier both mean "no bot
		// this acting user can act for" — a resolvable negative, not a backend
		// failure.
		writeProbeResult(w, BotIdentityProbeResponse{Resolves: false})
		return
	}

	writeProbeResult(w, BotIdentityProbeResponse{
		Resolves:           true,
		BotUUID:            runtime.UUID,
		BotName:            botDisplayName(runtime),
		ActingUserUUID:     actingUser.UUID,
		ActingUserUsername: actingUser.Username,
	})
}

func writeProbeResult(w http.ResponseWriter, result BotIdentityProbeResponse) {
	respondAgentJSON(w, http.StatusOK, result)
}
