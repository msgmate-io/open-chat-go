package runtimecfg

import "sync"

type Value struct {
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
}

var (
	mu     sync.RWMutex
	values = map[string]Value{}
)

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
