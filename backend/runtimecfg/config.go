package runtimecfg

import "sync"

type Value struct {
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
}

var (
	mu                sync.RWMutex
	values            = map[string]Value{}
	openChatBootstrap OpenChatBootstrap
)

type OpenChatBootstrap struct {
	BotSpecs         []string
	SSHDefaultOwners []string
	SSHKeySpecs      []string
	SSHServerSpecs   []string
}

func SetAll(next map[string]Value) {
	mu.Lock()
	defer mu.Unlock()
	values = make(map[string]Value, len(next))
	for key, value := range next {
		values[key] = value
	}
}

func GetAll() map[string]Value {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]Value, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func SetOpenChatBootstrap(next OpenChatBootstrap) {
	mu.Lock()
	defer mu.Unlock()
	openChatBootstrap = OpenChatBootstrap{
		BotSpecs:         append([]string(nil), next.BotSpecs...),
		SSHDefaultOwners: append([]string(nil), next.SSHDefaultOwners...),
		SSHKeySpecs:      append([]string(nil), next.SSHKeySpecs...),
		SSHServerSpecs:   append([]string(nil), next.SSHServerSpecs...),
	}
}

func GetOpenChatBootstrap() OpenChatBootstrap {
	mu.RLock()
	defer mu.RUnlock()
	return OpenChatBootstrap{
		BotSpecs:         append([]string(nil), openChatBootstrap.BotSpecs...),
		SSHDefaultOwners: append([]string(nil), openChatBootstrap.SSHDefaultOwners...),
		SSHKeySpecs:      append([]string(nil), openChatBootstrap.SSHKeySpecs...),
		SSHServerSpecs:   append([]string(nil), openChatBootstrap.SSHServerSpecs...),
	}
}
