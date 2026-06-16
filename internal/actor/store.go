package actor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func Load(path string) (State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, fmt.Errorf("read actor state: %w", err)
	}
	var state State
	if err := json.Unmarshal(b, &state); err != nil {
		return State{}, fmt.Errorf("decode actor state: %w", err)
	}
	if state.ActorID == "" {
		return State{}, fmt.Errorf("actor_id is required")
	}
	return state, nil
}

func Save(path string, state State) error {
	if state.ActorID == "" {
		return fmt.Errorf("actor_id is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create actor state directory: %w", err)
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode actor state: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write actor state: %w", err)
	}
	return nil
}
