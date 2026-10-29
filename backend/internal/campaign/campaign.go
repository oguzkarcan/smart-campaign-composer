// Package campaign runs a campaign from brief to approved content: it asks the
// LLM for a segment rule and messages, validates everything, and stores the
// result. Sending is handled by the delivery package.
package campaign

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

const (
	StatusDraft         = "draft"
	StatusAudienceReady = "audience_ready"
	StatusContentReady  = "content_ready"
	StatusApproved      = "approved"
	StatusSending       = "sending"
	StatusCompleted     = "completed"
	StatusFailed        = "failed"

	MaxBriefLength = 2000
)

// editable are the statuses before approval; only in these can the segment
// or content change.
var editable = []string{StatusDraft, StatusAudienceReady, StatusContentReady}

var (
	ErrNotFound = errors.New("campaign not found")
	// ErrInvalid wraps bad input from the marketer (brief, channels).
	ErrInvalid = errors.New("invalid input")
	// ErrConflict means the campaign is not in a status that allows the action.
	ErrConflict = errors.New("not allowed in the campaign's current status")
)

type Campaign struct {
	ID         int64             `json:"id"`
	Brief      string            `json:"brief"`
	Status     string            `json:"status"`
	Channels   []string          `json:"channels"`
	Rule       *segment.Rule     `json:"segment_rule"`
	Audience   *segment.Audience `json:"audience"`
	Contents   []Content         `json:"contents"`
	LastError  string            `json:"last_error,omitempty"`
	ApprovedAt *time.Time        `json:"approved_at,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Content is the current message for one channel.
type Content struct {
	Channel     string    `json:"channel"`
	Subject     string    `json:"subject,omitempty"`
	Body        string    `json:"body"`
	HumanEdited bool      `json:"human_edited"`
	Version     int       `json:"version"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Reachable is how many messages the campaign would send: per channel, the
// matched users who consented to it.
func (c Campaign) Reachable() int {
	if c.Audience == nil {
		return 0
	}
	n := 0
	for _, ch := range c.Channels {
		switch ch {
		case segment.ChannelSMS:
			n += c.Audience.SMS
		case segment.ChannelEmail:
			n += c.Audience.Email
		}
	}
	return n
}

// content returns the stored messages in the validator's shape.
func (c Campaign) content() content.Content {
	var out content.Content
	for _, m := range c.Contents {
		switch m.Channel {
		case content.ChannelSMS:
			out.SMS = &content.SMS{Body: m.Body}
		case content.ChannelEmail:
			out.Email = &content.Email{Subject: m.Subject, Body: m.Body}
		}
	}
	return out
}

// Store persists campaigns and answers audience queries. Writes that change
// the segment or content succeed only while the campaign is editable, and
// return ErrConflict otherwise, so a concurrent approval always wins.
type Store interface {
	Create(ctx context.Context, brief string, channels []string) (Campaign, error)
	Get(ctx context.Context, id int64) (Campaign, error)
	SaveSegment(ctx context.Context, id int64, r segment.Rule, a segment.Audience) error
	SaveContent(ctx context.Context, id int64, c content.Content) error
	EditContent(ctx context.Context, id int64, c content.Content, channel string) error
	SetError(ctx context.Context, id int64, msg string) error

	Catalog(ctx context.Context) (segment.Catalog, error)
	Count(ctx context.Context, r segment.Rule, now time.Time) (segment.Audience, error)
	MaxLengths(ctx context.Context, r segment.Rule, channels []string, now time.Time) (content.MaxLengths, error)

	// Recorder stores LLM attempts for one campaign in llm_generations.
	Recorder(campaignID int64) llm.Recorder
}

type Service struct {
	store Store
	llm   llm.Client
	now   func() time.Time
}

func NewService(store Store, client llm.Client) *Service {
	return &Service{store: store, llm: client, now: time.Now}
}

// Create stores a draft campaign. Channels come from the marketer, never from
// the LLM, and are returned in a canonical order (sms, email).
func (s *Service) Create(ctx context.Context, brief string, channels []string) (Campaign, error) {
	brief = strings.TrimSpace(brief)
	if n := utf8.RuneCountInString(brief); n == 0 || n > MaxBriefLength {
		return Campaign{}, fmt.Errorf("%w: the brief must be 1-%d characters, got %d", ErrInvalid, MaxBriefLength, n)
	}
	var canonical []string
	for _, ch := range []string{content.ChannelSMS, content.ChannelEmail} {
		if slices.Contains(channels, ch) {
			canonical = append(canonical, ch)
		}
	}
	for _, ch := range channels {
		if !slices.Contains(canonical, ch) {
			return Campaign{}, fmt.Errorf("%w: unknown channel %q, allowed: sms, email", ErrInvalid, ch)
		}
	}
	if len(canonical) == 0 {
		return Campaign{}, fmt.Errorf("%w: choose at least one channel (sms, email)", ErrInvalid)
	}
	return s.store.Create(ctx, brief, canonical)
}

func (s *Service) Get(ctx context.Context, id int64) (Campaign, error) {
	return s.store.Get(ctx, id)
}

// GenerateSegment asks the LLM for the audience rule and counts it. A new
// segment discards any content, since the audience it was written for changed.
// On failure the campaign keeps its status and records the error.
func (s *Service) GenerateSegment(ctx context.Context, id int64) (Campaign, error) {
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if !slices.Contains(editable, c.Status) {
		return Campaign{}, fmt.Errorf("%w: the segment cannot change after approval (status %s)", ErrConflict, c.Status)
	}
	cat, err := s.store.Catalog(ctx)
	if err != nil {
		return Campaign{}, err
	}

	req := llm.SegmentRequest(llm.SegmentInput{Brief: c.Brief, Catalog: cat})
	parse := func(b []byte) (segment.Rule, error) { return segment.Parse(b, cat) }
	rule, err := llm.Generate(ctx, s.llm, s.store.Recorder(id), req, parse, nil)
	if err != nil {
		return Campaign{}, s.fail(ctx, id, err)
	}

	audience, err := s.store.Count(ctx, rule, s.now())
	if err != nil {
		return Campaign{}, err
	}
	if err := s.store.SaveSegment(ctx, id, rule, audience); err != nil {
		return Campaign{}, err
	}
	return s.store.Get(ctx, id)
}

// GenerateContent asks the LLM for the messages of every campaign channel.
// Length limits use the longest first name and city in the reachable
// audience. onDelta receives the streamed text with its attempt number.
func (s *Service) GenerateContent(ctx context.Context, id int64, onDelta func(attempt int, text string)) (Campaign, error) {
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if c.Status != StatusAudienceReady && c.Status != StatusContentReady {
		return Campaign{}, fmt.Errorf("%w: content needs a generated audience first (status %s)", ErrConflict, c.Status)
	}
	if c.Reachable() == 0 {
		return Campaign{}, fmt.Errorf("%w: nobody in this audience consented to %s; change the brief or the channels",
			ErrConflict, strings.Join(c.Channels, " or "))
	}

	now := s.now()
	lens, err := s.store.MaxLengths(ctx, *c.Rule, c.Channels, now)
	if err != nil {
		return Campaign{}, err
	}
	req := llm.ContentRequest(llm.ContentInput{Brief: c.Brief, Channels: c.Channels, Rule: *c.Rule, Lengths: lens})
	parse := func(b []byte) (content.Content, error) { return content.Parse(b, c.Channels, lens) }
	result, err := llm.Generate(ctx, s.llm, s.store.Recorder(id), req, parse, onDelta)
	if err != nil {
		return Campaign{}, s.fail(ctx, id, err)
	}
	if err := s.store.SaveContent(ctx, id, result); err != nil {
		return Campaign{}, err
	}
	return s.store.Get(ctx, id)
}

// Edit is the marketer's change to one channel's message.
type Edit struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// EditContent replaces one channel's message with the marketer's text after
// the same validation generated content goes through, measured against the
// audience as it is now. Validation failures are a content.ValidationError.
func (s *Service) EditContent(ctx context.Context, id int64, channel string, e Edit) (Campaign, error) {
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if c.Status != StatusContentReady {
		return Campaign{}, fmt.Errorf("%w: only generated, unapproved content can be edited (status %s)", ErrConflict, c.Status)
	}
	if !slices.Contains(c.Channels, channel) {
		return Campaign{}, fmt.Errorf("%w: %q is not a channel of this campaign", ErrInvalid, channel)
	}

	edited := c.content()
	switch channel {
	case content.ChannelSMS:
		if strings.TrimSpace(e.Subject) != "" {
			return Campaign{}, content.ValidationError{{Path: "sms.subject", Message: "an SMS has no subject"}}
		}
		edited.SMS = &content.SMS{Body: e.Body}
	case content.ChannelEmail:
		edited.Email = &content.Email{Subject: e.Subject, Body: e.Body}
	}

	lens, err := s.store.MaxLengths(ctx, *c.Rule, c.Channels, s.now())
	if err != nil {
		return Campaign{}, err
	}
	if err := content.Validate(&edited, c.Channels, lens); err != nil {
		return Campaign{}, err
	}
	if err := s.store.EditContent(ctx, id, edited, channel); err != nil {
		return Campaign{}, err
	}
	return s.store.Get(ctx, id)
}

// fail records a generation error on the campaign for the UI and returns it.
// It is recorded even if the request was cancelled meanwhile.
func (s *Service) fail(ctx context.Context, id int64, err error) error {
	if serr := s.store.SetError(context.WithoutCancel(ctx), id, err.Error()); serr != nil {
		return errors.Join(err, serr)
	}
	return err
}
