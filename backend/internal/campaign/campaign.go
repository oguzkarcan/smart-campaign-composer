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
	StatusScheduled     = "scheduled"
	StatusSending       = "sending"
	StatusCompleted     = "completed"
	StatusFailed        = "failed"

	MaxBriefLength = 2000
	// MaxScheduleAhead is how far in the future a campaign can be scheduled.
	MaxScheduleAhead = 30 * 24 * time.Hour
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
	// ScheduledAt is when a scheduled campaign starts sending.
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Languages are the recipient languages the campaign has (or needs)
	// content for: those in the reachable audience now, plus any that
	// already have content. Set once the campaign has a segment.
	Languages []string `json:"languages,omitempty"`
	// VariableLengths is, per language, how many characters each
	// personalization variable counts as in the worst-case length check,
	// measured on the reachable audience who read that language now; Limits
	// are the maximum worst-case lengths by path ("sms.body"). A UI computes
	// the same worst case the server validates: template length with each
	// variable replaced by its counted length. Set once the campaign has a
	// segment.
	VariableLengths map[string]map[string]int `json:"variable_lengths,omitempty"`
	Limits          map[string]int            `json:"limits"`
}

// LanguageLengths are the longest variable values per language among the
// reachable audience. A language is present only if somebody reachable
// reads it, so its keys are the languages content must be written in.
type LanguageLengths map[string]content.MaxLengths

// languages returns the languages present, in content.Languages order.
func (l LanguageLengths) languages() []string {
	var out []string
	for _, lang := range content.Languages {
		if _, ok := l[lang]; ok {
			out = append(out, lang)
		}
	}
	return out
}

// Content is the current message for one language and channel.
type Content struct {
	Language    string    `json:"language"`
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

// hasContent reports whether there are messages for language.
func (c Campaign) hasContent(language string) bool {
	return slices.ContainsFunc(c.Contents, func(m Content) bool { return m.Language == language })
}

// content returns the stored messages of one language in the validator's
// shape.
func (c Campaign) content(language string) content.Content {
	var out content.Content
	for _, m := range c.Contents {
		if m.Language != language {
			continue
		}
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
	// SaveContent replaces all content with one message set per language.
	SaveContent(ctx context.Context, id int64, c map[string]content.Content) error
	// EditContent stores channel's message from c for language.
	EditContent(ctx context.Context, id int64, language string, c content.Content, channel string) error
	SetError(ctx context.Context, id int64, msg string) error
	// Approve moves a content_ready campaign to sending, or to scheduled if
	// sendAt is set, and enqueues its messages in one transaction; scheduled
	// messages are due at sendAt. Each message records its recipient's
	// language; only recipients whose language has content are queued. It
	// returns ErrConflict if the content is no longer at the validated
	// versions ("tr.sms" → version) or nobody can be reached.
	Approve(ctx context.Context, id int64, versions map[string]int, now time.Time, sendAt *time.Time) error
	// Progress counts the campaign's messages; ErrNotFound if it does not exist.
	Progress(ctx context.Context, id int64) (Progress, error)
	// List returns campaigns newest first, with ids below before (0: no bound).
	List(ctx context.Context, limit int, before int64) ([]Summary, error)

	Catalog(ctx context.Context) (segment.Catalog, error)
	Count(ctx context.Context, r segment.Rule, now time.Time) (segment.Audience, error)
	// MaxLengths measures the variables per language over the users the rule
	// matches who consented to at least one of channels.
	MaxLengths(ctx context.Context, r segment.Rule, channels []string, now time.Time) (LanguageLengths, error)

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
	c, err := s.store.Create(ctx, brief, canonical)
	if err != nil {
		return Campaign{}, err
	}
	return s.Get(ctx, c.ID)
}

// Get returns a campaign with the variable lengths its content is validated
// against, measured at the same moment as an edit would be.
func (s *Service) Get(ctx context.Context, id int64) (Campaign, error) {
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	c.Limits = content.Limits
	if c.Rule != nil {
		lens, err := s.store.MaxLengths(ctx, *c.Rule, c.Channels, s.now())
		if err != nil {
			return Campaign{}, err
		}
		c.VariableLengths = map[string]map[string]int{}
		for _, lang := range content.Languages {
			if _, reachable := lens[lang]; reachable || c.hasContent(lang) {
				c.Languages = append(c.Languages, lang)
				c.VariableLengths[lang] = lens[lang].Counted()
			}
		}
	}
	return c, nil
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
	return s.Get(ctx, id)
}

// GenerateContent asks the LLM for the messages of every campaign channel,
// once per language in the reachable audience, each with its own validation
// and repair rounds. Length limits use the longest first name and city among
// the reachable recipients of that language. onDelta receives the streamed
// text with its language and attempt number. Content is saved only if every
// language succeeds.
func (s *Service) GenerateContent(ctx context.Context, id int64, onDelta func(language string, attempt int, text string)) (Campaign, error) {
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

	all, err := s.store.MaxLengths(ctx, *c.Rule, c.Channels, s.now())
	if err != nil {
		return Campaign{}, err
	}
	languages := all.languages()
	if len(languages) == 0 {
		return Campaign{}, fmt.Errorf("%w: nobody in this audience can be reached on %s anymore; generate the audience again",
			ErrConflict, strings.Join(c.Channels, " or "))
	}
	results := map[string]content.Content{}
	for _, lang := range languages {
		lens := all[lang]
		req := llm.ContentRequest(llm.ContentInput{Brief: c.Brief, Channels: c.Channels, Rule: *c.Rule, Language: lang, Lengths: lens})
		parse := func(b []byte) (content.Content, error) { return content.Parse(b, c.Channels, lens) }
		var delta func(int, string)
		if onDelta != nil {
			delta = func(attempt int, text string) { onDelta(lang, attempt, text) }
		}
		result, err := llm.Generate(ctx, s.llm, s.store.Recorder(id), req, parse, delta)
		if err != nil {
			return Campaign{}, s.fail(ctx, id, inLanguage(lang, err))
		}
		results[lang] = result
	}
	if err := s.store.SaveContent(ctx, id, results); err != nil {
		return Campaign{}, err
	}
	return s.Get(ctx, id)
}

// Edit is the marketer's change to one channel's message.
type Edit struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// EditContent replaces one language's message for channel with the
// marketer's text after the same validation generated content goes through,
// measured against that language's audience as it is now. Validation
// failures are a content.ValidationError.
func (s *Service) EditContent(ctx context.Context, id int64, language, channel string, e Edit) (Campaign, error) {
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
	if !c.hasContent(language) {
		return Campaign{}, fmt.Errorf("%w: this campaign has no %q messages", ErrInvalid, language)
	}

	edited := c.content(language)
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
	// A language nobody reachable reads any more has no measurement and is
	// checked with the pessimistic default lengths.
	if err := content.Validate(&edited, c.Channels, lens[language]); err != nil {
		return Campaign{}, err
	}
	if err := s.store.EditContent(ctx, id, language, edited, channel); err != nil {
		return Campaign{}, err
	}
	return s.Get(ctx, id)
}

// Approve locks the content and queues one message per recipient and
// channel. The content is validated again against the audience as it is now,
// since names and cities may be longer than when it was written or edited;
// failures are a content.ValidationError and nothing is approved. Recipients
// are computed again too: the rule matches who it matches today, and consent
// is applied per channel by the store. Every language in today's reachable
// audience must have content; problems are reported with the language in
// the path ("en.sms.body").
//
// With sendAt the campaign is scheduled: recipients and content are fixed
// now, and the messages become due at sendAt. sendAt must be in the future
// and at most MaxScheduleAhead away; nil sends now.
func (s *Service) Approve(ctx context.Context, id int64, sendAt *time.Time) (Campaign, error) {
	now := s.now()
	if sendAt != nil {
		if !sendAt.After(now) {
			return Campaign{}, fmt.Errorf("%w: send_at must be in the future", ErrInvalid)
		}
		if sendAt.Sub(now) > MaxScheduleAhead {
			return Campaign{}, fmt.Errorf("%w: send_at can be at most %d days ahead", ErrInvalid, int(MaxScheduleAhead.Hours()/24))
		}
	}
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if c.Status != StatusContentReady {
		return Campaign{}, fmt.Errorf("%w: only generated, unapproved content can be approved (status %s)", ErrConflict, c.Status)
	}

	all, err := s.store.MaxLengths(ctx, *c.Rule, c.Channels, now)
	if err != nil {
		return Campaign{}, err
	}
	var problems content.ValidationError
	for _, lang := range all.languages() {
		if !c.hasContent(lang) {
			return Campaign{}, fmt.Errorf("%w: the audience now includes recipients who read %s, and there are no %s messages; "+
				"generate the messages again", ErrConflict, llm.LanguageNames[lang], llm.LanguageNames[lang])
		}
		stored := c.content(lang)
		err := content.Validate(&stored, c.Channels, all[lang])
		var verr content.ValidationError
		switch {
		case errors.As(err, &verr):
			problems = append(problems, verr.Prefix(lang)...)
		case err != nil:
			return Campaign{}, err
		}
	}
	if len(problems) > 0 {
		return Campaign{}, problems
	}
	versions := map[string]int{}
	for _, m := range c.Contents {
		versions[m.Language+"."+m.Channel] = m.Version
	}
	if err := s.store.Approve(ctx, id, versions, now, sendAt); err != nil {
		return Campaign{}, err
	}
	return s.Get(ctx, id)
}

// Counts are a campaign's messages by status.
type Counts struct {
	Total      int `json:"total"`
	Queued     int `json:"queued"`
	Processing int `json:"processing"`
	Retry      int `json:"retry"`
	Sent       int `json:"sent"`
	Failed     int `json:"failed"`
}

// Add counts n messages with status.
func (c *Counts) Add(status string, n int) {
	c.Total += n
	switch status {
	case "queued":
		c.Queued += n
	case "processing":
		c.Processing += n
	case "retry":
		c.Retry += n
	case "sent":
		c.Sent += n
	case "failed":
		c.Failed += n
	}
}

// FailureReason groups failed messages by their last error.
type FailureReason struct {
	Error string `json:"error"`
	Count int    `json:"count"`
}

// Progress is the live view of a campaign's delivery.
type Progress struct {
	CampaignID int64             `json:"campaign_id"`
	Status     string            `json:"status"`
	Done       bool              `json:"done"` // completed or failed; nothing will change
	Totals     Counts            `json:"totals"`
	Channels   map[string]Counts `json:"channels"`
	// Failures are the most common reasons messages failed.
	Failures []FailureReason `json:"failures"`
}

// Summary is a campaign in the list.
type Summary struct {
	ID          int64      `json:"id"`
	Brief       string     `json:"brief"`
	Status      string     `json:"status"`
	Channels    []string   `json:"channels"`
	Messages    Counts     `json:"messages"`
	LastError   string     `json:"last_error,omitempty"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// Progress returns the delivery counts; every campaign channel is present,
// even before approval (all zero).
func (s *Service) Progress(ctx context.Context, id int64) (Progress, error) {
	p, err := s.store.Progress(ctx, id)
	if err != nil {
		return Progress{}, err
	}
	p.Done = p.Status == StatusCompleted || p.Status == StatusFailed
	if p.Failures == nil {
		p.Failures = []FailureReason{}
	}
	return p, nil
}

// List returns up to limit campaigns, newest first; before pages backwards
// by id. A limit of 0 means DefaultListLimit.
func (s *Service) List(ctx context.Context, limit int, before int64) ([]Summary, error) {
	switch {
	case limit == 0:
		limit = DefaultListLimit
	case limit < 0 || limit > MaxListLimit:
		return nil, fmt.Errorf("%w: limit must be 1-%d", ErrInvalid, MaxListLimit)
	}
	if before < 0 {
		return nil, fmt.Errorf("%w: before must be a campaign id", ErrInvalid)
	}
	list, err := s.store.List(ctx, limit, before)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Summary{}
	}
	return list, nil
}

// inLanguage names the language a content generation failed for, and puts
// validation problems under it ("en.sms.body").
func inLanguage(lang string, err error) error {
	var gerr *llm.GenerationError
	var verr content.ValidationError
	if errors.As(err, &gerr) && errors.As(gerr.Err, &verr) {
		g := *gerr
		g.Err = verr.Prefix(lang)
		err = &g
	}
	return fmt.Errorf("%s messages: %w", llm.LanguageNames[lang], err)
}

// fail records a generation error on the campaign for the UI and returns it.
// It is recorded even if the request was cancelled meanwhile.
func (s *Service) fail(ctx context.Context, id int64, err error) error {
	if serr := s.store.SetError(context.WithoutCancel(ctx), id, err.Error()); serr != nil {
		return errors.Join(err, serr)
	}
	return err
}
