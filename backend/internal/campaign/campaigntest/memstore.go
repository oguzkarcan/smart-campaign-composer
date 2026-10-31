// Package campaigntest provides an in-memory campaign store for tests.
package campaigntest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

// MemStore is an in-memory campaign.Store with fixed audience numbers, for
// tests that do not need Postgres. It applies the same status guards as
// campaign.PGStore.
type MemStore struct {
	mu        sync.Mutex
	campaigns map[int64]*campaign.Campaign
	nextID    int64
	Audience  segment.Audience
	// Lens are the variable lengths per language of the reachable audience;
	// its keys are the languages content is written in. Turkish only by
	// default.
	Lens     campaign.LanguageLengths
	attempts map[int64]*llm.MemoryRecorder
	// Queued is how many messages approval enqueued per campaign: the
	// audience snapshot's consented counts for the campaign's channels.
	Queued map[int64]int
	// Messages are the message counts Progress and List report per
	// campaign and channel; tests set them to simulate delivery.
	Messages map[int64]map[string]campaign.Counts
	Failures map[int64][]campaign.FailureReason
	// People are the users every rule matches.
	People []campaign.Person
}

func NewMemStore() *MemStore {
	return &MemStore{
		campaigns: map[int64]*campaign.Campaign{},
		Audience: segment.Audience{Matched: 230, SMS: 120, Email: 160,
			Languages: map[string]segment.Counts{content.LanguageTR: {Matched: 230, SMS: 120, Email: 160}}},
		Lens:     campaign.LanguageLengths{content.LanguageTR: {content.VarFirstName: 13, content.VarCity: 10}},
		attempts: map[int64]*llm.MemoryRecorder{},
		Queued:   map[int64]int{},
		Messages: map[int64]map[string]campaign.Counts{},
		Failures: map[int64][]campaign.FailureReason{},
		People: []campaign.Person{
			{ID: 1, FirstName: "Ayşe", City: "İstanbul", Language: "tr", Phone: "+905550000001", Email: "ayse@example.com", SMSConsent: true, EmailConsent: true},
			{ID: 2, FirstName: "Mehmet", City: "Ankara", Language: "tr", Phone: "+905550000002", Email: "mehmet@example.com", EmailConsent: true},
			{ID: 3, FirstName: "Emily", City: "İzmir", Language: "en", Phone: "+905550000003", Email: "emily@example.com", SMSConsent: true},
		},
	}
}

// Catalog is the city and language catalog MemStore serves.
var Catalog = segment.Catalog{"city": {"Ankara", "İstanbul", "İzmir"}, "language": {"en", "tr"}}

func (s *MemStore) Create(_ context.Context, brief string, channels []string) (campaign.Campaign, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	c := &campaign.Campaign{ID: s.nextID, Brief: brief, Status: campaign.StatusDraft, Channels: channels, Contents: []campaign.Content{}}
	s.campaigns[c.ID] = c
	return *c, nil
}

func (s *MemStore) Get(_ context.Context, id int64) (campaign.Campaign, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.campaigns[id]
	if !ok {
		return campaign.Campaign{}, campaign.ErrNotFound
	}
	out := *c
	out.Contents = slices.Clone(c.Contents)
	return out, nil
}

func (s *MemStore) guard(id int64, allowed ...string) (*campaign.Campaign, error) {
	c, ok := s.campaigns[id]
	if !ok {
		return nil, campaign.ErrNotFound
	}
	if !slices.Contains(allowed, c.Status) {
		return nil, fmt.Errorf("%w (status %s)", campaign.ErrConflict, c.Status)
	}
	return c, nil
}

func (s *MemStore) SaveSegment(_ context.Context, id int64, r segment.Rule, a segment.Audience) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.guard(id, campaign.StatusDraft, campaign.StatusAudienceReady, campaign.StatusContentReady)
	if err != nil {
		return err
	}
	c.Rule, c.Audience, c.Status, c.LastError, c.Contents = &r, &a, campaign.StatusAudienceReady, "", []campaign.Content{}
	return nil
}

func (s *MemStore) SaveContent(_ context.Context, id int64, all map[string]content.Content) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.guard(id, campaign.StatusAudienceReady, campaign.StatusContentReady)
	if err != nil {
		return err
	}
	version := 1
	if len(c.Contents) > 0 {
		version = c.Contents[0].Version + 1
	}
	c.Contents = nil
	for _, lang := range content.Languages { // stored order: language, then channel
		cc, ok := all[lang]
		if !ok {
			continue
		}
		if cc.SMS != nil {
			c.Contents = append(c.Contents, campaign.Content{Language: lang, Channel: "sms", Body: cc.SMS.Body, Version: version})
		}
		if cc.Email != nil {
			c.Contents = append(c.Contents, campaign.Content{Language: lang, Channel: "email", Subject: cc.Email.Subject,
				Body: cc.Email.Body, Version: version})
		}
	}
	c.Status, c.LastError = campaign.StatusContentReady, ""
	return nil
}

func (s *MemStore) EditContent(_ context.Context, id int64, language string, cc content.Content, channel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.guard(id, campaign.StatusContentReady)
	if err != nil {
		return err
	}
	for i := range c.Contents {
		m := &c.Contents[i]
		if m.Language != language || m.Channel != channel {
			continue
		}
		if channel == "sms" {
			m.Body = cc.SMS.Body
		} else {
			m.Subject, m.Body = cc.Email.Subject, cc.Email.Body
		}
		m.HumanEdited = true
		m.Version++
	}
	return nil
}

func (s *MemStore) SetError(_ context.Context, id int64, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.campaigns[id].LastError = msg
	return nil
}

func (s *MemStore) Approve(_ context.Context, id int64, versions map[string]int, now time.Time, sendAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.guard(id, campaign.StatusContentReady)
	if err != nil {
		return err
	}
	current := map[string]int{}
	for _, m := range c.Contents {
		current[m.Language+"."+m.Channel] = m.Version
	}
	if !maps.Equal(current, versions) {
		return fmt.Errorf("%w: the content changed while approving", campaign.ErrConflict)
	}
	if c.Reachable() == 0 {
		return fmt.Errorf("%w: nobody can be reached", campaign.ErrConflict)
	}
	s.Queued[id] = c.Reachable()
	c.Status, c.ApprovedAt, c.ScheduledAt, c.LastError = campaign.StatusSending, &now, sendAt, ""
	if sendAt != nil {
		c.Status = campaign.StatusScheduled
	}
	return nil
}

func (s *MemStore) Progress(_ context.Context, id int64) (campaign.Progress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.campaigns[id]
	if !ok {
		return campaign.Progress{}, campaign.ErrNotFound
	}
	p := campaign.Progress{CampaignID: id, Status: c.Status, Channels: map[string]campaign.Counts{},
		Failures: slices.Clone(s.Failures[id])}
	for _, ch := range c.Channels {
		counts := s.Messages[id][ch]
		p.Channels[ch] = counts
		p.Totals.Add("queued", counts.Queued)
		p.Totals.Add("processing", counts.Processing)
		p.Totals.Add("retry", counts.Retry)
		p.Totals.Add("sent", counts.Sent)
		p.Totals.Add("failed", counts.Failed)
	}
	return p, nil
}

func (s *MemStore) List(_ context.Context, limit int, before int64) ([]campaign.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []campaign.Summary
	for id := s.nextID; id > 0 && len(out) < limit; id-- {
		c, ok := s.campaigns[id]
		if !ok || (before != 0 && id >= before) {
			continue
		}
		var m campaign.Counts
		for _, counts := range s.Messages[id] {
			m.Add("queued", counts.Queued)
			m.Add("processing", counts.Processing)
			m.Add("retry", counts.Retry)
			m.Add("sent", counts.Sent)
			m.Add("failed", counts.Failed)
		}
		out = append(out, campaign.Summary{ID: id, Brief: c.Brief, Status: c.Status, Channels: c.Channels,
			Messages: m, LastError: c.LastError, ApprovedAt: c.ApprovedAt, ScheduledAt: c.ScheduledAt})
	}
	return out, nil
}

// SetMessages sets the message counts of one campaign channel and its status.
func (s *MemStore) SetMessages(id int64, channel string, counts campaign.Counts, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Messages[id] == nil {
		s.Messages[id] = map[string]campaign.Counts{}
	}
	counts.Total = counts.Queued + counts.Processing + counts.Retry + counts.Sent + counts.Failed
	s.Messages[id][channel] = counts
	s.campaigns[id].Status = status
}

// People are the users Matching pages through; NewMemStore fills in a few.
func (s *MemStore) Matching(_ context.Context, _ segment.Rule, q campaign.PeopleQuery, _ time.Time) ([]campaign.Person, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	people := slices.Clone(s.People)
	key := func(p campaign.Person) string {
		switch q.Sort {
		case "first_name":
			return p.FirstName
		case "city":
			return p.City
		case "language":
			return p.Language
		}
		return fmt.Sprintf("%020d", p.ID)
	}
	slices.SortStableFunc(people, func(a, b campaign.Person) int {
		c := strings.Compare(key(a), key(b))
		if q.Order == "desc" {
			c = -c
		}
		return c
	})
	if q.Offset >= len(people) {
		return nil, len(people), nil
	}
	return people[q.Offset:min(q.Offset+q.Limit, len(people))], len(people), nil
}

func (s *MemStore) Catalog(context.Context) (segment.Catalog, error) { return Catalog, nil }

func (s *MemStore) Count(context.Context, segment.Rule, time.Time) (segment.Audience, error) {
	return s.Audience, nil
}

func (s *MemStore) MaxLengths(context.Context, segment.Rule, []string, time.Time) (campaign.LanguageLengths, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := campaign.LanguageLengths{}
	for lang, lens := range s.Lens {
		out[lang] = maps.Clone(lens)
	}
	return out, nil
}

func (s *MemStore) Recorder(id int64) llm.Recorder {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts[id] == nil {
		s.attempts[id] = &llm.MemoryRecorder{}
	}
	return s.attempts[id]
}

// SetStatus forces a campaign's status, e.g. to simulate an approval.
func (s *MemStore) SetStatus(id int64, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.campaigns[id].Status = status
}

// Attempts returns the LLM attempts recorded for a campaign.
func (s *MemStore) Attempts(id int64) []llm.Attempt {
	s.mu.Lock()
	rec := s.attempts[id]
	s.mu.Unlock()
	if rec == nil {
		return nil
	}
	return rec.Attempts
}
