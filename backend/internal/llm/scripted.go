package llm

import (
	"context"
	"errors"
	"sync"
)

// Scripted is a test client that returns prepared answers in order and keeps
// the requests it received, so tests can drive repair and failure paths.
type Scripted struct {
	mu       sync.Mutex
	answers  []ScriptedAnswer
	Requests []Request
}

// ScriptedAnswer is one prepared answer: Text, or Err for a failed call.
type ScriptedAnswer struct {
	Text string
	Err  error
}

func NewScripted(answers ...ScriptedAnswer) *Scripted {
	return &Scripted{answers: answers}
}

func (s *Scripted) Model() string { return "scripted" }

func (s *Scripted) Complete(_ context.Context, req Request, onDelta func(string)) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, req)
	if len(s.answers) == 0 {
		return Response{}, errors.New("scripted: no answers left")
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	if a.Err != nil {
		return Response{}, a.Err
	}
	if onDelta != nil {
		onDelta(a.Text)
	}
	return Response{Text: a.Text, InputTokens: 10, OutputTokens: 5}, nil
}

// MemoryRecorder keeps attempts in memory, for tests.
type MemoryRecorder struct {
	mu       sync.Mutex
	Attempts []Attempt
}

func (r *MemoryRecorder) RecordAttempt(_ context.Context, a Attempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Attempts = append(r.Attempts, a)
	return nil
}
