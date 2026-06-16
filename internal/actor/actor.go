package actor

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

type State struct {
	ActorID string `json:"actor_id"`
	Seq     uint64 `json:"seq"`
	Lamport uint64 `json:"lamport"`
}

func NewState() (State, error) {
	id, err := NewID()
	if err != nil {
		return State{}, err
	}
	return State{ActorID: id}, nil
}

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate actor id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *State) NextSeq() uint64 {
	s.Seq++
	return s.Seq
}

func (s *State) NextLamport(parentLamports ...uint64) uint64 {
	max := s.Lamport
	for _, l := range parentLamports {
		if l > max {
			max = l
		}
	}
	s.Lamport = max + 1
	return s.Lamport
}

func (s *State) IngestLamport(lamport uint64) {
	if lamport > s.Lamport {
		s.Lamport = lamport
	}
}
