package runtimecfg

import "sync"

type Value struct {
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
}

var (
	mu                sync.RWMutex
	values            = map[string]Value{}
	configSource      string
	openChatBootstrap OpenChatBootstrap
)

type OpenChatBootstrap struct {
	UserSpecs             []string
	BotSpecs              []string
	SSHDefaultOwners      []string
	SSHKeySpecs           []string
	SSHServerSpecs        []string
	SSHKeyGrantSpecs      []string
	SSHServerGrantSpecs   []string
	OpencodeDefaultOwners []string
	OpencodeProjectSpecs  []string
	GitDefaultOwners      []string
	GitTokenSpecs         []string
	GitRepositorySpecs    []string
	GitWorkspaceSpecs     []string
	GitWorkspaceGrantSpecs []string
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

// SetValue updates or inserts a single runtime value under lock. It lets the
// admin settings API mutate live values without replacing the whole map.
func SetValue(key string, value Value) {
	mu.Lock()
	defer mu.Unlock()
	if values == nil {
		values = map[string]Value{}
	}
	values[key] = value
}

// DeleteValue removes a runtime value. Missing keys are ignored.
func DeleteValue(key string) {
	mu.Lock()
	defer mu.Unlock()
	delete(values, key)
}

// SetConfigSource records the on-disk path the server was configured from. An
// empty value (or an inline config label) means the configuration cannot be
// persisted back to a file.
func SetConfigSource(path string) {
	mu.Lock()
	defer mu.Unlock()
	configSource = path
}

// GetConfigSource returns the recorded on-disk config path, if any.
func GetConfigSource() string {
	mu.RLock()
	defer mu.RUnlock()
	return configSource
}

func SetOpenChatBootstrap(next OpenChatBootstrap) {
	mu.Lock()
	defer mu.Unlock()
	openChatBootstrap = OpenChatBootstrap{
		UserSpecs:             append([]string(nil), next.UserSpecs...),
		BotSpecs:              append([]string(nil), next.BotSpecs...),
		SSHDefaultOwners:      append([]string(nil), next.SSHDefaultOwners...),
		SSHKeySpecs:           append([]string(nil), next.SSHKeySpecs...),
		SSHServerSpecs:        append([]string(nil), next.SSHServerSpecs...),
		SSHKeyGrantSpecs:      append([]string(nil), next.SSHKeyGrantSpecs...),
		SSHServerGrantSpecs:   append([]string(nil), next.SSHServerGrantSpecs...),
		OpencodeDefaultOwners: append([]string(nil), next.OpencodeDefaultOwners...),
		OpencodeProjectSpecs:  append([]string(nil), next.OpencodeProjectSpecs...),
		GitDefaultOwners:      append([]string(nil), next.GitDefaultOwners...),
		GitTokenSpecs:         append([]string(nil), next.GitTokenSpecs...),
		GitRepositorySpecs:    append([]string(nil), next.GitRepositorySpecs...),
		GitWorkspaceSpecs:     append([]string(nil), next.GitWorkspaceSpecs...),
		GitWorkspaceGrantSpecs: append([]string(nil), next.GitWorkspaceGrantSpecs...),
	}
}

func GetOpenChatBootstrap() OpenChatBootstrap {
	mu.RLock()
	defer mu.RUnlock()
	return OpenChatBootstrap{
		UserSpecs:             append([]string(nil), openChatBootstrap.UserSpecs...),
		BotSpecs:              append([]string(nil), openChatBootstrap.BotSpecs...),
		SSHDefaultOwners:      append([]string(nil), openChatBootstrap.SSHDefaultOwners...),
		SSHKeySpecs:           append([]string(nil), openChatBootstrap.SSHKeySpecs...),
		SSHServerSpecs:        append([]string(nil), openChatBootstrap.SSHServerSpecs...),
		SSHKeyGrantSpecs:      append([]string(nil), openChatBootstrap.SSHKeyGrantSpecs...),
		SSHServerGrantSpecs:   append([]string(nil), openChatBootstrap.SSHServerGrantSpecs...),
		OpencodeDefaultOwners: append([]string(nil), openChatBootstrap.OpencodeDefaultOwners...),
		OpencodeProjectSpecs:  append([]string(nil), openChatBootstrap.OpencodeProjectSpecs...),
		GitDefaultOwners:      append([]string(nil), openChatBootstrap.GitDefaultOwners...),
		GitTokenSpecs:         append([]string(nil), openChatBootstrap.GitTokenSpecs...),
		GitRepositorySpecs:    append([]string(nil), openChatBootstrap.GitRepositorySpecs...),
		GitWorkspaceSpecs:     append([]string(nil), openChatBootstrap.GitWorkspaceSpecs...),
		GitWorkspaceGrantSpecs: append([]string(nil), openChatBootstrap.GitWorkspaceGrantSpecs...),
	}
}
