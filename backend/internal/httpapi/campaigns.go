package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

const maxBodyBytes = 64 << 10

type createRequest struct {
	Brief    string   `json:"brief"`
	Channels []string `json:"channels"`
}

// handleCreateCampaign stores the campaign and generates its segment in the
// same request. If generation fails the campaign still exists (201) as a draft
// with last_error set, and the segment can be retried.
func (s *Server) handleCreateCampaign(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := s.campaigns.Create(r.Context(), req.Brief, req.Channels)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/campaigns/%d", c.ID))

	generated, err := s.campaigns.GenerateSegment(r.Context(), c.ID)
	switch {
	case err == nil:
		c = generated
	case isLLMFailure(err):
		if c, err = s.campaigns.Get(r.Context(), c.ID); err != nil {
			writeError(w, err)
			return
		}
	default:
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleGetCampaign(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := s.campaigns.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleGenerateSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := s.campaigns.GenerateSegment(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type deltaEvent struct {
	Attempt int    `json:"attempt"`
	Text    string `json:"text"`
}

// handleGenerateContent streams generation as server-sent events:
//
//	event: delta  data: {"attempt": 1, "text": "..."}   streamed model output
//	event: done   data: <campaign>                       validated and saved
//	event: error  data: {"status": 422, "error": "...", "problems": [...]}
//
// A delta with a higher attempt number starts a repair round; the UI should
// clear its preview. Errors before the first delta (unknown campaign, wrong
// status) are a normal JSON error response, not a stream.
func (s *Server) handleGenerateContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	stream := newSSE(w)
	c, err := s.campaigns.GenerateContent(r.Context(), id, func(attempt int, text string) {
		stream.send("delta", deltaEvent{Attempt: attempt, Text: text})
	})
	switch {
	case err != nil && !stream.started:
		writeError(w, err)
	case err != nil:
		status, body := errorBody(err)
		body.Status = status
		stream.send("error", body)
	default:
		stream.send("done", c)
	}
}

func (s *Server) handleEditContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var edit campaign.Edit
	if !decode(w, r, &edit) {
		return
	}
	c, err := s.campaigns.EditContent(r.Context(), id, r.PathValue("channel"), edit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: campaign.ErrNotFound.Error()})
		return 0, false
	}
	return id, true
}

// decode reads a small JSON body strictly: unknown fields are an error.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body: " + err.Error()})
		return false
	}
	return true
}

type problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type errorResponse struct {
	Status   int       `json:"status,omitempty"` // only in SSE error events
	Error    string    `json:"error"`
	Problems []problem `json:"problems,omitempty"`
}

func writeError(w http.ResponseWriter, err error) {
	status, body := errorBody(err)
	writeJSON(w, status, body)
}

// isLLMFailure reports whether err came from the model (invalid output after
// repairs, or a failed call) rather than from our own infrastructure.
func isLLMFailure(err error) bool {
	var gerr *llm.GenerationError
	var cerr *llm.CallError
	return errors.As(err, &gerr) || errors.As(err, &cerr)
}

// errorBody maps errors to a status and a message safe to show the marketer.
// Unexpected errors are logged and reported without internal details.
func errorBody(err error) (int, errorResponse) {
	body := errorResponse{Error: err.Error(), Problems: problems(err)}
	var gerr *llm.GenerationError
	var cerr *llm.CallError
	var verr content.ValidationError
	switch {
	case errors.Is(err, campaign.ErrNotFound):
		return http.StatusNotFound, body
	case errors.Is(err, campaign.ErrInvalid):
		return http.StatusBadRequest, body
	case errors.Is(err, campaign.ErrConflict):
		return http.StatusConflict, body
	case errors.As(err, &gerr), errors.As(err, &verr), errors.Is(err, llm.ErrRefusal):
		return http.StatusUnprocessableEntity, body
	case errors.As(err, &cerr):
		slog.Warn("llm call failed", "err", err)
		return http.StatusBadGateway, body
	case errors.Is(err, context.Canceled):
		return 499, errorResponse{Error: "request cancelled"} // client went away; nobody reads this
	default:
		slog.Error("request failed", "err", err)
		return http.StatusInternalServerError, errorResponse{Error: "internal error"}
	}
}

func problems(err error) []problem {
	var serr segment.ValidationError
	var cerr content.ValidationError
	var out []problem
	switch {
	case errors.As(err, &serr):
		for _, p := range serr {
			out = append(out, problem(p))
		}
	case errors.As(err, &cerr):
		for _, p := range cerr {
			out = append(out, problem(p))
		}
	}
	return out
}

// sse writes server-sent events. Headers are sent with the first event, so a
// handler can still answer with a plain error status until then.
type sse struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	started bool
	broken  bool
}

func newSSE(w http.ResponseWriter) *sse {
	return &sse{w: w, rc: http.NewResponseController(w)}
}

func (s *sse) send(event string, v any) {
	if s.broken {
		return
	}
	if !s.started {
		h := s.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	data, err := json.Marshal(v) // never contains raw newlines
	if err == nil {
		_, err = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	}
	if err == nil {
		err = s.rc.Flush()
	}
	if err != nil {
		s.broken = true // client gone; the request context ends generation
	}
}
